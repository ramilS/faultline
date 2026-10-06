//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/group"
	"github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// Replace semantics, not merge: a user absent from the new set loses access.
// Asserting only that the new members are present would pass for a merge,
// which would silently retain access someone meant to revoke.
func TestReplaceMembersRemovesAbsentUsers(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	created, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	alice := seedUser(t, ctx, pool, "alice@example.com", "Alice")
	bob := seedUser(t, ctx, pool, "bob@example.com", "Bob")

	if err := repository.ReplaceMembers(ctx, created.ID, []int64{alice, bob}); err != nil {
		t.Fatalf("ReplaceMembers: %v", err)
	}
	if err := repository.ReplaceMembers(ctx, created.ID, []int64{bob}); err != nil {
		t.Fatalf("ReplaceMembers: %v", err)
	}

	detail, err := repository.Find(ctx, created.ID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(detail.UserIDs) != 1 || detail.UserIDs[0] != bob {
		t.Errorf("user ids = %v, want just bob (%d)", detail.UserIDs, bob)
	}
}

// A raw pgx unique-violation must not reach the handler: the API maps by
// error identity, so an untranslated driver error becomes a 500 where the
// caller should see a 409.
func TestDuplicateNameIsAConflict(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	if _, err := repository.Create(ctx, group.NewGroup{Name: "backend"}); err != nil {
		t.Fatalf("first Create: %v", err)
	}
	_, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if !errors.Is(err, group.ErrConflict) {
		t.Fatalf("err = %v, want group.ErrConflict", err)
	}
}

func TestGroupRepositoryRoundTrip(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	created, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	found, err := repository.Find(ctx, created.ID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if found.Group.Name != "backend" {
		t.Errorf("found name = %q, want %q", found.Group.Name, "backend")
	}

	alice := seedUser(t, ctx, pool, "alice@example.com", "Alice")
	projectID := seedProject(t, ctx, pool, "web")

	if err := repository.ReplaceMembers(ctx, created.ID, []int64{alice}); err != nil {
		t.Fatalf("ReplaceMembers: %v", err)
	}
	if err := repository.ReplaceProjects(ctx, created.ID, []int64{projectID}); err != nil {
		t.Fatalf("ReplaceProjects: %v", err)
	}

	empty, err := repository.Create(ctx, group.NewGroup{Name: "empty"})
	if err != nil {
		t.Fatalf("Create empty: %v", err)
	}

	groups, err := repository.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	counts := make(map[int64]group.Group, len(groups))
	for _, current := range groups {
		counts[current.ID] = current
	}
	populated, ok := counts[created.ID]
	if !ok {
		t.Fatalf("List did not return the populated group %d", created.ID)
	}
	if populated.MemberCount != 1 || populated.ProjectCount != 1 {
		t.Errorf("populated counts = (%d, %d), want (1, 1)", populated.MemberCount, populated.ProjectCount)
	}
	emptyListed, ok := counts[empty.ID]
	if !ok {
		t.Fatalf("List dropped the empty group %d", empty.ID)
	}
	if emptyListed.MemberCount != 0 || emptyListed.ProjectCount != 0 {
		t.Errorf("empty counts = (%d, %d), want (0, 0)", emptyListed.MemberCount, emptyListed.ProjectCount)
	}
}

func TestReplaceMembersIsIdempotent(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	created, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	alice := seedUser(t, ctx, pool, "alice@example.com", "Alice")

	for i := range 2 {
		if err := repository.ReplaceMembers(ctx, created.ID, []int64{alice}); err != nil {
			t.Fatalf("ReplaceMembers[%d]: %v", i, err)
		}
	}

	detail, err := repository.Find(ctx, created.ID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(detail.UserIDs) != 1 || detail.UserIDs[0] != alice {
		t.Errorf("user ids = %v, want just alice (%d)", detail.UserIDs, alice)
	}
}

// As TestReplaceMembersRemovesAbsentUsers, for group_projects.
func TestReplaceProjectsRemovesAbsentProjects(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	created, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	web := seedProject(t, ctx, pool, "web")
	api := seedProject(t, ctx, pool, "api")

	if err := repository.ReplaceProjects(ctx, created.ID, []int64{web, api}); err != nil {
		t.Fatalf("ReplaceProjects: %v", err)
	}
	if err := repository.ReplaceProjects(ctx, created.ID, []int64{api}); err != nil {
		t.Fatalf("ReplaceProjects: %v", err)
	}

	detail, err := repository.Find(ctx, created.ID)
	if err != nil {
		t.Fatalf("Find: %v", err)
	}
	if len(detail.ProjectIDs) != 1 || detail.ProjectIDs[0] != api {
		t.Errorf("project ids = %v, want just api (%d)", detail.ProjectIDs, api)
	}
}

func TestDeleteCascadesMembershipAndGrants(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	created, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	alice := seedUser(t, ctx, pool, "alice@example.com", "Alice")
	web := seedProject(t, ctx, pool, "web")

	if err := repository.ReplaceMembers(ctx, created.ID, []int64{alice}); err != nil {
		t.Fatalf("ReplaceMembers: %v", err)
	}
	if err := repository.ReplaceProjects(ctx, created.ID, []int64{web}); err != nil {
		t.Fatalf("ReplaceProjects: %v", err)
	}

	if err := repository.Delete(ctx, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var memberRows, projectRows int
	mustScanRow(t, pool, []any{&memberRows}, `SELECT count(*) FROM group_members WHERE group_id = $1`, created.ID)
	mustScanRow(t, pool, []any{&projectRows}, `SELECT count(*) FROM group_projects WHERE group_id = $1`, created.ID)
	if memberRows != 0 {
		t.Errorf("group_members rows for deleted group = %d, want 0", memberRows)
	}
	if projectRows != 0 {
		t.Errorf("group_projects rows for deleted group = %d, want 0", projectRows)
	}
}

func TestFindReportsAMissingGroupAsNotFound(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	_, err := repository.Find(ctx, 999999)
	if !errors.Is(err, group.ErrNotFound) {
		t.Fatalf("err = %v, want group.ErrNotFound", err)
	}
}

// An empty user_ids body takes the all-DELETE-no-INSERT path, which commits
// successfully even against a missing group id unless the group's existence
// is checked first — so this and the non-empty case below must both be
// covered, since today they take different paths (200 vs 500).
func TestReplaceMembersOnMissingGroupReturnsNotFoundForAnEmptySet(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	err := repository.ReplaceMembers(ctx, 999999, nil)
	if !errors.Is(err, group.ErrNotFound) {
		t.Fatalf("err = %v, want group.ErrNotFound", err)
	}
}

// A non-empty user_ids body against a missing group hits the INSERT's
// foreign-key violation on group_members.group_id, which must still resolve
// to ErrNotFound rather than the FK-violation error the same insert would
// report for an unknown user id (TestReplaceMembersWithUnknownUserIDReturnsUnknownReference).
func TestReplaceMembersOnMissingGroupReturnsNotFoundForANonEmptySet(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	alice := seedUser(t, ctx, pool, "alice@example.com", "Alice")
	err := repository.ReplaceMembers(ctx, 999999, []int64{alice})
	if !errors.Is(err, group.ErrNotFound) {
		t.Fatalf("err = %v, want group.ErrNotFound", err)
	}
}

func TestReplaceProjectsOnMissingGroupReturnsNotFoundForAnEmptySet(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	err := repository.ReplaceProjects(ctx, 999999, nil)
	if !errors.Is(err, group.ErrNotFound) {
		t.Fatalf("err = %v, want group.ErrNotFound", err)
	}
}

func TestReplaceProjectsOnMissingGroupReturnsNotFoundForANonEmptySet(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	web := seedProject(t, ctx, pool, "web")
	err := repository.ReplaceProjects(ctx, 999999, []int64{web})
	if !errors.Is(err, group.ErrNotFound) {
		t.Fatalf("err = %v, want group.ErrNotFound", err)
	}
}

// An unknown user id against a valid, existing group must be reported
// distinctly from a missing group: the group exists, only the referenced
// user id does not, so ErrNotFound would misreport which resource is absent.
func TestReplaceMembersWithUnknownUserIDReturnsUnknownReference(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	created, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = repository.ReplaceMembers(ctx, created.ID, []int64{999999})
	if !errors.Is(err, group.ErrUnknownReference) {
		t.Fatalf("err = %v, want group.ErrUnknownReference", err)
	}
	if errors.Is(err, group.ErrNotFound) {
		t.Fatalf("err = %v, must not also match group.ErrNotFound (the group exists)", err)
	}
}

// As TestReplaceMembersWithUnknownUserIDReturnsUnknownReference, for
// group_projects.
func TestReplaceProjectsWithUnknownProjectIDReturnsUnknownReference(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewGroupRepository(pool)

	created, err := repository.Create(ctx, group.NewGroup{Name: "backend"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = repository.ReplaceProjects(ctx, created.ID, []int64{999999})
	if !errors.Is(err, group.ErrUnknownReference) {
		t.Fatalf("err = %v, want group.ErrUnknownReference", err)
	}
	if errors.Is(err, group.ErrNotFound) {
		t.Fatalf("err = %v, must not also match group.ErrNotFound (the group exists)", err)
	}
}
