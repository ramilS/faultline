package issue

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/sourcemap"
)

type fakeRepository struct {
	lastFilters    ListFilters
	lastEventQuery EventQuery
	page           Page
	err            error
	timeline       []Span
	timelineErr    error
	event          EventDetail

	mergeDetail                      Detail
	mergeErr                         error
	lastMergeSource, lastMergeTarget int64

	canBeAssigned     bool
	canBeAssignedErr  error
	lastAssigneeCheck int64
	lastAssigneeIssue int64

	tagKeys                             []TagKey
	tagValues                           []TagValue
	tagErr                              error
	lastTagKeysSlug                     string
	lastTagKeysScope                    access.Scope
	lastTagValuesSlug, lastTagValuesKey string
	lastTagValuesPrefix                 string
	lastTagValuesLimit                  int
	lastTagValuesScope                  access.Scope

	userFacet          UserFacet
	userErr            error
	lastUserIssueID    int64
	lastUserFacetLimit int

	// The scope forwarded to each repository method, captured so
	// TestServiceForwardsScopeToRepository can pin that the Service passes its
	// caller's scope straight through rather than a hardcoded one.
	lastDetailScope       access.Scope
	lastUpdateStatusScope access.Scope
	lastAssignScope       access.Scope
	lastEventsScope       access.Scope
	lastLatestEventScope  access.Scope
	lastEventScope        access.Scope
	lastStatsScope        access.Scope
	lastTimelineScope     access.Scope
	lastMergeScope        access.Scope
	lastUserFacetScope    access.Scope
}

func (f *fakeRepository) List(_ context.Context, filters ListFilters) (Page, error) {
	f.lastFilters = filters
	return f.page, f.err
}
func (f *fakeRepository) Detail(_ context.Context, scope access.Scope, _ int64) (Detail, error) {
	f.lastDetailScope = scope
	return Detail{}, f.err
}
func (f *fakeRepository) UpdateStatus(_ context.Context, scope access.Scope, _ int64, _ Status) error {
	f.lastUpdateStatusScope = scope
	return f.err
}
func (f *fakeRepository) Assign(_ context.Context, scope access.Scope, _ int64, _ *int64) error {
	f.lastAssignScope = scope
	return f.err
}
func (f *fakeRepository) CanBeAssigned(_ context.Context, issueID, userID int64) (bool, error) {
	f.lastAssigneeIssue = issueID
	f.lastAssigneeCheck = userID
	return f.canBeAssigned, f.canBeAssignedErr
}
func (f *fakeRepository) Events(_ context.Context, scope access.Scope, query EventQuery) (EventPage, error) {
	f.lastEventQuery = query
	f.lastEventsScope = scope
	return EventPage{}, f.err
}
func (f *fakeRepository) LatestEvent(_ context.Context, scope access.Scope, _ int64) (EventDetail, error) {
	f.lastLatestEventScope = scope
	return f.event, f.err
}
func (f *fakeRepository) Event(_ context.Context, scope access.Scope, _ uuid.UUID) (EventDetail, error) {
	f.lastEventScope = scope
	return f.event, f.err
}
func (f *fakeRepository) Stats(_ context.Context, scope access.Scope, _ int64, _ Period) ([]Bucket, error) {
	f.lastStatsScope = scope
	return nil, f.err
}
func (f *fakeRepository) Timeline(_ context.Context, scope access.Scope, _ uuid.UUID) ([]Span, error) {
	f.lastTimelineScope = scope
	return f.timeline, f.timelineErr
}
func (f *fakeRepository) MergeIssue(_ context.Context, scope access.Scope, sourceID, targetID int64) (Detail, error) {
	f.lastMergeSource, f.lastMergeTarget = sourceID, targetID
	f.lastMergeScope = scope
	return f.mergeDetail, f.mergeErr
}
func (f *fakeRepository) TagKeys(_ context.Context, scope access.Scope, slug string) ([]TagKey, error) {
	f.lastTagKeysScope, f.lastTagKeysSlug = scope, slug
	return f.tagKeys, f.tagErr
}
func (f *fakeRepository) TagValues(_ context.Context, scope access.Scope, slug, key, prefix string, limit int) ([]TagValue, error) {
	f.lastTagValuesScope = scope
	f.lastTagValuesSlug, f.lastTagValuesKey, f.lastTagValuesPrefix, f.lastTagValuesLimit = slug, key, prefix, limit
	return f.tagValues, f.tagErr
}
func (f *fakeRepository) IssueUsers(_ context.Context, scope access.Scope, issueID int64, limit int) (UserFacet, error) {
	f.lastUserIssueID, f.lastUserFacetLimit = issueID, limit
	f.lastUserFacetScope = scope
	return f.userFacet, f.userErr
}

