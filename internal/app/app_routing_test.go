package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/auth"
)

func TestHealthDoesNotRequireDatabase(t *testing.T) {
	database := &healthChecker{err: errors.New("offline")}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/healthz", http.NoBody)

	New(Dependencies{Database: database}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if database.calls != 0 {
		t.Fatalf("database calls = %d, want 0", database.calls)
	}
	assertStatusBody(t, recorder, "ok")
}

func TestReadyWhenDatabaseResponds(t *testing.T) {
	database := &healthChecker{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody)

	New(Dependencies{Database: database}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if database.calls != 1 {
		t.Fatalf("database calls = %d, want 1", database.calls)
	}
	assertStatusBody(t, recorder, "ready")
}

func TestUnavailableWhenDatabaseDoesNotRespond(t *testing.T) {
	database := &healthChecker{err: errors.New("offline")}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/readyz", http.NoBody)

	New(Dependencies{Database: database}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusServiceUnavailable)
	}
	assertStatusBody(t, recorder, "unavailable")
}

func TestRouterRegistersIngestAlongsideTheDashboardAPI(t *testing.T) {
	// ServeMux panics when two patterns overlap without one being more specific.
	// Constructing the real router is the only thing that catches it.
	handler := New(Dependencies{EnvelopeHandler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})})

	cases := []struct {
		method string
		path   string
		want   int
	}{
		{http.MethodPost, "/api/42/envelope/", http.StatusOK},
		{http.MethodPost, "/api/42/envelope", http.StatusOK},
		// "/extra" doesn't match either envelope pattern, so it falls through to
		// the reserved-API fallback. POST is an unsafe method there, and this
		// request carries no Origin header, so RequireSession's origin check
		// rejects it (403) before it ever gets to the missing-session check.
		{http.MethodPost, "/api/42/envelope/extra", http.StatusForbidden},
		{http.MethodGet, "/api/v1/bogus", http.StatusUnauthorized},
	}

	for _, testCase := range cases {
		t.Run(testCase.method+" "+testCase.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(testCase.method, testCase.path, http.NoBody))
			if recorder.Code != testCase.want {
				t.Fatalf("got %d, want %d", recorder.Code, testCase.want)
			}
		})
	}
}

func TestUnknownAPIPathDoesNotServeTheDashboardBundle(t *testing.T) {
	handler := New(Dependencies{})
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/bogus", http.NoBody))

	if contentType := recorder.Header().Get("Content-Type"); strings.HasPrefix(contentType, "text/html") {
		t.Fatalf("an unknown API path returned HTML (%s); it must stay on the API contract", contentType)
	}
}

func TestPublicRoutesRemainPublic(t *testing.T) {
	handler := New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession}))
	for _, path := range []string{"/healthz", "/readyz", "/", "/app.js"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
		handler.ServeHTTP(recorder, request)
		if recorder.Code == http.StatusUnauthorized {
			t.Fatalf("%s unexpectedly protected", path)
		}
	}
}

func TestDashboardFallback(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/issues/123", http.NoBody)

	New(Dependencies{Database: &healthChecker{}}).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if contentType := recorder.Header().Get("Content-Type"); contentType != "text/html; charset=utf-8" {
		t.Fatalf("Content-Type = %q, want text/html", contentType)
	}
}

func TestArtifactRoutesAbsentWithoutArtifactService(t *testing.T) {
	// With an authenticated session but no artifact service, the token and
	// source-map routes must not be registered — the request falls through to
	// the reserved-API catch-all (501), never a handler panic.
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})
	dependencies.ArtifactService = nil
	handler := New(dependencies)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/artifact-tokens", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("artifact-tokens status = %d, want 501 (route absent): %s", recorder.Code, recorder.Body.String())
	}
}

func TestGroupingRuleRoutesAbsentWithoutGroupingRuleService(t *testing.T) {
	// With an authenticated session but no grouping-rule service, the routes
	// must not be registered — the request falls through to the reserved-API
	// catch-all (501), never a handler panic.
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})
	dependencies.GroupingRuleService = nil
	handler := New(dependencies)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/grouping-rules", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("grouping-rules status = %d, want 501 (route absent): %s", recorder.Code, recorder.Body.String())
	}
}

