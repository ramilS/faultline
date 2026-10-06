package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/sourcemap"
)

type artifactServiceStub struct {
	created           artifactauth.Token
	rawToken          string
	createErr         error
	createProject     int64
	createName        string
	tokens            []artifactauth.Token
	listErr           error
	revokeErr         error
	revokedSlug       string
	revokedID         int64
	createCalls       int
	listCalls         int
	revokeCalls       int
	authenticated     artifactauth.Token
	authenticateErr   error
	authenticateRaw   string
	authenticateCalls int
}

func (s *artifactServiceStub) Create(_ context.Context, projectID int64, name string) (artifactauth.Token, string, error) {
	s.createCalls++
	s.createProject = projectID
	s.createName = name
	return s.created, s.rawToken, s.createErr
}

func (s *artifactServiceStub) List(context.Context, string) ([]artifactauth.Token, error) {
	s.listCalls++
	return s.tokens, s.listErr
}

func (s *artifactServiceStub) Revoke(_ context.Context, slug string, tokenID int64) error {
	s.revokeCalls++
	s.revokedSlug = slug
	s.revokedID = tokenID
	return s.revokeErr
}

func (s *artifactServiceStub) Authenticate(_ context.Context, raw string) (artifactauth.Token, error) {
	s.authenticateCalls++
	s.authenticateRaw = raw
	return s.authenticated, s.authenticateErr
}

type sourceMapRepositoryStub struct {
	putMap      sourcemap.Map
	putErr      error
	putInserted bool
	putProject  int64
	putRelease  string
	putPath     string
	putUpload   sourcemap.DecodedUpload
	putCalls    int

	bundleCalls   int
	bundleProject int64
	bundleVersion string
	bundleEntries []sourcemap.BundleEntry
	bundleErr     error
}

func (s *sourceMapRepositoryStub) Put(_ context.Context, projectID int64, release, generatedPath string, upload sourcemap.DecodedUpload) (sourcemap.Map, bool, error) {
	s.putCalls++
	s.putProject, s.putRelease, s.putPath, s.putUpload = projectID, release, generatedPath, upload
	return s.putMap, s.putInserted, s.putErr
}

func (*sourceMapRepositoryStub) Find(context.Context, int64, string, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}

func (*sourceMapRepositoryStub) FindByDebugID(context.Context, int64, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}

func (s *sourceMapRepositoryStub) PutBundle(_ context.Context, projectID int64, version string, entries []sourcemap.BundleEntry) error {
	s.bundleCalls++
	s.bundleProject, s.bundleVersion, s.bundleEntries = projectID, version, entries
	return s.bundleErr
}

type artifactProjectServiceStub struct {
	project project.Project
	err     error
	calls   int
}

func (s *artifactProjectServiceStub) FindProject(context.Context, access.Scope, string) (project.Project, error) {
	s.calls++
	return s.project, s.err
}

func newArtifactHandler(artifacts ArtifactService, projects ProjectResolver) *ArtifactHandler {
	return NewArtifactHandler(artifacts, projects, nil, Options{})
}

func newUploadHandler(artifacts ArtifactService, projects ProjectResolver, maps sourcemap.Repository) *ArtifactHandler {
	return NewArtifactHandler(artifacts, projects, maps, Options{})
}

var validSourceMap = []byte(`{"version":3,"file":"app.js","sources":["app.ts"],"sourcesContent":["console.log(1);"],"names":[],"mappings":"AAAA"}`)