func TestListIssuesClampsLimitAndDefaultsSort(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)

	if _, err := service.ListIssues(t.Context(), ListFilters{ProjectSlug: "web", Limit: 0}); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if repository.lastFilters.Limit != DefaultLimit {
		t.Fatalf("limit = %d, want default %d", repository.lastFilters.Limit, DefaultLimit)
	}
	if repository.lastFilters.Sort != SortLastSeen {
		t.Fatalf("sort = %q, want %q", repository.lastFilters.Sort, SortLastSeen)
	}

	if _, err := service.ListIssues(t.Context(), ListFilters{ProjectSlug: "web", Limit: 9000}); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if repository.lastFilters.Limit != MaxLimit {
		t.Fatalf("limit = %d, want max %d", repository.lastFilters.Limit, MaxLimit)
	}
}

func TestIssueEventsClampsLimitAndDefaultsOrder(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)

	// A query with no order is what every caller written before oldest-first
	// sends, so it must still reach the repository as a newest-first walk.
	if _, err := service.IssueEvents(t.Context(), access.Unrestricted(), EventQuery{IssueID: 7}); err != nil {
		t.Fatalf("IssueEvents: %v", err)
	}
	if repository.lastEventQuery.Order != EventOrderNewest {
		t.Fatalf("order = %q, want %q", repository.lastEventQuery.Order, EventOrderNewest)
	}
	if repository.lastEventQuery.Limit != DefaultLimit {
		t.Fatalf("limit = %d, want default %d", repository.lastEventQuery.Limit, DefaultLimit)
	}
	if repository.lastEventQuery.IssueID != 7 {
		t.Fatalf("issue id = %d, want 7", repository.lastEventQuery.IssueID)
	}

	if _, err := service.IssueEvents(t.Context(), access.Unrestricted(), EventQuery{IssueID: 7, Limit: 9000, Order: EventOrderOldest}); err != nil {
		t.Fatalf("IssueEvents: %v", err)
	}
	if repository.lastEventQuery.Limit != MaxLimit {
		t.Fatalf("limit = %d, want max %d", repository.lastEventQuery.Limit, MaxLimit)
	}
	if repository.lastEventQuery.Order != EventOrderOldest {
		t.Fatalf("order = %q, want %q", repository.lastEventQuery.Order, EventOrderOldest)
	}
}

func TestIssueEventsRejectsUnknownOrder(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)

	if _, err := service.IssueEvents(t.Context(), access.Unrestricted(), EventQuery{IssueID: 1, Order: EventOrder("sideways")}); err == nil {
		t.Fatalf("IssueEvents with bad order = nil error, want error")
	}
	if repository.lastEventQuery.IssueID != 0 {
		t.Fatalf("repository was called with %+v, want no call at all", repository.lastEventQuery)
	}
}

