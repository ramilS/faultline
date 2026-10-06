package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/project"
)

type projectServiceStub struct {
	projects     []project.Project
	listErr      error
	created      project.Project
	createErr    error
	createInputs []project.CreateProject
	found        project.Project
	findErr      error
	keys         []project.Key
	keysErr      error
	createdKey   project.Key
	rawKey       string
	createKeyErr error
	createKeyPID int64
	createKeyIn  project.CreateKey
	revokeErr    error
	revokedSlug  string
	revokedKeyID int64
	revokeCalls  int
}

func (s *projectServiceStub) ListProjects(context.Context, access.Scope) ([]project.Project, error) {
	return s.projects, s.listErr
}

func (s *projectServiceStub) CreateProject(_ context.Context, input project.CreateProject) (project.Project, error) {
	s.createInputs = append(s.createInputs, input)
	if s.createErr != nil {
		return project.Project{}, s.createErr
	}
	return s.created, nil
}

func (s *projectServiceStub) FindProject(context.Context, access.Scope, string) (project.Project, error) {
	return s.found, s.findErr
}

func (s *projectServiceStub) ListKeys(context.Context, access.Scope, string) ([]project.Key, error) {
	return s.keys, s.keysErr
}

func (s *projectServiceStub) CreateKey(_ context.Context, projectID int64, input project.CreateKey) (project.Key, string, error) {
	s.createKeyPID = projectID
	s.createKeyIn = input
	if s.createKeyErr != nil {
		return project.Key{}, "", s.createKeyErr
	}
	return s.createdKey, s.rawKey, nil
}

func (s *projectServiceStub) RevokeKey(_ context.Context, _ access.Scope, slug string, keyID int64) error {
	s.revokeCalls++
	s.revokedSlug = slug
	s.revokedKeyID = keyID
	return s.revokeErr
}

func newProjectHandler(service ProjectService) *ProjectHandler {
	return NewProjectHandler(service, Options{})
}

func decodeBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return body
}

func TestListProjectsReturnsShape(t *testing.T) {
	service := &projectServiceStub{projects: []project.Project{
		{ID: 1, Slug: "web", Name: "Web", Platform: project.PlatformJavaScript, CreatedAt: time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)},
	}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

	newProjectHandler(service).ListProjects(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	body := decodeBody(t, recorder)
	projects, ok := body["projects"].([]any)
	if !ok || len(projects) != 1 {
		t.Fatalf("projects = %v", body["projects"])
	}
	first := projects[0].(map[string]any)
	if first["slug"] != "web" || first["platform"] != project.PlatformJavaScript || first["created_at"] != "2026-07-13T08:00:00Z" {
		t.Fatalf("project entry = %v", first)
	}
	if _, present := first["raw_key"]; present {
		t.Fatal("project entry unexpectedly exposes raw_key")
	}
}

func TestListProjectsReportsServiceError(t *testing.T) {
	service := &projectServiceStub{listErr: context.DeadlineExceeded}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

	newProjectHandler(service).ListProjects(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
}

func TestCreateProjectValidatesRequest(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"wrong content type", "text/plain", `{"slug":"web","name":"Web","platform":"javascript"}`},
		{"malformed json", "application/json", `{`},
		{"unknown field", "application/json", `{"slug":"web","name":"Web","platform":"javascript","extra":1}`},
		{"invalid slug", "application/json", `{"slug":"Bad Slug","name":"Web","platform":"javascript"}`},
		{"unknown platform", "application/json", `{"slug":"web","name":"Web","platform":"cobol"}`},
		{"empty name", "application/json", `{"slug":"web","name":"   ","platform":"javascript"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &projectServiceStub{}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)

			newProjectHandler(service).CreateProject(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if len(service.createInputs) != 0 {
				t.Fatalf("service reached with invalid input: %+v", service.createInputs)
			}
		})
	}
}

func TestCreateProjectReturnsCreated(t *testing.T) {
	service := &projectServiceStub{created: project.Project{ID: 7, Slug: "web", Name: "Web", Platform: project.PlatformJavaScript, CreatedAt: time.Date(2026, 7, 13, 8, 0, 0, 0, time.UTC)}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"slug":"web","name":"Web","platform":"javascript"}`))
	request.Header.Set("Content-Type", "application/json")

	newProjectHandler(service).CreateProject(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	created := body["project"].(map[string]any)
	if created["id"].(float64) != 7 || created["slug"] != "web" {
		t.Fatalf("created project = %v", created)
	}
}

func TestCreateProjectAcceptsEveryPlatform(t *testing.T) {
	for _, platform := range []string{"kotlin-multiplatform", "nestjs", "android", "ios"} {
		t.Run(platform, func(t *testing.T) {
			service := &projectServiceStub{created: project.Project{ID: 7, Slug: "app", Name: "App", Platform: platform}}
			recorder := httptest.NewRecorder()
			body := `{"slug":"app","name":"App","platform":"` + platform + `"}`
			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")

			newProjectHandler(service).CreateProject(recorder, request)

			if recorder.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
			}
			if len(service.createInputs) != 1 || service.createInputs[0].Platform != platform {
				t.Fatalf("service input = %+v, want platform %q", service.createInputs, platform)
			}
			created := decodeBody(t, recorder)["project"].(map[string]any)
			if created["platform"] != platform {
				t.Fatalf("response platform = %v, want %q", created["platform"], platform)
			}
		})
	}
}