func TestUploadSourceMapCreatesArtifact(t *testing.T) {
	artifacts := &artifactServiceStub{authenticated: artifactauth.Token{ProjectID: 42}}
	maps := &sourceMapRepositoryStub{putInserted: true, putMap: sourcemap.Map{ID: 8, ReleaseID: 9, GeneratedPath: "/assets/app.js", ContentSHA256: [32]byte{1}, UncompressedBytes: int64(len(validSourceMap)), UploadedAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)}}
	request := uploadRequest(validSourceMap)
	recorder := httptest.NewRecorder()

	newUploadHandler(artifacts, &artifactProjectServiceStub{project: project.Project{ID: 42, Slug: "web"}}, maps).UploadSourceMap(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if artifacts.authenticateRaw != "fl_art_prefix_secret" || maps.putProject != 42 || maps.putRelease != "web@1" || maps.putPath != "/assets/app.js" || !bytes.Equal(maps.putUpload.JSON, validSourceMap) {
		t.Fatalf("upload inputs = token %q project=%d release=%q path=%q map=%q", artifacts.authenticateRaw, maps.putProject, maps.putRelease, maps.putPath, maps.putUpload.JSON)
	}
	body := decodeBody(t, recorder)
	if body["id"] != float64(8) || body["release_id"] != float64(9) || body["generated_path"] != "/assets/app.js" || body["content_sha256"] != "01"+strings.Repeat("00", 31) || body["uncompressed_bytes"] != float64(len(validSourceMap)) || body["uploaded_at"] != "2026-07-22T12:00:00Z" {
		t.Fatalf("response = %v", body)
	}
}

func TestUploadSourceMapMapsRepeatAndConflict(t *testing.T) {
	for _, test := range []struct {
		name     string
		inserted bool
		err      error
		status   int
	}{
		{name: "identical repeat", status: http.StatusOK},
		{name: "immutable conflict", err: sourcemap.ErrConflict, status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifacts := &artifactServiceStub{authenticated: artifactauth.Token{ProjectID: 42}}
			recorder := httptest.NewRecorder()
			newUploadHandler(artifacts, &artifactProjectServiceStub{project: project.Project{ID: 42}}, &sourceMapRepositoryStub{putMap: sourcemap.Map{ID: 8, GeneratedPath: "/assets/app.js"}, putInserted: test.inserted, putErr: test.err}).UploadSourceMap(recorder, uploadRequest(validSourceMap))
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}
			if test.status == http.StatusOK && decodeBody(t, recorder)["id"] != float64(8) {
				t.Fatalf("response = %s", recorder.Body.String())
			}
		})
	}
}

func TestUploadSourceMapRejectsInvalidCredentialsAndForeignProject(t *testing.T) {
	for _, test := range []struct {
		name          string
		authorization string
		authErr       error
		projectID     int64
		status        int
	}{
		{name: "missing bearer", status: http.StatusUnauthorized},
		{name: "malformed bearer", authorization: "Basic token", status: http.StatusUnauthorized},
		{name: "unknown bearer", authorization: "Bearer bad", authErr: artifactauth.ErrUnauthorized, status: http.StatusUnauthorized},
		{name: "revoked bearer", authorization: "Bearer revoked", authErr: artifactauth.ErrUnauthorized, status: http.StatusUnauthorized},
		{name: "foreign token", authorization: "Bearer foreign", projectID: 99, status: http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifacts := &artifactServiceStub{authenticated: artifactauth.Token{ProjectID: test.projectID}, authenticateErr: test.authErr}
			maps := &sourceMapRepositoryStub{putInserted: true}
			request := uploadRequest(validSourceMap)
			if test.authorization == "" {
				request.Header.Del("Authorization")
			} else {
				request.Header.Set("Authorization", test.authorization)
			}
			recorder := httptest.NewRecorder()
			newUploadHandler(artifacts, &artifactProjectServiceStub{project: project.Project{ID: 42}}, maps).UploadSourceMap(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}
			if maps.putCalls != 0 {
				t.Fatal("map persistence reached for unauthorized upload")
			}
		})
	}
}

func TestUploadSourceMapAuthenticatesBeforeDecodingBody(t *testing.T) {
	artifacts := &artifactServiceStub{authenticateErr: artifactauth.ErrUnauthorized}
	maps := &sourceMapRepositoryStub{}
	request := uploadRequest(bytes.Repeat([]byte(" "), sourcemap.MaxDecodedBytes+1))
	recorder := httptest.NewRecorder()

	newUploadHandler(artifacts, &artifactProjectServiceStub{project: project.Project{ID: 42}}, maps).UploadSourceMap(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", recorder.Code, recorder.Body.String())
	}
	if maps.putCalls != 0 {
		t.Fatal("map persistence reached for unauthorized upload")
	}
}