func TestValidateEventOrder(t *testing.T) {
	cases := map[EventOrder]bool{
		EventOrderNewest: true,
		EventOrderOldest: true,
		EventOrder(""):   false,
		"NEWEST":         false,
		"sideways":       false,
	}
	for order, want := range cases {
		if got := ValidateEventOrder(order); got != want {
			t.Fatalf("ValidateEventOrder(%q) = %t, want %t", order, got, want)
		}
	}
}

func TestListIssuesRejectsInvalidSort(t *testing.T) {
	service := NewService(&fakeRepository{})
	_, err := service.ListIssues(t.Context(), ListFilters{ProjectSlug: "web", Sort: Sort("bogus")})
	if err == nil {
		t.Fatalf("ListIssues with bad sort = nil error, want error")
	}
}

func TestListIssuesAcceptsUsersSeenSort(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	if _, err := service.ListIssues(t.Context(), ListFilters{ProjectSlug: "web", Sort: SortUsersSeen}); err != nil {
		t.Fatalf("ListIssues: %v", err)
	}
	if repository.lastFilters.Sort != SortUsersSeen {
		t.Fatalf("sort = %q, want %q", repository.lastFilters.Sort, SortUsersSeen)
	}
}

// TestListIssuesRejectsCursorSortMismatch pins the self-describing cursor
// contract at the layer every caller goes through: a cursor produced under
// one sort must not be reinterpreted under a different one, even though
// times_seen and users_seen share the same bigint value shape and so cannot
// be told apart by a type-based parse alone.
func TestListIssuesRejectsCursorSortMismatch(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	cursor := Cursor{Sort: SortTimesSeen, Value: "1000", ID: 7}

	_, err := service.ListIssues(t.Context(), ListFilters{
		ProjectSlug: "web", Sort: SortUsersSeen, Cursor: &cursor,
	})
	if !errors.Is(err, ErrCursorSortMismatch) {
		t.Fatalf("ListIssues with mismatched cursor sort = %v, want ErrCursorSortMismatch", err)
	}
	if repository.lastFilters.ProjectSlug != "" {
		t.Fatalf("repository was called with %+v, want no call at all", repository.lastFilters)
	}
}

func TestSetStatusRejectsInvalidStatus(t *testing.T) {
	service := NewService(&fakeRepository{})
	if err := service.SetStatus(t.Context(), access.Unrestricted(), 1, Status("nope")); err == nil {
		t.Fatalf("SetStatus with bad status = nil error, want error")
	}
}

func TestSetStatusPropagatesRepositoryError(t *testing.T) {
	sentinel := errors.New("boom")
	service := NewService(&fakeRepository{err: sentinel})
	if err := service.SetStatus(t.Context(), access.Unrestricted(), 1, StatusResolved); !errors.Is(err, sentinel) {
		t.Fatalf("SetStatus err = %v, want wrapped %v", err, sentinel)
	}
}

func ptrInt64(v int64) *int64 {
	return new(v)
}

func TestSetAssigneeRejectsNonPositiveIDWithSentinel(t *testing.T) {
	// Arrange
	repository := &fakeRepository{}
	service := NewService(repository)

	// Act - test zero ID
	err := service.SetAssignee(t.Context(), access.Unrestricted(), 1, ptrInt64(0))

	// Assert
	if err == nil {
		t.Fatalf("SetAssignee with ID 0 = nil error, want error")
	}
	if !errors.Is(err, ErrInvalidAssignee) {
		t.Fatalf("SetAssignee err = %v, want wrapped %v", err, ErrInvalidAssignee)
	}

	// Act - test negative ID
	err = service.SetAssignee(t.Context(), access.Unrestricted(), 1, ptrInt64(-1))

	// Assert
	if err == nil {
		t.Fatalf("SetAssignee with negative ID = nil error, want error")
	}
	if !errors.Is(err, ErrInvalidAssignee) {
		t.Fatalf("SetAssignee err = %v, want wrapped %v", err, ErrInvalidAssignee)
	}

	// Act - test unassign (nil)
	err = service.SetAssignee(t.Context(), access.Unrestricted(), 1, nil)

	// Assert
	if err != nil {
		t.Fatalf("SetAssignee with nil = %v error, want nil", err)
	}
	if repository.lastAssigneeCheck != 0 {
		t.Fatalf("lastAssigneeCheck = %d, want 0 — a rejected or empty id must not reach the repository",
			repository.lastAssigneeCheck)
	}
}

