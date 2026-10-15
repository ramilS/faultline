package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/auth"
)

// Every route must state its authorization posture. This is the whole point of
// the table: the posture is data, so a test can enumerate it.
func TestEveryRouteDeclaresAGate(t *testing.T) {
	dependencies := testDependencies(t)
	for _, current := range routeTable(dependencies, testAuthHandler(t, dependencies)) {
		if current.gate == gateUnset {
			t.Errorf("route %q declares no authorization gate", current.pattern)
		}
	}
}

// A route registered without a declared gate must stop the process at boot.
// Serving it would mean serving unscoped data, and a panic at startup is the
// only failure mode that cannot be missed.
func TestRegisterPanicsOnUndeclaredGate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("register accepted a route with no gate")
		}
	}()
	register(http.NewServeMux(), testAuthHandler(t, testDependencies(t)), []route{
		{pattern: "GET /api/v1/undeclared", handler: http.NotFoundHandler()},
	})
}

// The member-reachable set is the feature's security surface. Pinning its size
// makes any addition or removal a deliberate, reviewed change rather than a
// side effect of adding a route.
func TestMemberReachableRouteCount(t *testing.T) {
	count := 0
	dependencies := testDependencies(t)
	for _, current := range routeTable(dependencies, testAuthHandler(t, dependencies)) {
		if current.gate == gateMember {
			count++
		}
	}
	// 17 project-bearing routes (the 17th is the read-only data-scrubbing
	// configuration) plus GET /api/v1/users/assignable and the every-project
	// GET /api/v1/issues.
	if count != 19 {
		t.Errorf("gateMember routes = %d, want 19: a new project-bearing route must be gated as gateMember, and this count updated deliberately", count)
	}
}

// A malformed API path must never be served as a success, regardless of
// which HTTP status the mux's path-cleaning redirect happens to use.
func TestMalformedDoubleSlashPathNeverSucceeds(t *testing.T) {
	handler := New(testDependencies(t))
	request := httptest.NewRequest(http.MethodGet, "/api//v1/projects", http.NoBody)
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, request)

	if recorder.Code == http.StatusOK {
		t.Fatalf("double-slash API path returned 200, want a redirect or an error")
	}
	t.Logf("double-slash API path status = %d", recorder.Code)
}

// stubSSOService and stubIdentityLoginService satisfy api.SSOService and
// api.IdentityLoginService just well enough to build an *api.SSOHandler for
// routing tests — none of these tests ever invoke a handler.
type stubSSOService struct{}

func (stubSSOService) AuthCodeURL(string, string, string) string { return "" }

func (stubSSOService) Exchange(context.Context, string, string, string) (auth.IdentityClaims, error) {
	return auth.IdentityClaims{}, nil
}

type stubIdentityLoginService struct{}

func (stubIdentityLoginService) LoginWithIdentity(context.Context, string, auth.IdentityClaims) (auth.User, string, error) {
	return auth.User{}, "", nil
}

func testSSOHandler() *api.SSOHandler {
	return api.NewSSOHandler(stubSSOService{}, stubIdentityLoginService{}, nil, nil, api.Options{})
}

// With no SSOHandler wired, ssoRoutes must yield only the providers probe:
// the flow routes redirecting to a provider that does not exist would be
// worse than a 404.
func TestSSORoutesWithoutHandlerOnlyAdvertisesProviders(t *testing.T) {
	routes := ssoRoutes(Dependencies{})
	if len(routes) != 1 {
		t.Fatalf("routes = %d, want 1", len(routes))
	}
	if routes[0].pattern != "GET /api/v1/auth/providers" {
		t.Errorf("pattern = %q, want GET /api/v1/auth/providers", routes[0].pattern)
	}
}

// With an SSOHandler wired, ssoRoutes must yield all three routes.
func TestSSORoutesWithHandlerAdvertisesFullFlow(t *testing.T) {
	routes := ssoRoutes(Dependencies{SSOHandler: testSSOHandler()})
	wantPatterns := map[string]bool{
		"GET /api/v1/auth/providers":    true,
		"GET /api/v1/auth/sso/start":    true,
		"GET /api/v1/auth/sso/callback": true,
	}
	if len(routes) != len(wantPatterns) {
		t.Fatalf("routes = %d, want %d", len(routes), len(wantPatterns))
	}
	for _, current := range routes {
		if !wantPatterns[current.pattern] {
			t.Errorf("unexpected pattern %q", current.pattern)
		}
	}
}

// Every SSO route is gatePublic: the caller has no session yet by
// definition, and /auth/providers must be readable before anyone signs in.
func TestSSORoutesAreAllGatePublic(t *testing.T) {
	routes := ssoRoutes(Dependencies{SSOHandler: testSSOHandler()})
	for _, current := range routes {
		if current.gate != gatePublic {
			t.Errorf("route %q gate = %v, want gatePublic", current.pattern, current.gate)
		}
	}
}
