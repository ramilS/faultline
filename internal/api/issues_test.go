package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/sourcemap"
)

func containsPredicate(predicates []issue.Predicate, want issue.Predicate) bool {
	for _, predicate := range predicates {
		if reflect.DeepEqual(predicate, want) {
			return true
		}
	}
	return false
}

type stubIssueService struct {
	page        issue.Page
	detail      issue.Detail
	lastFilters issue.ListFilters
	lastStatus  issue.Status
	lastAssign  *int64
	assignSet   bool
	latest      issue.EventDetail
	eventQuery  issue.EventQuery
	stats       []issue.Bucket
	timeline    []issue.Span
	timelineErr error
	err         error

	mergeDetail                      issue.Detail
	lastMergeSource, lastMergeTarget int64

	tagKeys   []issue.TagKey
	tagValues []issue.TagValue
	userFacet issue.UserFacet

	// The scope forwarded to each method, captured so
	// TestIssueHandlerForwardsScopeToService can pin that IssueHandler passes
	// scopeOrRefuse's result straight through rather than a hardcoded scope.
	lastDetailScope      access.Scope
	lastSetStatusScope   access.Scope
	lastSetAssigneeScope access.Scope
	lastEventsScope      access.Scope
	lastLatestEventScope access.Scope
	lastEventScope       access.Scope
	lastStatsScope       access.Scope
	lastTimelineScope    access.Scope
	lastTagKeysScope     access.Scope
	lastTagValuesScope   access.Scope
	lastUserFacetScope   access.Scope
	lastMergeScope       access.Scope
}

func (s *stubIssueService) ListIssues(_ context.Context, filters issue.ListFilters) (issue.Page, error) {
	s.lastFilters = filters
	return s.page, s.err
}
func (s *stubIssueService) IssueDetail(_ context.Context, scope access.Scope, _ int64) (issue.Detail, error) {
	s.lastDetailScope = scope
	return s.detail, s.err
}
func (s *stubIssueService) SetStatus(_ context.Context, scope access.Scope, _ int64, status issue.Status) error {
	s.lastStatus = status
	s.lastSetStatusScope = scope
	return s.err
}
func (s *stubIssueService) SetAssignee(_ context.Context, scope access.Scope, _ int64, assignee *int64) error {
	s.lastAssign = assignee
	s.assignSet = true
	s.lastSetAssigneeScope = scope
	return s.err
}
func (s *stubIssueService) IssueEvents(_ context.Context, scope access.Scope, query issue.EventQuery) (issue.EventPage, error) {
	s.eventQuery = query
	s.lastEventsScope = scope
	return issue.EventPage{}, s.err
}
func (s *stubIssueService) LatestEvent(_ context.Context, scope access.Scope, _ int64) (issue.EventDetail, error) {
	s.lastLatestEventScope = scope
	return s.latest, s.err
}
func (s *stubIssueService) Event(_ context.Context, scope access.Scope, _ uuid.UUID) (issue.EventDetail, error) {
	s.lastEventScope = scope
	return s.latest, s.err
}
func (s *stubIssueService) IssueStats(_ context.Context, scope access.Scope, _ int64, _ issue.Period) ([]issue.Bucket, error) {
	s.lastStatsScope = scope
	return s.stats, s.err
}
func (s *stubIssueService) Timeline(_ context.Context, scope access.Scope, _ uuid.UUID) ([]issue.Span, error) {
	s.lastTimelineScope = scope
	return s.timeline, s.timelineErr
}
func (s *stubIssueService) TagKeys(_ context.Context, scope access.Scope, _ string) ([]issue.TagKey, error) {
	s.lastTagKeysScope = scope
	return s.tagKeys, s.err
}
func (s *stubIssueService) TagValues(_ context.Context, scope access.Scope, _, _, _ string, _ int) ([]issue.TagValue, error) {
	s.lastTagValuesScope = scope
	return s.tagValues, s.err
}
func (s *stubIssueService) IssueUsers(_ context.Context, scope access.Scope, _ int64, _ int) (issue.UserFacet, error) {
	s.lastUserFacetScope = scope
	return s.userFacet, s.err
}
func (s *stubIssueService) MergeIssue(_ context.Context, scope access.Scope, sourceID, targetID int64) (issue.Detail, error) {
	s.lastMergeSource, s.lastMergeTarget = sourceID, targetID
	s.lastMergeScope = scope
	return s.mergeDetail, s.err
}

// newIssueRequest builds a request carrying an unrestricted scope, matching
// what RequireSession would have resolved for an owner. Callers that exercise
// scope-specific behavior (e.g. a Limited scope) override it afterward via
// access.ContextWithScope, which simply replaces this default.
func newIssueRequest(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Origin", "http://example.test")
	}
	request.Host = "example.test"
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	return request
}