// /api/ is registered as a session-gated 501 fallback. Every /api/0 route the
// sentry-cli handler owns must out-rank it on specificity. Without this test
// a resolution change would turn the whole sentry-cli chunk-upload/assemble
// surface into 501s and no other assertion would notice: the CLI's error
// would be a transport failure, not a test failure.
//
// All nine rows expect the same 401, because all nine routes are registered
// and every one authenticates before doing anything else. The
// body assertion is still what matters, not just the status: if the /api/
// fallback ever won one of these routes back (a specificity regression),
// RequireSession's own order (middleware.go:49-56, SameOrigin(r) before the
// session check) would turn the two unsafe-method rows (POST/PUT, no Origin
// header here) into 403 "origin not allowed" while the safe-method (GET)
// rows would still read 401 "authentication required" — a status-only
// assertion would keep passing on the GET rows even though the fallback had
// silently taken over, because 401 is what both the real handler and the
// fallback answer for a safe method with no session. The message text is
// what actually distinguishes SentryCLIHandler's answer from the fallback's.
func TestSentryCLIRoutesOutrankTheAPIFallback(t *testing.T) {
	handler := New(Dependencies{
		Database:         &healthChecker{},
		ArtifactService:  stubArtifactService{},
		ProjectService:   stubProjectResolver{},
		UploadChunks:     stubChunkStore{},
		Releases:         stubReleaseStore{},
		SourceMaps:       stubSourceMapRepository{},
		ProguardMappings: stubProguardRepository{},
	})

	for _, target := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/0/organizations/demo/chunk-upload/"},
		{http.MethodPost, "/api/0/organizations/demo/chunk-upload/"},
		{http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/"},
		{http.MethodPost, "/api/0/projects/demo/demo/files/difs/assemble/"},
		{http.MethodPost, "/api/0/projects/demo/demo/files/dsyms/"},
		{http.MethodPost, "/api/0/projects/demo/demo/releases/"},
		{http.MethodPut, "/api/0/projects/demo/demo/releases/web@1.0.0/"},
		{http.MethodGet, "/api/0/organizations/demo/repos/"},
		{http.MethodGet, "/api/0/organizations/demo/releases/web@1.0.0/previous-with-commits/"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(target.method, target.path, http.NoBody)
		handler.ServeHTTP(recorder, request)

		// No Authorization header, so the correct answer is 401 from the
		// sentry-cli handler. 501 means the /api/ fallback swallowed it.
		if recorder.Code == http.StatusNotImplemented {
			t.Fatalf("%s %s fell through to the /api/ fallback", target.method, target.path)
		}
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s %s status = %d, want %d", target.method, target.path, recorder.Code, http.StatusUnauthorized)
		}
		// The status code alone does not distinguish SentryCLIHandler's 401
		// from the /api/ fallback's own: an unauthenticated GET with no
		// session cookie gets 401 from RequireSession too (see
		// TestSentryCLIRoutesAbsentWithoutUploadChunks, where the GET route
		// is genuinely absent and still answers 401). The error message is
		// what actually proves this request reached SentryCLIHandler: its own
		// wording ("invalid artifact token") versus the fallback's
		// ("authentication required").
		var decoded struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
			t.Fatalf("%s %s decode: %v", target.method, target.path, err)
		}
		if decoded.Error != "invalid artifact token" {
			t.Fatalf("%s %s error = %q, want %q (the /api/ fallback's own 401 says %q)",
				target.method, target.path, decoded.Error, "invalid artifact token", "authentication required")
		}
	}
}