// TestSetAssigneeRejectsADeactivatedUser keeps an issue from being stranded
// on an account nobody can act as: a deactivated user is absent from every
// picker and holds no session, so an assignment to one is unreachable work.
func TestSetAssigneeRejectsADeactivatedUser(t *testing.T) {
	repository := &fakeRepository{canBeAssigned: false}
	service := NewService(repository)

	err := service.SetAssignee(t.Context(), access.Unrestricted(), 1, ptrInt64(7))

	if !errors.Is(err, ErrInvalidAssignee) {
		t.Fatalf("SetAssignee err = %v, want wrapped %v", err, ErrInvalidAssignee)
	}
	if repository.lastAssigneeCheck != 7 {
		t.Fatalf("lastAssigneeCheck = %d, want 7", repository.lastAssigneeCheck)
	}
}

func TestSetAssigneeAcceptsAnActiveUser(t *testing.T) {
	repository := &fakeRepository{canBeAssigned: true}
	service := NewService(repository)

	if err := service.SetAssignee(t.Context(), access.Unrestricted(), 1, ptrInt64(7)); err != nil {
		t.Fatalf("SetAssignee: %v", err)
	}
}

// TestSetAssigneeReportsAFailedActivityCheckAsItself keeps a repository
// failure from being reported as a caller mistake: a 500 is honest here, a
// 400 saying "invalid assignee" would send the operator looking at the id.
func TestSetAssigneeReportsAFailedActivityCheckAsItself(t *testing.T) {
	sentinel := errors.New("database unavailable")
	service := NewService(&fakeRepository{canBeAssignedErr: sentinel})

	err := service.SetAssignee(t.Context(), access.Unrestricted(), 1, ptrInt64(7))

	if !errors.Is(err, sentinel) {
		t.Fatalf("SetAssignee err = %v, want wrapped %v", err, sentinel)
	}
	if errors.Is(err, ErrInvalidAssignee) {
		t.Fatal("a repository failure must not be reported as an invalid assignee")
	}
}

// TestListIssuesRejectsInvalidWindow pins that every Window.Validate failure
// surfaces through the service wrapped in ErrInvalidWindow — not just any
// error — because the HTTP handler classifies solely on that sentinel to
// return 400 instead of 500 for what is a client mistake, not a repository
// fault. Table cases mirror predicate_test.go's TestWindowValidate.
func TestListIssuesRejectsInvalidWindow(t *testing.T) {
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		window Window
	}{
		{
			name:   "trailing and absolute together",
			window: Window{Trailing: time.Hour, Start: base.Add(-time.Hour), End: base},
		},
		{name: "start without end", window: Window{Start: base}},
		{name: "end before start", window: Window{Start: base, End: base.Add(-time.Hour)}},
		{name: "negative trailing duration", window: Window{Trailing: -time.Hour}},
		{
			name:   "absolute window past the cap",
			window: Window{Start: base.Add(-MaxAbsoluteWindow - time.Minute), End: base},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewService(&fakeRepository{})
			_, err := service.ListIssues(t.Context(), ListFilters{
				ProjectSlug: "web",
				Window:      testCase.window,
			})
			if !errors.Is(err, ErrInvalidWindow) {
				t.Fatalf("ListIssues err = %v, want wrapped %v", err, ErrInvalidWindow)
			}
		})
	}
}