func TestListIssuesParsesFiltersAndReturnsPage(t *testing.T) {
	now := time.Now().UTC()
	service := &stubIssueService{page: issue.Page{
		Issues:     []issue.Issue{{ID: 1, Title: "Boom", Status: issue.StatusUnresolved, LastSeen: now, FirstSeen: now}},
		NextCursor: &issue.Cursor{Sort: issue.SortLastSeen, Value: now.Format(time.RFC3339Nano), ID: 1},
	}}
	handler := NewIssueHandler(service, Options{})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?status=unresolved&environment=prod&tag=tenant:acme&sort=times_seen&limit=10", "")
	request.SetPathValue("slug", "web")
	scope := access.Limited([]int64{1})
	request = request.WithContext(access.ContextWithScope(request.Context(), scope))
	handler.ListIssues(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if service.lastFilters.Sort != issue.SortTimesSeen {
		t.Fatalf("filters = %+v", service.lastFilters)
	}
	if !reflect.DeepEqual(service.lastFilters.Scope, scope) {
		t.Fatalf("filters.Scope = %+v, want %+v", service.lastFilters.Scope, scope)
	}
	wantEnvironment := issue.Predicate{Field: issue.FieldEnvironment, Values: []string{"prod"}}
	if !containsPredicate(service.lastFilters.Predicates, wantEnvironment) {
		t.Fatalf("predicates = %+v, want environment=prod", service.lastFilters.Predicates)
	}
	wantTag := issue.Predicate{Field: issue.FieldTag, Key: "tenant", Values: []string{"acme"}}
	if !containsPredicate(service.lastFilters.Predicates, wantTag) {
		t.Fatalf("predicates = %+v, want tag tenant:acme", service.lastFilters.Predicates)
	}
	var body struct {
		Issues     []map[string]any `json:"issues"`
		NextCursor *string          `json:"next_cursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Issues) != 1 || body.NextCursor == nil || *body.NextCursor == "" {
		t.Fatalf("body = %s", recorder.Body.String())
	}
}

// TestListIssuesWithoutSlugListsEveryProjectInScope pins the GET
// /api/v1/issues shape: no slug reaches the service, and each row names its
// own project on the wire so the dashboard can link back to it.
func TestListIssuesWithoutSlugListsEveryProjectInScope(t *testing.T) {
	now := time.Now().UTC()
	service := &stubIssueService{page: issue.Page{Issues: []issue.Issue{
		{ID: 1, ProjectSlug: "web", Title: "Boom", Status: issue.StatusUnresolved, LastSeen: now, FirstSeen: now},
	}}}
	handler := NewIssueHandler(service, Options{})

	recorder := httptest.NewRecorder()
	handler.ListIssues(recorder, newIssueRequest(http.MethodGet, "/api/v1/issues?status=unresolved", ""))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.lastFilters.ProjectSlug != "" {
		t.Fatalf("filters.ProjectSlug = %q, want empty", service.lastFilters.ProjectSlug)
	}
	var body struct {
		Issues []map[string]any `json:"issues"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Issues) != 1 || body.Issues[0]["project_slug"] != "web" {
		t.Fatalf("body = %s, want one issue with project_slug web", recorder.Body.String())
	}
}

// TestToIssueResponseKeepsLegacyShapeWhenCountsIsEmpty pins backward
// compatibility for a windowless request: counts_24h/counts_14d must render
// even when every bucket is genuinely zero (an issue with no events in that
// window), and counts/period_count must be absent — not present with a zero
// value — because their JSON tags are omitempty on a nil slice/pointer.
func TestToIssueResponseKeepsLegacyShapeWhenCountsIsEmpty(t *testing.T) {
	now := time.Now().UTC()
	source := issue.Issue{ID: 1, Title: "Boom", Status: issue.StatusUnresolved, FirstSeen: now, LastSeen: now}

	encoded, err := json.Marshal(toIssueResponse(source))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := decoded["counts_24h"]; !present {
		t.Fatalf("body = %s, want counts_24h present even with all-zero buckets", encoded)
	}
	if _, present := decoded["counts_14d"]; !present {
		t.Fatalf("body = %s, want counts_14d present even with all-zero buckets", encoded)
	}
	if _, present := decoded["counts"]; present {
		t.Fatalf("body = %s, want no counts field for a windowless response", encoded)
	}
	if _, present := decoded["period_count"]; present {
		t.Fatalf("body = %s, want no period_count field for a windowless response", encoded)
	}
}

// TestToIssueResponseSwitchesToWindowedShapeWhenCountsIsPopulated is the
// counterpart: once attachWindowedSparklines has populated Issue.Counts, the
// response must carry counts/period_count (period_count present as an
// explicit 0 when that's the real value, per the *int64 field) and drop the
// legacy counts_24h/counts_14d pair entirely.
func TestToIssueResponseSwitchesToWindowedShapeWhenCountsIsPopulated(t *testing.T) {
	now := time.Now().UTC()
	source := issue.Issue{
		ID: 1, Title: "Boom", Status: issue.StatusUnresolved, FirstSeen: now, LastSeen: now,
		Counts: []int64{0, 0, 0}, PeriodCount: 0,
	}

	encoded, err := json.Marshal(toIssueResponse(source))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if raw, present := decoded["counts"]; !present || string(raw) != "[0,0,0]" {
		t.Fatalf("body = %s, want counts = [0,0,0]", encoded)
	}
	if raw, present := decoded["period_count"]; !present || string(raw) != "0" {
		t.Fatalf("body = %s, want period_count present as 0, not omitted", encoded)
	}
	if _, present := decoded["counts_24h"]; present {
		t.Fatalf("body = %s, want no counts_24h field for a windowed response", encoded)
	}
	if _, present := decoded["counts_14d"]; present {
		t.Fatalf("body = %s, want no counts_14d field for a windowed response", encoded)
	}
}

// TestListIssuesAcceptsUsersSeenSort pins sort=users_seen as an accepted
// wire value, matching the column and the JSON field.
func TestListIssuesAcceptsUsersSeenSort(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?sort=users_seen", "")
	request.SetPathValue("slug", "web")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	handler.ListIssues(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.lastFilters.Sort != issue.SortUsersSeen {
		t.Fatalf("sort = %q, want %q", service.lastFilters.Sort, issue.SortUsersSeen)
	}
}

// TestToIssueResponseCarriesUsersSeen pins the exact wire key: users_seen
// must appear on the response with the issue's distinct-user count, on both
// the list rows and the detail (toDetailResponse embeds toIssueResponse), so
// this single assertion covers both call sites.
func TestToIssueResponseCarriesUsersSeen(t *testing.T) {
	now := time.Now().UTC()
	source := issue.Issue{ID: 1, Title: "Boom", Status: issue.StatusUnresolved, FirstSeen: now, LastSeen: now, UsersSeen: 42}

	encoded, err := json.Marshal(toIssueResponse(source))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	raw, present := decoded["users_seen"]
	if !present {
		t.Fatalf("body = %s, want users_seen present", encoded)
	}
	if string(raw) != "42" {
		t.Fatalf("users_seen = %s, want 42", raw)
	}
}

func TestListIssuesRejectsBadCursor(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?cursor=@@bad@@", "")
	request.SetPathValue("slug", "web")
	handler.ListIssues(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestListIssuesRejectsBadSort(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?sort=bogus", "")
	request.SetPathValue("slug", "web")
	handler.ListIssues(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestListIssuesRejectsBadStatusFilter(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?status=bogus", "")
	request.SetPathValue("slug", "web")
	handler.ListIssues(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// TestListIssuesMapsInvalidWindowTo400WithoutReporting pins the classification
// Finding 1 requires: a Window that fails Validate() is a client mistake, not
// a server fault, so it must come back as 400 and must not pollute the error
// reporter the way a genuine repository failure does.
func TestListIssuesMapsInvalidWindowTo400WithoutReporting(t *testing.T) {
	var reported error
	service := &stubIssueService{err: fmt.Errorf("%w: %w", issue.ErrInvalidWindow, errors.New("window: end must be after start"))}
	handler := NewIssueHandler(service, Options{ReportError: func(err error) { reported = err }})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues", "")
	request.SetPathValue("slug", "web")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	handler.ListIssues(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body.String())
	}
	if reported != nil {
		t.Fatalf("reportError called with %v, want no call for a client-side window error", reported)
	}
}

// TestListIssuesMapsGenericErrorTo500WithReporting is the counterpart
// regression: a non-window, non-classified error must still be a reported 500,
// so the 400 carve-out for ErrInvalidWindow doesn't silently swallow real
// repository faults too.
func TestListIssuesMapsGenericErrorTo500WithReporting(t *testing.T) {
	var reported error
	service := &stubIssueService{err: errors.New("boom")}
	handler := NewIssueHandler(service, Options{ReportError: func(err error) { reported = err }})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues", "")
	request.SetPathValue("slug", "web")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	handler.ListIssues(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %s)", recorder.Code, recorder.Body.String())
	}
	if reported == nil {
		t.Fatal("reportError not called, want a report for a genuine server fault")
	}
}

// The three collection reads cannot say "no such project/issue" through their
// rows — an empty page, an unreachable project, and a nonexistent one all look
// alike — so the repository reports issue.ErrNotFound and these handlers must
// turn it into a 404. Answering 200 with an empty body would show a member an
// issue stream, a tag vocabulary, or an occurrence chart belonging to a
// project they cannot reach.
func TestCollectionReadsMapNotFoundTo404(t *testing.T) {
	cases := []struct {
		name    string
		target  string
		pathKey string
		serve   func(*IssueHandler, http.ResponseWriter, *http.Request)
	}{
		{"list issues", "/api/v1/projects/secret/issues", "slug", (*IssueHandler).ListIssues},
		{"issue events", "/api/v1/issues/5/events", "id", (*IssueHandler).IssueEvents},
		{"issue stats", "/api/v1/issues/5/stats", "id", (*IssueHandler).IssueStats},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			var reported error
			service := &stubIssueService{err: fmt.Errorf("scoped read: %w", issue.ErrNotFound)}
			handler := NewIssueHandler(service, Options{ReportError: func(err error) { reported = err }})

			recorder := httptest.NewRecorder()
			request := newIssueRequest(http.MethodGet, testCase.target, "")
			request.SetPathValue(testCase.pathKey, map[string]string{"slug": "secret", "id": "5"}[testCase.pathKey])
			request = request.WithContext(access.ContextWithScope(request.Context(), access.Limited([]int64{1})))
			testCase.serve(handler, recorder, request)

			if recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", recorder.Code, recorder.Body.String())
			}
			if reported != nil {
				t.Fatalf("reportError called with %v, want no report for a boundary refusal", reported)
			}
		})
	}
}

func TestListIssuesResolvesAssignedMeFromSessionContext(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?assigned=me", "")
	request.SetPathValue("slug", "web")
	request = request.WithContext(ContextWithUser(request.Context(), auth.User{ID: 1, Email: "caller@example.com"}))
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	handler.ListIssues(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	want := issue.Predicate{Field: issue.FieldAssignee, Values: []string{"caller@example.com"}}
	if !containsPredicate(service.lastFilters.Predicates, want) {
		t.Fatalf("predicates = %+v, want assignee=caller@example.com", service.lastFilters.Predicates)
	}
}

func TestListIssuesRejectsAssignedMeWithoutSessionIdentity(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?assigned=me", "")
	request.SetPathValue("slug", "web")
	handler.ListIssues(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body %s)", recorder.Code, recorder.Body.String())
	}
}

// TestListIssuesAssignedMeIsScopedPerSession pins the security property this
// task exists for: two different sessions issuing the identical assigned=me
// request must each resolve to their own identity, never each other's.
func TestListIssuesAssignedMeIsScopedPerSession(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})

	for _, user := range []auth.User{{ID: 1, Email: "alice@example.com"}, {ID: 2, Email: "bob@example.com"}} {
		recorder := httptest.NewRecorder()
		request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues?assigned=me", "")
		request.SetPathValue("slug", "web")
		request = request.WithContext(ContextWithUser(request.Context(), user))
		request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
		handler.ListIssues(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("user %s: status = %d, want 200", user.Email, recorder.Code)
		}
		want := issue.Predicate{Field: issue.FieldAssignee, Values: []string{user.Email}}
		if !containsPredicate(service.lastFilters.Predicates, want) {
			t.Fatalf("user %s: predicates = %+v, want assignee=%s", user.Email, service.lastFilters.Predicates, user.Email)
		}
	}
}

func TestParseListFiltersMultiValue(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?level=error&level=fatal", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	if len(filters.Predicates) != 1 {
		t.Fatalf("got %d predicates, want 1", len(filters.Predicates))
	}
	predicate := filters.Predicates[0]
	if predicate.Field != issue.FieldLevel {
		t.Fatalf("field = %v, want FieldLevel", predicate.Field)
	}
	if !slices.Equal(predicate.Values, []string{"error", "fatal"}) {
		t.Fatalf("values = %v, want [error fatal]", predicate.Values)
	}
}

