//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/sentrycli"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestUploadChunkStoreRoundTripAndScoping(t *testing.T) {
	pool, projectID, otherID := newChunkFixture(t)
	store := storagepostgres.NewUploadChunkStore(pool)
	ctx := t.Context()

	body := []byte("chunk-body")
	checksum := sentrycli.ComputeChecksum(body)
	if err := store.Save(ctx, projectID, map[sentrycli.Checksum][]byte{checksum: body}); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Re-uploading a chunk is normal: sentry-cli retries and several assembles
	// may reference one chunk. Save must be idempotent, not a unique violation.
	if err := store.Save(ctx, projectID, map[sentrycli.Checksum][]byte{checksum: body}); err != nil {
		t.Fatalf("re-save: %v", err)
	}

	loaded, err := store.Load(ctx, projectID, []sentrycli.Checksum{checksum})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !bytes.Equal(loaded[checksum], body) {
		t.Fatalf("loaded = %q, want %q", loaded[checksum], body)
	}

	// A chunk staged by one project must be invisible to another: the checksum
	// is guessable from any public artifact, and a cross-project read would
	// let one tenant assemble another's source into their own bundle.
	foreign, err := store.Load(ctx, otherID, []sentrycli.Checksum{checksum})
	if err != nil {
		t.Fatalf("load for other project: %v", err)
	}
	if len(foreign) != 0 {
		t.Fatalf("other project loaded %d chunks, want 0", len(foreign))
	}

	if err := store.Delete(ctx, projectID, []sentrycli.Checksum{checksum}); err != nil {
		t.Fatalf("delete: %v", err)
	}
	after, err := store.Load(ctx, projectID, []sentrycli.Checksum{checksum})
	if err != nil {
		t.Fatalf("load after delete: %v", err)
	}
	if len(after) != 0 {
		t.Fatalf("loaded %d chunks after delete, want 0", len(after))
	}
}

func TestDeleteExpiredRemovesOnlyStaleChunks(t *testing.T) {
	pool, projectID, _ := newChunkFixture(t)
	store := storagepostgres.NewUploadChunkStore(pool)
	ctx := t.Context()

	fresh := sentrycli.ComputeChecksum([]byte("fresh"))
	stale := sentrycli.ComputeChecksum([]byte("stale"))
	if err := store.Save(ctx, projectID, map[sentrycli.Checksum][]byte{
		fresh: []byte("fresh"), stale: []byte("stale"),
	}); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		UPDATE upload_chunks SET created_at = now() - interval '2 days' WHERE checksum = $1
	`, stale[:]); err != nil {
		t.Fatalf("age chunk: %v", err)
	}

	removed, err := store.DeleteExpired(ctx, time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatalf("delete expired: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	loaded, err := store.Load(ctx, projectID, []sentrycli.Checksum{fresh, stale})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("surviving chunks = %d, want 1", len(loaded))
	}
}

func newChunkFixture(t *testing.T) (*pgxpool.Pool, int64, int64) {
	t.Helper()
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	var projectID, otherID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('chunks', 'Chunks', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('chunks-other', 'Other', 'other') RETURNING id
	`).Scan(&otherID); err != nil {
		t.Fatalf("insert other project: %v", err)
	}
	return pool, projectID, otherID
}

// Staging has no other bound: a successful assemble deletes what it consumed,
// and the age sweep does not run for 24 hours, so an upload that never
// assembles can fill the disk every project shares with one valid artifact
// token. The three assertions below are the whole contract:
//
//   - a retry that re-sends checksums already staged must NOT be refused, even
//     right at the ceiling. Chunks collapse on (project_id, checksum), so the
//     retry adds nothing; counting the request's own bytes into the total
//     instead would refuse exactly the client the protocol tells to retry.
//   - distinct chunks do accumulate, and
//   - once the total is at the ceiling, the next Save reports
//     ErrStagingQuotaExceeded, which the handler maps to 413 rather than 500.
func TestSaveRefusesAProjectOverItsStagingQuota(t *testing.T) {
	pool, projectID, _ := newChunkFixture(t)
	const quota = 100
	store := storagepostgres.NewUploadChunkStoreWithQuota(pool, quota)
	ctx := t.Context()

	first := bytes.Repeat([]byte("a"), 90)
	second := bytes.Repeat([]byte("b"), 90)
	third := bytes.Repeat([]byte("c"), 90)

	if err := store.Save(ctx, projectID, map[sentrycli.Checksum][]byte{sentrycli.ComputeChecksum(first): first}); err != nil {
		t.Fatalf("save first: %v", err)
	}
	// 90 staged against a ceiling of 100: a retry of the same checksum stays
	// at 90 and must be accepted.
	if err := store.Save(ctx, projectID, map[sentrycli.Checksum][]byte{sentrycli.ComputeChecksum(first): first}); err != nil {
		t.Fatalf("re-save of an already-staged checksum was refused: %v", err)
	}
	if staged := stagedBytes(t, pool, projectID); staged != 90 {
		t.Fatalf("staged bytes after the retry = %d, want 90 (the retry must dedupe, not accumulate)", staged)
	}

	if err := store.Save(ctx, projectID, map[sentrycli.Checksum][]byte{sentrycli.ComputeChecksum(second): second}); err != nil {
		t.Fatalf("save second: %v", err)
	}
	if staged := stagedBytes(t, pool, projectID); staged != 180 {
		t.Fatalf("staged bytes after a distinct chunk = %d, want 180", staged)
	}

	err := store.Save(ctx, projectID, map[sentrycli.Checksum][]byte{sentrycli.ComputeChecksum(third): third})
	if !errors.Is(err, sentrycli.ErrStagingQuotaExceeded) {
		t.Fatalf("save past the ceiling: err = %v, want ErrStagingQuotaExceeded", err)
	}
	if staged := stagedBytes(t, pool, projectID); staged != 180 {
		t.Fatalf("staged bytes after the refusal = %d, want 180 (the refused chunk must not be written)", staged)
	}
}

func stagedBytes(t *testing.T, pool *pgxpool.Pool, projectID int64) int64 {
	t.Helper()
	var staged int64
	if err := pool.QueryRow(t.Context(), `
		SELECT coalesce(sum(octet_length(body)), 0) FROM upload_chunks WHERE project_id = $1
	`, projectID).Scan(&staged); err != nil {
		t.Fatalf("measure staged bytes: %v", err)
	}
	return staged
}
