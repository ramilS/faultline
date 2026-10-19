//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestArtifactQuotaCountsSourceMapsAndProguardMappingsTogether(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	projects := storagepostgres.NewProjectRepository(pool).Projects
	mapping := proguardUpload(t, "quota")
	sourceMap := sourceMapUpload("0123456789")
	limit := int64(len(mapping.Compressed)+len(sourceMap.Compressed)) + 5
	quota := storagepostgres.WithArtifactQuota(limit)
	mappings := storagepostgres.NewProguardMappingRepository(pool, quota)
	sourceMaps := storagepostgres.NewSourceMapRepository(pool, quota)
	project := createProject(t, ctx, projects, "quota-owner")
	other := createProject(t, ctx, projects, "quota-other")

	if _, err := mappings.Put(ctx, project.ID, proguardUUID, mapping); err != nil {
		t.Fatalf("first mapping: %v", err)
	}
	if _, _, err := sourceMaps.Put(ctx, project.ID, "web@1", "/a.js", sourceMap); err != nil {
		t.Fatalf("source map within the quota: %v", err)
	}

	t.Run("a new source map past the cap is refused and leaves nothing behind", func(t *testing.T) {
		_, _, err := sourceMaps.Put(ctx, project.ID, "web@2", "/b.js", sourceMapUpload("another map"))
		if !errors.Is(err, storage.ErrArtifactQuotaExceeded) {
			t.Fatalf("err = %v, want quota exceeded", err)
		}
		var releases int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM releases WHERE project_id = $1 AND version = 'web@2'`, project.ID).Scan(&releases); err != nil || releases != 0 {
			t.Fatalf("releases = %d err=%v, want the refused upload rolled back", releases, err)
		}
	})

	t.Run("a new mapping past the cap is refused", func(t *testing.T) {
		_, err := mappings.Put(ctx, project.ID, "11111111-2222-5333-8444-555555555555", proguardUpload(t, "second mapping"))
		if !errors.Is(err, storage.ErrArtifactQuotaExceeded) {
			t.Fatalf("err = %v, want quota exceeded", err)
		}
	})

	t.Run("a bundle past the cap is refused as a whole", func(t *testing.T) {
		err := sourceMaps.PutBundle(ctx, project.ID, "web@3", []sourcemap.BundleEntry{{GeneratedPath: "/c.js", Upload: sourceMapUpload("bundle content")}})
		if !errors.Is(err, storage.ErrArtifactQuotaExceeded) {
			t.Fatalf("err = %v, want quota exceeded", err)
		}
	})

	t.Run("re-uploading stored content still succeeds at the cap", func(t *testing.T) {
		if created, err := mappings.Put(ctx, project.ID, proguardUUID, mapping); err != nil || created {
			t.Fatalf("re-put created=%v err=%v", created, err)
		}
		if _, inserted, err := sourceMaps.Put(ctx, project.ID, "web@1", "/a.js", sourceMap); err != nil || inserted {
			t.Fatalf("re-put inserted=%v err=%v", inserted, err)
		}
	})

	t.Run("another project has its own allowance", func(t *testing.T) {
		if _, err := mappings.Put(ctx, other.ID, proguardUUID, mapping); err != nil {
			t.Fatalf("other project: %v", err)
		}
	})
}

func TestArtifactQuotaOfZeroMeansNoCap(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "quota-off")
	mappings := storagepostgres.NewProguardMappingRepository(pool, storagepostgres.WithArtifactQuota(0))

	if _, err := mappings.Put(ctx, project.ID, proguardUUID, proguardUpload(t, "uncapped")); err != nil {
		t.Fatalf("put: %v", err)
	}
}

func TestArtifactQuotaCountsOnlyLiveBlobAfterBundleReplacement(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "quota-replacement")
	oldUpload := sourceMapUpload("old replacement")
	newUpload := sourceMapUpload("new replacement")
	quota := storagepostgres.WithArtifactQuota(int64(len(newUpload.Compressed)))
	repository := storagepostgres.NewSourceMapRepository(pool, quota)
	const debugID = "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f"

	if err := repository.PutBundle(ctx, project.ID, "", []sourcemap.BundleEntry{{DebugID: debugID, Upload: oldUpload}}); err != nil {
		t.Fatalf("put old bundle: %v", err)
	}
	if err := repository.PutBundle(ctx, project.ID, "", []sourcemap.BundleEntry{{DebugID: debugID, Upload: newUpload}}); err != nil {
		t.Fatalf("replace bundle within live quota: %v", err)
	}

	var storedBytes int64
	if err := pool.QueryRow(ctx, `
		SELECT COALESCE(SUM(octet_length(compressed_map)), 0)
		FROM source_map_blobs WHERE project_id = $1
	`, project.ID).Scan(&storedBytes); err != nil {
		t.Fatalf("measure stored source maps: %v", err)
	}
	if storedBytes != int64(len(newUpload.Compressed)) {
		t.Fatalf("stored bytes = %d, want live replacement bytes %d", storedBytes, len(newUpload.Compressed))
	}
}

func TestArtifactQuotaIgnoresHistoricalOrphanOnPut(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "quota-put-orphan")
	orphan := sourceMapUpload("historical orphan")
	live := sourceMapUpload("new live map")
	if _, err := pool.Exec(ctx, `
		INSERT INTO source_map_blobs (project_id, content_sha256, compressed_map, uncompressed_bytes)
		VALUES ($1, $2, $3, $4)
	`, project.ID, orphan.SHA256[:], orphan.Compressed, len(orphan.JSON)); err != nil {
		t.Fatalf("seed historical orphan: %v", err)
	}
	repository := storagepostgres.NewSourceMapRepository(pool, storagepostgres.WithArtifactQuota(int64(len(live.Compressed))))

	if _, _, err := repository.Put(ctx, project.ID, "web@1", "/app.js", live); err != nil {
		t.Fatalf("put live source map at exact quota: %v", err)
	}
	var blobs int
	var storedBytes int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(SUM(octet_length(compressed_map)), 0)
		FROM source_map_blobs WHERE project_id = $1
	`, project.ID).Scan(&blobs, &storedBytes); err != nil {
		t.Fatalf("measure stored source maps: %v", err)
	}
	if blobs != 1 || storedBytes != int64(len(live.Compressed)) {
		t.Fatalf("stored source maps = %d blobs/%d bytes, want 1/%d", blobs, storedBytes, len(live.Compressed))
	}
}