func TestServiceTimelineReturnsSpans(t *testing.T) {
	want := []Span{{SpanID: "s1", Name: "GET /invoice"}}
	service := NewService(&fakeRepository{timeline: want})
	got, err := service.Timeline(t.Context(), access.Unrestricted(), uuid.MustParse("00000000-0000-0000-0000-000000000001"))
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(got) != 1 || got[0].SpanID != "s1" {
		t.Fatalf("spans = %+v", got)
	}
}

func TestIssueStatsValidatesPeriod(t *testing.T) {
	// Arrange
	service := NewService(&fakeRepository{})

	// Act & Assert - test invalid period
	_, err := service.IssueStats(t.Context(), access.Unrestricted(), 1, Period("7y"))
	if err == nil {
		t.Fatalf("IssueStats with invalid period = nil error, want error")
	}

	// Act & Assert - test valid period
	_, err = service.IssueStats(t.Context(), access.Unrestricted(), 1, Period24h)
	if err != nil {
		t.Fatalf("IssueStats with valid period = %v error, want nil", err)
	}
}

type fakeResolver struct {
	exception *sourcemap.Exception
	err       error
	calls     int
	lastInput sourcemap.EventInput
}

func (f *fakeResolver) Resolve(_ context.Context, in sourcemap.EventInput) (*sourcemap.Exception, error) {
	f.calls++
	f.lastInput = in
	return f.exception, f.err
}

func symbolicatableEvent() EventDetail {
	release := "web@1"
	return EventDetail{
		Release:   &release,
		ProjectID: 7,
		Payload:   json.RawMessage(`{"exception":{"type":"TypeError","value":"boom"}}`),
	}
}

func TestLatestEventAttachesSymbolicatedException(t *testing.T) {
	resolver := &fakeResolver{exception: &sourcemap.Exception{Type: "TypeError", Value: "boom"}}
	repository := &fakeRepository{event: symbolicatableEvent()}
	service := NewService(repository, resolver)

	got, err := service.LatestEvent(t.Context(), access.Unrestricted(), 5)
	if err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if got.SymbolicatedException == nil || len(got.Payload) == 0 {
		t.Fatalf("LatestEvent = %#v", got)
	}
	if resolver.lastInput.ProjectID != 7 || resolver.lastInput.Release != "web@1" {
		t.Fatalf("resolver input = %#v", resolver.lastInput)
	}
}

func TestEventAttachesSymbolicatedException(t *testing.T) {
	resolver := &fakeResolver{exception: &sourcemap.Exception{Type: "TypeError", Value: "boom"}}
	service := NewService(&fakeRepository{event: symbolicatableEvent()}, resolver)

	got, err := service.Event(t.Context(), access.Unrestricted(), uuid.New())
	if err != nil {
		t.Fatalf("Event: %v", err)
	}
	if got.SymbolicatedException == nil {
		t.Fatalf("Event = %#v", got)
	}
}

func TestLatestEventKeepsExceptionAlongsideResolverError(t *testing.T) {
	// Resolve returns a complete exception together with its failure: an
	// unreadable source map costs the frames their original locations, not the
	// frames themselves. Dropping the exception here would turn one bad upload
	// into "No exception" for every event of that release.
	resolver := &fakeResolver{
		exception: &sourcemap.Exception{Type: "TypeError", Value: "boom"},
		err:       errors.New("resolver boom"),
	}
	service := NewService(&fakeRepository{event: symbolicatableEvent()}, resolver)

	got, err := service.LatestEvent(t.Context(), access.Unrestricted(), 5)
	if err != nil {
		t.Fatalf("LatestEvent must succeed despite resolver error, got %v", err)
	}
	if got.SymbolicatedException == nil {
		t.Fatal("expected the exception to survive a resolver error")
	}
}

