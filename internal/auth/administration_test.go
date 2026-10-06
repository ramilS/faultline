package auth_test

import (
	"crypto/rand"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
)

func TestSetRoleRefusesToDemoteTheLastOwner(t *testing.T) {
	owner := auth.User{ID: 1, Email: "owner@example.com", Name: "Owner", Role: auth.RoleOwner}
	users := &fakeUserRepository{byID: map[int64]auth.User{1: owner}, activeOwners: 1}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	_, err := admin.SetRole(t.Context(), 1, auth.RoleMember)

	if !errors.Is(err, auth.ErrLastOwner) {
		t.Fatalf("err = %v, want ErrLastOwner", err)
	}
	if users.updateCalls != 0 {
		t.Fatalf("updateCalls = %d, want 0 — the guard must run before the write", users.updateCalls)
	}
}

func TestSetRoleDemotesAnOwnerWhenAnotherRemains(t *testing.T) {
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{1: {ID: 1, Role: auth.RoleOwner}, 2: {ID: 2, Role: auth.RoleOwner}},
		activeOwners: 2,
	}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	updated, err := admin.SetRole(t.Context(), 1, auth.RoleMember)

	if err != nil {
		t.Fatalf("SetRole: %v", err)
	}
	if updated.Role != auth.RoleMember {
		t.Fatalf("Role = %q, want member", updated.Role)
	}
}

func TestSetRoleRejectsAnInvalidRole(t *testing.T) {
	users := &fakeUserRepository{byID: map[int64]auth.User{1: {ID: 1, Role: auth.RoleMember}}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	if _, err := admin.SetRole(t.Context(), 1, auth.Role("admin")); err == nil {
		t.Fatal("SetRole() error = nil, want validation error")
	}
	if users.updateCalls != 0 {
		t.Fatalf("updateCalls = %d, want 0 for an invalid role", users.updateCalls)
	}
}

func TestSetDeactivatedRefusesTheLastOwner(t *testing.T) {
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{1: {ID: 1, Role: auth.RoleOwner}},
		activeOwners: 1,
	}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	_, err := admin.SetDeactivated(t.Context(), 2, 1, true)

	if !errors.Is(err, auth.ErrLastOwner) {
		t.Fatalf("err = %v, want ErrLastOwner", err)
	}
}

func TestSetDeactivatedClearsEverySession(t *testing.T) {
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{2: {ID: 2, Role: auth.RoleMember}},
		activeOwners: 1,
	}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if _, err := admin.SetDeactivated(t.Context(), 1, 2, true); err != nil {
		t.Fatalf("SetDeactivated: %v", err)
	}

	// Access must end on the next request, not at token expiry. Setting the
	// column without clearing sessions leaves a working cookie for up to a week.
	if sessions.deletedUserIDs != 1 || sessions.lastDeletedUserID != 2 {
		t.Fatalf("deletedUserIDs = %d, last = %d; want 1 deletion for user 2",
			sessions.deletedUserIDs, sessions.lastDeletedUserID)
	}
}

func TestSetDeactivatedRefusesTheLastOwnerWithoutClearingSessions(t *testing.T) {
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{1: {ID: 1, Role: auth.RoleOwner}},
		activeOwners: 1,
	}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if _, err := admin.SetDeactivated(t.Context(), 2, 1, true); !errors.Is(err, auth.ErrLastOwner) {
		t.Fatalf("err = %v, want ErrLastOwner", err)
	}
	if sessions.deletedUserIDs != 0 {
		t.Fatalf("deletedUserIDs = %d, want 0 — a refused deactivation must not clear sessions", sessions.deletedUserIDs)
	}
}

func TestSetDeactivatedRefusesSelfDeactivation(t *testing.T) {
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{1: {ID: 1, Role: auth.RoleOwner}, 2: {ID: 2, Role: auth.RoleOwner}},
		activeOwners: 2,
	}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	_, err := admin.SetDeactivated(t.Context(), 1, 1, true)

	if !errors.Is(err, auth.ErrSelfDeactivation) {
		t.Fatalf("err = %v, want ErrSelfDeactivation", err)
	}
	if users.updateCalls != 0 {
		t.Fatalf("updateCalls = %d, want 0 — the self-check must run before the write", users.updateCalls)
	}
}

