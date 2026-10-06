package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
)

type stubFacetService struct {
	keys      []issue.TagKey
	values    []issue.TagValue
	err       error
	gotSlug   string
	gotKey    string
	gotPrefix string
	gotLimit  int
	gotScope  access.Scope
}

func (s *stubFacetService) TagKeys(_ context.Context, scope access.Scope, slug string) ([]issue.TagKey, error) {
	s.gotScope, s.gotSlug = scope, slug
	return s.keys, s.err
}

func (s *stubFacetService) TagValues(_ context.Context, scope access.Scope, slug, key, prefix string, limit int) ([]issue.TagValue, error) {
	s.gotScope = scope
	s.gotSlug, s.gotKey, s.gotPrefix, s.gotLimit = slug, key, prefix, limit
	return s.values, s.err
}

// An empty facet list and a project the caller cannot reach are the same
// response unless the boundary refusal is distinguished here: the repository
// reports issue.ErrNotFound for a slug that does not resolve within scope,
// and both facet routes must answer 404 rather than an empty vocabulary.
func TestFacetsMapNotFoundTo404(t *testing.T) {
	cases := map[string]func(*FacetHandler, http.ResponseWriter, *http.Request){
		"tag keys":   (*FacetHandler).TagKeys,
		"tag values": (*FacetHandler).TagValues,
	}
	for name, serve := range cases {
		t.Run(name, func(t *testing.T) {
			var reported error
			service := &stubFacetService{err: fmt.Errorf("scoped read: %w", issue.ErrNotFound)}
			handler := NewFacetHandler(service, Options{ReportError: func(err error) { reported = err }})

			request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/secret/tags", http.NoBody)
			request.SetPathValue("slug", "secret")
			request.SetPathValue("key", "browser")
			request = request.WithContext(access.ContextWithScope(request.Context(), access.Limited([]int64{1})))
			recorder := httptest.NewRecorder()
			serve(handler, recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", recorder.Code, recorder.Body.String())
			}
			if reported != nil {
				t.Fatalf("reportError called with %v, want no report for a boundary refusal", reported)
			}
		})
	}
}

func TestTagKeysReturnsProjectFacets(t *testing.T) {
	service := &stubFacetService{keys: []issue.TagKey{{Key: "browser", ValueCount: 3, TimesSeen: 12}}}
	handler := NewFacetHandler(service, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/demo/tags", http.NoBody)
	request.SetPathValue("slug", "demo")
	scope := access.Limited([]int64{1})
	request = request.WithContext(access.ContextWithScope(request.Context(), scope))
	recorder := httptest.NewRecorder()
	handler.TagKeys(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if service.gotScope.IsUnrestricted() || len(service.gotScope.ProjectIDs()) != 1 {
		t.Fatalf("scope = %+v, want the scope from the request context", service.gotScope)
	}
	var payload struct {
		Tags []struct {
			Key        string `json:"key"`
			ValueCount int64  `json:"value_count"`
		} `json:"tags"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(payload.Tags) != 1 || payload.Tags[0].Key != "browser" || payload.Tags[0].ValueCount != 3 {
		t.Fatalf("tags = %+v, want one browser entry with value_count 3", payload.Tags)
	}
	if service.gotSlug != "demo" {
		t.Fatalf("slug = %q, want demo", service.gotSlug)
	}
}

func TestTagValuesForwardsPrefix(t *testing.T) {
	service := &stubFacetService{values: []issue.TagValue{{Value: "Chrome", TimesSeen: 9}}}
	handler := NewFacetHandler(service, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/demo/tags/browser/values?query=Ch", http.NoBody)
	request.SetPathValue("slug", "demo")
	request.SetPathValue("key", "browser")
	scope := access.Limited([]int64{1})
	request = request.WithContext(access.ContextWithScope(request.Context(), scope))
	recorder := httptest.NewRecorder()
	handler.TagValues(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if service.gotKey != "browser" || service.gotPrefix != "Ch" {
		t.Fatalf("key/prefix = %q/%q, want browser/Ch", service.gotKey, service.gotPrefix)
	}
	if service.gotLimit != defaultFacetLimit {
		t.Fatalf("limit = %d, want %d", service.gotLimit, defaultFacetLimit)
	}
	if service.gotScope.IsUnrestricted() || len(service.gotScope.ProjectIDs()) != 1 {
		t.Fatalf("scope = %+v, want the scope from the request context", service.gotScope)
	}
}
