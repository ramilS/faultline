//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/sourcemap"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestSourceMapRepository(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()

	projects := storagepostgres.NewProjectRepository(pool).Projects
	repository := storagepostgres.NewSourceMapRepository(pool)

	t.Run("creates a release and map then finds it by project release and path", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "source-maps-owner")
		upload := sourceMapUpload("first source map")

		created, inserted, err := repository.Put(ctx, owner.ID, "web@1", "/assets/app.js", upload)
		if err != nil || !inserted {
			t.Fatalf("put created=%v err=%v", inserted, err)
		}
		if created.ID == 0 || created.ReleaseID == 0 || created.GeneratedPath != "/assets/app.js" ||
			created.ContentSHA256 != upload.SHA256 || !bytes.Equal(created.CompressedMap, upload.Compressed) ||
			created.UncompressedBytes != int64(len(upload.JSON)) {
			t.Fatalf("created map = %+v", created)
		}
		assertSourceMapUTC(t, "uploaded_at", created.UploadedAt)

		var releaseCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM releases WHERE project_id = $1 AND version = $2`, owner.ID, "web@1").Scan(&releaseCount); err != nil {
			t.Fatalf("count release: %v", err)
		}
		if releaseCount != 1 {
			t.Fatalf("release count = %d, want 1", releaseCount)
		}

		found, err := repository.Find(ctx, owner.ID, "web@1", "/assets/app.js")
		if err != nil {
			t.Fatalf("find map: %v", err)
		}
		if !sameSourceMap(found, created) {
			t.Fatalf("found map = %+v, want %+v", found, created)
		}
	})

	t.Run("is idempotent for an identical digest and conflicts for different content", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "source-maps-immutable")
		uploadA := sourceMapUpload("first immutable map")
		uploadB := sourceMapUpload("different immutable map")

		first, inserted, err := repository.Put(ctx, owner.ID, "web@1", "/assets/app.js", uploadA)
		if err != nil || !inserted {
			t.Fatalf("first put created=%v err=%v", inserted, err)
		}
		repeated, inserted, err := repository.Put(ctx, owner.ID, "web@1", "/assets/app.js", uploadA)
		if err != nil || inserted {
			t.Fatalf("repeat put created=%v err=%v", inserted, err)
		}
		if !sameSourceMap(repeated, first) {
			t.Fatalf("repeat map = %+v, want %+v", repeated, first)
		}

		conflicted, inserted, err := repository.Put(ctx, owner.ID, "web@1", "/assets/app.js", uploadB)
		if !errors.Is(err, sourcemap.ErrConflict) || inserted {
			t.Fatalf("conflicting put map=%+v created=%v err=%v, want ErrConflict", conflicted, inserted, err)
		}
		if conflicted.ID != 0 || conflicted.ReleaseID != 0 || conflicted.GeneratedPath != "" ||
			conflicted.ContentSHA256 != ([32]byte{}) || len(conflicted.CompressedMap) != 0 ||
			conflicted.UncompressedBytes != 0 || !conflicted.UploadedAt.IsZero() {
			t.Fatalf("conflicting map = %+v, want zero value", conflicted)
		}
		found, err := repository.Find(ctx, owner.ID, "web@1", "/assets/app.js")
		if err != nil {
			t.Fatalf("find immutable map: %v", err)
		}
		if !sameSourceMap(found, first) {
			t.Fatalf("map changed after conflict: got %+v want %+v", found, first)
		}
	})

	t.Run("isolates projects and cascades through releases on project deletion", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "source-maps-cascade-owner")
		foreign := createProject(t, ctx, projects, "source-maps-cascade-foreign")
		upload := sourceMapUpload("isolated map")
		if _, inserted, err := repository.Put(ctx, owner.ID, "web@1", "/assets/app.js", upload); err != nil || !inserted {
			t.Fatalf("put owner map created=%v err=%v", inserted, err)
		}

		if _, err := repository.Find(ctx, foreign.ID, "web@1", "/assets/app.js"); !errors.Is(err, sourcemap.ErrNotFound) {
			t.Fatalf("find foreign map error = %v, want ErrNotFound", err)
		}
		if _, inserted, err := repository.Put(ctx, foreign.ID, "web@1", "/assets/app.js", upload); err != nil || !inserted {
			t.Fatalf("put foreign map created=%v err=%v", inserted, err)
		}

		if _, err := pool.Exec(ctx, `DELETE FROM projects WHERE id = $1`, owner.ID); err != nil {
			t.Fatalf("delete owner: %v", err)
		}
		if _, err := repository.Find(ctx, owner.ID, "web@1", "/assets/app.js"); !errors.Is(err, sourcemap.ErrNotFound) {
			t.Fatalf("find cascade-deleted map error = %v, want ErrNotFound", err)
		}
		if _, err := repository.Find(ctx, foreign.ID, "web@1", "/assets/app.js"); err != nil {
			t.Fatalf("find foreign map after owner deletion: %v", err)
		}
	})

	// Two Puts of identical content under two different releases must land in
	// exactly one source_map_blobs row: upsertBlob's ON CONFLICT (project_id,
	// content_sha256) is the mechanism the whole blob/ref split exists for,
	// and nothing else in this file exercises it directly (the migration
	// integration test in this same package only exercises the backfill's
	// DISTINCT ON, not this live code path).
	t.Run("dedupes identical content across releases into one blob", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "source-maps-dedupe")
		upload := sourceMapUpload("shared across releases")

		first, inserted, err := repository.Put(ctx, owner.ID, "web@1", "/a.js", upload)
		if err != nil || !inserted {
			t.Fatalf("first put created=%v err=%v", inserted, err)
		}
		second, inserted, err := repository.Put(ctx, owner.ID, "web@2", "/a.js", upload)
		if err != nil || !inserted {
			t.Fatalf("second put created=%v err=%v", inserted, err)
		}

		if first.ID == second.ID {
			t.Fatalf("refs for distinct releases got the same id %d", first.ID)
		}
		if first.BlobID != second.BlobID {
			t.Fatalf("BlobID = %d and %d, want the same blob for identical content", first.BlobID, second.BlobID)
		}

		var blobCount int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_map_blobs WHERE project_id = $1`, owner.ID).Scan(&blobCount); err != nil {
			t.Fatalf("count blobs: %v", err)
		}
		if blobCount != 1 {
			t.Fatalf("source_map_blobs rows = %d, want 1", blobCount)
		}
	})
}