// The two upload-proguard routes exist only when a mapping repository is wired,
// so a deployment without one does not answer a mapping upload with a handler
// that would dereference a nil repository. Absent, the request lands on the
// /api/ fallback, whose answer is not the sentry-cli handler's.
func TestProguardUploadRoutesAbsentWithoutMappingRepository(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})
	dependencies.ArtifactService = stubArtifactService{}
	dependencies.ProjectService = stubProjectResolver{}
	dependencies.UploadChunks = stubChunkStore{}
	dependencies.Releases = stubReleaseStore{}
	dependencies.SourceMaps = stubSourceMapRepository{}
	handler := New(dependencies)

	for _, path := range []string{
		"/api/0/projects/demo/demo/files/difs/assemble/",
		"/api/0/projects/demo/demo/files/dsyms/",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		request.Header.Set("Cookie", api.SessionCookieName+"=token")
		request.Header.Set("Origin", "http://"+request.Host)
		handler.ServeHTTP(recorder, request)

		// Matches TestArtifactRoutesAbsentWithoutArtifactService and
		// TestGroupingRuleRoutesAbsentWithoutGroupingRuleService: 501 is what
		// the /api/ fallback itself answers, so this proves the request fell
		// through to it rather than merely failing to match "invalid artifact
		// token" (which a handler panic or an unrelated 4xx would also do).
		if recorder.Code != http.StatusNotImplemented {
			t.Fatalf("%s status = %d, want 501 (route absent): %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

// TestGroupRoutesAbsentWithoutGroupService closes the same gap as the two
// tests above, for group administration: before a recent fix, testDependencies
// always left GroupService nil, so every app test exercised only the false
// branch of app.go's "if dependencies.GroupService != nil" guard. The fix
// wired a non-nil stub into testDependencies, which flipped that coverage to
// the true branch — and left the false branch, asserted here, covered by
// nothing.
func TestGroupRoutesAbsentWithoutGroupService(t *testing.T) {
	// With an authenticated session but no group service, the routes must not
	// be registered — the request falls through to the reserved-API catch-all
	// (501), never a handler panic.
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})
	dependencies.GroupService = nil
	handler := New(dependencies)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/groups", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("groups status = %d, want 501 (route absent): %s", recorder.Code, recorder.Body.String())
	}
}

// TestSentryCLIRoutesAbsentWithoutUploadChunks closes the same gap as the
// tests above, for the sentry-cli surface: without an UploadChunks store, the
// chunk-upload route must not be registered — the request falls through to
// the reserved-API catch-all instead. Unlike the group/grouping-rule/artifact
// absent-route tests, the fallback's own answer here is 401, not 501: this
// request carries no session cookie, and GET is a safe method, so
// RequireSession's origin check passes and its missing-session check runs
// and returns 401 "authentication required" before notImplemented ever
// executes. 501 would mean an authenticated session reached the fallback
// instead of a real handler; this test's point is that no *unauthenticated*
// sentry-cli-shaped request reaches a real handler either.
func TestSentryCLIRoutesAbsentWithoutUploadChunks(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})
	dependencies.ArtifactService = &appArtifactServiceStub{}
	dependencies.UploadChunks = nil
	handler := New(dependencies)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/0/organizations/demo/chunk-upload/", http.NoBody)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("chunk-upload status = %d, want 401 (route absent, fallback's own session gate): %s", recorder.Code, recorder.Body.String())
	}
}

// The guard on sentryCLIRoutes exists so a narrow test can assemble a partial
// app without a handler dereferencing a nil dependency. Every dependency the
// handlers actually use must be in it — UploadChunks and ArtifactService alone
// left three (projects, source maps, releases) promising a safety they did not
// provide.
func TestSentryCLIRoutesAbsentWithoutEveryDependency(t *testing.T) {
	for _, missing := range []struct {
		name  string
		clear func(*Dependencies)
	}{
		{"ArtifactService", func(d *Dependencies) { d.ArtifactService = nil }},
		{"UploadChunks", func(d *Dependencies) { d.UploadChunks = nil }},
		{"ProjectService", func(d *Dependencies) { d.ProjectService = nil }},
		{"SourceMaps", func(d *Dependencies) { d.SourceMaps = nil }},
		{"Releases", func(d *Dependencies) { d.Releases = nil }},
	} {
		t.Run(missing.name, func(t *testing.T) {
			dependencies := Dependencies{
				Database:        &healthChecker{},
				ArtifactService: stubArtifactService{},
				ProjectService:  stubProjectResolver{},
				UploadChunks:    stubChunkStore{},
				Releases:        stubReleaseStore{},
				SourceMaps:      stubSourceMapRepository{},
			}
			missing.clear(&dependencies)

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", http.NoBody)
			New(dependencies).ServeHTTP(recorder, request)

			// No Authorization header and no Origin: the registered route
			// answers 401 "invalid artifact token", while the /api/ fallback
			// answers 403 from RequireSession's origin check on an unsafe
			// method. Anything but 403 means the route was registered.
			if recorder.Code != http.StatusForbidden {
				t.Fatalf("without %s: status = %d, want 403 (route absent, fallback's origin check): %s",
					missing.name, recorder.Code, recorder.Body.String())
			}
		})
	}
}
