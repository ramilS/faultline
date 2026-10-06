// Package auth owns credential verification, session lifecycle, and user
// administration: password hashing and comparison, login rate limiting,
// session issue/refresh/revocation, and the role checks the HTTP and CLI
// layers consult.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/mail"
	"strings"
	"time"
)

const (
	sessionTokenBytes       = 32
	sessionTouchInterval    = 5 * time.Minute
	sessionCreationAttempts = 3

	// SessionLifetime is how long a password sign-in's session stays valid.
	// Exported because internal/api derives the session cookie's MaxAge from
	// it: two independently maintained numbers would eventually disagree, and
	// a cookie outliving its session shows the user as signed in until the
	// next request 401s.
	SessionLifetime = 7 * 24 * time.Hour

	// SSOSessionLifetime is deliberately shorter than SessionLifetime. Without
	// SCIM, faultline learns nothing when an IdP account is disabled, so
	// session expiry is the only mechanism that actually revokes access.
	SSOSessionLifetime = 12 * time.Hour

	// dummyPasswordHash is a syntactically valid, fixed Argon2id hash used when
	// an email is unknown. Its value is deliberately unrelated to a real user.
	//nolint:gosec // G101: an all-zero placeholder hash, not a credential;
	// it exists so an unknown email costs the same time as a known one.
	dummyPasswordHash = "$argon2id$v=19$m=65536,t=3,p=2$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
)

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrInvalidSession     = errors.New("invalid session")
	ErrAuthenticationBusy = errors.New("authentication temporarily unavailable")

	// ErrSSONotConfigured is returned by LoginWithIdentity when the Service
	// was built without ServiceOptions.Identities. Exported so internal/api
	// can recognize it by identity (errors.Is) and report it as the wiring
	// bug it is, rather than relying on the convention that routes to
	// LoginWithIdentity only exist when SSO is configured.
	ErrSSONotConfigured = errors.New("sso not configured")

	// ErrEmailRequired, ErrEmailInvalid, and ErrNameRequired report user-input
	// validation failures on account creation and rename. Exported so callers
	// (internal/api) can map them to a 400 by identity (errors.Is) instead of
	// matching message text.
	ErrEmailRequired = errors.New("email is required")
	ErrEmailInvalid  = errors.New("email is invalid")
	ErrNameRequired  = errors.New("name is required")
)

// Clock supplies wall-clock time to authentication policy.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

type ServiceOptions struct {
	Clock   Clock
	Entropy io.Reader
	// ReportError receives operational detail an operator needs but a caller
	// must never see: a failed background cleanup, or a refused identity
	// link. Leave it nil to discard them.
	ReportError func(error)
	// Identities resolves identity-provider subjects to accounts for
	// LoginWithIdentity. Leave it nil when a deployment has no SSO provider
	// configured; LoginWithIdentity is simply never called in that case.
	Identities IdentityRepository
	// PasswordVerificationLimit sizes a fresh, disposable
	// passwordVerificationGate for this instance alone. Leave it zero in
	// production: Service and Administration must share one gate (see
	// verification_gate.go), and this field exists only so a test can give
	// one instance its own small, deterministic bound.
	PasswordVerificationLimit int
	// verificationGate, when set, is the exact gate instance to use —
	// how a test proves two different Service/Administration instances
	// share one bound. Unexported: production code never needs to set it,
	// since resolveVerificationGate's default already shares one gate.
	verificationGate *passwordVerificationGate
}

type Service struct {
	users            UserRepository
	sessions         SessionRepository
	identities       IdentityRepository
	clock            Clock
	entropy          io.Reader
	reportError      func(error)
	verifyPassword   func(string, string) (bool, error)
	verificationGate *passwordVerificationGate
}

func NewService(users UserRepository, sessions SessionRepository, options ServiceOptions) *Service {
	clock := options.Clock
	if clock == nil {
		clock = systemClock{}
	}
	entropy := options.Entropy
	if entropy == nil {
		entropy = rand.Reader
	}
	report := options.ReportError
	if report == nil {
		report = func(error) {}
	}
	return &Service{
		users:            users,
		sessions:         sessions,
		identities:       options.Identities,
		clock:            clock,
		entropy:          entropy,
		reportError:      report,
		verifyPassword:   VerifyPassword,
		verificationGate: resolveVerificationGate(options),
	}
}

