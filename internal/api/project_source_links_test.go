package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/project"
)

func putSourceLinks(t *testing.T, service ProjectService, body string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/v1/projects/shop/source-links", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "shop")
	newProjectHandler(service).UpdateSourceLinks(recorder, request)
	return recorder
}

func TestUpdateSourceLinksStoresTheSettingAndReturnsItByLiteralKeys(t *testing.T) {
	service := &projectServiceStub{updated: project.Project{ID: 3, Slug: "shop", Name: "Shop", Platform: project.PlatformPython}}

	recorder := putSourceLinks(t, service, `{"source_link_template":"https://github.com/acme/shop/blob/main/{path}#L{line}","source_stack_root":"/app/","source_root":"src/"}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	want := project.SourceLinks{Template: "https://github.com/acme/shop/blob/main/{path}#L{line}", StackRoot: "/app/", SourceRoot: "src/"}
	if service.updateSlug != "shop" || len(service.linkUpdates) != 1 || service.linkUpdates[0] != want {
		t.Fatalf("service slug = %q, links = %+v", service.updateSlug, service.linkUpdates)
	}
	stored := decodeBody(t, recorder)["project"].(map[string]any)
	if stored["source_link_template"] != want.Template || stored["source_stack_root"] != "/app/" || stored["source_root"] != "src/" || stored["slug"] != "shop" {
		t.Fatalf("project = %v", stored)
	}
}

func TestUpdateSourceLinksRejectsAnInvalidSettingNamingTheField(t *testing.T) {
	for _, test := range []struct {
		name  string
		body  string
		field string
	}{
		{"javascript template", `{"source_link_template":"javascript:alert(1)//{path}","source_stack_root":"","source_root":""}`, "source_link_template"},
		{"template without path", `{"source_link_template":"https://github.com/acme/shop/blob/main/","source_stack_root":"","source_root":""}`, "source_link_template"},
		{"relative template", `{"source_link_template":"/acme/shop/{path}","source_stack_root":"","source_root":""}`, "source_link_template"},
		{"control character in a root", `{"source_link_template":"","source_stack_root":"/app/\u0000","source_root":""}`, "source_stack_root"},
		{"missing root field", `{"source_link_template":""}`, "source_stack_root"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &projectServiceStub{}

			recorder := putSourceLinks(t, service, test.body)

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
			}
			if message, _ := decodeBody(t, recorder)["error"].(string); !strings.Contains(message, test.field) {
				t.Fatalf("error = %q, want it to name %s", message, test.field)
			}
		})
	}
}

func TestUpdateSourceLinksRejectsAMalformedRequest(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `{"source_link_template":1,"source_stack_root":"","source_root":""}`} {
		service := &projectServiceStub{}
		recorder := putSourceLinks(t, service, body)
		if recorder.Code != http.StatusBadRequest || len(service.linkUpdates) != 0 {
			t.Fatalf("body %s: status = %d, service calls = %d", body, recorder.Code, len(service.linkUpdates))
		}
	}
}

func TestUpdateSourceLinksMapsMissingProject(t *testing.T) {
	service := &projectServiceStub{updateErr: fmt.Errorf("update source links: %w", project.ErrNotFound)}

	recorder := putSourceLinks(t, service, `{"source_link_template":"","source_stack_root":"","source_root":""}`)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateSourceLinksRejectsAMember(t *testing.T) {
	service := &projectServiceStub{}
	handler := newProjectsRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, service)

	response := do(t, handler, jsonRequest(t, http.MethodPut, "/api/v1/projects/shop/source-links", `{"source_link_template":"","source_stack_root":"","source_root":""}`))

	if response.Code != http.StatusForbidden || len(service.linkUpdates) != 0 {
		t.Fatalf("status = %d, service calls = %d", response.Code, len(service.linkUpdates))
	}
}

func TestListProjectsShowsSourceLinksToAMember(t *testing.T) {
	links := project.SourceLinks{Template: "https://github.com/acme/shop/blob/{release}/{path}", StackRoot: "/app/", SourceRoot: "src/"}
	handler := newProjectsRoutesApp(t, auth.User{ID: 2, Role: auth.RoleMember}, &projectServiceStub{projects: []project.Project{{ID: 3, Slug: "shop", SourceLinks: links}}})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/projects", nil))

	listed := decodeBody(t, response)["projects"].([]any)[0].(map[string]any)
	if listed["source_link_template"] != links.Template || listed["source_stack_root"] != "/app/" || listed["source_root"] != "src/" {
		t.Fatalf("listed project = %v", listed)
	}
}