func TestSetDeactivatedSelfCheckPrecedesLastOwnerGuard(t *testing.T) {
	// This owner is also the last active owner, so both guards would refuse
	// the change. The self-check must win: it needs no database read, and a
	// self-deactivating owner should get the specific "you did this to
	// yourself" error rather than the more general last-owner one.
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{1: {ID: 1, Role: auth.RoleOwner}},
		activeOwners: 1,
	}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	_, err := admin.SetDeactivated(t.Context(), 1, 1, true)

	if !errors.Is(err, auth.ErrSelfDeactivation) {
		t.Fatalf("err = %v, want ErrSelfDeactivation (not ErrLastOwner)", err)
	}
	if errors.Is(err, auth.ErrLastOwner) {
		t.Fatal("err wraps ErrLastOwner — the self-check must short-circuit before the repository is ever consulted")
	}
	if users.updateCalls != 0 {
		t.Fatalf("updateCalls = %d, want 0", users.updateCalls)
	}
}

func TestSetDeactivatedAllowsDeactivatingADifferentOwner(t *testing.T) {
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{1: {ID: 1, Role: auth.RoleOwner}, 2: {ID: 2, Role: auth.RoleOwner}},
		activeOwners: 2,
	}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	updated, err := admin.SetDeactivated(t.Context(), 1, 2, true)

	if err != nil {
		t.Fatalf("SetDeactivated: %v", err)
	}
	if updated.IsActive() {
		t.Fatal("IsActive() = true, want false after deactivation")
	}
	if sessions.deletedUserIDs != 1 || sessions.lastDeletedUserID != 2 {
		t.Fatalf("deletedUserIDs = %d, last = %d; want 1 deletion for user 2", sessions.deletedUserIDs, sessions.lastDeletedUserID)
	}
}

func TestReactivationDoesNotClearSessions(t *testing.T) {
	users := &fakeUserRepository{
		byID:         map[int64]auth.User{2: {ID: 2, Role: auth.RoleMember}},
		activeOwners: 1,
	}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	// actorID == id here on purpose: reactivating yourself is the one
	// deactivated-bool value the self-check must not touch.
	if _, err := admin.SetDeactivated(t.Context(), 2, 2, false); err != nil {
		t.Fatalf("SetDeactivated: %v", err)
	}

	if sessions.deletedUserIDs != 0 {
		t.Fatalf("deletedUserIDs = %d, want 0 on reactivation", sessions.deletedUserIDs)
	}
}