func TestParseListFiltersNegation(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?environment=%21staging", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	if len(filters.Predicates) != 1 {
		t.Fatalf("got %d predicates, want 1", len(filters.Predicates))
	}
	predicate := filters.Predicates[0]
	if !predicate.Negate {
		t.Fatal("predicate.Negate = false, want true")
	}
	if !slices.Equal(predicate.Values, []string{"staging"}) {
		t.Fatalf("values = %v, want [staging]", predicate.Values)
	}
}

func TestParseListFiltersEscapedBangIsLiteral(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?environment=%5C%21staging", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	predicate := filters.Predicates[0]
	if predicate.Negate {
		t.Fatal("predicate.Negate = true, want false for an escaped bang")
	}
	if !slices.Equal(predicate.Values, []string{"!staging"}) {
		t.Fatalf("values = %v, want [!staging]", predicate.Values)
	}
}

func TestParseListFiltersRejectsUnknownLevel(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?level=critical", "")
	request.SetPathValue("slug", "demo")

	if _, err := parseListFilters(request, "", true); err == nil {
		t.Fatal("parseListFilters() = nil error, want rejection of an unknown level")
	}
}

func TestParseListFiltersAcceptsDebugLevel(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?level=debug", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	want := issue.Predicate{Field: issue.FieldLevel, Values: []string{"debug"}}
	if !containsPredicate(filters.Predicates, want) {
		t.Fatalf("predicates = %+v, want level=debug", filters.Predicates)
	}
}

