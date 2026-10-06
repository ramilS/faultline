package issue

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/sourcemap"
)

// Repository is the persistence contract behind the issue read/triage service.
type Repository interface {
	List(context.Context, ListFilters) (Page, error)
	Detail(context.Context, access.Scope, int64) (Detail, error)
	UpdateStatus(context.Context, access.Scope, int64, Status) error
	Assign(context.Context, access.Scope, int64, *int64) error
	// CanBeAssigned reports whether userID may be assigned issueID: the
	// account exists, is not deactivated, and reaches the issue's project.
	// The foreign key on issues.assignee_id already proves existence;
	// activity and access are the halves no constraint can express.
	CanBeAssigned(ctx context.Context, issueID, userID int64) (bool, error)
	Events(context.Context, access.Scope, EventQuery) (EventPage, error)
	LatestEvent(context.Context, access.Scope, int64) (EventDetail, error)
	Event(context.Context, access.Scope, uuid.UUID) (EventDetail, error)
	Stats(context.Context, access.Scope, int64, Period) ([]Bucket, error)
	Timeline(context.Context, access.Scope, uuid.UUID) ([]Span, error)
	MergeIssue(context.Context, access.Scope, int64, int64) (Detail, error)
	TagKeys(context.Context, access.Scope, string) ([]TagKey, error)
	TagValues(context.Context, access.Scope, string, string, string, int) ([]TagValue, error)
	IssueUsers(context.Context, access.Scope, int64, int) (UserFacet, error)
}

// Service applies issue query and triage policy over a Repository.
type Service struct {
	repository Repository
	resolver   sourcemap.Resolver
}

// NewService builds the issue service. An optional source-map resolver enables
// read-time browser symbolication; at most one resolver is used.
func NewService(repository Repository, resolvers ...sourcemap.Resolver) *Service {
	service := &Service{repository: repository}
	if len(resolvers) > 0 {
		service.resolver = resolvers[0]
	}
	return service
}

func ValidateStatus(status Status) bool {
	switch status {
	case StatusUnresolved, StatusResolved, StatusIgnored:
		return true
	default:
		return false
	}
}

func ValidateSort(sort Sort) bool {
	switch sort {
	case SortLastSeen, SortTimesSeen, SortFirstSeen, SortUsersSeen:
		return true
	default:
		return false
	}
}

func ValidateEventOrder(order EventOrder) bool {
	switch order {
	case EventOrderNewest, EventOrderOldest:
		return true
	default:
		return false
	}
}

func ValidatePeriod(period Period) bool {
	switch period {
	case Period24h, Period14d, Period90d:
		return true
	default:
		return false
	}
}

// NormalizeLimit defaults a zero limit and clamps into [1, MaxLimit].
func NormalizeLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultLimit
	case limit > MaxLimit:
		return MaxLimit
	default:
		return limit
	}
}

func (s *Service) ListIssues(ctx context.Context, filters ListFilters) (Page, error) {
	if filters.Sort == "" {
		filters.Sort = SortLastSeen
	}
	if !ValidateSort(filters.Sort) {
		return Page{}, fmt.Errorf("invalid sort")
	}
	// A cursor is self-describing: it carries the sort it was produced under.
	// Reject a mismatch here rather than letting the repository reinterpret the
	// boundary value under the wrong column — two sorts can share the same
	// value shape (times_seen and users_seen are both bigints), so a type-based
	// parse alone cannot catch a swapped cursor.
	if filters.Cursor != nil {
		if err := filters.Cursor.RequireSort(filters.Sort); err != nil {
			return Page{}, fmt.Errorf("list issues: %w", err)
		}
	}
	if err := filters.Window.Validate(); err != nil {
		return Page{}, fmt.Errorf("%w: %w", ErrInvalidWindow, err)
	}
	filters.Limit = NormalizeLimit(filters.Limit)
	page, err := s.repository.List(ctx, filters)
	if err != nil {
		return Page{}, fmt.Errorf("list issues: %w", err)
	}
	return page, nil
}

func (s *Service) IssueDetail(ctx context.Context, scope access.Scope, id int64) (Detail, error) {
	detail, err := s.repository.Detail(ctx, scope, id)
	if err != nil {
		return Detail{}, fmt.Errorf("issue detail: %w", err)
	}
	return detail, nil
}

func (s *Service) SetStatus(ctx context.Context, scope access.Scope, id int64, status Status) error {
	if !ValidateStatus(status) {
		return fmt.Errorf("invalid status")
	}
	if err := s.repository.UpdateStatus(ctx, scope, id, status); err != nil {
		return fmt.Errorf("set issue status: %w", err)
	}
	return nil
}

// SetAssignee assigns the issue to a user who may be assigned it, or clears
// the assignee when assigneeID is nil. Both a deactivated account and one
// that cannot reach the issue's project are refused: either strands the
// issue with somebody who can neither see it as theirs nor hand it on.
// Deactivation and access changes are never retroactive, so past assignments
// made while a user qualified stay exactly as they were — this guards the
// write, not the history.
func (s *Service) SetAssignee(ctx context.Context, scope access.Scope, id int64, assigneeID *int64) error {
	if assigneeID != nil {
		if *assigneeID <= 0 {
			return fmt.Errorf("invalid assignee id: %w", ErrInvalidAssignee)
		}
		allowed, err := s.repository.CanBeAssigned(ctx, id, *assigneeID)
		if err != nil {
			return fmt.Errorf("check assignee: %w", err)
		}
		if !allowed {
			return fmt.Errorf("assignee %d is not assignable: %w", *assigneeID, ErrInvalidAssignee)
		}
	}
	if err := s.repository.Assign(ctx, scope, id, assigneeID); err != nil {
		return fmt.Errorf("set issue assignee: %w", err)
	}
	return nil
}