func TestListAssignableExcludesDeactivated(t *testing.T) {
	deactivated := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	users := &fakeUserRepository{list: []auth.User{
		{ID: 1, Email: "a@example.com", Role: auth.RoleOwner},
		{ID: 2, Email: "b@example.com", Role: auth.RoleMember, DeactivatedAt: &deactivated},
	}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	assignable, err := admin.ListAssignable(t.Context(), access.Unrestricted(), "web")

	if err != nil {
		t.Fatalf("ListAssignable: %v", err)
	}
	if len(assignable) != 1 || assignable[0].ID != 1 {
		t.Fatalf("assignable = %+v, want only user 1", assignable)
	}
}

// TestListAssignableThreadsTheSlugAndScopeToTheRepository catches
// Administration.ListAssignable dropping either argument before it reaches
// the repository: a bug like that would leave
// TestListAssignableExcludesDeactivated passing unchanged, since that test
// only checks the fake's own (slug- and scope-blind) filtering.
func TestListAssignableThreadsTheSlugAndScopeToTheRepository(t *testing.T) {
	users := &fakeUserRepository{list: []auth.User{
		{ID: 1, Email: "a@example.com", Role: auth.RoleOwner},
	}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	scope := access.Limited([]int64{7})
	if _, err := admin.ListAssignable(t.Context(), scope, "web"); err != nil {
		t.Fatalf("ListAssignable: %v", err)
	}

	if users.lastAssignableSlug != "web" {
		t.Fatalf("lastAssignableSlug = %q, want %q", users.lastAssignableSlug, "web")
	}
	recordedScope, ok := users.lastAssignableScope.(access.Scope)
	if !ok {
		t.Fatalf("lastAssignableScope = %T, want access.Scope", users.lastAssignableScope)
	}
	if recordedScope.IsUnrestricted() || !slices.Equal(recordedScope.ProjectIDs(), []int64{7}) {
		t.Fatalf("lastAssignableScope = %+v, want the Limited([7]) scope passed in", recordedScope)
	}
}

func TestListReturnsEveryUserIncludingDeactivated(t *testing.T) {
	deactivated := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	users := &fakeUserRepository{list: []auth.User{
		{ID: 1, Email: "a@example.com", Role: auth.RoleOwner},
		{ID: 2, Email: "b@example.com", Role: auth.RoleMember, DeactivatedAt: &deactivated},
	}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	all, err := admin.List(t.Context())

	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List() = %+v, want both users", all)
	}
}

func TestAdministrationCreateNormalizesValidatesAndHashes(t *testing.T) {
	users := &fakeUserRepository{}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	user, err := admin.Create(t.Context(), " Member@Example.COM ", "  Member  ", "correct horse", auth.RoleMember)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.Email != "member@example.com" || user.Name != "Member" || user.Role != auth.RoleMember {
		t.Fatalf("Create() = %+v", user)
	}
	if len(users.created) != 1 {
		t.Fatalf("repository Create() calls = %d, want 1", len(users.created))
	}
	ok, err := auth.VerifyPassword(users.created[0].PasswordHash, "correct horse")
	if err != nil || !ok {
		t.Fatalf("stored password verification = (%v, %v)", ok, err)
	}

	for name, input := range map[string][4]string{
		"empty email":    {" ", "Name", "correct horse", "member"},
		"invalid email":  {"not-an-email", "Name", "correct horse", "member"},
		"empty name":     {"a@example.com", " \t", "correct horse", "member"},
		"short password": {"a@example.com", "Name", "too short", "member"},
		"invalid role":   {"a@example.com", "Name", "correct horse", "admin"},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(users.created)
			if _, err := admin.Create(t.Context(), input[0], input[1], input[2], auth.Role(input[3])); err == nil {
				t.Fatal("Create() error = nil, want validation error")
			}
			if len(users.created) != before {
				t.Fatal("invalid user reached repository")
			}
		})
	}
}

func TestAdministrationCreatePreservesConflictAsStableError(t *testing.T) {
	users := &fakeUserRepository{createErr: auth.ErrConflict}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	_, err := admin.Create(t.Context(), "a@example.com", "Name", "correct horse", auth.RoleMember)

	if !errors.Is(err, auth.ErrConflict) {
		t.Fatalf("Create() error = %v, want ErrConflict", err)
	}
}