func TestParseListFiltersRejectsBangOnlyValue(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?environment=%21", "")
	request.SetPathValue("slug", "demo")

	if _, err := parseListFilters(request, "", true); err == nil {
		t.Fatal("parseListFilters() = nil error, want rejection of a bang-only value")
	}
}

func TestParseListFiltersBangMidValueStaysLiteral(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?environment=staging%21east", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	predicate := filters.Predicates[0]
	if predicate.Negate {
		t.Fatal("predicate.Negate = true, want false for a mid-value bang")
	}
	if !slices.Equal(predicate.Values, []string{"staging!east"}) {
		t.Fatalf("values = %v, want [staging!east]", predicate.Values)
	}
}

func TestParseListFiltersPlainAndNegatedSameParamProduceTwoPredicates(t *testing.T) {
	request := newIssueRequest(http.MethodGet,
		"/api/v1/projects/demo/issues?environment=prod&environment=%21staging", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	if len(filters.Predicates) != 2 {
		t.Fatalf("got %d predicates, want 2: %+v", len(filters.Predicates), filters.Predicates)
	}
	wantIncluded := issue.Predicate{Field: issue.FieldEnvironment, Values: []string{"prod"}}
	wantExcluded := issue.Predicate{Field: issue.FieldEnvironment, Negate: true, Values: []string{"staging"}}
	if !containsPredicate(filters.Predicates, wantIncluded) {
		t.Fatalf("predicates = %+v, want included environment=prod", filters.Predicates)
	}
	if !containsPredicate(filters.Predicates, wantExcluded) {
		t.Fatalf("predicates = %+v, want excluded environment=staging", filters.Predicates)
	}
}

func TestParseListFiltersResolvesAssignedMeToSessionEmail(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?assigned=me", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "caller@example.com", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	want := issue.Predicate{Field: issue.FieldAssignee, Values: []string{"caller@example.com"}}
	if !containsPredicate(filters.Predicates, want) {
		t.Fatalf("predicates = %+v, want assignee=caller@example.com", filters.Predicates)
	}
}

func TestParseListFiltersResolvesNegatedAssignedMe(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?assigned=%21me", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "caller@example.com", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	want := issue.Predicate{Field: issue.FieldAssignee, Negate: true, Values: []string{"caller@example.com"}}
	if !containsPredicate(filters.Predicates, want) {
		t.Fatalf("predicates = %+v, want negated assignee=caller@example.com", filters.Predicates)
	}
}

func TestParseListFiltersRejectsAssignedMeWithoutSession(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?assigned=me", "")
	request.SetPathValue("slug", "demo")

	if _, err := parseListFilters(request, "", false); !errors.Is(err, errAssignedMeRequiresSession) {
		t.Fatalf("parseListFilters err = %v, want errAssignedMeRequiresSession", err)
	}
}

func TestParseListFiltersLeavesExplicitAssignedEmailUntouched(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?assigned=other@example.com", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "caller@example.com", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	want := issue.Predicate{Field: issue.FieldAssignee, Values: []string{"other@example.com"}}
	if !containsPredicate(filters.Predicates, want) {
		t.Fatalf("predicates = %+v, want assignee=other@example.com", filters.Predicates)
	}
}

func TestParseListFiltersLeavesAssignedNoneUntouched(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?assigned=none", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "caller@example.com", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	want := issue.Predicate{Field: issue.FieldAssignee, Values: []string{"none"}}
	if !containsPredicate(filters.Predicates, want) {
		t.Fatalf("predicates = %+v, want assignee=none", filters.Predicates)
	}
}

func TestParseListFiltersGroupsTagValuesByKey(t *testing.T) {
	request := newIssueRequest(http.MethodGet,
		"/api/v1/projects/demo/issues?tag=tenant:acme&tag=tenant:%21beta", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	if len(filters.Predicates) != 2 {
		t.Fatalf("got %d predicates, want 2: %+v", len(filters.Predicates), filters.Predicates)
	}
	wantIncluded := issue.Predicate{Field: issue.FieldTag, Key: "tenant", Values: []string{"acme"}}
	wantExcluded := issue.Predicate{Field: issue.FieldTag, Key: "tenant", Negate: true, Values: []string{"beta"}}
	if !containsPredicate(filters.Predicates, wantIncluded) {
		t.Fatalf("predicates = %+v, want included tag tenant=acme", filters.Predicates)
	}
	if !containsPredicate(filters.Predicates, wantExcluded) {
		t.Fatalf("predicates = %+v, want excluded tag tenant=beta", filters.Predicates)
	}
}

func TestParseStatsPeriodAcceptsPresets(t *testing.T) {
	cases := []struct {
		period string
		want   time.Duration
	}{
		{"24h", 24 * time.Hour},
		{"7d", 7 * 24 * time.Hour},
		{"14d", 14 * 24 * time.Hour},
		{"30d", 30 * 24 * time.Hour},
		{"90d", 90 * 24 * time.Hour},
	}
	for _, testCase := range cases {
		t.Run(testCase.period, func(t *testing.T) {
			got, err := parseStatsPeriod(testCase.period)
			if err != nil {
				t.Fatalf("parseStatsPeriod(%q): %v", testCase.period, err)
			}
			if got != testCase.want {
				t.Fatalf("parseStatsPeriod(%q) = %v, want %v", testCase.period, got, testCase.want)
			}
		})
	}
}

func TestParseStatsPeriodRejectsArbitraryDuration(t *testing.T) {
	if _, err := parseStatsPeriod("48h"); err == nil {
		t.Fatal("parseStatsPeriod(\"48h\") = nil error, want rejection of a non-preset duration")
	}
}

func TestParseListFiltersAppliesStatsPeriodAsTrailingWindow(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?statsPeriod=24h", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	if filters.Window.Trailing != 24*time.Hour {
		t.Fatalf("window.Trailing = %v, want 24h", filters.Window.Trailing)
	}
	if filters.Window.IsAbsolute() {
		t.Fatal("window.IsAbsolute() = true, want false for a statsPeriod-only window")
	}
}

func TestParseListFiltersRejectsInvalidStatsPeriod(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?statsPeriod=48h", "")
	request.SetPathValue("slug", "demo")

	if _, err := parseListFilters(request, "", true); err == nil {
		t.Fatal("parseListFilters() = nil error, want rejection of an invalid statsPeriod")
	}
}

func TestParseListFiltersAppliesStartAndEndAsAbsoluteWindow(t *testing.T) {
	request := newIssueRequest(http.MethodGet,
		"/api/v1/projects/demo/issues?start=2026-08-03T00:00:00Z&end=2026-08-04T00:00:00Z", "")
	request.SetPathValue("slug", "demo")

	filters, err := parseListFilters(request, "", true)
	if err != nil {
		t.Fatalf("parseListFilters: %v", err)
	}
	wantStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	wantEnd := time.Date(2026, 8, 4, 0, 0, 0, 0, time.UTC)
	if !filters.Window.Start.Equal(wantStart) || !filters.Window.End.Equal(wantEnd) {
		t.Fatalf("window = %+v, want start %v end %v", filters.Window, wantStart, wantEnd)
	}
}

func TestParseListFiltersRejectsUnparsableStart(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?start=not-a-time&end=2026-08-04T00:00:00Z", "")
	request.SetPathValue("slug", "demo")

	if _, err := parseListFilters(request, "", true); err == nil {
		t.Fatal("parseListFilters() = nil error, want rejection of an unparsable start")
	}
}

func TestParseListFiltersRejectsUnparsableEnd(t *testing.T) {
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/demo/issues?start=2026-08-03T00:00:00Z&end=not-a-time", "")
	request.SetPathValue("slug", "demo")

	if _, err := parseListFilters(request, "", true); err == nil {
		t.Fatal("parseListFilters() = nil error, want rejection of an unparsable end")
	}
}

func TestPatchIssueSetsStatusAndAssignee(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{"status":"resolved","assignee_id":7}`)
	request.SetPathValue("id", "5")
	handler.PatchIssue(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	if service.lastStatus != issue.StatusResolved {
		t.Fatalf("status = %q, want resolved", service.lastStatus)
	}
	if !service.assignSet || service.lastAssign == nil || *service.lastAssign != 7 {
		t.Fatalf("assignee not applied: set=%v value=%v", service.assignSet, service.lastAssign)
	}
}

func TestPatchIssueClearsAssignee(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{"assignee_id":null}`)
	request.SetPathValue("id", "5")
	handler.PatchIssue(recorder, request)
	if !service.assignSet || service.lastAssign != nil {
		t.Fatalf("expected explicit unassign, set=%v value=%v", service.assignSet, service.lastAssign)
	}
}

