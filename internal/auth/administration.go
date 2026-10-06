package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrLastOwner reports an operation that would leave the deployment with no
// active owner, and therefore nobody able to administer users. The guard is
// enforced atomically by UserRepository.Update itself (see the Postgres
// adapter), not by a separate count-then-write sequence here — two
// concurrent requests each changing a different one of the last two owners
// must not both be able to observe "another owner exists" and both proceed.
var ErrLastOwner = errors.New("last active owner")

// ErrSelfDeactivation reports an attempt by an actor to deactivate their own
// account. Deactivation deletes every session for the target user, so a
// self-deactivation would end the acting user's own session mid-request —
// an instant, un-undoable lockout with no other session left to reverse it
// from. Unlike ErrLastOwner this check touches no database state (it is a
// plain actorID == id comparison), so it belongs entirely in the service.
var ErrSelfDeactivation = errors.New("cannot deactivate own account")

// Administration is the administrative write path for user accounts:
// creating users, changing roles, deactivating accounts, and resetting
// passwords. It is kept separate from Service (the authentication path
// reached by every request through Authenticate) so that
// internal/api/users.go depends on a narrow interface and cannot
// accidentally reach Login or Authenticate.
type Administration struct {
	users            UserRepository
	sessions         SessionRepository
	entropy          io.Reader
	verifyPassword   func(string, string) (bool, error)
	verificationGate *passwordVerificationGate
}

// NewAdministration returns an Administration backed by users and sessions.
func NewAdministration(users UserRepository, sessions SessionRepository, options ServiceOptions) *Administration {
	entropy := options.Entropy
	if entropy == nil {
		entropy = rand.Reader
	}
	return &Administration{
		users:            users,
		sessions:         sessions,
		entropy:          entropy,
		verifyPassword:   VerifyPassword,
		verificationGate: resolveVerificationGate(options),
	}
}