func TestLatestEventLeavesSymbolicationNilWhenResolverReturnsNothing(t *testing.T) {
	resolver := &fakeResolver{err: errors.New("resolver boom")}
	service := NewService(&fakeRepository{event: symbolicatableEvent()}, resolver)

	got, err := service.LatestEvent(t.Context(), access.Unrestricted(), 5)
	if err != nil {
		t.Fatalf("LatestEvent must succeed despite resolver error, got %v", err)
	}
	if got.SymbolicatedException != nil {
		t.Fatalf("expected nil symbolication, got %#v", got.SymbolicatedException)
	}
}

func TestLatestEventWithoutResolverReturnsPlainEvent(t *testing.T) {
	service := NewService(&fakeRepository{event: symbolicatableEvent()})

	got, err := service.LatestEvent(t.Context(), access.Unrestricted(), 5)
	if err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if got.SymbolicatedException != nil {
		t.Fatalf("expected nil symbolication without a resolver, got %#v", got.SymbolicatedException)
	}
}

func TestLatestEventResolvesExceptionWithoutRelease(t *testing.T) {
	// A release scopes the source-map lookup; it is not a precondition for
	// reading frames out of the payload. An event without one still has a
	// stack trace to show, so the resolver runs with an empty release.
	resolver := &fakeResolver{exception: &sourcemap.Exception{Type: "X"}}
	event := symbolicatableEvent()
	event.Release = nil
	service := NewService(&fakeRepository{event: event}, resolver)

	got, err := service.LatestEvent(t.Context(), access.Unrestricted(), 5)
	if err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolver.calls)
	}
	if resolver.lastInput.Release != "" {
		t.Fatalf("resolver release = %q, want empty", resolver.lastInput.Release)
	}
	if got.SymbolicatedException == nil {
		t.Fatal("expected an exception for a release-less event")
	}
}

func TestLatestEventSkipsResolverWithoutPayload(t *testing.T) {
	resolver := &fakeResolver{exception: &sourcemap.Exception{Type: "X"}}
	event := symbolicatableEvent()
	event.Payload = nil
	service := NewService(&fakeRepository{event: event}, resolver)

	if _, err := service.LatestEvent(t.Context(), access.Unrestricted(), 5); err != nil {
		t.Fatalf("LatestEvent: %v", err)
	}
	if resolver.calls != 0 {
		t.Fatalf("resolver should not be called without a payload, calls=%d", resolver.calls)
	}
}

func TestMergeIssueRejectsSameIssue(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)

	if _, err := service.MergeIssue(t.Context(), access.Unrestricted(), 5, 5); !errors.Is(err, ErrSameIssue) {
		t.Fatalf("MergeIssue(5, 5) error = %v, want ErrSameIssue", err)
	}
	if repository.lastMergeSource != 0 {
		t.Fatal("repository.MergeIssue must not be called for a same-issue merge")
	}
}

func TestMergeIssueDelegatesToRepository(t *testing.T) {
	want := Detail{Issue: Issue{ID: 9, Title: "Target"}}
	repository := &fakeRepository{mergeDetail: want}
	service := NewService(repository)

	got, err := service.MergeIssue(t.Context(), access.Unrestricted(), 5, 9)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeIssue(5, 9) = %+v, %v, want %+v", got, err, want)
	}
	if repository.lastMergeSource != 5 || repository.lastMergeTarget != 9 {
		t.Fatalf("repository received source=%d target=%d, want 5, 9", repository.lastMergeSource, repository.lastMergeTarget)
	}
}

func TestMergeIssuePropagatesRepositoryError(t *testing.T) {
	repository := &fakeRepository{mergeErr: ErrAlreadyMerged}
	service := NewService(repository)

	if _, err := service.MergeIssue(t.Context(), access.Unrestricted(), 5, 9); !errors.Is(err, ErrAlreadyMerged) {
		t.Fatalf("MergeIssue error = %v, want ErrAlreadyMerged", err)
	}
}

