//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// TestAssignableRosterIsScopedToTheProject exercises the callee side: given
// a caller who may reach "web" (an owner, unrestricted), which of the
// project's own users come back as assignable.
func TestAssignableRosterIsScopedToTheProject(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repositories := postgres.NewAuthRepository(pool)

	// alice reaches web through a group; bob reaches nothing; olivia is an owner.
	alice, bob, olivia, web := seedAssignmentFixture(t, pool)

	users, err := repositories.Users.ListAssignableForProject(ctx, access.Unrestricted(), "web")
	if err != nil {
		t.Fatalf("ListAssignableForProject: %v", err)
	}

	found := map[int64]bool{}
	for _, user := range users {
		found[user.ID] = true
	}
	if !found[alice] {
		t.Error("a member with access to the project is not assignable")
	}
	if found[bob] {
		t.Error("a member with no access to the project is assignable")
	}
	if !found[olivia] {
		t.Error("an owner is not assignable; owners read every project")
	}
	_ = web
}

// TestAssignableRosterRefusesACallerOutOfScope exercises the caller side —
// the CRITICAL finding this task fixes. A caller whose own scope does not
// include "web" must be refused with the not-found sentinel, and must not
// receive any roster at all: scope is bound only on the group-membership
// branch of the roster query, so an empty-success answer here would still
// leak the fixed owner roster through the `u.role = 'owner'` branch.
func TestAssignableRosterRefusesACallerOutOfScope(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repositories := postgres.NewAuthRepository(pool)

	seedAssignmentFixture(t, pool)
	mobile := seedProject(t, ctx, pool, "mobile")

	users, err := repositories.Users.ListAssignableForProject(ctx, access.Limited([]int64{mobile}), "web")

	if !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("err = %v, want auth.ErrNotFound", err)
	}
	if len(users) != 0 {
		t.Fatalf("users = %+v, want none — an out-of-scope caller must not see the owner roster", users)
	}
}

func TestCanBeAssignedRefusesAUserWithoutAccess(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)

	alice, bob, _, web := seedAssignmentFixture(t, pool)
	localIssue := seedIssueForProject(t, pool, web)

	allowed, err := repository.CanBeAssigned(ctx, localIssue, alice)
	if err != nil || !allowed {
		t.Errorf("CanBeAssigned(alice) = (%v, %v), want (true, nil)", allowed, err)
	}
	allowed, err = repository.CanBeAssigned(ctx, localIssue, bob)
	if err != nil || allowed {
		t.Errorf("CanBeAssigned(bob) = (%v, %v), want (false, nil)", allowed, err)
	}
}

func TestCanBeAssignedRefusesADeactivatedUserWithAccess(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)

	alice, _, _, web := seedAssignmentFixture(t, pool)
	localIssue := seedIssueForProject(t, pool, web)
	mustExec(t, pool, `UPDATE users SET deactivated_at = now() WHERE id = $1`, alice)

	allowed, err := repository.CanBeAssigned(ctx, localIssue, alice)
	if err != nil || allowed {
		t.Errorf("CanBeAssigned = (%v, %v), want (false, nil)", allowed, err)
	}
}

// TestCanBeAssignedRefusesAMissingUser pins the branch a foreign key would
// otherwise leave untested now that the old activity-only check (and its
// dedicated missing-user case) is gone: a user id that names nobody must
// read the same as one that names a deactivated or access-less account.
func TestCanBeAssignedRefusesAMissingUser(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)

	_, _, _, web := seedAssignmentFixture(t, pool)
	localIssue := seedIssueForProject(t, pool, web)

	allowed, err := repository.CanBeAssigned(ctx, localIssue, 999999)
	if err != nil || allowed {
		t.Errorf("CanBeAssigned(missing user) = (%v, %v), want (false, nil)", allowed, err)
	}
}

// seedAssignmentFixture seeds a project ("web") reachable through a group,
// a member who belongs to that group (alice), a member who belongs to no
// group (bob), and an owner (olivia, who reaches every project without any
// group membership). It returns their ids and the project's id.
func seedAssignmentFixture(t *testing.T, pool *pgxpool.Pool) (alice, bob, olivia, webProjectID int64) {
	t.Helper()
	ctx := t.Context()

	web := seedProject(t, ctx, pool, "web")

	alice = seedUser(t, ctx, pool, "alice@example.com", "Alice")
	bob = seedUser(t, ctx, pool, "bob@example.com", "Bob")
	olivia = seedUser(t, ctx, pool, "olivia@example.com", "Olivia")
	mustExec(t, pool, `UPDATE users SET role = 'owner' WHERE id = $1`, olivia)

	var frontend int64
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name) VALUES ('frontend') RETURNING id`).Scan(&frontend); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	mustExec(t, pool, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, frontend, alice)
	mustExec(t, pool, `INSERT INTO group_projects (group_id, project_id) VALUES ($1, $2)`, frontend, web)

	return alice, bob, olivia, web
}

// seedIssueForProject inserts a minimal issue row directly (bypassing the
// batch writer other issue fixtures use) since these tests only need an id
// to pass to CanBeAssigned, not the facets ingestion populates.
func seedIssueForProject(t *testing.T, pool *pgxpool.Pool, projectID int64) int64 {
	t.Helper()
	ctx := t.Context()
	var issueID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, first_seen, last_seen)
		VALUES ($1, $2, 'boom', now(), now())
		RETURNING id
	`, projectID, hex64(uuid.NewString())).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}
	return issueID
}
