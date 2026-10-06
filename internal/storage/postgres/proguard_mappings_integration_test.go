//go:build integration

package postgres_test

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/proguard"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

const proguardUUID = "cd395950-361a-563d-9df3-49aea740c6f6"

func proguardUpload(t *testing.T, body string) proguard.DecodedUpload {
	t.Helper()
	upload, err := proguard.DecodeUpload(strings.NewReader("com.acme.Cart -> a.a:\n    1:1:void run():3:3 -> a\n# " + body + "\n"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	return upload
}

func TestProguardMappingRepository(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	projects := storagepostgres.NewProjectRepository(pool).Projects
	repository := storagepostgres.NewProguardMappingRepository(pool)

	owner := createProject(t, ctx, projects, "proguard-owner")
	other := createProject(t, ctx, projects, "proguard-other")
	upload := proguardUpload(t, "first")

	t.Run("stores a mapping and finds it again", func(t *testing.T) {
		created, err := repository.Put(ctx, owner.ID, proguardUUID, upload)
		if err != nil || !created {
			t.Fatalf("put created=%v err=%v", created, err)
		}

		stored, err := repository.Find(ctx, owner.ID, proguardUUID)
		if err != nil {
			t.Fatalf("find: %v", err)
		}
		if stored.UUID != proguardUUID || !bytes.Equal(stored.Compressed, upload.Compressed) ||
			stored.UncompressedBytes != upload.UncompressedBytes {
			t.Fatalf("stored = %+v", stored)
		}
	})

	t.Run("a repeated upload of the same bytes is not created again", func(t *testing.T) {
		created, err := repository.Put(ctx, owner.ID, proguardUUID, upload)
		if err != nil || created {
			t.Fatalf("put created=%v err=%v, want created=false", created, err)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM proguard_mappings WHERE project_id = $1`, owner.ID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("rows = %d err=%v, want 1", count, err)
		}
	})

	t.Run("different bytes under a taken uuid are a conflict", func(t *testing.T) {
		_, err := repository.Put(ctx, owner.ID, proguardUUID, proguardUpload(t, "second"))
		if !errors.Is(err, proguard.ErrConflict) {
			t.Fatalf("put error = %v, want ErrConflict", err)
		}
	})

	t.Run("a mapping belongs to one project", func(t *testing.T) {
		if _, err := repository.Find(ctx, other.ID, proguardUUID); !errors.Is(err, proguard.ErrNotFound) {
			t.Fatalf("find in another project error = %v, want ErrNotFound", err)
		}
		if created, err := repository.Put(ctx, other.ID, proguardUUID, proguardUpload(t, "other")); err != nil || !created {
			t.Fatalf("the same uuid in another project created=%v err=%v", created, err)
		}
	})

	t.Run("a uuid that was never uploaded or is malformed is not found", func(t *testing.T) {
		for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-a-uuid", ""} {
			if _, err := repository.Find(ctx, owner.ID, id); !errors.Is(err, proguard.ErrNotFound) {
				t.Fatalf("find(%q) error = %v, want ErrNotFound", id, err)
			}
		}
	})
}