func (s *Service) IssueEvents(ctx context.Context, scope access.Scope, query EventQuery) (EventPage, error) {
	if query.Order == "" {
		query.Order = EventOrderNewest
	}
	if !ValidateEventOrder(query.Order) {
		return EventPage{}, fmt.Errorf("invalid event order")
	}
	query.Limit = NormalizeLimit(query.Limit)
	page, err := s.repository.Events(ctx, scope, query)
	if err != nil {
		return EventPage{}, fmt.Errorf("issue events: %w", err)
	}
	return page, nil
}

func (s *Service) LatestEvent(ctx context.Context, scope access.Scope, id int64) (EventDetail, error) {
	event, err := s.repository.LatestEvent(ctx, scope, id)
	if err != nil {
		return EventDetail{}, fmt.Errorf("latest event: %w", err)
	}
	return s.symbolicate(ctx, event), nil
}

func (s *Service) Event(ctx context.Context, scope access.Scope, eventID uuid.UUID) (EventDetail, error) {
	event, err := s.repository.Event(ctx, scope, eventID)
	if err != nil {
		return EventDetail{}, fmt.Errorf("event: %w", err)
	}
	return s.symbolicate(ctx, event), nil
}

// symbolicate attaches the event's exception, with original-source locations
// on whichever frames a stored source map resolves.
//
// The release is passed through when the event has one and left empty when it
// does not: it scopes the source-map lookup, so an event without one gets
// unmapped frames rather than no frames. Gating the whole call on it hid every
// release-less stack trace behind the dashboard's "No exception" state.
//
// A resolver failure is swallowed so a readable event never turns into a 500.
// It is not lost: the resolver installed in production is wrapped in
// internal/command/run.go's reportingResolver, which reports the failure and
// returns a nil error, so the error this call discards is always nil there.
// Nor does a failure discard the exception, which Resolve returns fully built
// alongside it — one unreadable source map costs the frames their original
// locations, not their existence.
func (s *Service) symbolicate(ctx context.Context, event EventDetail) EventDetail {
	if s.resolver == nil || len(event.Payload) == 0 {
		return event
	}
	var release string
	if event.Release != nil {
		release = *event.Release
	}
	exception, _ := s.resolver.Resolve(ctx, sourcemap.EventInput{
		ProjectID: event.ProjectID,
		Release:   release,
		Payload:   event.Payload,
	})
	if exception == nil {
		return event
	}
	event.SymbolicatedException = exception
	return event
}

func (s *Service) Timeline(ctx context.Context, scope access.Scope, eventID uuid.UUID) ([]Span, error) {
	spans, err := s.repository.Timeline(ctx, scope, eventID)
	if err != nil {
		return nil, fmt.Errorf("timeline: %w", err)
	}
	return spans, nil
}

// MergeIssue merges sourceID into targetID: future matching events land on
// targetID via two auto-generated fingerprint-kind grouping rules, and
// sourceID is tombstoned (merged_into_id set), not deleted.
func (s *Service) MergeIssue(ctx context.Context, scope access.Scope, sourceID, targetID int64) (Detail, error) {
	if sourceID == targetID {
		return Detail{}, fmt.Errorf("merge issue: %w", ErrSameIssue)
	}
	detail, err := s.repository.MergeIssue(ctx, scope, sourceID, targetID)
	if err != nil {
		return Detail{}, fmt.Errorf("merge issue: %w", err)
	}
	return detail, nil
}

// TagKeys lists a project's tag vocabulary for the filter bar's key picker.
func (s *Service) TagKeys(ctx context.Context, scope access.Scope, slug string) ([]TagKey, error) {
	keys, err := s.repository.TagKeys(ctx, scope, slug)
	if err != nil {
		return nil, fmt.Errorf("tag keys: %w", err)
	}
	return keys, nil
}

// TagValues lists the values of one tag key matching prefix, for autocomplete.
func (s *Service) TagValues(ctx context.Context, scope access.Scope, slug, key, prefix string, limit int) ([]TagValue, error) {
	values, err := s.repository.TagValues(ctx, scope, slug, key, prefix, limit)
	if err != nil {
		return nil, fmt.Errorf("tag values: %w", err)
	}
	return values, nil
}

// IssueUsers returns the affected-users facet for id: a times_seen-ordered
// page of distinct identities capped at limit, plus the issue's total
// distinct-identity count. limit is normalized the same way list and event
// pagination are (NormalizeLimit): zero or negative means "no explicit
// choice", not "zero rows", and an oversized value is clamped to MaxLimit.
func (s *Service) IssueUsers(ctx context.Context, scope access.Scope, id int64, limit int) (UserFacet, error) {
	facet, err := s.repository.IssueUsers(ctx, scope, id, NormalizeLimit(limit))
	if err != nil {
		return UserFacet{}, fmt.Errorf("issue users: %w", err)
	}
	return facet, nil
}

func (s *Service) IssueStats(ctx context.Context, scope access.Scope, id int64, period Period) ([]Bucket, error) {
	if !ValidatePeriod(period) {
		return nil, fmt.Errorf("invalid period")
	}
	buckets, err := s.repository.Stats(ctx, scope, id, period)
	if err != nil {
		return nil, fmt.Errorf("issue stats: %w", err)
	}
	return buckets, nil
}