func TestTagKeysDelegatesToRepository(t *testing.T) {
	want := []TagKey{{Key: "browser", ValueCount: 3, TimesSeen: 12}}
	repository := &fakeRepository{tagKeys: want}
	service := NewService(repository)

	scope := access.Limited([]int64{1})
	got, err := service.TagKeys(t.Context(), scope, "demo")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("TagKeys = %+v, %v, want %+v", got, err, want)
	}
	if repository.lastTagKeysSlug != "demo" {
		t.Fatalf("repository received slug %q, want demo", repository.lastTagKeysSlug)
	}
	if !reflect.DeepEqual(repository.lastTagKeysScope, scope) {
		t.Fatalf("repository received scope %+v, want %+v", repository.lastTagKeysScope, scope)
	}
}

func TestTagKeysPropagatesRepositoryError(t *testing.T) {
	repository := &fakeRepository{tagErr: ErrNotFound}
	service := NewService(repository)

	if _, err := service.TagKeys(t.Context(), access.Unrestricted(), "demo"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TagKeys error = %v, want ErrNotFound", err)
	}
}

func TestTagValuesDelegatesToRepository(t *testing.T) {
	want := []TagValue{{Value: "chrome", TimesSeen: 9}}
	repository := &fakeRepository{tagValues: want}
	service := NewService(repository)

	scope := access.Limited([]int64{1})
	got, err := service.TagValues(t.Context(), scope, "demo", "browser", "ch", 50)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("TagValues = %+v, %v, want %+v", got, err, want)
	}
	if repository.lastTagValuesSlug != "demo" || repository.lastTagValuesKey != "browser" ||
		repository.lastTagValuesPrefix != "ch" || repository.lastTagValuesLimit != 50 {
		t.Fatalf("repository received (%q, %q, %q, %d), want (demo, browser, ch, 50)",
			repository.lastTagValuesSlug, repository.lastTagValuesKey, repository.lastTagValuesPrefix, repository.lastTagValuesLimit)
	}
	if !reflect.DeepEqual(repository.lastTagValuesScope, scope) {
		t.Fatalf("repository received scope %+v, want %+v", repository.lastTagValuesScope, scope)
	}
}

func TestTagValuesPropagatesRepositoryError(t *testing.T) {
	repository := &fakeRepository{tagErr: ErrNotFound}
	service := NewService(repository)

	if _, err := service.TagValues(t.Context(), access.Unrestricted(), "demo", "browser", "", 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("TagValues error = %v, want ErrNotFound", err)
	}
}

func TestIssueUsersDelegatesToRepository(t *testing.T) {
	want := UserFacet{Users: []UserIdentity{{Key: "email", Value: "a@example.com", TimesSeen: 12}}, Total: 37}
	repository := &fakeRepository{userFacet: want}
	service := NewService(repository)

	got, err := service.IssueUsers(t.Context(), access.Unrestricted(), 5, 50)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("IssueUsers = %+v, %v, want %+v", got, err, want)
	}
	if repository.lastUserIssueID != 5 {
		t.Fatalf("repository received issue id %d, want 5", repository.lastUserIssueID)
	}
}

func TestIssueUsersNormalizesLimit(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)

	if _, err := service.IssueUsers(t.Context(), access.Unrestricted(), 5, 0); err != nil {
		t.Fatalf("IssueUsers: %v", err)
	}
	if repository.lastUserFacetLimit != DefaultLimit {
		t.Fatalf("limit = %d, want default %d", repository.lastUserFacetLimit, DefaultLimit)
	}

	if _, err := service.IssueUsers(t.Context(), access.Unrestricted(), 5, MaxLimit+1000); err != nil {
		t.Fatalf("IssueUsers: %v", err)
	}
	if repository.lastUserFacetLimit != MaxLimit {
		t.Fatalf("limit = %d, want clamped %d", repository.lastUserFacetLimit, MaxLimit)
	}
}