func sourceMapUpload(content string) sourcemap.DecodedUpload {
	json := []byte(content)
	return sourcemap.DecodedUpload{
		JSON:       json,
		Compressed: []byte("compressed " + content),
		SHA256:     sha256.Sum256(json),
	}
}

func sameSourceMap(left, right sourcemap.Map) bool {
	return left.ID == right.ID &&
		left.BlobID == right.BlobID &&
		left.ReleaseID == right.ReleaseID &&
		left.GeneratedPath == right.GeneratedPath &&
		left.ContentSHA256 == right.ContentSHA256 &&
		bytes.Equal(left.CompressedMap, right.CompressedMap) &&
		left.UncompressedBytes == right.UncompressedBytes &&
		left.UploadedAt.Equal(right.UploadedAt)
}

func assertSourceMapUTC(t *testing.T, name string, timestamp time.Time) {
	t.Helper()
	if timestamp.Location() != time.UTC {
		t.Fatalf("%s location = %s, want UTC", name, timestamp.Location())
	}
}

// newSourceMapFixture is a migrated database plus one ready project, for
// tests scoped to a single project rather than the multi-project comparisons
// TestSourceMapRepository itself needs.
func newSourceMapFixture(t *testing.T) (sourcemap.Repository, *pgxpool.Pool, int64) {
	t.Helper()
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewSourceMapRepository(pool)
	projects := storagepostgres.NewProjectRepository(pool).Projects
	owner := createProject(t, t.Context(), projects, "source-map-fixture")
	return repository, pool, owner.ID
}

// decodeTestUpload runs a small valid source map through the real decode path
// so bundle tests exercise the same validation a live upload would.
func decodeTestUpload(t *testing.T) sourcemap.DecodedUpload {
	t.Helper()
	const validMap = `{"version":3,"file":"app.js","sources":["app.ts"],"sourcesContent":["console.log(1);"],"names":[],"mappings":"AAAA"}`
	upload, err := sourcemap.DecodeUpload(bytes.NewReader([]byte(validMap)), "")
	if err != nil {
		t.Fatalf("decode test upload: %v", err)
	}
	return upload
}