func TestUploadSourceMapRejectsInvalidInput(t *testing.T) {
	for _, test := range []struct {
		name            string
		query           string
		contentType     string
		contentEncoding string
		body            []byte
		status          int
	}{
		{name: "missing release", query: "path=%2Fassets%2Fapp.js", contentType: "application/json", body: validSourceMap, status: http.StatusBadRequest},
		{name: "blank release", query: "release=%20%20%20&path=%2Fassets%2Fapp.js", contentType: "application/json", body: validSourceMap, status: http.StatusBadRequest},
		{name: "overlong release", query: "release=" + strings.Repeat("a", 201) + "&path=%2Fassets%2Fapp.js", contentType: "application/json", body: validSourceMap, status: http.StatusBadRequest},
		{name: "invalid path", query: "release=web%401&path=relative", contentType: "application/json", body: validSourceMap, status: http.StatusBadRequest},
		{name: "overlong path", query: "release=web%401&path=%2F" + strings.Repeat("a", 2048), contentType: "application/json", body: validSourceMap, status: http.StatusBadRequest},
		{name: "content type parameters accepted", query: "release=web%401&path=%2Fassets%2Fapp.js", contentType: "application/json; charset=utf-8", body: validSourceMap, status: http.StatusCreated},
		{name: "wrong content type", query: "release=web%401&path=%2Fassets%2Fapp.js", contentType: "text/plain", body: validSourceMap, status: http.StatusBadRequest},
		{name: "unsupported encoding", query: "release=web%401&path=%2Fassets%2Fapp.js", contentType: "application/json", contentEncoding: "br", body: validSourceMap, status: http.StatusBadRequest},
		{name: "malformed map", query: "release=web%401&path=%2Fassets%2Fapp.js", contentType: "application/json", body: []byte(`{`), status: http.StatusBadRequest},
		{name: "oversized map", query: "release=web%401&path=%2Fassets%2Fapp.js", contentType: "application/json", body: bytes.Repeat([]byte(" "), sourcemap.MaxDecodedBytes+1), status: http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			artifacts := &artifactServiceStub{authenticated: artifactauth.Token{ProjectID: 42}}
			maps := &sourceMapRepositoryStub{putInserted: true}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/sourcemaps?"+test.query, bytes.NewReader(test.body))
			request.Header.Set("Authorization", "Bearer fl_art_prefix_secret")
			request.Header.Set("Content-Type", test.contentType)
			request.Header.Set("Content-Encoding", test.contentEncoding)
			request.SetPathValue("slug", "web")
			recorder := httptest.NewRecorder()
			newUploadHandler(artifacts, &artifactProjectServiceStub{project: project.Project{ID: 42}}, maps).UploadSourceMap(recorder, request)
			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}
		})
	}
}

func uploadRequest(body []byte) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/sourcemaps?release=web%401&path=%2Fassets%2Fapp.js", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer fl_art_prefix_secret")
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	return request
}