func TestPatchIssueRejectsEmptyBody(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{}`)
	request.SetPathValue("id", "5")
	handler.PatchIssue(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestPatchIssueRejectsUnknownField(t *testing.T) {
	// Arrange
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{"status":"resolved","bogus":1}`)
	request.SetPathValue("id", "5")

	// Act
	handler.PatchIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestPatchIssueMapsNotFound(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: issue.ErrNotFound}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{"status":"resolved"}`)
	request.SetPathValue("id", "5")

	// Act
	handler.PatchIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestPatchIssueMapsInvalidAssignee(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: issue.ErrInvalidAssignee}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{"assignee_id":7}`)
	request.SetPathValue("id", "5")

	// Act
	handler.PatchIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestPatchIssueMapsInternalError(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: errors.New("boom")}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{"status":"resolved"}`)
	request.SetPathValue("id", "5")

	// Act
	handler.PatchIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
}

func TestIssueDetailReturnsIssue(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	service := &stubIssueService{detail: issue.Detail{Issue: issue.Issue{
		ID:        5,
		Title:     "Boom",
		Status:    issue.StatusUnresolved,
		FirstSeen: now,
		LastSeen:  now,
	}}}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5", "")
	request.SetPathValue("id", "5")

	// Act
	handler.IssueDetail(recorder, request)

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Issue struct {
			ID int64 `json:"id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Issue.ID != 5 {
		t.Fatalf("issue.id = %d, want 5", body.Issue.ID)
	}
}

