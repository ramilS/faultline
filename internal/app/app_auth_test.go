package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/project"
)

func TestProtectedAPIRequiresAuthentication(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)

	New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "" {
		t.Fatalf("unrelated protected API Cache-Control = %q, want empty", got)
	}
}

func TestCurrentUserMiddlewareResponsesArePrivateAndNotStored(t *testing.T) {
	for _, test := range []struct {
		name    string
		service *authServiceStub
		cookie  string
		status  int
	}{
		{"unauthenticated", &authServiceStub{}, "", http.StatusUnauthorized},
		{"authentication service error", &authServiceStub{authErr: errors.New("storage unavailable")}, api.SessionCookieName + "=token", http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
			if test.cookie != "" {
				request.Header.Set("Cookie", test.cookie)
			}

			New(authDependencies(test.service)).ServeHTTP(recorder, request)

			if recorder.Code != test.status {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.status, recorder.Body.String())
			}
			if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Fatalf("Cache-Control = %q, want private, no-store", got)
			}
		})
	}
}

func TestProtectedAPIReachesReservedHandlerWithValidSession(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/reserved", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")

	New(authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNotImplemented)
	}
}

func TestProjectRoutesRequireSession(t *testing.T) {
	for _, path := range []string{"/api/v1/projects", "/api/v1/projects/web/keys"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, path, http.NoBody)

		New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", path, recorder.Code)
		}
	}
}

func TestArtifactTokenRoutesRequireSession(t *testing.T) {
	for _, target := range []string{
		"/api/v1/projects/web/artifact-tokens",
		"/api/v1/projects/web/artifact-tokens/5/revoke",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, http.NoBody)

		New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", target, recorder.Code)
		}
	}
}

func TestArtifactTokenListRouteUsesHandler(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})
	dependencies.ProjectService = &appProjectServiceStub{found: project.Project{ID: 1, Slug: "web"}}
	dependencies.ArtifactService = &appArtifactServiceStub{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/artifact-tokens", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}

func TestGroupingRuleRoutesRequireSession(t *testing.T) {
	for _, target := range []string{
		"/api/v1/projects/web/grouping-rules",
		"/api/v1/projects/web/grouping-rules/2/move",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, http.NoBody)

		New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", target, recorder.Code)
		}
	}
}

func TestGroupingRuleListRouteUsesHandler(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})
	dependencies.ProjectService = &appProjectServiceStub{found: project.Project{ID: 1, Slug: "web"}}
	dependencies.GroupingRuleService = &appGroupingRuleServiceStub{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/grouping-rules", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}

func TestIssueRoutesRequireSession(t *testing.T) {
	handler := New(Dependencies{IssueService: &stubIssueService{}})
	for _, target := range []string{
		"/api/v1/projects/web/issues",
		"/api/v1/issues/5",
		"/api/v1/issues/5/events",
		"/api/v1/issues/5/events/latest",
		"/api/v1/issues/5/stats",
		"/api/v1/events/00000000-0000-0000-0000-000000000000",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, http.NoBody)
		handler.ServeHTTP(recorder, request)
		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", target, recorder.Code)
		}
	}
}

func TestSourceMapRouteUsesBearerAuthenticationWithoutSession(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})
	dependencies.ProjectService = &appProjectServiceStub{found: project.Project{ID: 42, Slug: "web"}}
	dependencies.ArtifactService = &appArtifactServiceStub{authenticated: artifactauth.Token{ProjectID: 42}}
	dependencies.SourceMaps = &appSourceMapRepositoryStub{inserted: true}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/sourcemaps?release=web%401&path=%2Fassets%2Fapp.js", strings.NewReader(`{"version":3,"file":"app.js","sources":["app.ts"],"sourcesContent":["console.log(1);"],"names":[],"mappings":"AAAA"}`))
	request.Header.Set("Authorization", "Bearer fl_art_prefix_secret")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
}

func TestUserRoutesRequireSession(t *testing.T) {
	for _, target := range []string{
		"/api/v1/users",
		"/api/v1/users/1",
		"/api/v1/users/1/password",
		"/api/v1/users/assignable",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, target, http.NoBody)

		New(authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusUnauthorized {
			t.Fatalf("%s status = %d, want 401", target, recorder.Code)
		}
	}
}

func TestUserRoutesRejectASignedInMember(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})
	dependencies.UserAdminService = &appUserAdminServiceStub{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
}

func TestUserListRouteUsesHandlerForAnOwner(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1, Role: auth.RoleOwner}})
	dependencies.UserAdminService = &appUserAdminServiceStub{list: []auth.User{{ID: 1, Email: "owner@example.com", Role: auth.RoleOwner}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAssignableUsersRouteAllowsAMemberWithoutOwnerRights(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})
	dependencies.UserAdminService = &appUserAdminServiceStub{assignable: []auth.User{{ID: 1, Email: "a@example.com"}}}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/users/assignable?project=web", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
}