func (s *Service) CreateUser(ctx context.Context, email, name, password string, role Role) (User, error) {
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

	passwordHash, err := HashPassword(password, s.entropy)
	if err != nil {
		return User{}, fmt.Errorf("hash password: %w", err)
	}
	user, err := s.users.Create(ctx, NewUser{Email: email, Name: name, PasswordHash: passwordHash, Role: role})
	if err != nil {
		return User{}, fmt.Errorf("create user: %w", err)
	}
	return user, nil
}

func validateEmail(email string) error {
	if email == "" {
		return ErrEmailRequired
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return ErrEmailInvalid
	}
	return nil
}

func (s *Service) Login(ctx context.Context, email, password string) (User, string, error) {
	user, err := s.users.FindByEmail(ctx, NormalizeEmail(email))
	passwordHash := user.PasswordHash
	unknownUser := false
	if errors.Is(err, ErrNotFound) {
		unknownUser = true
		passwordHash = dummyPasswordHash
	} else if err != nil {
		return User{}, "", fmt.Errorf("find user: %w", err)
	}
	// An SSO-provisioned account stores SQL NULL, which reads back empty.
	// Substituting the dummy hash keeps its cost identical to an unknown
	// email's; `unknownUser` then refuses it after verification.
	if passwordHash == "" {
		unknownUser = true
		passwordHash = dummyPasswordHash
	}

	passwordOK, verifyErr := s.verifyLoginPassword(passwordHash, password)
	if errors.Is(verifyErr, ErrAuthenticationBusy) {
		return User{}, "", ErrAuthenticationBusy
	}
	// Checked after verification rather than before, so a deactivated account
	// costs the same time as an active one with a wrong password. Returning
	// early on !IsActive() would make deactivation observable by timing.
	if unknownUser || verifyErr != nil || !passwordOK || !user.IsActive() {
		return User{}, "", ErrInvalidCredentials
	}

	token, err := s.issueSession(ctx, user, SessionLifetime)
	if err != nil {
		return User{}, "", err
	}
	return user, token, nil
}

func (s *Service) verifyLoginPassword(passwordHash, password string) (bool, error) {
	return s.verificationGate.verify(s.verifyPassword, passwordHash, password)
}

// LoginWithIdentity signs in the account behind an identity-provider subject,
// creating it on first sign-in. The caller has already applied the domain
// policy: this method trusts claims and does not re-check them.
func (s *Service) LoginWithIdentity(ctx context.Context, provider string, claims IdentityClaims) (User, string, error) {
	if s.identities == nil {
		return User{}, "", ErrSSONotConfigured
	}
	user, err := s.resolveIdentity(ctx, provider, claims)
	if err != nil {
		return User{}, "", err
	}
	if !user.IsActive() {
		return User{}, "", ErrInvalidCredentials
	}
	token, err := s.issueSession(ctx, user, SSOSessionLifetime)
	if err != nil {
		return User{}, "", err
	}
	return user, token, nil
}

// resolveIdentity finds the account behind an identity: by subject first,
// then by the email claim (which links the identity to a pre-existing
// account, so a team already using passwords can move to SSO), and failing
// both by provisioning a new member.
//
// The email fallback stops at owner accounts: it refuses with
// ErrInvalidCredentials so the caller's response stays generic, and reports
// the refusal for the operator. A member link risks one scoped account; an
// owner link is total control of the deployment handed to whoever holds that
// mailbox, on a claim Google itself says not to use as a primary identifier.
func (s *Service) resolveIdentity(ctx context.Context, provider string, claims IdentityClaims) (User, error) {
	user, err := s.identities.FindUserBySubject(ctx, provider, claims.Subject)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return User{}, fmt.Errorf("find identity: %w", err)
	}

	email := NormalizeEmail(claims.Email)
	existing, err := s.users.FindByEmail(ctx, email)
	switch {
	case err == nil:
		if existing.IsOwner() {
			s.reportError(fmt.Errorf(
				"sso: refused to link %s subject %q to owner account %q (id %d): "+
					"linking matches on the email claim, which Google documents as neither "+
					"unique to an account nor stable over time, so whoever controls that "+
					"mailbox would obtain an owner session; owners sign in with a password, "+
					"which this deployment retains, and SSO is for member accounts. To give "+
					"this person SSO access, give them a separate member account",
				provider, claims.Subject, existing.Email, existing.ID))
			return User{}, ErrInvalidCredentials
		}
		if linkErr := s.identities.Link(ctx, provider, claims.Subject, existing.ID); linkErr != nil {
			if errors.Is(linkErr, ErrConflict) {
				retried, findErr := s.identities.FindUserBySubject(ctx, provider, claims.Subject)
				if findErr != nil {
					return User{}, fmt.Errorf("find identity after link conflict: %w", findErr)
				}
				return retried, nil
			}
			return User{}, fmt.Errorf("link identity: %w", linkErr)
		}
		return existing, nil
	case errors.Is(err, ErrNotFound):
	default:
		return User{}, fmt.Errorf("find user: %w", err)
	}

	created, err := s.identities.CreateWithIdentity(ctx, provider, claims.Subject, NewIdentityUser{
		Email: email,
		Name:  identityName(claims),
		Role:  RoleMember,
	})
	if errors.Is(err, ErrConflict) {
		retried, findErr := s.identities.FindUserBySubject(ctx, provider, claims.Subject)
		if findErr != nil {
			return User{}, fmt.Errorf("find identity after create conflict: %w", findErr)
		}
		return retried, nil
	}
	if err != nil {
		return User{}, fmt.Errorf("provision identity: %w", err)
	}
	return created, nil
}