func TestIssueDetailIncludesFingerprint(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	service := &stubIssueService{detail: issue.Detail{Issue: issue.Issue{
		ID:          5,
		Fingerprint: "abc123",
		Title:       "Boom",
		Status:      issue.StatusUnresolved,
		FirstSeen:   now,
		LastSeen:    now,
	}}}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5", "")
	request.SetPathValue("id", "5")

	// Act
	handler.IssueDetail(recorder, request)

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Issue struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Issue.Fingerprint != "abc123" {
		t.Fatalf("issue.fingerprint = %q, want %q (body = %s)", body.Issue.Fingerprint, "abc123", recorder.Body.String())
	}
}

// TestListIssuesIncludesFingerprint is the list-path twin of
// TestIssueDetailIncludesFingerprint. Both paths render through
// toIssueResponse, and the web client requires IssueSummary.fingerprint on
// both, so the wire key needs asserting once per path — an anonymous struct
// with an explicit tag, so a rename of the JSON tag fails here rather than
// silently in the dashboard.
func TestListIssuesIncludesFingerprint(t *testing.T) {
	// Arrange
	now := time.Now().UTC()
	service := &stubIssueService{page: issue.Page{
		Issues: []issue.Issue{{
			ID:          5,
			Fingerprint: "abc123",
			Title:       "Boom",
			Status:      issue.StatusUnresolved,
			FirstSeen:   now,
			LastSeen:    now,
		}},
	}}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/projects/web/issues", "")
	request.SetPathValue("slug", "web")

	// Act
	handler.ListIssues(recorder, request)

	// Assert
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Issues []struct {
			Fingerprint string `json:"fingerprint"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Issues) != 1 || body.Issues[0].Fingerprint != "abc123" {
		t.Fatalf("issues = %+v, want one issue with fingerprint %q (body = %s)", body.Issues, "abc123", recorder.Body.String())
	}
}

func TestIssueDetailReturnsTombstoneShapeForMergedIssue(t *testing.T) {
	// Arrange
	targetID := int64(9)
	service := &stubIssueService{detail: issue.Detail{Issue: issue.Issue{ID: 5, MergedIntoID: &targetID}}}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5", "")
	request.SetPathValue("id", "5")

	// Act
	handler.IssueDetail(recorder, request)

	// Assert
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"merged_into_id":9`) {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), `"tags"`) {
		t.Fatal("tombstone response must not include the normal detail shape")
	}
}