// One bundle entry with a release must produce two refs on one blob: the
// debug-id address the SDK reports, and the release+path address a non-Sentry
// SDK still needs. The schema CHECK forbids one row carrying both.
func TestPutBundleWritesBothAddressesOnOneBlob(t *testing.T) {
	repository, pool, projectID := newSourceMapFixture(t)
	ctx := t.Context()
	upload := decodeTestUpload(t)

	entries := []sourcemap.BundleEntry{{
		DebugID:       "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f",
		GeneratedPath: "/assets/app.js",
		Upload:        upload,
	}}
	if err := repository.PutBundle(ctx, projectID, "web@1", entries); err != nil {
		t.Fatalf("put bundle: %v", err)
	}

	byDebugID, err := repository.FindByDebugID(ctx, projectID, "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f")
	if err != nil {
		t.Fatalf("find by debug id: %v", err)
	}
	byPath, err := repository.Find(ctx, projectID, "web@1", "/assets/app.js")
	if err != nil {
		t.Fatalf("find by path: %v", err)
	}
	if byDebugID.BlobID != byPath.BlobID {
		t.Fatalf("blob ids differ: %d vs %d", byDebugID.BlobID, byPath.BlobID)
	}
	if byDebugID.ID == byPath.ID {
		t.Fatal("both addresses resolved to one ref row; want two")
	}

	// Re-uploading the identical bundle must be a no-op, not a unique violation:
	// a CI job that reruns its build step re-uploads byte-identical artifacts.
	if err := repository.PutBundle(ctx, projectID, "web@1", entries); err != nil {
		t.Fatalf("re-put bundle: %v", err)
	}
	var refs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_map_refs WHERE project_id = $1`, projectID).Scan(&refs); err != nil {
		t.Fatalf("count refs: %v", err)
	}
	if refs != 2 {
		t.Fatalf("refs after re-put = %d, want 2", refs)
	}
}

// A debug id belongs to one project. A second project's identical id must not
// resolve to the first project's content.
func TestFindByDebugIDIsProjectScoped(t *testing.T) {
	repository, pool, projectID := newSourceMapFixture(t)
	ctx := t.Context()
	const debugID = "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f"
	if err := repository.PutBundle(ctx, projectID, "", []sourcemap.BundleEntry{{
		DebugID: debugID, GeneratedPath: "/assets/app.js", Upload: decodeTestUpload(t),
	}}); err != nil {
		t.Fatalf("put bundle: %v", err)
	}

	var otherID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('other', 'Other', 'other') RETURNING id
	`).Scan(&otherID); err != nil {
		t.Fatalf("insert other project: %v", err)
	}
	if _, err := repository.FindByDebugID(ctx, otherID, debugID); !errors.Is(err, sourcemap.ErrNotFound) {
		t.Fatalf("cross-project find error = %v, want ErrNotFound", err)
	}
}