func TestCreateArtifactTokenReturnsRawTokenOnce(t *testing.T) {
	service := &artifactServiceStub{
		created:  artifactauth.Token{ID: 5, ProjectID: 42, Name: "CI", TokenPrefix: "prefix", TokenHash: [32]byte{1}, CreatedAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)},
		rawToken: "fl_art_prefix_secret",
	}
	projects := &artifactProjectServiceStub{project: project.Project{ID: 42, Slug: "web"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/artifact-tokens", strings.NewReader(`{"name":"CI"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")

	newArtifactHandler(service, projects).CreateToken(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if service.createProject != 42 || service.createName != "CI" {
		t.Fatalf("create input = project %d, name %q", service.createProject, service.createName)
	}
	body := decodeBody(t, recorder)
	if body["raw_token"] != "fl_art_prefix_secret" {
		t.Fatalf("raw_token = %v", body["raw_token"])
	}
	token := body["token"].(map[string]any)
	if token["name"] != "CI" || token["token_prefix"] != "prefix" {
		t.Fatalf("token = %v", token)
	}
	if _, present := token["token_hash"]; present {
		t.Fatal("token unexpectedly exposes token_hash")
	}
}

func TestListArtifactTokensRedactsCredentials(t *testing.T) {
	lastUsed := time.Date(2026, 7, 22, 13, 0, 0, 0, time.UTC)
	service := &artifactServiceStub{tokens: []artifactauth.Token{{
		ID: 5, ProjectID: 42, Name: "CI", TokenPrefix: "prefix", TokenHash: [32]byte{1}, CreatedAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC), LastUsedAt: &lastUsed,
	}}}
	projects := &artifactProjectServiceStub{project: project.Project{ID: 42, Slug: "web"}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/artifact-tokens", http.NoBody)
	request.SetPathValue("slug", "web")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

	newArtifactHandler(service, projects).ListTokens(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	body := decodeBody(t, recorder)
	tokens := body["tokens"].([]any)
	if len(tokens) != 1 {
		t.Fatalf("tokens = %v", tokens)
	}
	token := tokens[0].(map[string]any)
	if token["token_prefix"] != "prefix" || token["last_used_at"] != "2026-07-22T13:00:00Z" {
		t.Fatalf("token = %v", token)
	}
	for _, field := range []string{"raw_token", "token_hash"} {
		if _, present := token[field]; present {
			t.Fatalf("listed token unexpectedly exposes %s", field)
		}
	}
}

func TestCreateArtifactTokenRejectsInvalidRequest(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"wrong content type", "text/plain", `{"name":"CI"}`},
		{"unknown field", "application/json", `{"name":"CI","extra":true}`},
		{"blank name", "application/json", `{"name":"  "}`},
		{"long name", "application/json", `{"name":"` + strings.Repeat("a", 101) + `"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &artifactServiceStub{}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/artifact-tokens", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			request.SetPathValue("slug", "web")

			newArtifactHandler(service, &artifactProjectServiceStub{project: project.Project{ID: 42}}).CreateToken(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if service.createCalls != 0 {
				t.Fatal("create service reached for invalid request")
			}
		})
	}
}

func TestArtifactTokensMapMissingProjectAndToken(t *testing.T) {
	t.Run("missing project", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/ghost/artifact-tokens", http.NoBody)
		request.SetPathValue("slug", "ghost")
		request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

		newArtifactHandler(&artifactServiceStub{}, &artifactProjectServiceStub{err: project.ErrNotFound}).ListTokens(recorder, request)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
		}
	})
	t.Run("missing project when revoking", func(t *testing.T) {
		service := &artifactServiceStub{}
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/ghost/artifact-tokens/5/revoke", http.NoBody)
		request.SetPathValue("slug", "ghost")
		request.SetPathValue("id", "5")

		newArtifactHandler(service, &artifactProjectServiceStub{err: project.ErrNotFound}).RevokeToken(recorder, request)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
		}
		if service.revokeCalls != 0 {
			t.Fatal("revoke service reached for missing project")
		}
	})
	t.Run("missing token", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/artifact-tokens/5/revoke", http.NoBody)
		request.SetPathValue("slug", "web")
		request.SetPathValue("id", "5")

		newArtifactHandler(&artifactServiceStub{revokeErr: artifactauth.ErrNotFound}, &artifactProjectServiceStub{}).RevokeToken(recorder, request)

		if recorder.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
		}
	})
}

func TestRevokeArtifactTokenRejectsBadID(t *testing.T) {
	service := &artifactServiceStub{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/artifact-tokens/nope/revoke", http.NoBody)
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "nope")

	newArtifactHandler(service, &artifactProjectServiceStub{}).RevokeToken(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if service.revokeCalls != 0 {
		t.Fatal("revoke service reached for invalid id")
	}
}

func TestRevokeArtifactTokenReturnsNoContent(t *testing.T) {
	service := &artifactServiceStub{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/artifact-tokens/5/revoke", http.NoBody)
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "5")

	newArtifactHandler(service, &artifactProjectServiceStub{}).RevokeToken(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", recorder.Code, recorder.Body.String())
	}
	if service.revokedSlug != "web" || service.revokedID != 5 {
		t.Fatalf("revoked slug/id = %q/%d", service.revokedSlug, service.revokedID)
	}
}

func TestArtifactTokenReportsServiceErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*ArtifactHandler, http.ResponseWriter, *http.Request)
	}{
		{
			name: "create",
			call: func(handler *ArtifactHandler, w http.ResponseWriter, r *http.Request) { handler.CreateToken(w, r) },
		},
		{
			name: "list",
			call: func(handler *ArtifactHandler, w http.ResponseWriter, r *http.Request) { handler.ListTokens(w, r) },
		},
		{
			name: "revoke",
			call: func(handler *ArtifactHandler, w http.ResponseWriter, r *http.Request) { handler.RevokeToken(w, r) },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &artifactServiceStub{createErr: errors.New("failed"), listErr: errors.New("failed"), revokeErr: errors.New("failed")}
			recorder := httptest.NewRecorder()
			method := http.MethodGet
			var body *strings.Reader
			switch test.name {
			case "create":
				method = http.MethodPost
				body = strings.NewReader(`{"name":"CI"}`)
			case "revoke":
				method = http.MethodPost
				body = strings.NewReader("")
			default:
				body = strings.NewReader("")
			}
			request := httptest.NewRequest(method, "/api/v1/projects/web/artifact-tokens", body)
			if test.name == "create" {
				request.Header.Set("Content-Type", "application/json")
			}
			request.SetPathValue("slug", "web")
			if test.name == "revoke" {
				request.SetPathValue("id", "5")
			}
			// "list" reads the scope directly (scopeOrRefuse); "create" and
			// "revoke" are owner-gated and ignore it, so setting it
			// unconditionally is harmless and keeps this table simple.
			request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))

			test.call(newArtifactHandler(service, &artifactProjectServiceStub{project: project.Project{ID: 42}}), recorder, request)

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

// newArtifactRoutesApp wires ArtifactHandler exactly as internal/app/app.go
// wires it (RequireSession composed with RequireOwner on the write routes,
// RequireSession alone on the list), so these tests exercise the real gate
// rather than the handler in isolation. Source-map upload authenticates on
// its own token, outside RequireSession, so it is not part of this app.
func newArtifactRoutesApp(t *testing.T, user auth.User, service ArtifactService, projects ProjectResolver) http.Handler {
	t.Helper()
	authService := &authServiceStub{authUser: user}
	limiter := newTestLimiter()
	authHandler := NewAuthHandler(authService, limiter, &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	artifactHandler := NewArtifactHandler(service, projects, nil, Options{})
	owner := func(next http.HandlerFunc) http.Handler {
		return authHandler.RequireSession(authHandler.RequireOwner(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/projects/{slug}/artifact-tokens", authHandler.RequireSession(http.HandlerFunc(artifactHandler.ListTokens)))
	mux.Handle("POST /api/v1/projects/{slug}/artifact-tokens", owner(artifactHandler.CreateToken))
	mux.Handle("POST /api/v1/projects/{slug}/artifact-tokens/{id}/revoke", owner(artifactHandler.RevokeToken))
	return mux
}

func TestCreateArtifactTokenRejectsAMember(t *testing.T) {
	handler := newArtifactRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &artifactServiceStub{}, &artifactProjectServiceStub{project: project.Project{ID: 42, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/projects/web/artifact-tokens", `{"name":"CI"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestRevokeArtifactTokenRejectsAMember(t *testing.T) {
	handler := newArtifactRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &artifactServiceStub{}, &artifactProjectServiceStub{project: project.Project{ID: 42, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/projects/web/artifact-tokens/5/revoke", ""))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}
