package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/project"
)

type stubGroupingRuleService struct {
	rules      []grouping.Rule
	rule       grouping.Rule
	err        error
	lastMove   grouping.MoveDirection
	lastCreate struct {
		projectID         int64
		matcher, template string
	}
	lastUpdate struct {
		projectID, ruleID int64
		matcher, template string
	}
	lastDelete                                                  struct{ projectID, ruleID int64 }
	listCalls, createCalls, updateCalls, moveCalls, deleteCalls int
}

func (s *stubGroupingRuleService) ListRules(context.Context, string) ([]grouping.Rule, error) {
	s.listCalls++
	return s.rules, s.err
}

func (s *stubGroupingRuleService) CreateRule(_ context.Context, projectID int64, matcher, template string) (grouping.Rule, error) {
	s.createCalls++
	s.lastCreate.projectID, s.lastCreate.matcher, s.lastCreate.template = projectID, matcher, template
	return s.rule, s.err
}

func (s *stubGroupingRuleService) UpdateRule(_ context.Context, projectID, ruleID int64, matcher, template string) (grouping.Rule, error) {
	s.updateCalls++
	s.lastUpdate.projectID, s.lastUpdate.ruleID = projectID, ruleID
	s.lastUpdate.matcher, s.lastUpdate.template = matcher, template
	return s.rule, s.err
}

func (s *stubGroupingRuleService) MoveRule(_ context.Context, _, _ int64, direction grouping.MoveDirection) (grouping.Rule, error) {
	s.moveCalls++
	s.lastMove = direction
	return s.rule, s.err
}

func (s *stubGroupingRuleService) DeleteRule(_ context.Context, projectID, ruleID int64) error {
	s.deleteCalls++
	s.lastDelete.projectID, s.lastDelete.ruleID = projectID, ruleID
	return s.err
}

type stubGroupingRuleProjects struct {
	project project.Project
	err     error
}

func (s *stubGroupingRuleProjects) FindProject(context.Context, access.Scope, string) (project.Project, error) {
	return s.project, s.err
}

