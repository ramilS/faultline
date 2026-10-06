package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/group"
)

type groupServiceStub struct {
	groups []group.Group

	detail  group.Detail
	findErr error

	created   group.Group
	createErr error

	renamed   group.Group
	renameErr error

	deleteErr error

	replaceMembersResult []int64
	replaceMembersErr    error
	lastMemberIDs        []int64

	replaceProjectsResult []int64
	replaceProjectsErr    error
	lastProjectIDs        []int64
}

func (s *groupServiceStub) List(context.Context) ([]group.Group, error) {
	return s.groups, nil
}

func (s *groupServiceStub) Find(_ context.Context, id int64) (group.Detail, error) {
	return s.detail, s.findErr
}

func (s *groupServiceStub) Create(_ context.Context, name string) (group.Group, error) {
	return s.created, s.createErr
}

func (s *groupServiceStub) Rename(_ context.Context, id int64, name string) (group.Group, error) {
	return s.renamed, s.renameErr
}

func (s *groupServiceStub) Delete(_ context.Context, id int64) error {
	return s.deleteErr
}

func (s *groupServiceStub) ReplaceMembers(_ context.Context, id int64, userIDs []int64) ([]int64, error) {
	s.lastMemberIDs = userIDs
	if s.replaceMembersErr != nil {
		return nil, s.replaceMembersErr
	}
	return s.replaceMembersResult, nil
}

func (s *groupServiceStub) ReplaceProjects(_ context.Context, id int64, projectIDs []int64) ([]int64, error) {
	s.lastProjectIDs = projectIDs
	if s.replaceProjectsErr != nil {
		return nil, s.replaceProjectsErr
	}
	return s.replaceProjectsResult, nil
}

// The key names are asserted as literal strings: decoding into the handler's
// own response type would take the field name from the same side on both
// ends, so a rename would keep this green while breaking the dashboard.
func TestListGroupsRespondsWithNamesAndCounts(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{
		groups: []group.Group{{ID: 1, Name: "backend", MemberCount: 2, ProjectCount: 1}},
	}, Options{})

	recorder := httptest.NewRecorder()
	handler.ListGroups(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/groups", http.NoBody))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	body := recorder.Body.String()
	for _, key := range []string{`"groups"`, `"name"`, `"member_count"`, `"project_count"`} {
		if !strings.Contains(body, key) {
			t.Errorf("body is missing %s: %s", key, body)
		}
	}
	if !strings.Contains(body, `"backend"`) {
		t.Errorf("body is missing the group name: %s", body)
	}
}

// The handler forwards the name unchanged and maps group.ErrInvalidName to
// 400; the service (proven separately in group/service_test.go) is what
// actually rejects a blank name. Naming this test "rejects a blank name"
// would misstate what's under test here.
func TestCreateGroupMapsAnInvalidNameToBadRequest(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{createErr: group.ErrInvalidName}, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"  "}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.CreateGroup(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateGroupReportsADuplicateAsConflict(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{createErr: group.ErrConflict}, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"backend"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.CreateGroup(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
}

func TestCreateGroupRespondsCreated(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{created: group.Group{ID: 1, Name: "backend"}}, Options{})

	request := httptest.NewRequest(http.MethodPost, "/api/v1/groups", strings.NewReader(`{"name":"backend"}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.CreateGroup(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"backend"`) {
		t.Errorf("body is missing the created group's name: %s", recorder.Body.String())
	}
}

func TestPatchGroupRenames(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{renamed: group.Group{ID: 1, Name: "platform"}}, Options{})

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/1", strings.NewReader(`{"name":"platform"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.PatchGroup(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"platform"`) {
		t.Errorf("body is missing the new name: %s", recorder.Body.String())
	}
}

func TestPatchGroupWithDuplicateNameRespondsConflict(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{renameErr: group.ErrConflict}, Options{})

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/groups/1", strings.NewReader(`{"name":"platform"}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.PatchGroup(recorder, request)

	if recorder.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", recorder.Code, recorder.Body.String())
	}
}

func TestDeleteGroupRespondsNoContent(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{}, Options{})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/groups/1", http.NoBody)
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.DeleteGroup(recorder, request)

	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", recorder.Code, recorder.Body.String())
	}
	if recorder.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", recorder.Body.String())
	}
}