// A second entry that supplies neither address must fail the whole bundle,
// not just itself: the first entry's ref would otherwise survive uncommitted
// work from a call the caller was told failed, corrupting the ref count a
// retry expects to see.
func TestPutBundleRollsBackWholeBundleOnInvalidEntry(t *testing.T) {
	repository, pool, projectID := newSourceMapFixture(t)
	ctx := t.Context()

	entries := []sourcemap.BundleEntry{
		{DebugID: "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f", GeneratedPath: "/assets/app.js", Upload: decodeTestUpload(t)},
		{Upload: decodeTestUpload(t)}, // neither DebugID nor GeneratedPath set
	}
	if err := repository.PutBundle(ctx, projectID, "web@1", entries); err == nil {
		t.Fatal("put bundle with an addressless entry = nil error, want an error")
	}

	var refs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_map_refs WHERE project_id = $1`, projectID).Scan(&refs); err != nil {
		t.Fatalf("count refs: %v", err)
	}
	if refs != 0 {
		t.Fatalf("refs after failed bundle = %d, want 0", refs)
	}
}

// A bundle entry with only a url and no debug-id header is the legacy case
// the release+path address exists to serve (a non-Sentry SDK, or an older
// build tool). It must write exactly the release+path ref and no debug-id
// ref — not an empty-string debug id masquerading as one.
func TestPutBundleWritesReleasePathRefWithoutDebugID(t *testing.T) {
	repository, pool, projectID := newSourceMapFixture(t)
	ctx := t.Context()

	entries := []sourcemap.BundleEntry{
		{GeneratedPath: "/assets/legacy.js", Upload: decodeTestUpload(t)},
	}
	if err := repository.PutBundle(ctx, projectID, "web@1", entries); err != nil {
		t.Fatalf("put bundle: %v", err)
	}

	found, err := repository.Find(ctx, projectID, "web@1", "/assets/legacy.js")
	if err != nil {
		t.Fatalf("find by path: %v", err)
	}
	if found.GeneratedPath != "/assets/legacy.js" {
		t.Fatalf("found map GeneratedPath = %q, want /assets/legacy.js", found.GeneratedPath)
	}

	var refs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_map_refs WHERE project_id = $1`, projectID).Scan(&refs); err != nil {
		t.Fatalf("count refs: %v", err)
	}
	if refs != 1 {
		t.Fatalf("refs = %d, want 1", refs)
	}

	var debugRefs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_map_refs WHERE project_id = $1 AND debug_id IS NOT NULL`, projectID).Scan(&debugRefs); err != nil {
		t.Fatalf("count debug refs: %v", err)
	}
	if debugRefs != 0 {
		t.Fatalf("debug-id refs = %d, want 0 (no debug id was supplied)", debugRefs)
	}
}

// debug_id is a Postgres uuid column: a malformed id must miss like any other
// unknown one, not surface the driver's parse error to a caller whose only
// failure check is errors.Is(err, ErrNotFound).
func TestFindByDebugIDRejectsMalformedID(t *testing.T) {
	repository, _, projectID := newSourceMapFixture(t)
	ctx := t.Context()

	if _, err := repository.FindByDebugID(ctx, projectID, "not-a-uuid"); !errors.Is(err, sourcemap.ErrNotFound) {
		t.Fatalf("malformed debug id error = %v, want ErrNotFound", err)
	}
}

// The backfill must collapse identical content across releases into one blob.
// A per-row copy would satisfy every other assertion here and silently double
// storage for every project that ships the same chunk in two releases.
//
// Row counts alone prove almost nothing about a migration whose source table
// is dropped on the same commit: blobs==1 && refs==2 holds just as well with a
// wrong project_id, a wrong release_id, a swapped generated_path or corrupted
// map bytes, and there is no source_maps left to recover the truth from. So
// every backfilled column is asserted against the value it came from.
func TestMigration000014DeduplicatesBackfilledContent(t *testing.T) {
	fixture := newLegacySourceMapFixture(t)
	pool, projectID, releaseIDs, digest := fixture.pool, fixture.projectID, fixture.releaseIDs, fixture.digest
	ctx := t.Context()

	if err := migrate.Run(fixture.databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	var (
		blobID            int64
		blobProject       int64
		storedHash        []byte
		storedMap         []byte
		uncompressedBytes int64
		blobs             int
	)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_map_blobs`).Scan(&blobs); err != nil {
		t.Fatalf("count blobs: %v", err)
	}
	if blobs != 1 {
		t.Fatalf("blobs = %d, want 1: identical content across two releases must share one blob", blobs)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id, project_id, content_sha256, compressed_map, uncompressed_bytes FROM source_map_blobs
	`).Scan(&blobID, &blobProject, &storedHash, &storedMap, &uncompressedBytes); err != nil {
		t.Fatalf("read blob: %v", err)
	}
	if blobProject != projectID {
		t.Fatalf("blob project_id = %d, want %d", blobProject, projectID)
	}
	if !bytes.Equal(storedHash, digest[:]) {
		t.Fatalf("blob content_sha256 = %x, want %x", storedHash, digest)
	}
	if !bytes.Equal(storedMap, []byte("gzipbytes")) {
		t.Fatalf("blob compressed_map = %q, want %q", storedMap, "gzipbytes")
	}
	if uncompressedBytes != 9 {
		t.Fatalf("blob uncompressed_bytes = %d, want 9", uncompressedBytes)
	}

	rows, err := pool.Query(ctx, `
		SELECT blob_id, project_id, release_id, generated_path, debug_id IS NULL
		FROM source_map_refs ORDER BY release_id
	`)
	if err != nil {
		t.Fatalf("read refs: %v", err)
	}
	defer rows.Close()
	var backfilled []int64
	for rows.Next() {
		var refBlob, refProject, refRelease int64
		var path string
		var debugIDAbsent bool
		if err := rows.Scan(&refBlob, &refProject, &refRelease, &path, &debugIDAbsent); err != nil {
			t.Fatalf("scan ref: %v", err)
		}
		if refBlob != blobID {
			t.Fatalf("ref blob_id = %d, want the single blob %d", refBlob, blobID)
		}
		if refProject != projectID {
			t.Fatalf("ref project_id = %d, want %d", refProject, projectID)
		}
		if path != "/assets/app.js" {
			t.Fatalf("ref generated_path = %q, want /assets/app.js", path)
		}
		if !debugIDAbsent {
			t.Fatalf("ref for release %d carries a debug_id; source_maps had none to carry", refRelease)
		}
		backfilled = append(backfilled, refRelease)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read refs: %v", err)
	}
	if !slices.Equal(backfilled, releaseIDs) {
		t.Fatalf("refs point at releases %v, want %v", backfilled, releaseIDs)
	}
}

// 000014 drops source_maps, so its down migration is the only thing standing
// between a rollback and permanent data loss. "14 clean" after fourteen downs
// says nothing about whether the rows came back with their content intact.
func TestMigration000014RestoresSourceMapsOnTheWayDown(t *testing.T) {
	fixture := newLegacySourceMapFixture(t)
	pool, releaseIDs, digest := fixture.pool, fixture.releaseIDs, fixture.digest
	databaseURL := fixture.databaseURL
	ctx := t.Context()

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := migrate.Run(databaseURL, migrate.ToVersion(13), &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate down to 13: %v", err)
	}

	rows, err := pool.Query(ctx, `
		SELECT release_id, generated_path, content_sha256, compressed_map, uncompressed_bytes
		FROM source_maps ORDER BY release_id
	`)
	if err != nil {
		t.Fatalf("read restored source_maps: %v", err)
	}
	defer rows.Close()
	var restored []int64
	for rows.Next() {
		var releaseID, uncompressedBytes int64
		var path string
		var storedHash, storedMap []byte
		if err := rows.Scan(&releaseID, &path, &storedHash, &storedMap, &uncompressedBytes); err != nil {
			t.Fatalf("scan restored row: %v", err)
		}
		if path != "/assets/app.js" {
			t.Fatalf("restored generated_path = %q, want /assets/app.js", path)
		}
		if !bytes.Equal(storedHash, digest[:]) {
			t.Fatalf("restored content_sha256 = %x, want %x", storedHash, digest)
		}
		if !bytes.Equal(storedMap, []byte("gzipbytes")) {
			t.Fatalf("restored compressed_map = %q, want %q", storedMap, "gzipbytes")
		}
		if uncompressedBytes != 9 {
			t.Fatalf("restored uncompressed_bytes = %d, want 9", uncompressedBytes)
		}
		restored = append(restored, releaseID)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read restored source_maps: %v", err)
	}
	if !slices.Equal(restored, releaseIDs) {
		t.Fatalf("restored rows for releases %v, want %v", restored, releaseIDs)
	}
}

// legacySourceMapFixture is a database held at the last schema version where
// source_maps exists, seeded with the rows migration 000014 must carry across.
// databaseURL is carried alongside the pool because StartTimescale creates a
// fresh database per call and both tests migrate the one they seeded.
type legacySourceMapFixture struct {
	databaseURL string
	pool        *pgxpool.Pool
	projectID   int64
	releaseIDs  []int64
	digest      [32]byte
}

// newLegacySourceMapFixture stands a database up at version 13 — the last
// schema where source_maps exists — and seeds one project with two releases
// sharing byte-identical map content, the shape both 000014 assertions need.
func newLegacySourceMapFixture(t *testing.T) legacySourceMapFixture {
	t.Helper()
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)

	if err := migrate.Run(databaseURL, migrate.ToVersion(13), &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate to 13: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	var projectID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('backfill', 'Backfill', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	digest := sha256.Sum256([]byte("identical"))
	releaseIDs := make([]int64, 0, 2)
	for _, version := range []string{"web@1", "web@2"} {
		var releaseID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO releases (project_id, version) VALUES ($1, $2) RETURNING id
		`, projectID, version).Scan(&releaseID); err != nil {
			t.Fatalf("insert release %s: %v", version, err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO source_maps (release_id, generated_path, content_sha256, compressed_map, uncompressed_bytes)
			VALUES ($1, '/assets/app.js', $2, $3, 9)
		`, releaseID, digest[:], []byte("gzipbytes")); err != nil {
			t.Fatalf("insert source map %s: %v", version, err)
		}
		releaseIDs = append(releaseIDs, releaseID)
	}
	return legacySourceMapFixture{
		databaseURL: databaseURL, pool: pool, projectID: projectID,
		releaseIDs: releaseIDs, digest: digest,
	}
}