func TestRenameTrimsAndRejectsAnEmptyName(t *testing.T) {
	users := &fakeUserRepository{byID: map[int64]auth.User{1: {ID: 1, Name: "Old Name"}}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	updated, err := admin.Rename(t.Context(), 1, "  New Name  ")
	if err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if updated.Name != "New Name" {
		t.Fatalf("Name = %q, want %q", updated.Name, "New Name")
	}

	if _, err := admin.Rename(t.Context(), 1, "   "); err == nil {
		t.Fatal("Rename() error = nil, want error for a blank name")
	}
	if users.updateCalls != 1 {
		t.Fatalf("updateCalls = %d, want 1 — the blank rename must not reach the repository", users.updateCalls)
	}
}

func TestSetPasswordValidatesAndHashesBeforeWriting(t *testing.T) {
	users := &fakeUserRepository{byID: map[int64]auth.User{1: {ID: 1}}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	if err := admin.SetPassword(t.Context(), 1, "too short"); err == nil {
		t.Fatal("SetPassword() error = nil, want a password-validation error")
	}
	if users.passwordWrites != 0 {
		t.Fatalf("passwordWrites = %d, want 0 for an invalid password", users.passwordWrites)
	}

	if err := admin.SetPassword(t.Context(), 1, "a-valid-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if users.passwordWrites != 1 {
		t.Fatalf("passwordWrites = %d, want 1", users.passwordWrites)
	}
	ok, err := auth.VerifyPassword(users.byID[1].PasswordHash, "a-valid-password")
	if err != nil || !ok {
		t.Fatalf("stored password verification = (%v, %v)", ok, err)
	}
}

// TestSetPasswordClearsTheTargetsSessions pins the containment property an
// administrative reset exists for: every reachable caller acts on somebody
// else (the HTTP route refuses a self-targeted reset, the CLI has no session
// of its own), so the holder of the target's old cookie must not keep access
// after the reset.
func TestSetPasswordClearsTheTargetsSessions(t *testing.T) {
	users := &fakeUserRepository{byID: map[int64]auth.User{1: {ID: 1}}}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if err := admin.SetPassword(t.Context(), 1, "a-valid-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if sessions.deletedUserIDs != 1 || sessions.lastDeletedUserID != 1 {
		t.Fatalf("deletedUserIDs = %d, lastDeletedUserID = %d; want 1 and 1",
			sessions.deletedUserIDs, sessions.lastDeletedUserID)
	}
	if len(sessions.keptCalls) != 0 {
		t.Fatalf("keptCalls = %v, want none — an administrative reset spares no session", sessions.keptCalls)
	}
}

// TestSetPasswordLeavesOtherUsersSessionsAlone proves the revocation is
// scoped to the target: resetting one colleague's password must not sign the
// rest of the deployment out.
func TestSetPasswordLeavesOtherUsersSessionsAlone(t *testing.T) {
	users := &fakeUserRepository{byID: map[int64]auth.User{1: {ID: 1}, 2: {ID: 2}}}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if err := admin.SetPassword(t.Context(), 2, "a-valid-password"); err != nil {
		t.Fatalf("SetPassword: %v", err)
	}
	if sessions.deletedUserIDs != 1 || sessions.lastDeletedUserID != 2 {
		t.Fatalf("deletedUserIDs = %d, lastDeletedUserID = %d; want 1 and 2 (only the target)",
			sessions.deletedUserIDs, sessions.lastDeletedUserID)
	}
}

// TestSetPasswordReportsAFailedRevocation keeps a failed session clear from
// being reported as a successful reset: an operator told "done" while the
// stolen cookie still works is the failure this whole change removes.
func TestSetPasswordReportsAFailedRevocation(t *testing.T) {
	users := &fakeUserRepository{byID: map[int64]auth.User{1: {ID: 1}}}
	sessions := &fakeSessionRepository{deleteErr: errors.New("connection reset")}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if err := admin.SetPassword(t.Context(), 1, "a-valid-password"); err == nil {
		t.Fatal("SetPassword() error = nil, want the revocation failure reported")
	}
}

func TestChangeOwnPasswordRejectsAWrongCurrentPassword(t *testing.T) {
	hash, err := auth.HashPassword("the-real-one-12", rand.Reader)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUserRepository{byID: map[int64]auth.User{5: {ID: 5, PasswordHash: hash, Role: auth.RoleMember}}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	err = admin.ChangeOwnPassword(t.Context(), 5, "not-the-real-one", "a-new-password-12", actingSession)

	if !errors.Is(err, auth.ErrCurrentPasswordMismatch) {
		t.Fatalf("err = %v, want ErrCurrentPasswordMismatch", err)
	}
	if users.passwordWrites != 0 {
		t.Fatalf("passwordWrites = %d, want 0 — verification must precede the write", users.passwordWrites)
	}
}

func TestChangeOwnPasswordAcceptsTheCorrectCurrentPassword(t *testing.T) {
	hash, err := auth.HashPassword("the-real-one-12", rand.Reader)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUserRepository{byID: map[int64]auth.User{5: {ID: 5, PasswordHash: hash, Role: auth.RoleMember}}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	if err := admin.ChangeOwnPassword(t.Context(), 5, "the-real-one-12", "a-new-password-12", actingSession); err != nil {
		t.Fatalf("ChangeOwnPassword: %v", err)
	}
	if users.passwordWrites != 1 {
		t.Fatalf("passwordWrites = %d, want 1", users.passwordWrites)
	}
}

func TestChangeOwnPasswordAppliesTheLengthRule(t *testing.T) {
	hash, err := auth.HashPassword("the-real-one-12", rand.Reader)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUserRepository{byID: map[int64]auth.User{5: {ID: 5, PasswordHash: hash}}}
	admin := auth.NewAdministration(users, &fakeSessionRepository{}, auth.ServiceOptions{})

	// Correct current password, too-short replacement. The self-service path must
	// not be a way around ValidatePassword.
	err = admin.ChangeOwnPassword(t.Context(), 5, "the-real-one-12", "short", actingSession)

	if err == nil || errors.Is(err, auth.ErrCurrentPasswordMismatch) {
		t.Fatalf("err = %v, want a password-validation error", err)
	}
	if users.passwordWrites != 0 {
		t.Fatalf("passwordWrites = %d, want 0", users.passwordWrites)
	}
}

// actingSession stands in for the token hash of the session a self-service
// password change arrives on — the one session ChangeOwnPassword must spare.
var actingSession = [32]byte{'a', 'c', 't', 'i', 'n', 'g'}

// TestChangeOwnPasswordRevokesEveryOtherSessionAndKeepsTheActingOne is the
// point of the screen: a user who suspects a session is compromised changes
// their password and the other session ends. Sparing the acting session is
// equally load-bearing — being signed out of the tab you are working in
// would make the control feel broken.
func TestChangeOwnPasswordRevokesEveryOtherSessionAndKeepsTheActingOne(t *testing.T) {
	hash, err := auth.HashPassword("the-real-one-12", rand.Reader)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUserRepository{byID: map[int64]auth.User{5: {ID: 5, PasswordHash: hash}}}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if err := admin.ChangeOwnPassword(t.Context(), 5, "the-real-one-12", "a-new-password-12", actingSession); err != nil {
		t.Fatalf("ChangeOwnPassword: %v", err)
	}

	if len(sessions.keptCalls) != 1 {
		t.Fatalf("keptCalls = %v, want exactly one revocation", sessions.keptCalls)
	}
	if got := sessions.keptCalls[0]; got.userID != 5 || got.keep != actingSession {
		t.Fatalf("revocation = %+v, want userID 5 sparing the acting session", got)
	}
	if sessions.deletedUserIDs != 0 {
		t.Fatalf("deletedUserIDs = %d, want 0 — the acting session must not be swept up", sessions.deletedUserIDs)
	}
}

// TestChangeOwnPasswordRevokesNothingWhenVerificationFails keeps the
// revocation behind the current-password proof: a wrong guess must not be a
// way to sign the account's other sessions out.
func TestChangeOwnPasswordRevokesNothingWhenVerificationFails(t *testing.T) {
	hash, err := auth.HashPassword("the-real-one-12", rand.Reader)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUserRepository{byID: map[int64]auth.User{5: {ID: 5, PasswordHash: hash}}}
	sessions := &fakeSessionRepository{}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if err := admin.ChangeOwnPassword(t.Context(), 5, "not-the-real-one", "a-new-password-12", actingSession); !errors.Is(err, auth.ErrCurrentPasswordMismatch) {
		t.Fatalf("err = %v, want ErrCurrentPasswordMismatch", err)
	}
	if len(sessions.keptCalls) != 0 {
		t.Fatalf("keptCalls = %v, want none", sessions.keptCalls)
	}
}

// TestChangeOwnPasswordReportsAFailedRevocation refuses to call the change a
// success when the other sessions may still be alive.
func TestChangeOwnPasswordReportsAFailedRevocation(t *testing.T) {
	hash, err := auth.HashPassword("the-real-one-12", rand.Reader)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUserRepository{byID: map[int64]auth.User{5: {ID: 5, PasswordHash: hash}}}
	sessions := &fakeSessionRepository{keepErr: errors.New("connection reset")}
	admin := auth.NewAdministration(users, sessions, auth.ServiceOptions{})

	if err := admin.ChangeOwnPassword(t.Context(), 5, "the-real-one-12", "a-new-password-12", actingSession); err == nil {
		t.Fatal("ChangeOwnPassword() error = nil, want the revocation failure reported")
	}
	if users.passwordWrites != 1 {
		t.Fatalf("passwordWrites = %d, want 1 — the password write precedes revocation deliberately", users.passwordWrites)
	}
}