// TestChangeOwnPasswordRouteAllowsAMemberWithoutOwnerRights confirms the
// wiring: POST /api/v1/auth/password sits behind RequireSession alone, not
// RequireOwner, unlike every other route on userHandler.
func TestChangeOwnPasswordRouteAllowsAMemberWithoutOwnerRights(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})
	dependencies.UserAdminService = &appUserAdminServiceStub{}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/auth/password",
		strings.NewReader(`{"current_password":"the-real-one-12","new_password":"a-new-password-12"}`))
	request.Header.Set("Cookie", api.SessionCookieName+"=token")
	request.Header.Set("Origin", "https://example.com")
	request.Header.Set("Content-Type", "application/json")

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", recorder.Code, recorder.Body.String())
	}
}

func TestUserMutationsRequireOrigin(t *testing.T) {
	for _, path := range []string{"/api/v1/users", "/api/v1/users/1/password", "/api/v1/auth/password"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		request.Header.Set("Cookie", api.SessionCookieName+"=token")

		dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1, Role: auth.RoleOwner}})
		dependencies.UserAdminService = &appUserAdminServiceStub{}
		New(dependencies).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403 without Origin", path, recorder.Code)
		}
	}
}

// TestProjectMutationRoutesRejectASignedInMember proves the gate at the
// point that actually matters: internal/app/app.go's own wiring, not just
// the ProjectHandler/RequireOwner composition in isolation. A member reading
// this response never sees the create/revoke UI, but nothing stops them
// calling the API directly.
func TestProjectMutationRoutesRejectASignedInMember(t *testing.T) {
	for _, path := range []string{
		"/api/v1/projects",
		"/api/v1/projects/web/keys",
		"/api/v1/projects/web/keys/1/revoke",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "https://example.com"+path, http.NoBody)
		request.Header.Set("Cookie", api.SessionCookieName+"=token")
		request.Header.Set("Origin", "https://example.com")

		New(authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403: %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGroupingRuleMutationRoutesRejectASignedInMember(t *testing.T) {
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v1/projects/web/grouping-rules"},
		{http.MethodPatch, "/api/v1/projects/web/grouping-rules/2"},
		{http.MethodPost, "/api/v1/projects/web/grouping-rules/2/move"},
		{http.MethodDelete, "/api/v1/projects/web/grouping-rules/2"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(test.method, "https://example.com"+test.path, http.NoBody)
		request.Header.Set("Cookie", api.SessionCookieName+"=token")
		request.Header.Set("Origin", "https://example.com")

		dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})
		dependencies.GroupingRuleService = &appGroupingRuleServiceStub{}
		New(dependencies).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s %s status = %d, want 403: %s", test.method, test.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestDataScrubbingUpdateRouteRejectsASignedInMember(t *testing.T) {
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "https://example.com/api/v1/projects/web/data-scrubbing", http.NoBody)
	request.Header.Set("Cookie", api.SessionCookieName+"=token")
	request.Header.Set("Origin", "https://example.com")

	dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})
	dependencies.DataScrubbingService = &appDataScrubbingServiceStub{}
	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
}

func TestGlobalDataScrubbingRoutesRejectASignedInMember(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(method, "https://example.com/api/v1/settings/data-scrubbing", http.NoBody)
		request.Header.Set("Cookie", api.SessionCookieName+"=token")
		request.Header.Set("Origin", "https://example.com")

		dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})
		dependencies.DataScrubbingService = &appDataScrubbingServiceStub{}
		dependencies.GlobalDataScrubbingService = &appGlobalDataScrubbingServiceStub{}
		New(dependencies).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403: %s", method, recorder.Code, recorder.Body.String())
		}
	}
}

func TestGlobalDataScrubbingRouteServesAnOwner(t *testing.T) {
	for name, withProjectService := range map[string]bool{"with the project service": true, "global service only": false} {
		t.Run(name, func(t *testing.T) {
			dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 1, Role: auth.RoleOwner}})
			dependencies.GlobalDataScrubbingService = &appGlobalDataScrubbingServiceStub{}
			if withProjectService {
				dependencies.DataScrubbingService = &appDataScrubbingServiceStub{}
			}
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/settings/data-scrubbing", http.NoBody)
			request.Header.Set("Cookie", api.SessionCookieName+"=token")

			New(dependencies).ServeHTTP(recorder, request)

			if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"rules":[]}` {
				t.Fatalf("response = %d %s, want 200 {\"rules\":[]}", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestArtifactTokenMutationRoutesRejectASignedInMember(t *testing.T) {
	for _, path := range []string{
		"/api/v1/projects/web/artifact-tokens",
		"/api/v1/projects/web/artifact-tokens/5/revoke",
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "https://example.com"+path, http.NoBody)
		request.Header.Set("Cookie", api.SessionCookieName+"=token")
		request.Header.Set("Origin", "https://example.com")

		dependencies := authDependencies(&authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}})
		dependencies.ArtifactService = &appArtifactServiceStub{}
		New(dependencies).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403: %s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestProjectMutationsRequireOrigin(t *testing.T) {
	for _, path := range []string{"/api/v1/projects", "/api/v1/projects/web/keys/1/revoke"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, path, http.NoBody)
		request.Header.Set("Cookie", api.SessionCookieName+"=token")

		New(authDependencies(&authServiceStub{authUser: auth.User{ID: 1}})).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Fatalf("%s status = %d, want 403 without Origin", path, recorder.Code)
		}
	}
}
