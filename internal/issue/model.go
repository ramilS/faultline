// Package issue defines the issue query and triage domain contracts.
package issue

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/sourcemap"
)

var (
	ErrNotFound = errors.New("not found")
	// ErrInvalidAssignee reports an assignee id that is not a positive number,
	// names no user row, or names a deactivated account. The HTTP handler maps
	// it to 400 by identity: all three are caller mistakes, not server faults.
	ErrInvalidAssignee = errors.New("invalid assignee")
	// ErrSameIssue reports a merge request with source == target. The HTTP
	// handler must map this to 400: it is a
	// well-formed but invalid request, not a missing resource.
	ErrSameIssue = errors.New("cannot merge an issue into itself")
	// ErrAlreadyMerged reports a merge where the source or target already has
	// a non-nil merged_into_id. The HTTP handler must map this to 404, not 409: an already-merged issue is
	// surfaced identically to a missing one, the same way a cross-project
	// issue id is (see issueRepository.MergeIssue), so a caller cannot
	// distinguish "never existed" from "merged away".
	ErrAlreadyMerged = errors.New("issue already merged")
	// ErrInvalidWindow reports a Window that failed Validate (trailing and
	// absolute set together, start without end, end before start, or an
	// absolute range over MaxAbsoluteWindow). It is a well-formed but invalid
	// request, not a server fault, so the HTTP handler must map it to 400.
	ErrInvalidWindow = errors.New("invalid window")
)

type Status string

const (
	StatusUnresolved Status = "unresolved"
	StatusResolved   Status = "resolved"
	StatusIgnored    Status = "ignored"
)

type Sort string

const (
	SortLastSeen  Sort = "last_seen"
	SortTimesSeen Sort = "times_seen"
	SortFirstSeen Sort = "first_seen"
	SortUsersSeen Sort = "users_seen"
)

type Period string

const (
	Period24h Period = "24h"
	Period14d Period = "14d"
	Period90d Period = "90d"
)

const (
	// DefaultLimit and MaxLimit bound the issue-list page size.
	DefaultLimit = 50
	MaxLimit     = 100
	// spark24hBuckets and spark14dBuckets size the list occurrence sparklines.
	spark24hBuckets = 24
	spark14dBuckets = 14
)

// Assignee is the triage owner rendered on issues; nil means unassigned.
type Assignee struct {
	ID    int64
	Name  string
	Email string
}

// Issue is a grouped error with its triage state and list sparklines.
type Issue struct {
	ID int64
	// ProjectSlug names the project the issue belongs to, so a list spanning
	// every project can link each row back to its own project.
	ProjectSlug string
	Fingerprint string
	Title       string
	Culprit     string
	Level       string
	Status      Status
	Assignee    *Assignee
	TimesSeen   int64
	// UsersSeen is the distinct-identity count aggregated into issues.users_seen
	// (see upsertUserIdentities). Past ingest.MaxUsersPerIssue it saturates into
	// a floor rather than staying exact — new identities stop being recorded,
	// but the count itself never drifts from a real count(*) over issue_users.
	UsersSeen int64
	FirstSeen time.Time
	LastSeen  time.Time
	Counts24h [spark24hBuckets]int64
	Counts14d [spark14dBuckets]int64
	// Counts and PeriodCount are populated only for a windowed request; a
	// windowless one keeps Counts24h/Counts14d so existing callers are
	// untouched. Bucket width follows the window — see bucketWidth.
	Counts      []int64
	PeriodCount int64
	// MergedIntoID is set once this issue has been merged away (tombstoned):
	// nil for a live issue, otherwise the id of the issue it was merged into.
	MergedIntoID *int64
}

// TagValue is one distinct value of a tag key with its occurrence window.
type TagValue struct {
	Value     string
	TimesSeen int64
	FirstSeen time.Time
	LastSeen  time.Time
}

// TagKey is one distinct tag key in a project, with how many distinct values
// it has and how often it has been seen. Powers the facet key list.
type TagKey struct {
	Key        string
	ValueCount int64
	TimesSeen  int64
}

// TagFacet groups the retained values of a single tag key.
type TagFacet struct {
	Key    string
	Values []TagValue
}

