package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/auth"
)

func TestEncodedStructuralCharactersCannotBypassProtectedAPIBoundary(t *testing.T) {
	handler := New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession}))
	for _, path := range []string{
		"/api%2fv1/projects",
		"/api%2Fv1/projects",
		"/api%5cv1/projects",
		"/api%5Cv1/projects",
		"/x/%2e%2e/api/v1/projects",
		"/x/%2E%2e/api/v1/projects",
		"/x/.%2e/api/v1/projects",
		"/x/%2e./api/v1/projects",
		"/x%2f%2e%2e%2fapi/v1/projects",
		`/x\..\api/v1/projects`,
		`/x%5c..\api/v1/projects`,
	} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, http.NoBody)

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "application/json" {
				t.Fatalf("Content-Type = %q, want application/json", got)
			}
			if got := recorder.Body.String(); got != "{\"error\":\"invalid api path\"}\n" {
				t.Fatalf("body = %q", got)
			}
		})
	}
}

// "/api/%2e/v1/projects" decodes (for bypass detection) to /api/v1/projects,
// but its raw path has a literal "." second segment, which ServeMux does not
// clean for a subtree match the way it cleans literal dot-segments — so it
// falls under the broad "/api/" fallback rather than the "GET /api/v1/projects"
// route. That fallback is still authHandler.RequireSession, so this is not a
// bypass: the request never reaches the projects handler or the dashboard, it
// just gets the reserved-API 401 instead of guardAPIBoundary's 400. Moving the
// ingest route onto /api/{project_id}/envelope forced the exact "/api/v1/"
// subtree (which caught this case as a mismatch) out in favor of the broader
// "/api/" subtree registered in app.go.
func TestEncodedDotSegmentWithinAPIFallsUnderTheReservedAPIFallbackNotTheBypassGuard(t *testing.T) {
	handler := New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/%2e/v1/projects", http.NoBody)

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); strings.HasPrefix(got, "text/html") {
		t.Fatalf("Content-Type = %q, want it to stay off the dashboard bundle", got)
	}
}

func TestEscapedNonSeparatorDashboardPathRemainsPublic(t *testing.T) {
	handler := New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession}))
	for _, path := range []string{"/issues/hello%20world", "/issues/%61lpha"} {
		t.Run(path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, path, http.NoBody)

			handler.ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", recorder.Code)
			}
			if got := recorder.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
				t.Fatalf("Content-Type = %q", got)
			}
		})
	}
}

func TestEncodedNonstructuralDotWithinAPIRemainsProtected(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/file%2etxt", http.NoBody)

	New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", recorder.Code, recorder.Body.String())
	}
}

func TestRawBackslashStructuralPathCannotBypassProtectedAPIBoundary(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	request.URL.Path = `/x\..\api/v1/projects`
	request.URL.RawPath = `/x\..\api/v1/projects`

	New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}

func TestStructuralPathDetectorHandlesRawBackslashes(t *testing.T) {
	if !escapedStructuralPathTargetsAPI(`/x\..\api/v1/projects`) {
		t.Fatal("raw backslash path did not canonicalize into API boundary")
	}
}
