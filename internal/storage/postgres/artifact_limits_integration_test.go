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