// identityName falls back to the address when the provider sends no name;
// users.name is NOT NULL and an empty display name is worse than a duplicate.
func identityName(claims IdentityClaims) string {
	if name := strings.TrimSpace(claims.Name); name != "" {
		return name
	}
	return NormalizeEmail(claims.Email)
}

// issueSession mints a new session token for user, valid for lifetime. Both
// the password and SSO login paths call this; only the lifetime differs.
func (s *Service) issueSession(ctx context.Context, user User, lifetime time.Duration) (string, error) {
	now := s.clock.Now()
	for range sessionCreationAttempts {
		rawToken := make([]byte, sessionTokenBytes)
		if _, err := io.ReadFull(s.entropy, rawToken); err != nil {
			return "", fmt.Errorf("generate session token: %w", err)
		}
		token := base64.RawURLEncoding.EncodeToString(rawToken)
		session := NewSession{
			UserID:     user.ID,
			TokenHash:  sha256.Sum256(rawToken),
			CreatedAt:  now,
			ExpiresAt:  now.Add(lifetime),
			LastSeenAt: now,
		}
		if err := s.sessions.Create(ctx, session); err != nil {
			if errors.Is(err, ErrConflict) {
				continue
			}
			return "", fmt.Errorf("create session: %w", err)
		}
		s.cleanupExpired(ctx, now)
		return token, nil
	}
	return "", errors.New("create session: token conflicts exhausted")
}

func TokenHash(token string) ([32]byte, error) {
	var zero [32]byte
	rawToken, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(rawToken) != sessionTokenBytes || base64.RawURLEncoding.EncodeToString(rawToken) != token {
		return zero, ErrInvalidSession
	}
	return sha256.Sum256(rawToken), nil
}

// Authenticate resolves a raw session token to the session it names and the
// user that session belongs to. The Session is returned alongside the User
// because a handler may need to name the acting session itself — revoking a
// user's other sessions on a self-service password change has to spare the
// one making the request — and Session.TokenHash read back from the row is
// loaded data, not a value re-derived from the cookie by a later caller.
func (s *Service) Authenticate(ctx context.Context, token string) (Session, User, error) {
	hash, err := TokenHash(token)
	if err != nil {
		return Session{}, User{}, ErrInvalidSession
	}
	session, user, err := s.sessions.FindByTokenHash(ctx, hash)
	if errors.Is(err, ErrNotFound) {
		return Session{}, User{}, ErrInvalidSession
	}
	if err != nil {
		return Session{}, User{}, fmt.Errorf("find session: %w", err)
	}

	now := s.clock.Now()
	if !now.Before(session.ExpiresAt) {
		return Session{}, User{}, ErrInvalidSession
	}
	if elapsed := now.Sub(session.LastSeenAt); elapsed >= sessionTouchInterval {
		if err := s.sessions.Touch(ctx, hash, now); err != nil {
			if errors.Is(err, ErrNotFound) {
				return Session{}, User{}, ErrInvalidSession
			}
			return Session{}, User{}, fmt.Errorf("touch session: %w", err)
		}
	}
	return session, user, nil
}

func (s *Service) Logout(ctx context.Context, token string) error {
	now := s.clock.Now()
	hash, err := TokenHash(token)
	if err == nil {
		if err := s.sessions.DeleteByTokenHash(ctx, hash); err != nil && !errors.Is(err, ErrNotFound) {
			return fmt.Errorf("delete session: %w", err)
		}
	}
	s.cleanupExpired(ctx, now)
	return nil
}

func (s *Service) cleanupExpired(ctx context.Context, now time.Time) {
	if err := s.sessions.DeleteExpired(ctx, now); err != nil {
		s.reportError(fmt.Errorf("delete expired sessions: %w", err))
	}
}