// List returns every user, active or not.
func (a *Administration) List(ctx context.Context) ([]User, error) {
	users, err := a.users.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

// ListAssignable returns the active users who may be assigned an issue in
// the project identified by slug, for use by any signed-in caller that needs
// to pick a user without administrative rights. It carries the same Role and
// DeactivatedAt fields as List because UserRepository has no narrower read —
// internal/api/users.go must build a response DTO that omits them rather
// than serializing this value directly.
//
// scope bounds who may ask, not just who may be picked: slug is resolved
// against the caller's own scope before the roster is built, so a member
// requesting a project outside their groups gets ErrNotFound — identical to
// a slug naming no project at all — rather than a roster for a project they
// must not even learn exists.
func (a *Administration) ListAssignable(ctx context.Context, scope ProjectScope, slug string) ([]User, error) {
	users, err := a.users.ListAssignableForProject(ctx, scope, slug)
	if err != nil {
		return nil, fmt.Errorf("list assignable users: %w", err)
	}
	return users, nil
}

// Create validates and inserts a new account with a hashed password.
func (a *Administration) Create(ctx context.Context, email, name, password string, role Role) (User, error) {
	email = NormalizeEmail(email)
	name = strings.TrimSpace(name)
	if err := validateEmail(email); err != nil {
		return User{}, err
	}
	if name == "" {
		return User{}, ErrNameRequired
	}
	if err := ValidatePassword(password); err != nil {
		return User{}, err
	}
	role, err := ParseRole(string(role))
	if err != nil {
		return User{}, err
	}

	passwordHash, err := HashPassword(password, a.entropy)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	user, err := a.users.Create(ctx, NewUser{Email: email, Name: name, PasswordHash: passwordHash, Role: role})
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

// Rename changes a user's display name.
func (a *Administration) Rename(ctx context.Context, id int64, name string) (User, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return User{}, ErrNameRequired
	}
	updated, err := a.users.Update(ctx, id, UserChanges{Name: &name})
	if err != nil {
		return User{}, fmt.Errorf("rename user: %w", err)
	}
	return updated, nil
}

// SetRole changes a user's role, refusing to demote the last active owner.
// UserRepository.Update enforces this atomically; see ErrLastOwner.
func (a *Administration) SetRole(ctx context.Context, id int64, role Role) (User, error) {
	role, err := ParseRole(string(role))
	if err != nil {
		return User{}, err
	}
	updated, err := a.users.Update(ctx, id, UserChanges{Role: &role})
	if err != nil {
		return User{}, fmt.Errorf("set role: %w", err)
	}
	return updated, nil
}

// SetDeactivated deactivates or reactivates a user, on behalf of actorID.
// It refuses two things: an actor deactivating their own account
// (ErrSelfDeactivation), checked first since it needs no database read, and
// deactivating the last active owner (ErrLastOwner, enforced atomically by
// UserRepository.Update — see its doc comment). Neither check applies to
// reactivation: reactivating yourself is moot (a deactivated account has no
// session to have made the request with), and reactivation can never remove
// an owner. Deactivating a user also deletes every one of their sessions, so
// access ends on the next request rather than at token expiry; reactivating
// leaves sessions alone, since there is nothing to clear.
func (a *Administration) SetDeactivated(ctx context.Context, actorID, id int64, deactivated bool) (User, error) {
	if deactivated && actorID == id {
		return User{}, fmt.Errorf("set deactivated: %w", ErrSelfDeactivation)
	}
	updated, err := a.users.Update(ctx, id, UserChanges{Deactivated: &deactivated})
	if err != nil {
		return User{}, fmt.Errorf("set deactivated: %w", err)
	}
	if deactivated {
		if err := a.sessions.DeleteByUserID(ctx, id); err != nil {
			return User{}, fmt.Errorf("clear sessions for deactivated user: %w", err)
		}
	}
	return updated, nil
}

// ErrCurrentPasswordMismatch reports that ChangeOwnPassword's caller-supplied
// current password does not match the stored one. It covers both a wrong
// password and a verification error (e.g. a malformed stored hash): neither
// may be distinguished from the other in the response, the same way Login
// never lets an unknown email surface differently from a wrong password.
var ErrCurrentPasswordMismatch = errors.New("current password does not match")

// ChangeOwnPassword is the self-service counterpart to SetPassword: the
// caller changes their own password, and must prove they hold the current
// one first — without that check a stolen session could silently change the
// account's password and lock out the real owner. Verification happens
// before ValidatePassword and before any write, in that order, and — like
// Login — runs through the shared passwordVerificationGate: Argon2id is
// memory-hard, so an authenticated caller hammering this endpoint is an
// unbounded-concurrency resource-exhaustion vector, not just a guessing
// one. ErrAuthenticationBusy is reported as itself, distinct from
// ErrCurrentPasswordMismatch, so a saturated server is never told to the
// caller as "your password is wrong".
//
// keepSession is the token hash of the session the request arrived on. Once
// the new password is stored, every other session for the account is
// revoked and that one is spared: this is the screen a user reaches for when
// they suspect a session is compromised, so it has to actually end the other
// sessions — a session's expires_at is fixed at login and Touch never
// extends it, so without this the intruder keeps the cookie for up to its
// full lifetime with nothing the victim can do. Sparing the acting session
// is the point: the user must not be signed out of the tab they are using.
func (a *Administration) ChangeOwnPassword(ctx context.Context, id int64, current, next string, keepSession [32]byte) error {
	user, err := a.users.FindByID(ctx, id)
	if err != nil {
		return fmt.Errorf("find user: %w", err)
	}
	matches, err := a.verificationGate.verify(a.verifyPassword, user.PasswordHash, current)
	if errors.Is(err, ErrAuthenticationBusy) {
		return ErrAuthenticationBusy
	}
	if err != nil || !matches {
		// A verification error and a mismatch are the same outcome to the
		// caller: neither may reveal whether the stored hash is malformed.
		return ErrCurrentPasswordMismatch
	}
	if err := ValidatePassword(next); err != nil {
		return err
	}
	hash, err := HashPassword(next, a.entropy)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := a.users.UpdatePasswordHash(ctx, id, hash); err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	// After the write, not before: a revocation that succeeded while the
	// password write failed would sign the user out everywhere for nothing.
	if err := a.sessions.DeleteByUserIDExcept(ctx, id, keepSession); err != nil {
		return fmt.Errorf("revoke other sessions: %w", err)
	}
	return nil
}

// SetPassword sets a user's password to a new value chosen by an
// administrator, and ends every session the target had open. Both callers
// act on somebody else — the HTTP route refuses a self-targeted reset
// (internal/api/users.go, which points an owner at ChangeOwnPassword
// instead) and the CLI has no session of its own — so an administrative
// reset is always somebody containing an account they no longer trust: a
// stolen laptop, a departing colleague, a compromised deployment recovered
// from the host. Leaving the target's sessions alive would let the holder of
// the old cookie carry on for the rest of its lifetime, which is the one
// outcome the operator reaching for this control is trying to prevent.
// Self-service password change, which keeps the acting session and must
// verify the current password first, is ChangeOwnPassword.
func (a *Administration) SetPassword(ctx context.Context, id int64, password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	passwordHash, err := HashPassword(password, a.entropy)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := a.users.UpdatePasswordHash(ctx, id, passwordHash); err != nil {
		return fmt.Errorf("set password: %w", err)
	}
	if err := a.sessions.DeleteByUserID(ctx, id); err != nil {
		return fmt.Errorf("clear sessions after password reset: %w", err)
	}
	return nil
}