func TestIssueDetailMapsNotFound(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: issue.ErrNotFound}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5", "")
	request.SetPathValue("id", "5")

	// Act
	handler.IssueDetail(recorder, request)

	// Assert
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestLatestEventReturnsPayload(t *testing.T) {
	service := &stubIssueService{}
	service.err = nil
	handler := NewIssueHandler(service, Options{})
	service.latest = issue.EventDetail{
		EventID: uuid.New(),
		Payload: json.RawMessage(`{"message":"boom"}`),
	}

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events/latest", "")
	request.SetPathValue("id", "5")
	handler.LatestEvent(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		Event struct {
			Payload json.RawMessage `json:"payload"`
		} `json:"event"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	var payloadData map[string]any
	if err := json.Unmarshal(body.Event.Payload, &payloadData); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payloadData["message"] != "boom" {
		t.Fatalf("payload message = %v, want boom", payloadData["message"])
	}
}

func TestLatestEventSerializesSymbolicatedException(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})
	service.latest = issue.EventDetail{
		EventID: uuid.New(),
		Payload: json.RawMessage(`{"exception":{"type":"TypeError"}}`),
		SymbolicatedException: &sourcemap.Exception{
			Type:  "TypeError",
			Value: "cart is undefined",
			Frames: []sourcemap.Frame{{
				Generated: sourcemap.Position{Filename: "https://cdn/app.js", Line: 1, Column: 100, InApp: true},
				Original:  &sourcemap.Position{Function: "throwCheckoutError", Filename: "src/checkout.ts", Line: 42, Column: 9, InApp: true},
				Context:   []sourcemap.ContextLine{{Line: 42, Text: "  throwCheckoutError(cart)", Focus: true}},
			}},
		},
	}

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events/latest", "")
	request.SetPathValue("id", "5")
	handler.LatestEvent(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}

	var body struct {
		Event struct {
			SymbolicatedException *struct {
				Type   string `json:"type"`
				Frames []struct {
					Original *struct {
						Filename string `json:"filename"`
						Line     int    `json:"line"`
						Column   int    `json:"column"`
					} `json:"original"`
					Context []struct {
						Line  int  `json:"line"`
						Focus bool `json:"focus"`
					} `json:"context"`
				} `json:"frames"`
			} `json:"symbolicated_exception"`
		} `json:"event"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	exception := body.Event.SymbolicatedException
	if exception == nil || exception.Type != "TypeError" || len(exception.Frames) != 1 {
		t.Fatalf("symbolicated_exception = %#v", exception)
	}
	original := exception.Frames[0].Original
	if original == nil || original.Filename != "src/checkout.ts" || original.Line != 42 || original.Column != 9 {
		t.Fatalf("original frame = %#v", original)
	}
	if len(exception.Frames[0].Context) != 1 || !exception.Frames[0].Context[0].Focus {
		t.Fatalf("context = %#v", exception.Frames[0].Context)
	}
}

func TestLatestEventOmitsSymbolicatedExceptionWhenAbsent(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})
	service.latest = issue.EventDetail{
		EventID: uuid.New(),
		Payload: json.RawMessage(`{"message":"boom"}`),
	}

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events/latest", "")
	request.SetPathValue("id", "5")
	handler.LatestEvent(recorder, request)

	var body struct {
		Event struct {
			SymbolicatedException *json.RawMessage `json:"symbolicated_exception"`
		} `json:"event"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Event.SymbolicatedException != nil {
		t.Fatalf("expected null symbolicated_exception, got %s", string(*body.Event.SymbolicatedException))
	}
}

func TestIssueStatsRejectsBadPeriod(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/stats?period=7y", "")
	request.SetPathValue("id", "5")
	handler.IssueStats(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestIssueEventsRejectsBadCursor(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events?cursor=@@bad@@", "")
	request.SetPathValue("id", "5")
	handler.IssueEvents(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestIssueEventsRejectsBadLimit(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events?limit=-1", "")
	request.SetPathValue("id", "5")
	handler.IssueEvents(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestIssueEventsRejectsUnknownOrder(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events?order=sideways", "")
	request.SetPathValue("id", "5")
	handler.IssueEvents(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestIssueEventsDefaultsToNewestFirst(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events", "")
	request.SetPathValue("id", "5")
	handler.IssueEvents(recorder, request)

	// An omitted parameter must not change the existing contract: every caller
	// written before oldest-first existed passes no order and expects newest.
	if service.eventQuery.Order != issue.EventOrderNewest {
		t.Fatalf("order = %q, want %q", service.eventQuery.Order, issue.EventOrderNewest)
	}
}

func TestIssueEventsPassesOldestOrderThrough(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events?order=oldest", "")
	request.SetPathValue("id", "5")
	handler.IssueEvents(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if service.eventQuery.Order != issue.EventOrderOldest {
		t.Fatalf("order = %q, want %q", service.eventQuery.Order, issue.EventOrderOldest)
	}
	if service.eventQuery.IssueID != 5 {
		t.Fatalf("issue id = %d, want 5", service.eventQuery.IssueID)
	}
}

func TestIssueEventsParsesEnvironmentAndWindow(t *testing.T) {
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})

	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/1/events?environment=production&statsPeriod=14d", "")
	request.SetPathValue("id", "1")
	handler.IssueEvents(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body = %s)", recorder.Code, recorder.Body.String())
	}
	if service.eventQuery.Environment != "production" {
		t.Fatalf("environment = %q, want %q", service.eventQuery.Environment, "production")
	}
	if service.eventQuery.Window.Trailing != 14*24*time.Hour {
		t.Fatalf("window trailing = %s, want 336h", service.eventQuery.Window.Trailing)
	}
}

func TestIssueEventsRejectsInvalidStatsPeriod(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/1/events?statsPeriod=6h", "")
	request.SetPathValue("id", "1")
	handler.IssueEvents(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "6h") {
		t.Fatalf("body = %s, want no echo of the client's own filter value", recorder.Body.String())
	}
}

func TestEventByIDRejectsBadUUID(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/events/not-a-uuid", "")
	request.SetPathValue("event_id", "not-a-uuid")
	handler.EventByID(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestTimelineReturnsSpans(t *testing.T) {
	parent := "s1"
	service := &stubIssueService{timeline: []issue.Span{
		{SpanID: "s1", Name: "root", StartTime: time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC), DurationMS: 10, Status: "ok", Attributes: []byte(`{"k":"v"}`)},
		{SpanID: "s2", ParentSpanID: &parent, Name: "child", StartTime: time.Date(2026, 7, 15, 12, 0, 0, 1_000_000, time.UTC), DurationMS: 3, Status: "ok", Attributes: []byte(`{}`)},
	}}
	handler := NewIssueHandler(service, Options{})

	request := httptest.NewRequest(http.MethodGet, "/api/v1/events/11111111-1111-1111-1111-111111111111/timeline", http.NoBody)
	request.SetPathValue("event_id", "11111111-1111-1111-1111-111111111111")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	recorder := httptest.NewRecorder()
	handler.Timeline(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Spans []struct {
			SpanID       string          `json:"span_id"`
			ParentSpanID *string         `json:"parent_span_id"`
			Name         string          `json:"name"`
			StartTime    string          `json:"start_time"`
			DurationMS   float64         `json:"duration_ms"`
			Status       string          `json:"status"`
			Attributes   json.RawMessage `json:"attributes"`
		} `json:"spans"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Spans) != 2 || body.Spans[0].SpanID != "s1" || body.Spans[1].ParentSpanID == nil {
		t.Fatalf("spans = %+v", body.Spans)
	}
	if body.Spans[0].StartTime != "2026-07-15T12:00:00Z" || body.Spans[0].DurationMS != 10 {
		t.Fatalf("span[0] = %+v", body.Spans[0])
	}
}

