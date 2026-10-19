//go:build integration

package postgres_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/artifactcatalog"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestArtifactCatalogGroupsFiltersPagesAndScopes(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	projects := storagepostgres.NewProjectRepository(pool).Projects
	owner := createProject(t, ctx, projects, "artifact-catalog")
	foreign := createProject(t, ctx, projects, "artifact-catalog-foreign")
	base := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

	var releaseID, blobID int64
	if err := pool.QueryRow(ctx, `INSERT INTO releases(project_id, version) VALUES($1, 'web@1') RETURNING id`, owner.ID).Scan(&releaseID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO source_map_blobs(project_id, content_sha256, compressed_map, uncompressed_bytes, created_at) VALUES($1,$2,$3,100,$4) RETURNING id`, owner.ID, bytes.Repeat([]byte{1}, 32), []byte("map"), base.Add(3*time.Hour)).Scan(&blobID); err != nil {
		t.Fatal(err)
	}
	// Two live addresses on one physical blob: release/path plus debug id.
	if _, err := pool.Exec(ctx, `INSERT INTO source_map_refs(blob_id,project_id,release_id,generated_path,uploaded_at) VALUES($1,$2,$3,'/app.js',$4)`, blobID, owner.ID, releaseID, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO source_map_refs(blob_id,project_id,debug_id,uploaded_at) VALUES($1,$2,'11111111-1111-1111-1111-111111111111',$3)`, blobID, owner.ID, base.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	// An unreferenced source-map blob is intentionally absent from inventory.
	if _, err := pool.Exec(ctx, `INSERT INTO source_map_blobs(project_id,content_sha256,compressed_map,uncompressed_bytes,created_at) VALUES($1,$2,'orphan',999,$3)`, owner.ID, bytes.Repeat([]byte{9}, 32), base.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO proguard_mappings(project_id,mapping_uuid,content_sha256,compressed_mapping,uncompressed_bytes,uploaded_at,last_used_at) VALUES($1,'22222222-2222-2222-2222-222222222222',$2,'mapping',200,$3,$4)`, owner.ID, bytes.Repeat([]byte{2}, 32), base.Add(3*time.Hour), base.Add(5*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO proguard_mappings(project_id,mapping_uuid,content_sha256,compressed_mapping,uncompressed_bytes,uploaded_at,last_used_at) VALUES($1,'66666666-6666-6666-6666-666666666666',$2,'mapping-2',250,$3,$4)`, owner.ID, bytes.Repeat([]byte{6}, 32), base.Add(3*time.Hour), base.Add(4*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var nativeBlobID int64
	if err := pool.QueryRow(ctx, `INSERT INTO native_debug_file_blobs(project_id,content_sha256,compressed_file,uncompressed_bytes,created_at,last_used_at) VALUES($1,$2,'native',300,$3,$4) RETURNING id`, owner.ID, bytes.Repeat([]byte{3}, 32), base.Add(time.Hour), base.Add(6*time.Hour)).Scan(&nativeBlobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO native_debug_file_refs(project_id,blob_id,debug_id,object_index,cpu_name,vmaddr) VALUES($1,$2,'33333333-3333-3333-3333-333333333333',0,'arm64',0),($1,$2,'44444444-4444-4444-4444-444444444444',1,'x86_64',0)`, owner.ID, nativeBlobID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO proguard_mappings(project_id,mapping_uuid,content_sha256,compressed_mapping,uncompressed_bytes,uploaded_at) VALUES($1,'55555555-5555-5555-5555-555555555555',$2,'foreign',400,$3)`, foreign.ID, bytes.Repeat([]byte{5}, 32), base.Add(10*time.Hour)); err != nil {
		t.Fatal(err)
	}

	repository := storagepostgres.NewArtifactCatalogRepository(pool)
	page, err := repository.List(ctx, owner.ID, artifactcatalog.ListOptions{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Artifacts) != 4 || page.NextCursor != "" {
		t.Fatalf("page=%+v", page)
	}
	if page.Artifacts[0].Type != artifactcatalog.SourceMap || len(page.Artifacts[0].SourceMapRefs) != 2 || page.Artifacts[0].SizeBytes != 100 {
		t.Fatalf("source=%+v", page.Artifacts[0])
	}
	if page.Artifacts[1].Type != artifactcatalog.ProguardMapping || page.Artifacts[1].MappingUUID != "66666666-6666-6666-6666-666666666666" {
		t.Fatalf("mapping=%+v", page.Artifacts[1])
	}
	if page.Artifacts[2].Type != artifactcatalog.ProguardMapping || page.Artifacts[2].MappingUUID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("mapping=%+v", page.Artifacts[2])
	}
	if page.Artifacts[3].Type != artifactcatalog.NativeDebugFile || len(page.Artifacts[3].NativeDebugIDRefs) != 2 {
		t.Fatalf("native=%+v", page.Artifacts[3])
	}

	filtered, err := repository.List(ctx, owner.ID, artifactcatalog.ListOptions{Type: artifactcatalog.SourceMap, Limit: 10})
	if err != nil || len(filtered.Artifacts) != 1 {
		t.Fatalf("filtered=%+v err=%v", filtered, err)
	}
	foreignPage, err := repository.List(ctx, foreign.ID, artifactcatalog.ListOptions{Limit: 10})
	if err != nil || len(foreignPage.Artifacts) != 1 || foreignPage.Artifacts[0].MappingUUID != "55555555-5555-5555-5555-555555555555" {
		t.Fatalf("foreign=%+v err=%v", foreignPage, err)
	}

	var got []string
	cursor := ""
	for {
		one, err := repository.List(ctx, owner.ID, artifactcatalog.ListOptions{Limit: 1, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		if len(one.Artifacts) != 1 {
			t.Fatalf("page at %q has %d rows", cursor, len(one.Artifacts))
		}
		got = append(got, string(one.Artifacts[0].Type))
		if one.NextCursor == "" {
			break
		}
		cursor = one.NextCursor
	}
	if want := []string{"source_map", "proguard_mapping", "proguard_mapping", "native_debug_file"}; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("paged=%v", got)
	}
	if _, err := repository.List(ctx, owner.ID, artifactcatalog.ListOptions{Limit: 1, Cursor: "invalid"}); err != artifactcatalog.ErrInvalidCursor {
		t.Fatalf("cursor error=%v", err)
	}
}
