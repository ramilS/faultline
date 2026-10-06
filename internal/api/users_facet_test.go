package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
)

type stubUsersFacetService struct {
	facet    issue.UserFacet
	err      error
	gotID    int64
	gotLimit int
	called   bool
}

func (s *stubUsersFacetService) IssueUsers(_ context.Context, _ access.Scope, id int64, limit int) (issue.UserFacet, error) {
	s.called = true
	s.gotID, s.gotLimit = id, limit
	return s.facet, s.err
}

func sortedKeys(m map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func newIssueUsersRequest(target string) *http.Request {
	request := newIssueRequest(http.MethodGet, target, "")
	request.SetPathValue("id", "5")
	return request
}

// TestIssueUsersReturnsExactResponseShape pins the wire shape of both the
// envelope and a user row: a field added to either later must fail this test
// loudly rather than leak onto the wire unnoticed.
func TestIssueUsersReturnsExactResponseShape(t *testing.T) {
	lastSeen := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	service := &stubUsersFacetService{facet: issue.UserFacet{
		Users: []issue.UserIdentity{{Key: "email", Value: "a@example.com", TimesSeen: 12, LastSeen: lastSeen}},
		Total: 37,
	}}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.gotID != 5 {
		t.Fatalf("issue id = %d, want 5", service.gotID)
	}

	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got, want := sortedKeys(envelope), []string{"saturated", "total", "users"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("envelope keys = %v, want %v", got, want)
	}

	var users []map[string]json.RawMessage
	if err := json.Unmarshal(envelope["users"], &users); err != nil {
		t.Fatalf("unmarshal users: %v", err)
	}
	if len(users) != 1 {
		t.Fatalf("users = %d, want 1 (body %s)", len(users), recorder.Body.String())
	}
	if got, want := sortedKeys(users[0]), []string{"key", "last_seen", "times_seen", "value"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("user row keys = %v, want %v", got, want)
	}

	var payload struct {
		Users []struct {
			Key       string `json:"key"`
			Value     string `json:"value"`
			TimesSeen int64  `json:"times_seen"`
			LastSeen  string `json:"last_seen"`
		} `json:"users"`
		Total     int64 `json:"total"`
		Saturated bool  `json:"saturated"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if payload.Total != 37 || payload.Saturated {
		t.Fatalf("total/saturated = %d/%v, want 37/false", payload.Total, payload.Saturated)
	}
	if payload.Users[0].Key != "email" || payload.Users[0].Value != "a@example.com" ||
		payload.Users[0].TimesSeen != 12 || payload.Users[0].LastSeen != lastSeen.Format(time.RFC3339) {
		t.Fatalf("user row = %+v", payload.Users[0])
	}
}

// TestIssueUsersPreservesServiceOrderOnATimesSeenTie proves the handler does
// not re-sort or otherwise disturb the order the service (backed by the
// times_seen DESC, user_key, user_value query) hands it: a naive
// implementation that rebuilt the list through a map, or applied its own
// unrelated sort, would scramble this order and fail here even though the
// service-level ordering it depends on is exercised in
// internal/storage/postgres, not here.
func TestIssueUsersPreservesServiceOrderOnATimesSeenTie(t *testing.T) {
	now := time.Now().UTC()
	service := &stubUsersFacetService{facet: issue.UserFacet{
		Users: []issue.UserIdentity{
			{Key: "id", Value: "u-1", TimesSeen: 40, LastSeen: now},
			// Tied at 12: "email" sorts before "ip", the tie-break the
			// storage layer applies (user_key ASC after user_value's PK
			// sibling), so this order is what a correct implementation
			// must reproduce.
			{Key: "email", Value: "a@example.com", TimesSeen: 12, LastSeen: now},
			{Key: "ip", Value: "10.0.0.1", TimesSeen: 12, LastSeen: now},
		},
		Total: 3,
	}}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users"))

	var payload struct {
		Users []struct {
			Key string `json:"key"`
		} `json:"users"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := []string{"id", "email", "ip"}
	got := make([]string, len(payload.Users))
	for i, row := range payload.Users {
		got[i] = row.Key
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

// TestIssueUsersSaturatedDerivesFromTotalNotPageSize is the regression test
// for the task's second binding constraint: both cases below return a page
// of exactly one row while Total sits at (and one below) the cap, so a
// buggy `len(users) >= cap` computation would report false in both cases
// instead of true/false — this fails loudly if saturated is ever computed
// from the page instead of the total.
func TestIssueUsersSaturatedDerivesFromTotalNotPageSize(t *testing.T) {
	tests := []struct {
		name  string
		total int64
		want  bool
	}{
		{"at the cap", ingest.MaxUsersPerIssue, true},
		{"above the cap", ingest.MaxUsersPerIssue + 1, true},
		{"one below the cap", ingest.MaxUsersPerIssue - 1, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &stubUsersFacetService{facet: issue.UserFacet{
				Users: []issue.UserIdentity{{Key: "email", Value: "a@example.com", TimesSeen: 1}},
				Total: test.total,
			}}
			handler := NewUsersFacetHandler(service, Options{})

			recorder := httptest.NewRecorder()
			handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users"))

			var payload struct {
				Saturated bool `json:"saturated"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if payload.Saturated != test.want {
				t.Fatalf("saturated = %v, want %v (total %d, page size 1)", payload.Saturated, test.want, test.total)
			}
		})
	}
}

func TestIssueUsersNotFound(t *testing.T) {
	service := &stubUsersFacetService{err: issue.ErrNotFound}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users"))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestIssueUsersRejectsNonNumericID(t *testing.T) {
	handler := NewUsersFacetHandler(&stubUsersFacetService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/abc/users", "")
	request.SetPathValue("id", "abc")

	handler.IssueUsers(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body.String())
	}
}

// TestIssueUsersRequiresSession exercises the real RequireSession gate
// (rather than the handler in isolation) with no session cookie at all,
// mirroring the "missing" case of
// TestRequireSessionRejectsMissingMalformedDuplicateAndInvalidCookies.
func TestIssueUsersRequiresSession(t *testing.T) {
	authHandler := NewAuthHandler(&authServiceStub{}, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	usersFacetHandler := NewUsersFacetHandler(&stubUsersFacetService{}, Options{})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/issues/5/users", http.NoBody)

	authHandler.RequireSession(http.HandlerFunc(usersFacetHandler.IssueUsers)).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %s)", recorder.Code, recorder.Body.String())
	}
}

func TestIssueUsersLimitAbsentDefersToService(t *testing.T) {
	service := &stubUsersFacetService{}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if !service.called || service.gotLimit != 0 {
		t.Fatalf("limit = %d (called %v), want 0 (unset) and called", service.gotLimit, service.called)
	}
}

func TestIssueUsersLimitZeroDefersToService(t *testing.T) {
	service := &stubUsersFacetService{}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users?limit=0"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.gotLimit != 0 {
		t.Fatalf("limit = %d, want 0 (unset)", service.gotLimit)
	}
}

func TestIssueUsersRejectsNegativeLimit(t *testing.T) {
	service := &stubUsersFacetService{}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users?limit=-1"))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.called {
		t.Fatalf("service reached for a rejected limit")
	}
}

func TestIssueUsersRejectsNonNumericLimit(t *testing.T) {
	service := &stubUsersFacetService{}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users?limit=abc"))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.called {
		t.Fatalf("service reached for a rejected limit")
	}
}

// TestIssueUsersForwardsAnOversizedLimitForTheServiceToNormalize matches the
// policy IssueEvents follows: the HTTP layer only rejects malformed input
// (non-numeric, negative); clamping an oversized value into range is the
// service's job (issue.NormalizeLimit), exercised in
// internal/issue/service_test.go.
func TestIssueUsersForwardsAnOversizedLimitForTheServiceToNormalize(t *testing.T) {
	service := &stubUsersFacetService{}
	handler := NewUsersFacetHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.IssueUsers(recorder, newIssueUsersRequest("/api/v1/issues/5/users?limit=999999999"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.gotLimit != 999999999 {
		t.Fatalf("limit = %d, want 999999999 forwarded as-is", service.gotLimit)
	}
}
