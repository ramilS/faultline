package auth

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrNotFound = errors.New("not found")
	ErrConflict = errors.New("conflict")

	// ErrInvalidRole reports a role value that is neither RoleOwner nor
	// RoleMember. Exported so internal/api can map it to a 400 by identity
	// (errors.Is) instead of matching its message text.
	ErrInvalidRole = errors.New("invalid role")
)

// Role is a user's authorization level. Exactly two roles exist; there is no
// third value or alias.
type Role string

const (
	RoleOwner  Role = "owner"
	RoleMember Role = "member"
)

// ParseRole validates value against the known roles, erroring on anything
// else.
func ParseRole(value string) (Role, error) {
	switch role := Role(value); role {
	case RoleOwner, RoleMember:
		return role, nil
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidRole, value)
	}
}

type User struct {
	ID    int64
	Email string
	Name  string
	// PasswordHash is carried only by the two lookups UserRepository
	// documents as carrying it. Which those are, and why the rest leave this
	// empty, is stated on UserRepository below.
	PasswordHash  string
	Role          Role
	DeactivatedAt *time.Time
	CreatedAt     time.Time
}

// IsActive reports whether the account has not been deactivated.
func (u User) IsActive() bool {
	return u.DeactivatedAt == nil
}

// IsOwner reports whether the account holds the owner role.
func (u User) IsOwner() bool {
	return u.Role == RoleOwner
}

type NewUser struct {
	Email        string
	Name         string
	PasswordHash string
	Role         Role
}

type Session struct {
	ID         int64
	UserID     int64
	TokenHash  [32]byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

type NewSession struct {
	UserID     int64
	TokenHash  [32]byte
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

// UserChanges is a partial update to a user. Pointer fields distinguish "not
// supplied" from "set to zero" — a PATCH that omits Role must not reset it to
// "".
type UserChanges struct {
	Name        *string
	Role        *Role
	Deactivated *bool
}

// ProjectScope is the minimal view of a caller's project access that this
// package needs to bound ListAssignableForProject: whether every project is
// reachable, and if not, which ones are.
//
// It is declared locally rather than importing internal/access's Scope type
// because internal/access itself imports internal/auth (Resolver.ScopeFor
// takes a User) — importing it back here would cycle. Any access.Scope value
// satisfies this interface structurally, with no import required on either
// side: a caller in internal/api or internal/command passes its
// access.Scope straight through.
type ProjectScope interface {
	IsUnrestricted() bool
	ProjectIDs() []int64
}

// UserRepository is the account store. Exactly two of its methods —
// FindByEmail and FindByID — return User.PasswordHash populated, because
// exactly two callers verify a password: Service.Login and
// Administration.ChangeOwnPassword. Every other method leaves it empty, and
// implementations must not select the column for them.
//
// That split is not tidiness. An SSO-provisioned account stores SQL NULL in
// users.password_hash, so a projection that selects the column has to
// COALESCE it or fail to scan; a list projection that never reads the value
// is a NULL-scan crash waiting for the next person to add a column to it,
// and it loads credential material into memory for handlers that have no use
// for it.
type UserRepository interface {
	// Create returns the new account without its password hash: the caller
	// supplied the hash, so returning it teaches nothing.
	Create(context.Context, NewUser) (User, error)
	// FindByEmail returns the account with User.PasswordHash populated —
	// empty when the row's column is NULL. This is the password sign-in
	// lookup.
	FindByEmail(context.Context, string) (User, error)
	// FindByID returns the account with User.PasswordHash populated — empty
	// when the row's column is NULL. This is the self-service
	// password-change lookup.
	FindByID(context.Context, int64) (User, error)
	// List returns every user, ordered by email, without password hashes.
	List(context.Context) ([]User, error)
	// ListActive returns users with deactivated_at IS NULL, ordered by
	// email, without password hashes.
	ListActive(context.Context) ([]User, error)
	// ListAssignableForProject returns the active users who may be assigned
	// an issue in the project identified by slug: every owner, plus the
	// members who reach it through a group. slug is resolved within scope
	// first — a slug that exists but falls outside scope must read
	// identically to one that does not exist at all, so the caller learns
	// nothing about a project it cannot reach. When it does not resolve,
	// this returns ErrNotFound rather than an empty slice: scope is bound
	// only on the group-membership half of the query, so an empty-slice
	// answer would still surface every owner. Password hashes are not
	// returned.
	ListAssignableForProject(ctx context.Context, scope ProjectScope, slug string) ([]User, error)
	// Update applies changes and returns the updated account, without its
	// password hash — UpdatePasswordHash is the only way to change one.
	Update(context.Context, int64, UserChanges) (User, error)
	UpdatePasswordHash(context.Context, int64, string) error
}

// ProviderGoogle names the only identity provider faultline speaks to. It is
// stored in user_identities.provider so a second provider can be added later
// without rewriting existing rows.
const ProviderGoogle = "google"

// NewIdentityUser is an account created from an external identity. It carries
// no password: CreateWithIdentity writes SQL NULL, and Login refuses an
// account whose hash reads back empty.
type NewIdentityUser struct {
	Email string
	Name  string
	Role  Role
}

// IdentityRepository maps identity-provider subjects to faultline accounts.
type IdentityRepository interface {
	// FindUserBySubject returns the account linked to (provider, subject),
	// or ErrNotFound. The account carries no password hash: this path never
	// verifies one.
	FindUserBySubject(ctx context.Context, provider, subject string) (User, error)
	// Link attaches an identity to an existing account. It returns
	// ErrConflict when the identity was claimed concurrently.
	Link(ctx context.Context, provider, subject string, userID int64) error
	// CreateWithIdentity creates a passwordless account and its identity in
	// one transaction, so a failure cannot leave an account nobody can reach.
	// It returns ErrConflict when either the email or the identity was taken
	// concurrently.
	CreateWithIdentity(ctx context.Context, provider, subject string, input NewIdentityUser) (User, error)
}

type SessionRepository interface {
	Create(context.Context, NewSession) error
	// FindByTokenHash returns the session and the account it belongs to. The
	// account carries no password hash: a request authenticated by a session
	// cookie never verifies a password.
	FindByTokenHash(context.Context, [32]byte) (Session, User, error)
	Touch(context.Context, [32]byte, time.Time) error
	DeleteByTokenHash(context.Context, [32]byte) error
	DeleteExpired(context.Context, time.Time) error
	DeleteByUserID(context.Context, int64) error
	// DeleteByUserIDExcept removes every session belonging to userID apart
	// from the one whose token hash is keep. It is what a self-service
	// password change uses: every other session is revoked, and the caller's
	// own — the session the request arrived on — survives, so changing a
	// password never signs the user out of the tab they are working in. A
	// keep value that matches no session degrades to DeleteByUserID, which is
	// the safe direction to fail.
	DeleteByUserIDExcept(ctx context.Context, userID int64, keep [32]byte) error
}