func TestBundleReplacementQuotaFailureRestoresPreviousBlobAndRef(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "quota-replacement-rollback")
	oldUpload := sourceMapUpload("old")
	newUpload := sourceMapUpload("replacement that is too large")
	repository := storagepostgres.NewSourceMapRepository(pool, storagepostgres.WithArtifactQuota(int64(len(oldUpload.Compressed))))
	const debugID = "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f"

	if err := repository.PutBundle(ctx, project.ID, "", []sourcemap.BundleEntry{{DebugID: debugID, Upload: oldUpload}}); err != nil {
		t.Fatalf("put old bundle: %v", err)
	}
	err := repository.PutBundle(ctx, project.ID, "", []sourcemap.BundleEntry{{DebugID: debugID, Upload: newUpload}})
	if !errors.Is(err, storage.ErrArtifactQuotaExceeded) {
		t.Fatalf("replacement error = %v, want quota exceeded", err)
	}

	found, err := repository.FindByDebugID(ctx, project.ID, debugID)
	if err != nil {
		t.Fatalf("find restored map: %v", err)
	}
	if found.ContentSHA256 != oldUpload.SHA256 {
		t.Fatalf("restored hash = %x, want %x", found.ContentSHA256, oldUpload.SHA256)
	}
	var blobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM source_map_blobs WHERE project_id = $1`, project.ID).Scan(&blobs); err != nil {
		t.Fatalf("count source-map blobs: %v", err)
	}
	if blobs != 1 {
		t.Fatalf("source-map blobs after rollback = %d, want 1", blobs)
	}
}
