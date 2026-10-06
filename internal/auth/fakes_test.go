package auth_test

import (
	"context"
	"time"

	"github.com/ramilS/faultline/internal/auth"
)

// fakeUserRepository is the shared UserRepository test double for the
// administration service and the tests built on it (self-service password
// change). Its Update mirrors the guard the real Postgres
// adapter enforces atomically: a role change away from owner, or a
// deactivation, is refused when activeOwners would drop to zero. That lets
// the same guard behavior be exercised here without a real database — the
// atomicity guarantee itself is exercised separately, against Postgres, in
// internal/storage/postgres/auth_integration_test.go.
type fakeUserRepository struct {
	byID         map[int64]auth.User
	list         []auth.User
	activeOwners int
	updateCalls  int

	created        []auth.NewUser
	createErr      error
	passwordWrites int

	// lastAssignableScope and lastAssignableSlug record what
	// ListAssignableForProject was actually called with, so a test can
	// assert Administration.ListAssignable threaded both arguments through
	// rather than dropping one on the floor before reaching the repository.
	lastAssignableScope auth.ProjectScope
	lastAssignableSlug  string
}

func (r *fakeUserRepository) Create(_ context.Context, input auth.NewUser) (auth.User, error) {
	r.created = append(r.created, input)
	if r.createErr != nil {
		return auth.User{}, r.createErr
	}
	if r.byID == nil {
		r.byID = map[int64]auth.User{}
	}
	user := auth.User{
		ID:           int64(len(r.byID) + 1),
		Email:        input.Email,
		Name:         input.Name,
		PasswordHash: input.PasswordHash,
		Role:         input.Role,
	}
	r.byID[user.ID] = user
	return withoutPasswordHash(user), nil
}

// withoutPasswordHash strips the field the UserRepository contract says
// Create and Update do not return, so this fake cannot let a caller read a
// hash the real repository would never have loaded.
func withoutPasswordHash(user auth.User) auth.User {
	user.PasswordHash = ""
	return user
}

func (r *fakeUserRepository) FindByEmail(_ context.Context, email string) (auth.User, error) {
	for _, user := range r.byID {
		if user.Email == email {
			return user, nil
		}
	}
	return auth.User{}, auth.ErrNotFound
}

func (r *fakeUserRepository) FindByID(_ context.Context, id int64) (auth.User, error) {
	user, ok := r.byID[id]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}
	return user, nil
}

func (r *fakeUserRepository) List(context.Context) ([]auth.User, error) {
	return r.list, nil
}

func (r *fakeUserRepository) ListActive(context.Context) ([]auth.User, error) {
	active := make([]auth.User, 0, len(r.list))
	for _, user := range r.list {
		if user.IsActive() {
			active = append(active, user)
		}
	}
	return active, nil
}

// ListAssignableForProject ignores scope and slug when computing its
// result — this fake models no groups, so it filters exactly as ListActive
// does — but it records both, so TestListAssignableThreadsTheSlugAndScope
// can catch Administration.ListAssignable silently dropping either one
// before it reaches the repository. The project-scoping half of the
// contract itself is exercised against real Postgres, in
// internal/storage/postgres/assignable_integration_test.go.
func (r *fakeUserRepository) ListAssignableForProject(_ context.Context, scope auth.ProjectScope, slug string) ([]auth.User, error) {
	r.lastAssignableScope = scope
	r.lastAssignableSlug = slug
	return r.ListActive(context.Background())
}

// Update applies changes in-memory, refusing (without writing, and without
// incrementing updateCalls) any change that would strip the last active
// owner of ownership or deactivate them.
func (r *fakeUserRepository) Update(_ context.Context, id int64, changes auth.UserChanges) (auth.User, error) {
	subject, ok := r.byID[id]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}

	removesActiveOwner := subject.IsOwner() && subject.IsActive() &&
		((changes.Role != nil && *changes.Role != auth.RoleOwner) ||
			(changes.Deactivated != nil && *changes.Deactivated))
	if removesActiveOwner && r.activeOwners <= 1 {
		return auth.User{}, auth.ErrLastOwner
	}

	r.updateCalls++
	if changes.Name != nil {
		subject.Name = *changes.Name
	}
	if changes.Role != nil {
		subject.Role = *changes.Role
	}
	if changes.Deactivated != nil {
		if *changes.Deactivated {
			if subject.DeactivatedAt == nil {
				now := time.Now().UTC()
				subject.DeactivatedAt = &now
			}
		} else {
			subject.DeactivatedAt = nil
		}
	}
	if r.byID == nil {
		r.byID = map[int64]auth.User{}
	}
	r.byID[id] = subject
	return withoutPasswordHash(subject), nil
}

func (r *fakeUserRepository) UpdatePasswordHash(_ context.Context, id int64, hash string) error {
	subject, ok := r.byID[id]
	if !ok {
		return auth.ErrNotFound
	}
	r.passwordWrites++
	subject.PasswordHash = hash
	r.byID[id] = subject
	return nil
}

// fakeSessionRepository is the shared SessionRepository test double for
// administration tests. Only the two user-scoped deletes are exercised; the
// rest are no-op stubs to satisfy the interface.
type fakeSessionRepository struct {
	deletedUserIDs    int
	lastDeletedUserID int64
	deleteErr         error

	keptCalls []keepSessionCall
	keepErr   error
}

// keepSessionCall records one DeleteByUserIDExcept call: whose sessions were
// revoked and which one was spared.
type keepSessionCall struct {
	userID int64
	keep   [32]byte
}

func (r *fakeSessionRepository) Create(context.Context, auth.NewSession) error { return nil }

func (r *fakeSessionRepository) FindByTokenHash(context.Context, [32]byte) (auth.Session, auth.User, error) {
	return auth.Session{}, auth.User{}, auth.ErrNotFound
}

func (r *fakeSessionRepository) Touch(context.Context, [32]byte, time.Time) error { return nil }

func (r *fakeSessionRepository) DeleteByTokenHash(context.Context, [32]byte) error { return nil }

func (r *fakeSessionRepository) DeleteExpired(context.Context, time.Time) error { return nil }

func (r *fakeSessionRepository) DeleteByUserID(_ context.Context, userID int64) error {
	if r.deleteErr != nil {
		return r.deleteErr
	}
	r.deletedUserIDs++
	r.lastDeletedUserID = userID
	return nil
}

func (r *fakeSessionRepository) DeleteByUserIDExcept(_ context.Context, userID int64, keep [32]byte) error {
	if r.keepErr != nil {
		return r.keepErr
	}
	r.keptCalls = append(r.keptCalls, keepSessionCall{userID: userID, keep: keep})
	return nil
}
