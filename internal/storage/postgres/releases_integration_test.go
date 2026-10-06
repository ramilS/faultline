//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestReleaseStoreUpsertCreatesRelease(t *testing.T) {
	pool, projectID, _ := newReleaseFixture(t)
	store := storagepostgres.NewReleaseStore(pool)
	ctx := t.Context()

	if err := store.Upsert(ctx, projectID, "web@1.0.0"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	// The (project_id, version) pair is the identity a caller addresses a
	// release by; exactly one row must now resolve to it.
	if count := countReleases(t, ctx, pool, projectID, "web@1.0.0"); count != 1 {
		t.Fatalf("releases for (project, version) = %d, want 1", count)
	}
	_, releasedAt := releaseRow(t, ctx, pool, projectID, "web@1.0.0")
	if releasedAt != nil {
		t.Fatalf("released_at = %v, want nil before Finalize", releasedAt)
	}
}

// The bundler plugin calls releases.new on every build, so a second Upsert of
// a version it already created is the normal path, not an edge case. A test
// that only checks err == nil would also pass if the second call silently
// inserted a duplicate row and Postgres happened not to enforce the unique
// constraint; asserting the row count and that the id did not change is what
// actually pins idempotency.
func TestReleaseStoreUpsertIsIdempotent(t *testing.T) {
	pool, projectID, _ := newReleaseFixture(t)
	store := storagepostgres.NewReleaseStore(pool)
	ctx := t.Context()

	if err := store.Upsert(ctx, projectID, "web@1.0.0"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	firstID, _ := releaseRow(t, ctx, pool, projectID, "web@1.0.0")

	if err := store.Upsert(ctx, projectID, "web@1.0.0"); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	secondID, _ := releaseRow(t, ctx, pool, projectID, "web@1.0.0")

	if count := countReleases(t, ctx, pool, projectID, "web@1.0.0"); count != 1 {
		t.Fatalf("releases after re-upsert = %d, want 1", count)
	}
	if secondID != firstID {
		t.Fatalf("release id changed across upserts: %d -> %d, want unchanged", firstID, secondID)
	}
}

// released_at has no existing coverage anywhere else in the package: nothing
// about upsertRelease (used by source_maps.go) ever touches that column. This
// is the one assertion that would fail if Finalize forgot to set it.
func TestReleaseStoreFinalizeSetsReleasedAt(t *testing.T) {
	pool, projectID, _ := newReleaseFixture(t)
	store := storagepostgres.NewReleaseStore(pool)
	ctx := t.Context()
	at := time.Date(2026, 9, 11, 10, 30, 0, 0, time.UTC)

	if err := store.Upsert(ctx, projectID, "web@1.0.0"); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := store.Finalize(ctx, projectID, "web@1.0.0", at); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	_, releasedAt := releaseRow(t, ctx, pool, projectID, "web@1.0.0")
	if releasedAt == nil {
		t.Fatal("released_at = nil, want it set")
	}
	if !releasedAt.Equal(at) {
		t.Fatalf("released_at = %v, want %v", releasedAt, at)
	}
}

// Finalize also has to work when it is the first call the store sees for a
// version — the plugin's finalize request can reach a Faultline that never
// received the create.
func TestReleaseStoreFinalizeCreatesReleaseWhenMissing(t *testing.T) {
	pool, projectID, _ := newReleaseFixture(t)
	store := storagepostgres.NewReleaseStore(pool)
	ctx := t.Context()
	at := time.Date(2026, 9, 11, 10, 30, 0, 0, time.UTC)

	if err := store.Finalize(ctx, projectID, "web@2.0.0", at); err != nil {
		t.Fatalf("finalize: %v", err)
	}

	if count := countReleases(t, ctx, pool, projectID, "web@2.0.0"); count != 1 {
		t.Fatalf("releases for (project, version) = %d, want 1", count)
	}
	_, releasedAt := releaseRow(t, ctx, pool, projectID, "web@2.0.0")
	if releasedAt == nil || !releasedAt.Equal(at) {
		t.Fatalf("released_at = %v, want %v", releasedAt, at)
	}
}

// sentry-cli finalizes on every build, so a second finalize of the same
// version happens in practice. The chosen, pinned behaviour: the later call
// wins outright, matching a build that re-declares its own release complete
// at a later timestamp.
func TestReleaseStoreFinalizeOverwritesOnRepeatedCalls(t *testing.T) {
	pool, projectID, _ := newReleaseFixture(t)
	store := storagepostgres.NewReleaseStore(pool)
	ctx := t.Context()
	first := time.Date(2026, 9, 11, 10, 0, 0, 0, time.UTC)
	second := time.Date(2026, 9, 11, 11, 0, 0, 0, time.UTC)

	if err := store.Finalize(ctx, projectID, "web@1.0.0", first); err != nil {
		t.Fatalf("finalize: %v", err)
	}
	if err := store.Finalize(ctx, projectID, "web@1.0.0", second); err != nil {
		t.Fatalf("re-finalize: %v", err)
	}

	if count := countReleases(t, ctx, pool, projectID, "web@1.0.0"); count != 1 {
		t.Fatalf("releases after re-finalize = %d, want 1", count)
	}
	_, releasedAt := releaseRow(t, ctx, pool, projectID, "web@1.0.0")
	if releasedAt == nil || !releasedAt.Equal(second) {
		t.Fatalf("released_at = %v, want %v (the later call)", releasedAt, second)
	}
}

// Two projects can legitimately ship the same version string — "web@1.0.0" is
// about as generic as a release name gets — and must get two independent
// rows. Neither Upsert nor Finalize on one project may reach the other's.
func TestReleaseStoreIsProjectScoped(t *testing.T) {
	pool, projectID, otherID := newReleaseFixture(t)
	store := storagepostgres.NewReleaseStore(pool)
	ctx := t.Context()

	if err := store.Upsert(ctx, projectID, "web@1.0.0"); err != nil {
		t.Fatalf("upsert project: %v", err)
	}
	if err := store.Upsert(ctx, otherID, "web@1.0.0"); err != nil {
		t.Fatalf("upsert other project: %v", err)
	}
	ownID, _ := releaseRow(t, ctx, pool, projectID, "web@1.0.0")
	foreignID, _ := releaseRow(t, ctx, pool, otherID, "web@1.0.0")
	if ownID == foreignID {
		t.Fatalf("both projects resolved to release id %d, want two independent rows", ownID)
	}

	at := time.Date(2026, 9, 11, 10, 30, 0, 0, time.UTC)
	if err := store.Finalize(ctx, projectID, "web@1.0.0", at); err != nil {
		t.Fatalf("finalize project: %v", err)
	}

	_, ownReleasedAt := releaseRow(t, ctx, pool, projectID, "web@1.0.0")
	if ownReleasedAt == nil || !ownReleasedAt.Equal(at) {
		t.Fatalf("own project released_at = %v, want %v", ownReleasedAt, at)
	}
	_, foreignReleasedAt := releaseRow(t, ctx, pool, otherID, "web@1.0.0")
	if foreignReleasedAt != nil {
		t.Fatalf("other project released_at = %v, want nil: finalize leaked across projects", foreignReleasedAt)
	}
}

func countReleases(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, version string) int {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM releases WHERE project_id = $1 AND version = $2
	`, projectID, version).Scan(&count); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	return count
}

func releaseRow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, version string) (int64, *time.Time) {
	t.Helper()
	var id int64
	var releasedAt *time.Time
	if err := pool.QueryRow(ctx, `
		SELECT id, released_at FROM releases WHERE project_id = $1 AND version = $2
	`, projectID, version).Scan(&id, &releasedAt); err != nil {
		t.Fatalf("select release: %v", err)
	}
	return id, releasedAt
}

func newReleaseFixture(t *testing.T) (*pgxpool.Pool, int64, int64) {
	t.Helper()
	pool := testdatabase.Migrated(t)
	ctx := t.Context()

	var projectID, otherID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('releases', 'Releases', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('releases-other', 'Other', 'other') RETURNING id
	`).Scan(&otherID); err != nil {
		t.Fatalf("insert other project: %v", err)
	}
	return pool, projectID, otherID
}
