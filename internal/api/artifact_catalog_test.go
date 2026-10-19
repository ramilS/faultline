package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/artifactcatalog"
	"github.com/ramilS/faultline/internal/project"
)

type artifactCatalogStub struct {
	page      artifactcatalog.Page
	err       error
	projectID int64
	options   artifactcatalog.ListOptions
	calls     int
}

func (s *artifactCatalogStub) List(_ context.Context, projectID int64, options artifactcatalog.ListOptions) (artifactcatalog.Page, error) {
	s.calls++
	s.projectID, s.options = projectID, options
	return s.page, s.err
}

func catalogRequest(target string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, target, nil)
	request.SetPathValue("slug", "web")
	return request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
}

func TestListArtifactCatalogMapsTypesAndReferences(t *testing.T) {
	uploaded := time.Date(2026, 10, 18, 8, 30, 0, 0, time.UTC)
	used := uploaded.Add(time.Hour)
	catalog := &artifactCatalogStub{page: artifactcatalog.Page{
		Artifacts: []artifactcatalog.Artifact{
			{Type: artifactcatalog.SourceMap, ID: 1, ContentSHA256: "abcd", SizeBytes: 12, UploadedAt: uploaded, SourceMapRefs: []artifactcatalog.SourceMapReference{{Release: "web@1", GeneratedPath: "/app.js"}, {DebugID: "debug-one"}}},
			{Type: artifactcatalog.ProguardMapping, ID: 2, MappingUUID: "mapping-one", SizeBytes: 34, UploadedAt: uploaded, LastUsedAt: &used},
			{Type: artifactcatalog.NativeDebugFile, ID: 3, ContentSHA256: "ef01", SizeBytes: 56, UploadedAt: uploaded, LastUsedAt: &used, NativeDebugIDRefs: []artifactcatalog.NativeReference{{DebugID: "native-one", CPUName: "arm64"}}},
		},
		NextCursor: "next-page",
	}}
	recorder := httptest.NewRecorder()
	handler := NewArtifactCatalogHandler(catalog, &artifactProjectServiceStub{project: project.Project{ID: 42}}, Options{})

	handler.List(recorder, catalogRequest("/api/v1/projects/web/artifacts?type=source_map&limit=3&cursor=opaque"))

	if recorder.Code != http.StatusOK || catalog.projectID != 42 || catalog.options.Type != artifactcatalog.SourceMap || catalog.options.Limit != 3 || catalog.options.Cursor != "opaque" {
		t.Fatalf("status=%d project=%d options=%+v body=%s", recorder.Code, catalog.projectID, catalog.options, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	artifacts := body["artifacts"].([]any)
	if len(artifacts) != 3 || body["next_cursor"] != "next-page" {
		t.Fatalf("response=%v", body)
	}
	source := artifacts[0].(map[string]any)
	sourceRefs := source["references"].([]any)
	if source["content_sha256"] != "abcd" || source["last_used_at"] != nil || len(sourceRefs) != 2 {
		t.Fatalf("source=%v", source)
	}
	if releaseRef := sourceRefs[0].(map[string]any); releaseRef["release"] != "web@1" || releaseRef["generated_path"] != "/app.js" {
		t.Fatalf("release reference=%v", releaseRef)
	}
	if debugRef := sourceRefs[1].(map[string]any); debugRef["debug_id"] != "debug-one" {
		t.Fatalf("debug reference=%v", debugRef)
	}
	if _, present := source["mapping_uuid"]; present {
		t.Fatalf("source exposes mapping_uuid: %v", source)
	}
	proguard := artifacts[1].(map[string]any)
	if proguard["mapping_uuid"] != "mapping-one" || proguard["last_used_at"] != "2026-10-18T09:30:00Z" {
		t.Fatalf("proguard=%v", proguard)
	}
	native := artifacts[2].(map[string]any)
	nativeRef := native["references"].([]any)[0].(map[string]any)
	if nativeRef["debug_id"] != "native-one" || nativeRef["cpu_name"] != "arm64" {
		t.Fatalf("native reference=%v", nativeRef)
	}
}

func TestListArtifactCatalogRejectsInvalidQuery(t *testing.T) {
	for _, target := range []string{
		"/api/v1/projects/web/artifacts?type=unknown",
		"/api/v1/projects/web/artifacts?type=",
		"/api/v1/projects/web/artifacts?limit=0",
		"/api/v1/projects/web/artifacts?limit=101",
		"/api/v1/projects/web/artifacts?limit=nope",
		"/api/v1/projects/web/artifacts?cursor=first&cursor=second",
	} {
		t.Run(target, func(t *testing.T) {
			catalog := &artifactCatalogStub{}
			recorder := httptest.NewRecorder()
			NewArtifactCatalogHandler(catalog, &artifactProjectServiceStub{project: project.Project{ID: 42}}, Options{}).List(recorder, catalogRequest(target))
			if recorder.Code != http.StatusBadRequest || catalog.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", recorder.Code, catalog.calls, recorder.Body.String())
			}
		})
	}
}

func TestListArtifactCatalogMapsCursorAndRepositoryFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		err    error
		status int
	}{
		{name: "invalid cursor", err: artifactcatalog.ErrInvalidCursor, status: http.StatusBadRequest},
		{name: "repository failure", err: errors.New("database down"), status: http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			catalog := &artifactCatalogStub{err: test.err}
			recorder := httptest.NewRecorder()
			NewArtifactCatalogHandler(catalog, &artifactProjectServiceStub{project: project.Project{ID: 42}}, Options{}).List(recorder, catalogRequest("/api/v1/projects/web/artifacts?cursor=bad"))
			if recorder.Code != test.status {
				t.Fatalf("status=%d want=%d body=%s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
}