func TestTimelineRejectsInvalidEventID(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events/not-a-uuid/timeline", http.NoBody)
	request.SetPathValue("event_id", "not-a-uuid")
	recorder := httptest.NewRecorder()
	handler.Timeline(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestTimelineNotFound(t *testing.T) {
	handler := NewIssueHandler(&stubIssueService{timelineErr: issue.ErrNotFound}, Options{})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events/11111111-1111-1111-1111-111111111111/timeline", http.NoBody)
	request.SetPathValue("event_id", "11111111-1111-1111-1111-111111111111")
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	recorder := httptest.NewRecorder()
	handler.Timeline(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestMergeIssue(t *testing.T) {
	// Arrange
	service := &stubIssueService{mergeDetail: issue.Detail{Issue: issue.Issue{ID: 9, Title: "Target"}}}
	handler := NewIssueHandler(service, Options{})
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":9}`)
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusOK || service.lastMergeSource != 5 || service.lastMergeTarget != 9 {
		t.Fatalf("status = %d, source = %d, target = %d", recorder.Code, service.lastMergeSource, service.lastMergeTarget)
	}
	var body struct {
		Issue struct {
			ID int64 `json:"id"`
		} `json:"issue"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Issue.ID != 9 {
		t.Fatalf("issue.id = %d, want 9", body.Issue.ID)
	}
}

func TestMergeIssueRejectsSameIssue(t *testing.T) {
	// Arrange
	service := &stubIssueService{}
	handler := NewIssueHandler(service, Options{})
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":5}`)
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestMergeIssueRejectsInvalidBody(t *testing.T) {
	// Arrange
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":0}`)
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestMergeIssueNotFound(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: issue.ErrNotFound}
	handler := NewIssueHandler(service, Options{})
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":9}`)
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestMergeIssueAlreadyMerged(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: issue.ErrAlreadyMerged}
	handler := NewIssueHandler(service, Options{})
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":9}`)
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (not 409 — an already-merged issue must be indistinguishable from a missing one)", recorder.Code)
	}
}

// TestMergeIssueRejectsAtRuleCap is a regression test for I-1: merging at the
// project's grouping-rule cap must map grouping.ErrTooManyRules to 400, the
// same status the rules-CRUD handler already uses for it (see
// applyRuleError in grouping_rules.go) — not fall through to the default
// 500-plus-error-report branch.
func TestMergeIssueRejectsAtRuleCap(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: grouping.ErrTooManyRules}
	handler := NewIssueHandler(service, Options{})
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":9}`)
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestMergeIssueMapsInternalError(t *testing.T) {
	// Arrange
	service := &stubIssueService{err: errors.New("boom")}
	handler := NewIssueHandler(service, Options{})
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":9}`)
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
}

func TestMergeIssueRejectsMissingContentType(t *testing.T) {
	// Arrange
	handler := NewIssueHandler(&stubIssueService{}, Options{})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/issues/5/merge", strings.NewReader(`{"target_id":9}`))
	request.SetPathValue("id", "5")
	recorder := httptest.NewRecorder()

	// Act
	handler.MergeIssue(recorder, request)

	// Assert
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

// TestIssueHandlerForwardsScopeToService closes a coverage gap:
// newIssueRequest stamps access.Unrestricted() as its default scope, so a
// handler that dropped scopeOrRefuse's result and forwarded a hardcoded
// access.Unrestricted() instead would still pass every test that relies on
// that default. Each case here overrides the context with its own distinct
// access.Limited scope and asserts the exact value the service received,
// so a dropped or hardcoded scope fails here even though it would pass
// under the shared default.
func TestIssueHandlerForwardsScopeToService(t *testing.T) {
	withScope := func(request *http.Request, scope access.Scope) *http.Request {
		return request.WithContext(access.ContextWithScope(request.Context(), scope))
	}

	t.Run("IssueDetail", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{101})
		request := withScope(newIssueRequest(http.MethodGet, "/api/v1/issues/5", ""), scope)
		request.SetPathValue("id", "5")
		handler.IssueDetail(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastDetailScope, scope) {
			t.Fatalf("scope = %+v, want %+v", service.lastDetailScope, scope)
		}
	})

	t.Run("PatchIssue", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{102})
		request := withScope(newIssueRequest(http.MethodPatch, "/api/v1/issues/5", `{"status":"resolved","assignee_id":7}`), scope)
		request.SetPathValue("id", "5")
		handler.PatchIssue(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastSetStatusScope, scope) {
			t.Fatalf("SetStatus scope = %+v, want %+v", service.lastSetStatusScope, scope)
		}
		if !reflect.DeepEqual(service.lastSetAssigneeScope, scope) {
			t.Fatalf("SetAssignee scope = %+v, want %+v", service.lastSetAssigneeScope, scope)
		}
	})

	t.Run("MergeIssue", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{103})
		request := withScope(newIssueRequest(http.MethodPost, "/api/v1/issues/5/merge", `{"target_id":9}`), scope)
		request.SetPathValue("id", "5")
		handler.MergeIssue(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastMergeScope, scope) {
			t.Fatalf("scope = %+v, want %+v", service.lastMergeScope, scope)
		}
	})

	t.Run("IssueEvents", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{104})
		request := withScope(newIssueRequest(http.MethodGet, "/api/v1/issues/5/events", ""), scope)
		request.SetPathValue("id", "5")
		handler.IssueEvents(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastEventsScope, scope) {
			t.Fatalf("scope = %+v, want %+v", service.lastEventsScope, scope)
		}
	})

	t.Run("LatestEvent", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{105})
		request := withScope(newIssueRequest(http.MethodGet, "/api/v1/issues/5/events/latest", ""), scope)
		request.SetPathValue("id", "5")
		handler.LatestEvent(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastLatestEventScope, scope) {
			t.Fatalf("scope = %+v, want %+v", service.lastLatestEventScope, scope)
		}
	})

	t.Run("EventByID", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{106})
		eventID := "11111111-1111-1111-1111-111111111111"
		request := withScope(newIssueRequest(http.MethodGet, "/api/v1/events/"+eventID, ""), scope)
		request.SetPathValue("event_id", eventID)
		handler.EventByID(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastEventScope, scope) {
			t.Fatalf("scope = %+v, want %+v", service.lastEventScope, scope)
		}
	})

	t.Run("Timeline", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{107})
		eventID := "11111111-1111-1111-1111-111111111111"
		request := withScope(newIssueRequest(http.MethodGet, "/api/v1/events/"+eventID+"/timeline", ""), scope)
		request.SetPathValue("event_id", eventID)
		handler.Timeline(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastTimelineScope, scope) {
			t.Fatalf("scope = %+v, want %+v", service.lastTimelineScope, scope)
		}
	})

	t.Run("IssueStats", func(t *testing.T) {
		service := &stubIssueService{}
		handler := NewIssueHandler(service, Options{})
		scope := access.Limited([]int64{108})
		request := withScope(newIssueRequest(http.MethodGet, "/api/v1/issues/5/stats", ""), scope)
		request.SetPathValue("id", "5")
		handler.IssueStats(httptest.NewRecorder(), request)
		if !reflect.DeepEqual(service.lastStatsScope, scope) {
			t.Fatalf("scope = %+v, want %+v", service.lastStatsScope, scope)
		}
	})
}