func TestListGroupingRules(t *testing.T) {
	service := &stubGroupingRuleService{rules: []grouping.Rule{{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "billing"}}}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/grouping-rules", http.NoBody)
	request.SetPathValue("slug", "web")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	recorder := httptest.NewRecorder()
	handler.ListRules(recorder, request)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"matcher":"logger:billing"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestListGroupingRulesProjectNotFound(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{err: project.ErrNotFound}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/missing/grouping-rules", http.NoBody)
	request.SetPathValue("slug", "missing")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	recorder := httptest.NewRecorder()
	handler.ListRules(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if service.listCalls != 0 {
		t.Fatalf("service reached for an unresolved project")
	}
}

func TestListGroupingRulesReportsServiceError(t *testing.T) {
	service := &stubGroupingRuleService{err: context.DeadlineExceeded}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	var reported error
	handler := NewGroupingRuleHandler(service, projects, Options{ReportError: func(err error) { reported = err }})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/grouping-rules", http.NoBody)
	request.SetPathValue("slug", "web")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	recorder := httptest.NewRecorder()
	handler.ListRules(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if !errors.Is(reported, context.DeadlineExceeded) {
		t.Fatalf("reported error = %v, want wrapped context.DeadlineExceeded", reported)
	}
}

func TestCreateGroupingRule(t *testing.T) {
	service := &stubGroupingRuleService{rule: grouping.Rule{ID: 2, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "billing"}}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules", strings.NewReader(`{"matcher":"logger:billing","template":"billing"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	recorder := httptest.NewRecorder()
	handler.CreateRule(recorder, request)

	if recorder.Code != http.StatusCreated || !strings.Contains(recorder.Body.String(), `"id":2`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if service.lastCreate.projectID != 5 || service.lastCreate.matcher != "logger:billing" || service.lastCreate.template != "billing" {
		t.Fatalf("lastCreate = %+v", service.lastCreate)
	}
}

func TestCreateGroupingRuleValidatesRequest(t *testing.T) {
	for _, test := range []struct {
		name        string
		contentType string
		body        string
	}{
		{"wrong content type", "text/plain", `{"matcher":"logger:billing","template":"billing"}`},
		{"malformed json", "application/json", `{`},
		{"unknown field", "application/json", `{"matcher":"logger:billing","template":"billing","extra":1}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &stubGroupingRuleService{}
			projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
			handler := NewGroupingRuleHandler(service, projects, Options{})

			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules", strings.NewReader(test.body))
			request.Header.Set("Content-Type", test.contentType)
			request.SetPathValue("slug", "web")
			recorder := httptest.NewRecorder()
			handler.CreateRule(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if service.createCalls != 0 {
				t.Fatalf("service reached with invalid input")
			}
		})
	}
}

func TestCreateGroupingRuleInvalidMatcherReturns400(t *testing.T) {
	service := &stubGroupingRuleService{err: grouping.ErrInvalidMatcher}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules", strings.NewReader(`{"matcher":"bad","template":"literal"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	recorder := httptest.NewRecorder()
	handler.CreateRule(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestCreateGroupingRuleTooManyRulesReturns400(t *testing.T) {
	service := &stubGroupingRuleService{err: grouping.ErrTooManyRules}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules", strings.NewReader(`{"matcher":"logger:billing","template":"billing"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	recorder := httptest.NewRecorder()
	handler.CreateRule(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestCreateGroupingRuleReportsMissingProject(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{err: project.ErrNotFound}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/missing/grouping-rules", strings.NewReader(`{"matcher":"logger:billing","template":"billing"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "missing")
	recorder := httptest.NewRecorder()
	handler.CreateRule(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
	if service.createCalls != 0 {
		t.Fatalf("service reached for an unresolved project")
	}
}

func TestCreateGroupingRuleReportsInternalError(t *testing.T) {
	service := &stubGroupingRuleService{err: context.DeadlineExceeded}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	var reported error
	handler := NewGroupingRuleHandler(service, projects, Options{ReportError: func(err error) { reported = err }})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules", strings.NewReader(`{"matcher":"logger:billing","template":"billing"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	recorder := httptest.NewRecorder()
	handler.CreateRule(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if !errors.Is(reported, context.DeadlineExceeded) {
		t.Fatalf("reported error = %v, want wrapped context.DeadlineExceeded", reported)
	}
}

func TestUpdateGroupingRule(t *testing.T) {
	service := &stubGroupingRuleService{rule: grouping.Rule{ID: 2, ProjectID: 5, Position: 1, Matcher: "logger:refunds", Template: "refunds"}}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/web/grouping-rules/2", strings.NewReader(`{"matcher":"logger:refunds","template":"refunds"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.UpdateRule(recorder, request)

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"template":"refunds"`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if service.lastUpdate.projectID != 5 || service.lastUpdate.ruleID != 2 {
		t.Fatalf("lastUpdate = %+v", service.lastUpdate)
	}
}

func TestUpdateGroupingRuleRejectsInvalidID(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/web/grouping-rules/abc", strings.NewReader(`{"matcher":"logger:billing","template":"billing"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "abc")
	recorder := httptest.NewRecorder()
	handler.UpdateRule(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if service.updateCalls != 0 {
		t.Fatalf("service reached with invalid id")
	}
}

func TestUpdateGroupingRuleRejectsWrongContentType(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/web/grouping-rules/2", strings.NewReader(`{"matcher":"logger:billing","template":"billing"}`))
	request.Header.Set("Content-Type", "text/plain")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.UpdateRule(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if service.updateCalls != 0 {
		t.Fatalf("service reached with invalid content type")
	}
}

func TestUpdateGroupingRuleInvalidTemplateReturns400(t *testing.T) {
	service := &stubGroupingRuleService{err: grouping.ErrInvalidTemplate}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/web/grouping-rules/2", strings.NewReader(`{"matcher":"logger:billing","template":"{bad"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.UpdateRule(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// TestUpdateGroupingRuleNotFound also covers the cross-project scoping case: a
// rule ID that exists but belongs to a different project is indistinguishable,
// at the repository layer, from one that does not exist at all under the
// resolved project ID — both surface as ErrRuleNotFound and must map to 404,
// never leak the other project's rule.
func TestUpdateGroupingRuleNotFound(t *testing.T) {
	service := &stubGroupingRuleService{err: grouping.ErrRuleNotFound}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/projects/web/grouping-rules/999", strings.NewReader(`{"matcher":"logger:billing","template":"billing"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "999")
	recorder := httptest.NewRecorder()
	handler.UpdateRule(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if service.lastUpdate.projectID != 5 {
		t.Fatalf("update scoped to project id = %d, want 5", service.lastUpdate.projectID)
	}
}

func TestMoveGroupingRule(t *testing.T) {
	service := &stubGroupingRuleService{rule: grouping.Rule{ID: 2, ProjectID: 5, Position: 2}}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules/2/move", strings.NewReader(`{"direction":"down"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.MoveRule(recorder, request)

	if recorder.Code != http.StatusOK || service.lastMove != grouping.MoveDown {
		t.Fatalf("status = %d, lastMove = %q", recorder.Code, service.lastMove)
	}
}

func TestMoveGroupingRuleInvalidDirectionReturns400(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{"missing direction", `{}`},
		{"empty direction", `{"direction":""}`},
		{"unknown direction", `{"direction":"sideways"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &stubGroupingRuleService{}
			projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
			handler := NewGroupingRuleHandler(service, projects, Options{})

			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules/2/move", strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/json")
			request.SetPathValue("slug", "web")
			request.SetPathValue("id", "2")
			recorder := httptest.NewRecorder()
			handler.MoveRule(recorder, request)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if service.moveCalls != 0 {
				t.Fatalf("service reached with invalid direction")
			}
		})
	}
}

func TestMoveGroupingRuleRejectsWrongContentType(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules/2/move", strings.NewReader(`{"direction":"down"}`))
	request.Header.Set("Content-Type", "text/plain")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.MoveRule(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if service.moveCalls != 0 {
		t.Fatalf("service reached with invalid content type")
	}
}

func TestMoveGroupingRuleNotFound(t *testing.T) {
	service := &stubGroupingRuleService{err: grouping.ErrRuleNotFound}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/grouping-rules/999/move", strings.NewReader(`{"direction":"up"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "999")
	recorder := httptest.NewRecorder()
	handler.MoveRule(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestMoveGroupingRuleReportsMissingProject(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{err: project.ErrNotFound}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/missing/grouping-rules/2/move", strings.NewReader(`{"direction":"up"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "missing")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.MoveRule(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if service.moveCalls != 0 {
		t.Fatalf("service reached for an unresolved project")
	}
}

func TestDeleteGroupingRule(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/web/grouping-rules/2", http.NoBody)
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.DeleteRule(recorder, request)

	if recorder.Code != http.StatusNoContent || service.lastDelete.projectID != 5 || service.lastDelete.ruleID != 2 {
		t.Fatalf("status = %d, lastDelete = %+v", recorder.Code, service.lastDelete)
	}
}

func TestDeleteGroupingRuleNotFound(t *testing.T) {
	service := &stubGroupingRuleService{err: grouping.ErrRuleNotFound}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/web/grouping-rules/9", http.NoBody)
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "9")
	recorder := httptest.NewRecorder()
	handler.DeleteRule(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestDeleteGroupingRuleRejectsInvalidID(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/web/grouping-rules/abc", http.NoBody)
	request.SetPathValue("slug", "web")
	request.SetPathValue("id", "abc")
	recorder := httptest.NewRecorder()
	handler.DeleteRule(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if service.deleteCalls != 0 {
		t.Fatalf("service reached with invalid id")
	}
}

func TestDeleteGroupingRuleReportsMissingProject(t *testing.T) {
	service := &stubGroupingRuleService{}
	projects := &stubGroupingRuleProjects{err: project.ErrNotFound}
	handler := NewGroupingRuleHandler(service, projects, Options{})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/projects/missing/grouping-rules/2", http.NoBody)
	request.SetPathValue("slug", "missing")
	request.SetPathValue("id", "2")
	recorder := httptest.NewRecorder()
	handler.DeleteRule(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if service.deleteCalls != 0 {
		t.Fatalf("service reached for an unresolved project")
	}
}

// newGroupingRulesRoutesApp wires GroupingRuleHandler exactly as
// internal/app/app.go wires it (RequireSession composed with RequireOwner on
// every write route, RequireSession alone on the list), so these tests
// exercise the real gate rather than the handler in isolation.
func newGroupingRulesRoutesApp(t *testing.T, user auth.User, service GroupingRuleService, projects ProjectResolver) http.Handler {
	t.Helper()
	authService := &authServiceStub{authUser: user}
	limiter := newTestLimiter()
	authHandler := NewAuthHandler(authService, limiter, &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	ruleHandler := NewGroupingRuleHandler(service, projects, Options{})
	owner := func(next http.HandlerFunc) http.Handler {
		return authHandler.RequireSession(authHandler.RequireOwner(next))
	}
	mux := http.NewServeMux()
	mux.Handle("GET /api/v1/projects/{slug}/grouping-rules", authHandler.RequireSession(http.HandlerFunc(ruleHandler.ListRules)))
	mux.Handle("POST /api/v1/projects/{slug}/grouping-rules", owner(ruleHandler.CreateRule))
	mux.Handle("PATCH /api/v1/projects/{slug}/grouping-rules/{id}", owner(ruleHandler.UpdateRule))
	mux.Handle("POST /api/v1/projects/{slug}/grouping-rules/{id}/move", owner(ruleHandler.MoveRule))
	mux.Handle("DELETE /api/v1/projects/{slug}/grouping-rules/{id}", owner(ruleHandler.DeleteRule))
	return mux
}

func TestCreateGroupingRuleRejectsAMember(t *testing.T) {
	handler := newGroupingRulesRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &stubGroupingRuleService{}, &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/projects/web/grouping-rules", `{"matcher":"logger:billing","template":"billing"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestUpdateGroupingRuleRejectsAMember(t *testing.T) {
	handler := newGroupingRulesRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &stubGroupingRuleService{}, &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/projects/web/grouping-rules/3", `{"matcher":"logger:billing","template":"billing"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestMoveGroupingRuleRejectsAMember(t *testing.T) {
	handler := newGroupingRulesRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &stubGroupingRuleService{}, &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/projects/web/grouping-rules/3/move", `{"direction":"up"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestDeleteGroupingRuleRejectsAMember(t *testing.T) {
	handler := newGroupingRulesRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &stubGroupingRuleService{}, &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}})

	response := do(t, handler, jsonRequest(t, http.MethodDelete, "/api/v1/projects/web/grouping-rules/3", ""))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
}

func TestDeleteGroupingRuleRejectsUnauthenticated(t *testing.T) {
	handler := newGroupingRulesRoutesApp(t, auth.User{}, &stubGroupingRuleService{}, &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "web"}})

	req := httptest.NewRequest(http.MethodDelete, "https://example.com/api/v1/projects/web/grouping-rules/3", http.NoBody)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.com")
	response := do(t, handler, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", response.Code, response.Body.String())
	}
}