func TestIssueUsersPropagatesRepositoryError(t *testing.T) {
	repository := &fakeRepository{userErr: ErrNotFound}
	service := NewService(repository)

	if _, err := service.IssueUsers(t.Context(), access.Unrestricted(), 5, 50); !errors.Is(err, ErrNotFound) {
		t.Fatalf("IssueUsers error = %v, want ErrNotFound", err)
	}
}

// TestServiceForwardsScopeToRepository closes a coverage gap: TagKeys and
// TagValues already had their forwarded scope captured and asserted, but
// every other Service method that takes an access.Scope discarded it in the
// fakeRepository stub, so a Service method that forwarded a hardcoded scope
// instead of its caller's would have passed every test here. One distinct
// access.Scope per call, asserted against what the repository actually
// received, closes that gap for the rest of the interface.
func TestServiceForwardsScopeToRepository(t *testing.T) {
	repository := &fakeRepository{}
	service := NewService(repository)
	ctx := t.Context()
	eventID := uuid.New()

	cases := []struct {
		name  string
		scope access.Scope
		call  func(access.Scope)
		got   func() access.Scope
	}{
		{
			name:  "IssueDetail",
			scope: access.Limited([]int64{1}),
			call:  func(scope access.Scope) { _, _ = service.IssueDetail(ctx, scope, 5) },
			got:   func() access.Scope { return repository.lastDetailScope },
		},
		{
			name:  "SetStatus",
			scope: access.Limited([]int64{2}),
			call:  func(scope access.Scope) { _ = service.SetStatus(ctx, scope, 5, StatusResolved) },
			got:   func() access.Scope { return repository.lastUpdateStatusScope },
		},
		{
			name:  "SetAssignee",
			scope: access.Limited([]int64{3}),
			call:  func(scope access.Scope) { _ = service.SetAssignee(ctx, scope, 5, nil) },
			got:   func() access.Scope { return repository.lastAssignScope },
		},
		{
			name:  "IssueEvents",
			scope: access.Limited([]int64{4}),
			call:  func(scope access.Scope) { _, _ = service.IssueEvents(ctx, scope, EventQuery{}) },
			got:   func() access.Scope { return repository.lastEventsScope },
		},
		{
			name:  "LatestEvent",
			scope: access.Limited([]int64{5}),
			call:  func(scope access.Scope) { _, _ = service.LatestEvent(ctx, scope, 5) },
			got:   func() access.Scope { return repository.lastLatestEventScope },
		},
		{
			name:  "Event",
			scope: access.Limited([]int64{6}),
			call:  func(scope access.Scope) { _, _ = service.Event(ctx, scope, eventID) },
			got:   func() access.Scope { return repository.lastEventScope },
		},
		{
			name:  "IssueStats",
			scope: access.Limited([]int64{7}),
			call:  func(scope access.Scope) { _, _ = service.IssueStats(ctx, scope, 5, Period24h) },
			got:   func() access.Scope { return repository.lastStatsScope },
		},
		{
			name:  "Timeline",
			scope: access.Limited([]int64{8}),
			call:  func(scope access.Scope) { _, _ = service.Timeline(ctx, scope, eventID) },
			got:   func() access.Scope { return repository.lastTimelineScope },
		},
		{
			name:  "MergeIssue",
			scope: access.Limited([]int64{9}),
			call:  func(scope access.Scope) { _, _ = service.MergeIssue(ctx, scope, 5, 6) },
			got:   func() access.Scope { return repository.lastMergeScope },
		},
		{
			name:  "IssueUsers",
			scope: access.Limited([]int64{10}),
			call:  func(scope access.Scope) { _, _ = service.IssueUsers(ctx, scope, 5, 50) },
			got:   func() access.Scope { return repository.lastUserFacetScope },
		},
	}

	for _, current := range cases {
		t.Run(current.name, func(t *testing.T) {
			current.call(current.scope)
			if got := current.got(); !reflect.DeepEqual(got, current.scope) {
				t.Fatalf("repository received scope %+v, want %+v", got, current.scope)
			}
		})
	}
}