func TestDeleteGroupOnMissingGroupRespondsNotFound(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{deleteErr: group.ErrNotFound}, Options{})

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/groups/1", http.NoBody)
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.DeleteGroup(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPutMembersReplacesTheSet(t *testing.T) {
	service := &groupServiceStub{replaceMembersResult: []int64{4, 9}}
	handler := NewGroupHandler(service, Options{})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/1/members", strings.NewReader(`{"user_ids":[4,9]}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.PutMembers(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"user_ids"`) {
		t.Errorf("body is missing user_ids: %s", recorder.Body.String())
	}
	if len(service.lastMemberIDs) != 2 || service.lastMemberIDs[0] != 4 || service.lastMemberIDs[1] != 9 {
		t.Errorf("ids sent to the service = %v, want [4 9]", service.lastMemberIDs)
	}
}

// The service dedupes before writing (internal/group/service.go), so the
// wire response must echo what the service reports as applied, never the raw
// request body: PUT {"user_ids":[4,4,9]} stores [4,9], and a caller reading
// [4,4,9] back would believe a membership exists that was never written.
func TestPutMembersEchoesTheAppliedSetNotTheRawRequest(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{replaceMembersResult: []int64{4, 9}}, Options{})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/1/members", strings.NewReader(`{"user_ids":[4,4,9]}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.PutMembers(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"user_ids":[4,9]`) {
		t.Errorf("body = %s, want user_ids [4,9] (the applied set), not the raw request [4,4,9]", recorder.Body.String())
	}
}

func TestPutMembersOnMissingGroupRespondsNotFound(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{replaceMembersErr: group.ErrNotFound}, Options{})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/999/members", strings.NewReader(`{"user_ids":[]}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "999")
	recorder := httptest.NewRecorder()
	handler.PutMembers(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPutMembersWithUnknownUserIDRespondsBadRequest(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{replaceMembersErr: group.ErrUnknownReference}, Options{})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/1/members", strings.NewReader(`{"user_ids":[4]}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.PutMembers(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPutProjectsReplacesTheSet(t *testing.T) {
	service := &groupServiceStub{replaceProjectsResult: []int64{7}}
	handler := NewGroupHandler(service, Options{})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/1/projects", strings.NewReader(`{"project_ids":[7]}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.PutProjects(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"project_ids"`) {
		t.Errorf("body is missing project_ids: %s", recorder.Body.String())
	}
	if len(service.lastProjectIDs) != 1 || service.lastProjectIDs[0] != 7 {
		t.Errorf("ids sent to the service = %v, want [7]", service.lastProjectIDs)
	}
}

func TestPutProjectsOnMissingGroupRespondsNotFound(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{replaceProjectsErr: group.ErrNotFound}, Options{})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/999/projects", strings.NewReader(`{"project_ids":[]}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "999")
	recorder := httptest.NewRecorder()
	handler.PutProjects(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

func TestPutProjectsWithUnknownProjectIDRespondsBadRequest(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{replaceProjectsErr: group.ErrUnknownReference}, Options{})

	request := httptest.NewRequest(http.MethodPut, "/api/v1/groups/1/projects", strings.NewReader(`{"project_ids":[7]}`))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.PutProjects(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}

func TestGroupNotFoundRespondsNotFound(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{findErr: group.ErrNotFound}, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/groups/1", http.NoBody)
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.GetGroup(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
}

// groupDetailResponse embeds groupResponse to promote member_count onto the
// same JSON object as user_ids/project_ids. Embedding happens to serialize
// that way today; a future switch to a named field would break the wire
// shape while every other test here stayed green, so the promoted key is
// asserted literally.
func TestGetGroupRespondsWithMembershipAndCounts(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{detail: group.Detail{
		Group:      group.Group{ID: 1, Name: "backend", MemberCount: 2, ProjectCount: 1},
		UserIDs:    []int64{4, 9},
		ProjectIDs: []int64{7},
	}}, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/groups/1", http.NoBody)
	request.SetPathValue("id", "1")
	recorder := httptest.NewRecorder()
	handler.GetGroup(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	body := recorder.Body.String()
	for _, key := range []string{`"user_ids"`, `"project_ids"`, `"member_count"`, `"project_count"`} {
		if !strings.Contains(body, key) {
			t.Errorf("body is missing %s: %s", key, body)
		}
	}
	if !strings.Contains(body, `"user_ids":[4,9]`) {
		t.Errorf("body is missing the member ids: %s", body)
	}
	if !strings.Contains(body, `"project_ids":[7]`) {
		t.Errorf("body is missing the project ids: %s", body)
	}
}

func TestMalformedGroupIDRespondsBadRequest(t *testing.T) {
	handler := NewGroupHandler(&groupServiceStub{}, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/groups/abc", http.NoBody)
	request.SetPathValue("id", "abc")
	recorder := httptest.NewRecorder()
	handler.GetGroup(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
}
