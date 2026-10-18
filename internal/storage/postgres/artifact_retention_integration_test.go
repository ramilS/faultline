//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// The quota protects the disk, and the disk holds gzip: a 90MB R8 mapping
// takes 7MB. Counting uncompressed bytes filled a 1GiB quota in eleven builds.
func TestArtifactQuotaCountsStoredBytes(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "quota-stored")
	large := proguardUpload(t, "large when uncompressed")
	large.UncompressedBytes = proguard.MaxDecodedBytes
	second := proguardUpload(t, "second")
	mappings := storagepostgres.NewProguardMappingRepository(pool, storagepostgres.WithArtifactQuota(int64(len(large.Compressed)+len(second.Compressed)-1)))

	if _, err := mappings.Put(ctx, project.ID, proguardUUID, large); err != nil {
		t.Fatalf("a mapping whose stored bytes fit: %v", err)
	}
	if _, err := mappings.Put(ctx, project.ID, "11111111-2222-5333-8444-555555555555", second); !errors.Is(err, storage.ErrArtifactQuotaExceeded) {
		t.Fatalf("err = %v, want quota exceeded once the stored bytes pass the cap", err)
	}
}

func TestProguardRetentionKeepsTheMostRecentlyUsedMappings(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "proguard-retention")
	mappings := storagepostgres.NewProguardMappingRepository(pool, storagepostgres.WithDebugFileRetention(2))
	const store, local, used, next = "a0000000-0000-4000-8000-000000000001", "a0000000-0000-4000-8000-000000000002", "a0000000-0000-4000-8000-000000000003", "a0000000-0000-4000-8000-000000000004"
	put := func(id string) {
		t.Helper()
		if _, err := mappings.Put(ctx, project.ID, id, proguardUpload(t, id)); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	stored := func(id string) bool {
		t.Helper()
		_, err := mappings.Find(ctx, project.ID, id)
		if err != nil && !errors.Is(err, proguard.ErrNotFound) {
			t.Fatalf("find %s: %v", id, err)
		}
		return err == nil
	}

	put(store)
	put(local)
	if err := mappings.MarkUsed(ctx, project.ID, store); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	put(used)
	if !stored(store) || stored(local) || !stored(used) {
		t.Fatalf("store=%v local=%v used=%v, want the mapping a crash used kept over the newer unused one", stored(store), stored(local), stored(used))
	}

	put(used)
	put(next)
	if stored(store) || !stored(used) || !stored(next) {
		t.Fatalf("store=%v used=%v next=%v, want a re-upload to count as use", stored(store), stored(used), stored(next))
	}
}

func TestNativeRetentionKeepsTheMostRecentlyUsedDebugFiles(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	project := createProject(t, ctx, storagepostgres.NewProjectRepository(pool).Projects, "native-retention")
	files := storagepostgres.NewNativeDebugFileRepository(pool, storagepostgres.WithDebugFileRetention(2))
	const store, local, newest = "b0000000-0000-4000-8000-000000000001", "b0000000-0000-4000-8000-000000000002", "b0000000-0000-4000-8000-000000000003"
	for _, id := range []string{store, local} {
		if err := files.Put(ctx, project.ID, nativeUpload(id, id)); err != nil {
			t.Fatalf("put %s: %v", id, err)
		}
	}
	if err := files.MarkUsed(ctx, project.ID, store); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	if err := files.Put(ctx, project.ID, nativeUpload(newest, newest)); err != nil {
		t.Fatalf("put newest: %v", err)
	}

	for id, want := range map[string]bool{store: true, local: false, newest: true} {
		_, err := files.Find(ctx, project.ID, id)
		if got := err == nil; got != want || (err != nil && !errors.Is(err, native.ErrNotFound)) {
			t.Fatalf("%s stored=%v err=%v, want stored=%v", id, got, err, want)
		}
	}
	var blobs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM native_debug_file_blobs WHERE project_id = $1`, project.ID).Scan(&blobs); err != nil || blobs != 2 {
		t.Fatalf("blobs = %d err=%v, want the evicted blob deleted with its refs", blobs, err)
	}
}