func TestCreateProjectMapsConflict(t *testing.T) {
	service := &projectServiceStub{createErr: project.ErrConflict}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects", strings.NewReader(`{"slug":"web","name":"Web","platform":"javascript"}`))
	request.Header.Set("Content-Type", "application/json")

	newProjectHandler(service).CreateProject(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
}

func TestListKeysReturnsShapeWithoutRawKey(t *testing.T) {
	revoked := time.Date(2026, 7, 14, 9, 0, 0, 0, time.UTC)
	service := &projectServiceStub{
		found: project.Project{ID: 1, Slug: "web"},
		keys: []project.Key{
			{ID: 1, ProjectID: 1, PublicKey: "public-1", RateLimitRPS: 200, RateLimitBurst: 1000},
			{ID: 2, ProjectID: 1, PublicKey: "public-2", RateLimitRPS: 50, RateLimitBurst: 100, RevokedAt: &revoked},
		},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/keys", http.NoBody)
	request.SetPathValue("slug", "web")
	// An owner in context: this test asserts the full, unmasked shape.
	// Masking for a member is covered separately by
	// TestListKeysMasksThePublicKeyForAMember.
	request = request.WithContext(ContextWithUser(request.Context(), auth.User{ID: 1, Role: auth.RoleOwner}))
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

	newProjectHandler(service).ListKeys(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	keys := body["keys"].([]any)
	if len(keys) != 2 {
		t.Fatalf("keys = %v", keys)
	}
	first := keys[0].(map[string]any)
	if first["public_key"] != "public-1" || first["revoked_at"] != nil {
		t.Fatalf("active key = %v", first)
	}
	if _, present := first["raw_key"]; present {
		t.Fatal("listed key unexpectedly exposes raw_key")
	}
	second := keys[1].(map[string]any)
	if second["revoked_at"] != "2026-07-14T09:00:00Z" {
		t.Fatalf("revoked key = %v", second)
	}
}

// TestListKeysRefusesARequestWithNoUserInContext pins the fail-closed
// decision explicitly. A zero-value auth.User would also mask (its Role is
// not "owner"), but that is an accident of the zero value rather than a
// decision, and this route decides who is handed a live ingest credential.
func TestListKeysRefusesARequestWithNoUserInContext(t *testing.T) {
	service := &projectServiceStub{
		found: project.Project{ID: 1, Slug: "web"},
		keys:  []project.Key{{ID: 1, ProjectID: 1, PublicKey: "public-1"}},
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/keys", http.NoBody)
	request.SetPathValue("slug", "web")
	// No auth.User in context (the thing under test), but a scope must still
	// be present or scopeOrRefuse would answer 500 before the user check ever
	// runs and this test would stop exercising what it says it exercises.
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

	newProjectHandler(service).ListKeys(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "public-1") {
		t.Fatal("response leaked an ingest key to a request with no resolved user")
	}
}

func TestListKeysReportsMissingProject(t *testing.T) {
	service := &projectServiceStub{findErr: project.ErrNotFound}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/ghost/keys", http.NoBody)
	request.SetPathValue("slug", "ghost")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

	newProjectHandler(service).ListKeys(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateKeyReturnsRawKeyOnceForResolvedProject(t *testing.T) {
	service := &projectServiceStub{
		found:      project.Project{ID: 42, Slug: "web"},
		createdKey: project.Key{ID: 5, ProjectID: 42, PublicKey: "public-5", RateLimitRPS: 200, RateLimitBurst: 1000},
		rawKey:     strings.Repeat("a", 64),
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/keys", strings.NewReader(`{"rate_limit_rps":200,"rate_limit_burst":1000}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")

	newProjectHandler(service).CreateKey(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if service.createKeyPID != 42 {
		t.Fatalf("created key project id = %d, want 42", service.createKeyPID)
	}
	body := decodeBody(t, recorder)
	if body["raw_key"] != strings.Repeat("a", 64) {
		t.Fatalf("raw_key = %v", body["raw_key"])
	}
	key := body["key"].(map[string]any)
	if key["public_key"] != "public-5" {
		t.Fatalf("key = %v", key)
	}
	if _, present := key["raw_key"]; present {
		t.Fatal("nested key object unexpectedly exposes raw_key")
	}
}

func TestCreateKeyValidatesRateSettings(t *testing.T) {
	service := &projectServiceStub{found: project.Project{ID: 42, Slug: "web"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/keys", strings.NewReader(`{"rate_limit_rps":0,"rate_limit_burst":1000}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")

	newProjectHandler(service).CreateKey(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateKeyReportsMissingProject(t *testing.T) {
	service := &projectServiceStub{findErr: project.ErrNotFound}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/ghost/keys", strings.NewReader(`{"rate_limit_rps":200,"rate_limit_burst":1000}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "ghost")

	newProjectHandler(service).CreateKey(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestRevokeKeyRejectsInvalidID(t *testing.T) {
	service := &projectServiceStub{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/keys/abc/revoke", http.NoBody)
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "abc")

	newProjectHandler(service).RevokeKey(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if service.revokeCalls != 0 {
		t.Fatalf("service reached with invalid id")
	}
}

func TestRevokeKeyIsIdempotentSuccess(t *testing.T) {
	service := &projectServiceStub{found: project.Project{ID: 1, Slug: "web"}}
	for attempt := range 2 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/keys/5/revoke", http.NoBody)
		request.SetPathValue("slug", "web")
		request.SetPathValue("id", "5")

		newProjectHandler(service).RevokeKey(recorder, request)

		if recorder.Code != http.StatusNoContent {
			t.Fatalf("attempt %d status = %d, want 204: %s", attempt, recorder.Code, recorder.Body.String())
		}
	}
	if service.revokedKeyID != 5 || service.revokedSlug != "web" {
		t.Fatalf("revoked slug/id = %s/%d", service.revokedSlug, service.revokedKeyID)
	}
}

func TestRevokeKeyMapsMissingKey(t *testing.T) {
	service := &projectServiceStub{revokeErr: project.ErrNotFound}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/keys/5/revoke", http.NoBody)
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "5")

	newProjectHandler(service).RevokeKey(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

// newProjectsRoutesApp wires ProjectHandler exactly as internal/app/app.go
// wires it (RequireSession composed with RequireOwner on the write routes,
// RequireSession alone on the reads), so these tests exercise the real gate
// rather than the handler in isolation.
func newProjectsRoutesApp(t *testing.T, user auth.User, service ProjectService) http.Handler {
	t.Helper()
	authService := &authServiceStub{authUser: user}
	limiter := newTestLimiter()
	authHandler := NewAuthHandler(authService, limiter, &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	projectHandler := NewProjectHandler(service, Options{})
	owner := func(next http.HandlerFunc) http.Handler {
		return authHandler.RequireSession(authHandler.RequireOwner(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/projects", authHandler.RequireSession(http.HandlerFunc(projectHandler.ListProjects)))
	mux.Handle("POST /api/v1/projects", owner(projectHandler.CreateProject))
	mux.Handle("GET /api/v1/projects/{slug}/keys", authHandler.RequireSession(http.HandlerFunc(projectHandler.ListKeys)))
	mux.Handle("POST /api/v1/projects/{slug}/keys", owner(projectHandler.CreateKey))
	mux.Handle("POST /api/v1/projects/{slug}/keys/{id}/revoke", owner(projectHandler.RevokeKey))
	return mux
}

func TestCreateProjectRejectsAMember(t *testing.T) {
	handler := newProjectsRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &projectServiceStub{})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/projects", `{"slug":"web","name":"Web","platform":"javascript"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestCreateProjectRejectsUnauthenticated(t *testing.T) {
	handler := newProjectsRoutesApp(t, auth.User{}, &projectServiceStub{})

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/projects", strings.NewReader(`{"slug":"web","name":"Web","platform":"javascript"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.com")
	response := do(t, handler, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", response.Code, response.Body.String())
	}
}

func TestCreateKeyRejectsAMember(t *testing.T) {
	handler := newProjectsRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &projectServiceStub{found: project.Project{ID: 1, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/projects/web/keys", `{"rate_limit_rps":200,"rate_limit_burst":1000}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestRevokeKeyRejectsAMember(t *testing.T) {
	handler := newProjectsRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &projectServiceStub{found: project.Project{ID: 1, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/projects/web/keys/5/revoke", ""))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestListKeysMasksThePublicKeyForAMember(t *testing.T) {
	handler := newProjectsRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &projectServiceStub{
		found: project.Project{ID: 1, Slug: "web"},
		keys:  []project.Key{{ID: 1, ProjectID: 1, PublicKey: "abcdefghijklmnopqrstuvwxyz012345"}},
	})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/projects/web/keys", nil))
	body := response.Body.String()

	// The full credential must not reach a member's browser at all. A UI-only
	// mask over a complete payload is not access control.
	if strings.Contains(body, "abcdefghijklmnopqrstuvwxyz012345") {
		t.Fatalf("body contains the full key: %s", body)
	}
	if !strings.Contains(body, "abcd") || !strings.Contains(body, "2345") {
		t.Fatalf("body should keep the first and last four characters: %s", body)
	}
}

func TestListKeysReturnsTheFullPublicKeyForAnOwner(t *testing.T) {
	handler := newProjectsRoutesApp(t, auth.User{ID: 1, Role: auth.RoleOwner}, &projectServiceStub{
		found: project.Project{ID: 1, Slug: "web"},
		keys:  []project.Key{{ID: 1, ProjectID: 1, PublicKey: "abcdefghijklmnopqrstuvwxyz012345"}},
	})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/projects/web/keys", nil))

	if !strings.Contains(response.Body.String(), "abcdefghijklmnopqrstuvwxyz012345") {
		t.Fatalf("an owner must receive the full key: %s", response.Body.String())
	}
}
