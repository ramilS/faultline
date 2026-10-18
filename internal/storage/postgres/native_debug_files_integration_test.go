//go:build integration

package postgres_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func nativeUpload(body string, ids ...string) native.Upload {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	_, _ = writer.Write([]byte(body))
	_ = writer.Close()
	objects := make([]native.Object, len(ids))
	for i, id := range ids {
		objects[i] = native.Object{DebugID: id, CPU: "arm64", Index: i, VMAddr: 0x1000}
	}
	return native.Upload{Compressed: compressed.Bytes(), ContentSHA256: sha256.Sum256([]byte(body)), UncompressedBytes: int64(len(body)), Objects: objects}
}

func TestNativeDebugFileRepository(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	projects := storagepostgres.NewProjectRepository(pool).Projects
	repository := storagepostgres.NewNativeDebugFileRepository(pool)
	owner := createProject(t, ctx, projects, "native-owner")
	other := createProject(t, ctx, projects, "native-other")
	firstID := "01000000-0000-0000-0000-000000000000"
	secondID := "02000000-0000-0000-0000-000000000000"
	upload := nativeUpload("universal", firstID, secondID)

	if err := repository.Put(ctx, owner.ID, upload); err != nil {
		t.Fatalf("put: %v", err)
	}
	for index, id := range []string{firstID, secondID} {
		stored, err := repository.Find(ctx, owner.ID, id)
		if err != nil || stored.ObjectIndex != index || !bytes.Equal(stored.Compressed, upload.Compressed) {
			t.Fatalf("find %s: %+v, %v", id, stored, err)
		}
	}
	var blobs, refs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM native_debug_file_blobs WHERE project_id=$1`, owner.ID).Scan(&blobs); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM native_debug_file_refs WHERE project_id=$1`, owner.ID).Scan(&refs); err != nil {
		t.Fatal(err)
	}
	if blobs != 1 || refs != 2 {
		t.Fatalf("blobs=%d refs=%d, want one charged blob and two refs", blobs, refs)
	}

	if err := repository.Put(ctx, owner.ID, upload); err != nil {
		t.Fatalf("idempotent put: %v", err)
	}
	if err := repository.Put(ctx, owner.ID, nativeUpload("different", firstID)); !errors.Is(err, native.ErrConflict) {
		t.Fatalf("conflict error=%v", err)
	}
	if _, err := repository.Find(ctx, other.ID, firstID); !errors.Is(err, native.ErrNotFound) {
		t.Fatalf("cross-project find=%v", err)
	}
	if err := repository.Put(ctx, other.ID, nativeUpload("other", firstID)); err != nil {
		t.Fatalf("same UUID in other project: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM projects WHERE id=$1`, owner.ID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM native_debug_file_blobs WHERE project_id=$1`, owner.ID).Scan(&blobs); err != nil || blobs != 0 {
		t.Fatalf("cascade blobs=%d err=%v", blobs, err)
	}
}

func TestNativeDebugFileQuotaChargesBlobOnce(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "native-quota")
	repository := storagepostgres.NewNativeDebugFileRepository(pool, storagepostgres.WithArtifactQuota(9))
	firstID := "11000000-0000-0000-0000-000000000000"
	secondID := "12000000-0000-0000-0000-000000000000"
	if err := repository.Put(ctx, project.ID, nativeUpload("123456789", firstID, secondID)); err != nil {
		t.Fatalf("universal at quota: %v", err)
	}
	if err := repository.Put(ctx, project.ID, nativeUpload("x", "13000000-0000-0000-0000-000000000000")); !errors.Is(err, storage.ErrArtifactQuotaExceeded) {
		t.Fatalf("quota error=%v", err)
	}
}