// Detail is the issue-detail read: the issue plus its reserved-tag facets.
type Detail struct {
	Issue        Issue
	Environments []string
	Releases     []string
	Tags         []TagFacet
	// Regression is set while the issue is open because a resolved issue
	// came back, and nil otherwise.
	Regression *Regression
}

// Regression is when a resolved issue last came back, and the release of the
// event that brought it back, or "" when that event named none.
type Regression struct {
	At      time.Time
	Release string
}

// UserIdentity is one distinct end-user identity attributed to an issue —
// one row of issue_users. Key is one of the four fixed identity fields
// ("id", "email", "username", "ip").
type UserIdentity struct {
	Key       string
	Value     string
	TimesSeen int64
	LastSeen  time.Time
}

// UserFacet is the affected-users read for one issue: a times_seen-ordered
// page of identities, plus Total — issues.users_seen, the issue's full
// distinct-identity count. Total is independent of len(Users): it does not
// shrink to the page size, which is what lets a caller tell a short page
// apart from a saturated issue (see ingest.MaxUsersPerIssue).
type UserFacet struct {
	Users []UserIdentity
	Total int64
}

// EventSummary is one row in the issue event list.
type EventSummary struct {
	EventID     uuid.UUID
	Timestamp   time.Time
	ReceivedAt  time.Time
	Level       string
	Environment string
	Release     *string
	Message     string
}

// EventDetail is a single event with its correlation id and full payload.
// SymbolicatedException is populated only when a source-map resolver maps the
// event's browser frames; the raw Payload is never mutated.
type EventDetail struct {
	EventSummary
	ProjectID             int64
	RequestID             *string
	Payload               json.RawMessage
	SymbolicatedException *sourcemap.Exception
}

// Bucket is one point on an occurrence graph.
type Bucket struct {
	Bucket time.Time
	Count  int64
}

// ListFilters is the fully-resolved input to Repository.List. Predicates are
// AND'd; Window and Text are separate because the 2.9b query compiler produces
// predicates but never owns the time range or the free-text term.
type ListFilters struct {
	// ProjectSlug narrows the list to one project; empty lists every project
	// Scope admits.
	ProjectSlug string
	Window      Window
	Predicates  []Predicate
	Text        string
	Sort        Sort
	Limit       int
	Cursor      *Cursor
	// Scope restricts the list to projects the caller may read. It is part of
	// the filter value rather than a separate parameter so that a call site
	// cannot construct a list request that forgot it.
	Scope access.Scope
}

// Page is a slice of issues plus the cursor for the next page (nil at end).
type Page struct {
	Issues     []Issue
	NextCursor *Cursor
}

// EventOrder is the direction a walk through an issue's events runs in.
type EventOrder string

const (
	// EventOrderNewest walks from the most recent event backwards. It is the
	// default: every caller written before oldest-first existed passes no
	// order and must keep getting this.
	EventOrderNewest EventOrder = "newest"
	// EventOrderOldest walks forward from the first occurrence, so "when did
	// this start?" is one request instead of paging the whole stream.
	EventOrderOldest EventOrder = "oldest"
)

// EventCursor is the keyset position in an issue's event stream. A cursor is
// only meaningful under the order that produced it: the keyset comparison flips
// with the ORDER BY, so replaying a newest-first cursor under an ascending walk
// silently yields an empty page or an endless one.
type EventCursor struct {
	ReceivedAt time.Time
	EventID    uuid.UUID
}

// EventQuery is the fully-resolved input to Repository.Events. Environment
// and Window are optional scopes on top of the cursor walk: a zero value of
// either (empty string, zero Window) means unscoped, matching every event
// exactly as an unset predicate always has.
type EventQuery struct {
	IssueID     int64
	Cursor      *EventCursor
	Limit       int
	Order       EventOrder
	Environment string
	Window      Window
}

// EventPage is a slice of event summaries plus the next keyset position.
type EventPage struct {
	Events     []EventSummary
	NextCursor *EventCursor
}

// Span is one span on a request timeline, ordered by StartTime by the query.
type Span struct {
	SpanID       string
	ParentSpanID *string
	Name         string
	StartTime    time.Time
	DurationMS   float64
	Status       string
	Attributes   json.RawMessage
}
