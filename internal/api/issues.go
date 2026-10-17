package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/sourcemap"
)

const issueBodyLimit = 16 << 10

// IssueService is the issue query and triage behavior the HTTP layer needs.
type IssueService interface {
	ListIssues(context.Context, issue.ListFilters) (issue.Page, error)
	IssueDetail(context.Context, access.Scope, int64) (issue.Detail, error)
	SetStatus(context.Context, access.Scope, int64, issue.Status) error
	SetAssignee(context.Context, access.Scope, int64, *int64) error
	IssueEvents(context.Context, access.Scope, issue.EventQuery) (issue.EventPage, error)
	LatestEvent(context.Context, access.Scope, int64) (issue.EventDetail, error)
	Event(context.Context, access.Scope, uuid.UUID) (issue.EventDetail, error)
	IssueStats(context.Context, access.Scope, int64, issue.Period) ([]issue.Bucket, error)
	Timeline(context.Context, access.Scope, uuid.UUID) ([]issue.Span, error)
	MergeIssue(context.Context, access.Scope, int64, int64) (issue.Detail, error)
	TagKeys(context.Context, access.Scope, string) ([]issue.TagKey, error)
	TagValues(context.Context, access.Scope, string, string, string, int) ([]issue.TagValue, error)
	IssueUsers(context.Context, access.Scope, int64, int) (issue.UserFacet, error)
}

type IssueHandler struct {
	service     IssueService
	reportError func(error)
}

func NewIssueHandler(service IssueService, options Options) *IssueHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &IssueHandler{service: service, reportError: reportError}
}

type assigneeResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type issueResponse struct {
	ID          int64             `json:"id"`
	ProjectSlug string            `json:"project_slug"`
	Fingerprint string            `json:"fingerprint"`
	Title       string            `json:"title"`
	Culprit     string            `json:"culprit"`
	Level       string            `json:"level"`
	Status      string            `json:"status"`
	Assignee    *assigneeResponse `json:"assignee"`
	TimesSeen   int64             `json:"times_seen"`
	// UsersSeen is issues.users_seen (see issue.Issue.UsersSeen). Past
	// ingest.MaxUsersPerIssue it is a floor, not an exact count: new
	// identities stop being recorded once the cap is hit, so a client
	// rendering this value should treat it as "at least" past that point.
	UsersSeen int64  `json:"users_seen"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
	// Counts and PeriodCount are populated for a windowed list request;
	// Counts24h/Counts14d are populated for a windowless one. Exactly one
	// pair is ever non-empty — see toIssueResponse.
	Counts      []int64 `json:"counts,omitempty"`
	PeriodCount *int64  `json:"period_count,omitempty"`
	Counts24h   []int64 `json:"counts_24h,omitempty"`
	Counts14d   []int64 `json:"counts_14d,omitempty"`
}

type listIssuesResponse struct {
	Issues     []issueResponse `json:"issues"`
	NextCursor *string         `json:"next_cursor"`
}

type tagValueResponse struct {
	Value     string `json:"value"`
	TimesSeen int64  `json:"times_seen"`
	FirstSeen string `json:"first_seen"`
	LastSeen  string `json:"last_seen"`
}

type tagFacetResponse struct {
	Key    string             `json:"key"`
	Values []tagValueResponse `json:"values"`
}

type issueDetailResponse struct {
	Issue        issueResponse       `json:"issue"`
	Environments []string            `json:"environments"`
	Releases     []string            `json:"releases"`
	Tags         []tagFacetResponse  `json:"tags"`
	Regression   *regressionResponse `json:"regression"`
}

// regressionResponse is null on an issue that is not open because it came back.
type regressionResponse struct {
	At      string  `json:"at"`
	Release *string `json:"release"`
}

func (h *IssueHandler) ListIssues(w http.ResponseWriter, r *http.Request) {
	user, hasSession := UserFromContext(r.Context())
	filters, err := parseListFilters(r, user.Email, hasSession)
	if errors.Is(err, errAssignedMeRequiresSession) {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid issue filters")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	filters.Scope = scope
	page, err := h.service.ListIssues(r.Context(), filters)
	if !h.applyListIssuesError(w, err) {
		return
	}
	items := make([]issueResponse, 0, len(page.Issues))
	for _, listed := range page.Issues {
		items = append(items, toIssueResponse(listed))
	}
	var nextCursor *string
	if page.NextCursor != nil {
		token := issue.EncodeCursor(*page.NextCursor)
		nextCursor = &token
	}
	writeJSON(w, http.StatusOK, listIssuesResponse{Issues: items, NextCursor: nextCursor})
}

// applyListIssuesError maps a ListIssues error to a response and reports
// whether the caller may continue (true) or a response was already written
// (false). ErrInvalidWindow is a well-formed but invalid request — an
// over-cap range, start without end, end before start, or trailing and
// absolute set together — so it maps to 400 without going through
// reportError, which is reserved for genuine server faults. ErrNotFound
// means the slug named no project this caller may read — an unreachable one
// and a nonexistent one arrive here identically. It answers 404 rather than
// an empty page, because an empty page reads as a project the caller can see
// that happens to hold nothing.
func (h *IssueHandler) applyListIssuesError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, issue.ErrNotFound):
		writeError(w, http.StatusNotFound, "project not found")
	case errors.Is(err, issue.ErrInvalidWindow):
		writeError(w, http.StatusBadRequest, "invalid issue filters")
	default:
		h.reportError(fmt.Errorf("list issues: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return false
}

// mergedIssueResponse is the tombstone-shaped detail response: it deliberately
// carries only the redirect target, not the normal detail fields (Tags,
// Environments, Releases), which Detail leaves nil for a tombstoned issue.
type mergedIssueResponse struct {
	MergedIntoID int64 `json:"merged_into_id"`
}

func (h *IssueHandler) IssueDetail(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	detail, ok := h.loadDetail(w, r, scope, id, "issue detail")
	if !ok {
		return
	}
	if detail.Issue.MergedIntoID != nil {
		writeJSON(w, http.StatusOK, mergedIssueResponse{MergedIntoID: *detail.Issue.MergedIntoID})
		return
	}
	writeJSON(w, http.StatusOK, toDetailResponse(detail))
}

// loadDetail fetches the issue detail for id, writing the appropriate error
// response and reporting whether the caller may continue (true) or a
// response was already written (false). operation labels the reported error.
func (h *IssueHandler) loadDetail(w http.ResponseWriter, r *http.Request, scope access.Scope, id int64, operation string) (issue.Detail, bool) {
	detail, err := h.service.IssueDetail(r.Context(), scope, id)
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "issue not found")
		return issue.Detail{}, false
	}
	if err != nil {
		h.reportError(fmt.Errorf("%s: %w", operation, err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return issue.Detail{}, false
	}
	return detail, true
}

type patchIssueRequest struct {
	Status      *string `json:"status"`
	AssigneeID  *int64  `json:"assignee_id"`
	assigneeSet bool
}

// UnmarshalJSON records whether assignee_id was present so an explicit null
// (unassign) is distinguishable from an omitted field.
func (p *patchIssueRequest) UnmarshalJSON(data []byte) error {
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(data, &probe); err != nil {
		return err
	}
	for key := range probe {
		if key != "status" && key != "assignee_id" {
			return fmt.Errorf("unknown field %q", key)
		}
	}
	if raw, ok := probe["status"]; ok {
		var status string
		if err := json.Unmarshal(raw, &status); err != nil {
			return err
		}
		p.Status = &status
	}
	if raw, ok := probe["assignee_id"]; ok {
		p.assigneeSet = true
		if string(raw) != "null" {
			var id int64
			if err := json.Unmarshal(raw, &id); err != nil {
				return err
			}
			p.AssigneeID = &id
		}
	}
	return nil
}

func (h *IssueHandler) PatchIssue(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input patchIssueRequest
	if err := decodeSingleJSON(w, r, issueBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if input.Status == nil && !input.assigneeSet {
		writeError(w, http.StatusBadRequest, "no issue fields to update")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	if input.Status != nil {
		status := issue.Status(*input.Status)
		err := h.service.SetStatus(r.Context(), scope, id, status)
		if !h.applyTriageError(w, "set issue status", err) {
			return
		}
	}
	if input.assigneeSet {
		err := h.service.SetAssignee(r.Context(), scope, id, input.AssigneeID)
		if !h.applyTriageError(w, "set issue assignee", err) {
			return
		}
	}
	detail, ok := h.loadDetail(w, r, scope, id, "reload issue")
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, toDetailResponse(detail))
}

// applyTriageError maps a mutation error to a response and reports whether the
// caller may continue (true) or a response was already written (false).
func (h *IssueHandler) applyTriageError(w http.ResponseWriter, operation string, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, issue.ErrNotFound):
		writeError(w, http.StatusNotFound, "issue not found")
	case errors.Is(err, issue.ErrInvalidAssignee):
		writeError(w, http.StatusBadRequest, "invalid assignee")
	case isInvalidTriageInput(err):
		writeError(w, http.StatusBadRequest, "invalid issue update")
	default:
		h.reportError(fmt.Errorf("%s: %w", operation, err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return false
}

// isInvalidTriageInput matches the service's status validation failure, which
// carries no sentinel; it is a user input error, not a server fault.
func isInvalidTriageInput(err error) bool {
	return strings.Contains(err.Error(), "invalid status")
}

type mergeIssueRequest struct {
	TargetID int64 `json:"target_id"`
}

func (h *IssueHandler) MergeIssue(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input mergeIssueRequest
	if err := decodeSingleJSON(w, r, issueBodyLimit, &input); err != nil || input.TargetID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if id == input.TargetID {
		writeError(w, http.StatusBadRequest, "cannot merge an issue into itself")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	detail, err := h.service.MergeIssue(r.Context(), scope, id, input.TargetID)
	if !h.applyMergeError(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, toDetailResponse(detail))
}

// applyMergeError maps a merge error to a response and reports whether the
// caller may continue writing a success body (true) or a response was
// already written (false). ErrAlreadyMerged maps to 404, not 409: per
// issue.ErrAlreadyMerged's doc comment, an already-merged issue must be
// indistinguishable from a missing one.
func (h *IssueHandler) applyMergeError(w http.ResponseWriter, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, issue.ErrSameIssue):
		writeError(w, http.StatusBadRequest, "cannot merge an issue into itself")
	case errors.Is(err, issue.ErrAlreadyMerged):
		writeError(w, http.StatusNotFound, "issue already merged")
	case errors.Is(err, issue.ErrNotFound):
		writeError(w, http.StatusNotFound, "issue not found")
	case errors.Is(err, grouping.ErrTooManyRules):
		writeError(w, http.StatusBadRequest, "merging would exceed the project's grouping rule limit")
	default:
		h.reportError(fmt.Errorf("merge issue: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return false
}

// splitNegation strips a leading `!` and reports it as negation. A leading
// `\!` is an escaped literal bang, matching the DSL's escaping rule so the
// parameter form and the 2.9b query form agree on what a value means.
func splitNegation(value string) (string, bool) {
	if strings.HasPrefix(value, `\!`) {
		return value[1:], false
	}
	if strings.HasPrefix(value, "!") {
		return value[1:], true
	}
	return value, false
}

// collectPredicate folds every occurrence of one query parameter into a
// predicate pair: repeated values OR within each. Negated and plain
// occurrences of the same key produce two predicates, which then AND —
// `environment=prod&environment=!staging` means "prod, and not staging".
func collectPredicate(values []string, field issue.Field, key string, validate func(string) bool) ([]issue.Predicate, error) {
	var included, excluded []string
	for _, raw := range values {
		value, negated := splitNegation(raw)
		if value == "" {
			return nil, fmt.Errorf("empty filter value")
		}
		if validate != nil && !validate(value) {
			return nil, fmt.Errorf("invalid value %q", value)
		}
		if negated {
			excluded = append(excluded, value)
			continue
		}
		included = append(included, value)
	}
	predicates := make([]issue.Predicate, 0, 2)
	if len(included) > 0 {
		predicates = append(predicates, issue.Predicate{Field: field, Key: key, Values: included})
	}
	if len(excluded) > 0 {
		predicates = append(predicates, issue.Predicate{Field: field, Key: key, Negate: true, Values: excluded})
	}
	return predicates, nil
}

// errAssignedMeRequiresSession guards the `me` sentinel: every issue route is
// wrapped in RequireSession, so a missing session here means something is
// wrong with the wiring, not the request. Resolving "me" to an empty identity
// would silently turn into a lookup for a user with no email — an empty,
// confusing result — so this fails closed with an explicit error instead.
var errAssignedMeRequiresSession = errors.New("assigned=me requires an authenticated session")

// resolveAssignee substitutes the caller's own email for the `me` sentinel in
// assigned filter values, negated or not. It runs here, at the HTTP layer,
// specifically so the repository never has to know who is asking — that
// keeps the same predicate model usable by the 2.9b query compiler, which has
// no session either.
func resolveAssignee(values []string, sessionEmail string, hasSession bool) ([]string, error) {
	resolved := make([]string, len(values))
	for index, raw := range values {
		value, negated := splitNegation(raw)
		if value != "me" {
			resolved[index] = raw
			continue
		}
		if !hasSession {
			return nil, errAssignedMeRequiresSession
		}
		if negated {
			resolved[index] = "!" + sessionEmail
			continue
		}
		resolved[index] = sessionEmail
	}
	return resolved, nil
}

// validLevel must track the vocabulary internal/ingest/envelope.go's
// validLevel accepts, or a legitimately ingested level becomes unfilterable.
func validLevel(value string) bool {
	switch value {
	case "fatal", "error", "warning", "info", "debug":
		return true
	default:
		return false
	}
}

func validStatus(value string) bool {
	return issue.ValidateStatus(issue.Status(value))
}

// predicateParams lists the simple query parameters that fold directly into
// one field via collectPredicate. tag is handled separately because its
// values must be grouped by key first.
var predicateParams = []struct {
	param    string
	field    issue.Field
	validate func(string) bool
}{
	{"status", issue.FieldStatus, validStatus},
	{"level", issue.FieldLevel, validLevel},
	{"environment", issue.FieldEnvironment, nil},
	{"release", issue.FieldRelease, nil},
	// assigned accepts "me", "none", or an email; "me" is resolved to the
	// caller's identity by resolveAssignee before collectPredicate runs.
	{"assigned", issue.FieldAssignee, nil},
}

// parseListFilters reads the request's query filters. sessionEmail and
// hasSession carry the authenticated caller's identity — sourced from
// UserFromContext by the handler, never from the request itself — so
// resolveAssignee can substitute it for the `me` sentinel.
func parseListFilters(r *http.Request, sessionEmail string, hasSession bool) (issue.ListFilters, error) {
	query := r.URL.Query()
	filters := issue.ListFilters{
		ProjectSlug: r.PathValue("slug"),
		Text:        query.Get("query"),
		Sort:        issue.Sort(query.Get("sort")),
	}
	for _, spec := range predicateParams {
		values := query[spec.param]
		if spec.field == issue.FieldAssignee {
			resolved, err := resolveAssignee(values, sessionEmail, hasSession)
			if err != nil {
				return issue.ListFilters{}, err
			}
			values = resolved
		}
		predicates, err := collectPredicate(values, spec.field, "", spec.validate)
		if err != nil {
			return issue.ListFilters{}, err
		}
		filters.Predicates = append(filters.Predicates, predicates...)
	}
	tagPredicates, err := collectTagPredicates(query["tag"])
	if err != nil {
		return issue.ListFilters{}, err
	}
	filters.Predicates = append(filters.Predicates, tagPredicates...)
	if period := query.Get("statsPeriod"); period != "" {
		trailing, err := parseStatsPeriod(period)
		if err != nil {
			return issue.ListFilters{}, err
		}
		filters.Window.Trailing = trailing
	}
	if start := query.Get("start"); start != "" {
		parsed, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return issue.ListFilters{}, fmt.Errorf("invalid start")
		}
		filters.Window.Start = parsed
	}
	if end := query.Get("end"); end != "" {
		parsed, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return issue.ListFilters{}, fmt.Errorf("invalid end")
		}
		filters.Window.End = parsed
	}
	if err := applyCursor(r, &filters); err != nil {
		return issue.ListFilters{}, err
	}
	return filters, nil
}

// parseStatsPeriod accepts only the fixed preset set rather than an arbitrary
// duration, so the sparkline bucket sizing in toIssueResponse has a closed set
// of cases to cover.
func parseStatsPeriod(period string) (time.Duration, error) {
	switch period {
	case "24h":
		return 24 * time.Hour, nil
	case "7d":
		return 7 * 24 * time.Hour, nil
	case "14d":
		return 14 * 24 * time.Hour, nil
	case "30d":
		return 30 * 24 * time.Hour, nil
	case "90d":
		return 90 * 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("invalid statsPeriod %q", period)
	}
}

// collectTagPredicates groups tag=key:value occurrences by key before
// collapsing each key's values into a predicate pair, so `tag=tenant:acme&
// tag=tenant:!beta` produces one included and one excluded predicate for
// "tenant" rather than two single-value predicates.
func collectTagPredicates(pairs []string) ([]issue.Predicate, error) {
	keys := make([]string, 0, len(pairs))
	valuesByKey := make(map[string][]string, len(pairs))
	for _, pair := range pairs {
		key, value, ok := strings.Cut(pair, ":")
		if !ok || key == "" || value == "" {
			return nil, fmt.Errorf("invalid tag filter")
		}
		if _, seen := valuesByKey[key]; !seen {
			keys = append(keys, key)
		}
		valuesByKey[key] = append(valuesByKey[key], value)
	}
	predicates := make([]issue.Predicate, 0, len(keys))
	for _, key := range keys {
		grouped, err := collectPredicate(valuesByKey[key], issue.FieldTag, key, nil)
		if err != nil {
			return nil, err
		}
		predicates = append(predicates, grouped...)
	}
	return predicates, nil
}

// applyCursor parses the limit and cursor query parameters into filters, in
// place. The cursor is authoritative for continuation: it dictates the sort a
// paged request must keep using, regardless of the sort query param, so sort
// validation runs after it is applied.
func applyCursor(r *http.Request, filters *issue.ListFilters) error {
	query := r.URL.Query()
	if raw := query.Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return fmt.Errorf("invalid limit")
		}
		filters.Limit = limit
	}
	if raw := query.Get("cursor"); raw != "" {
		cursor, err := issue.DecodeCursor(raw)
		if err != nil {
			return err
		}
		filters.Cursor = &cursor
		filters.Sort = cursor.Sort
	}
	if filters.Sort != "" && !issue.ValidateSort(filters.Sort) {
		return fmt.Errorf("invalid sort")
	}
	return nil
}

func issueIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid issue id")
		return 0, false
	}
	return id, true
}

func toIssueResponse(source issue.Issue) issueResponse {
	var assignee *assigneeResponse
	if source.Assignee != nil {
		assignee = &assigneeResponse{ID: source.Assignee.ID, Name: source.Assignee.Name, Email: source.Assignee.Email}
	}
	response := issueResponse{
		ID:          source.ID,
		ProjectSlug: source.ProjectSlug,
		Fingerprint: source.Fingerprint,
		Title:       source.Title,
		Culprit:     source.Culprit,
		Level:       source.Level,
		Status:      string(source.Status),
		Assignee:    assignee,
		TimesSeen:   source.TimesSeen,
		UsersSeen:   source.UsersSeen,
		FirstSeen:   source.FirstSeen.Format(time.RFC3339),
		LastSeen:    source.LastSeen.Format(time.RFC3339),
	}
	// A windowless request never populates Issue.Counts (attachSparklines
	// takes that branch only for a non-zero Window), so its emptiness is what
	// distinguishes "legacy" from "windowed" here — not the window itself,
	// which this response type has no access to.
	if len(source.Counts) == 0 {
		response.Counts24h = source.Counts24h[:]
		response.Counts14d = source.Counts14d[:]
		return response
	}
	response.Counts = source.Counts
	periodCount := source.PeriodCount
	response.PeriodCount = &periodCount
	return response
}

func toDetailResponse(source issue.Detail) issueDetailResponse {
	tags := make([]tagFacetResponse, 0, len(source.Tags))
	for _, facet := range source.Tags {
		values := make([]tagValueResponse, 0, len(facet.Values))
		for _, value := range facet.Values {
			values = append(values, tagValueResponse{
				Value:     value.Value,
				TimesSeen: value.TimesSeen,
				FirstSeen: value.FirstSeen.Format(time.RFC3339),
				LastSeen:  value.LastSeen.Format(time.RFC3339),
			})
		}
		tags = append(tags, tagFacetResponse{Key: facet.Key, Values: values})
	}
	response := issueDetailResponse{
		Issue:        toIssueResponse(source.Issue),
		Environments: nonNilStrings(source.Environments),
		Releases:     nonNilStrings(source.Releases),
		Tags:         tags,
	}
	if source.Regression != nil {
		response.Regression = &regressionResponse{At: source.Regression.At.Format(time.RFC3339)}
		if source.Regression.Release != "" {
			response.Regression.Release = &source.Regression.Release
		}
	}
	return response
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

type eventResponse struct {
	EventID               string                         `json:"event_id"`
	Timestamp             string                         `json:"timestamp"`
	ReceivedAt            string                         `json:"received_at"`
	Level                 string                         `json:"level"`
	Environment           string                         `json:"environment"`
	Release               *string                        `json:"release"`
	RequestID             *string                        `json:"request_id"`
	Message               string                         `json:"message"`
	Payload               json.RawMessage                `json:"payload"`
	SymbolicatedException *symbolicatedExceptionResponse `json:"symbolicated_exception"`
}

type symbolicatedPositionResponse struct {
	Function string `json:"function"`
	Filename string `json:"filename"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	InApp    bool   `json:"in_app"`
}

type sourceContextLineResponse struct {
	Line  int    `json:"line"`
	Text  string `json:"text"`
	Focus bool   `json:"focus"`
}

type symbolicatedFrameResponse struct {
	Generated symbolicatedPositionResponse  `json:"generated"`
	Original  *symbolicatedPositionResponse `json:"original"`
	Context   []sourceContextLineResponse   `json:"context"`
}

type symbolicatedExceptionResponse struct {
	Type   string                         `json:"type"`
	Value  string                         `json:"value"`
	Frames []symbolicatedFrameResponse    `json:"frames"`
	Cause  *symbolicatedExceptionResponse `json:"cause"`
}

type eventSummaryResponse struct {
	EventID     string  `json:"event_id"`
	Timestamp   string  `json:"timestamp"`
	ReceivedAt  string  `json:"received_at"`
	Level       string  `json:"level"`
	Environment string  `json:"environment"`
	Release     *string `json:"release"`
	Message     string  `json:"message"`
}

type eventsResponse struct {
	Events     []eventSummaryResponse `json:"events"`
	NextCursor *string                `json:"next_cursor"`
}

type eventDetailResponse struct {
	Event eventResponse `json:"event"`
}

type bucketResponse struct {
	Bucket string `json:"bucket"`
	Count  int64  `json:"count"`
}

type statsResponse struct {
	Period  string           `json:"period"`
	Buckets []bucketResponse `json:"buckets"`
}

type spanResponse struct {
	SpanID       string          `json:"span_id"`
	ParentSpanID *string         `json:"parent_span_id"`
	Name         string          `json:"name"`
	StartTime    string          `json:"start_time"`
	DurationMS   float64         `json:"duration_ms"`
	Status       string          `json:"status"`
	Attributes   json.RawMessage `json:"attributes"`
}

type timelineResponse struct {
	Spans []spanResponse `json:"spans"`
}

func (h *IssueHandler) IssueEvents(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	cursor, err := parseEventCursor(r.URL.Query().Get("cursor"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid cursor")
		return
	}
	limit := 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}
	order := issue.EventOrder(r.URL.Query().Get("order"))
	if order == "" {
		order = issue.EventOrderNewest
	}
	if !issue.ValidateEventOrder(order) {
		writeError(w, http.StatusBadRequest, "invalid order")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	window, err := parseEventWindow(r.URL.Query())
	if err != nil {
		// Opaque, like the sibling ListIssues handler above: the body never
		// echoes the client's own filter values back at it.
		writeError(w, http.StatusBadRequest, "invalid event filters")
		return
	}
	page, err := h.service.IssueEvents(r.Context(), scope, issue.EventQuery{
		IssueID:     id,
		Cursor:      cursor,
		Limit:       limit,
		Order:       order,
		Environment: r.URL.Query().Get("environment"),
		Window:      window,
	})
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("issue events: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]eventSummaryResponse, 0, len(page.Events))
	for _, summary := range page.Events {
		items = append(items, toEventSummaryResponse(summary))
	}
	var nextCursor *string
	if page.NextCursor != nil {
		token := encodeEventCursor(*page.NextCursor)
		nextCursor = &token
	}
	writeJSON(w, http.StatusOK, eventsResponse{Events: items, NextCursor: nextCursor})
}

func (h *IssueHandler) LatestEvent(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	event, err := h.service.LatestEvent(r.Context(), scope, id)
	h.writeEventOrError(w, "latest event", event, err)
}

func (h *IssueHandler) EventByID(w http.ResponseWriter, r *http.Request) {
	eventID, err := uuid.Parse(r.PathValue("event_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid event id")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	event, err := h.service.Event(r.Context(), scope, eventID)
	h.writeEventOrError(w, "event", event, err)
}

func (h *IssueHandler) Timeline(w http.ResponseWriter, r *http.Request) {
	eventID, err := uuid.Parse(r.PathValue("event_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid event id")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	spans, err := h.service.Timeline(r.Context(), scope, eventID)
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("timeline: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]spanResponse, 0, len(spans))
	for _, span := range spans {
		items = append(items, toSpanResponse(span))
	}
	writeJSON(w, http.StatusOK, timelineResponse{Spans: items})
}

func toSpanResponse(source issue.Span) spanResponse {
	attributes := source.Attributes
	if len(attributes) == 0 {
		attributes = json.RawMessage("{}")
	}
	return spanResponse{
		SpanID:       source.SpanID,
		ParentSpanID: source.ParentSpanID,
		Name:         source.Name,
		StartTime:    source.StartTime.Format(time.RFC3339),
		DurationMS:   source.DurationMS,
		Status:       source.Status,
		Attributes:   attributes,
	}
}

func (h *IssueHandler) writeEventOrError(w http.ResponseWriter, operation string, event issue.EventDetail, err error) {
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("%s: %w", operation, err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, eventDetailResponse{Event: toEventResponse(event)})
}

func (h *IssueHandler) IssueStats(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	period := issue.Period(r.URL.Query().Get("period"))
	if period == "" {
		period = issue.Period24h
	}
	if !issue.ValidatePeriod(period) {
		writeError(w, http.StatusBadRequest, "invalid period")
		return
	}
	buckets, err := h.service.IssueStats(r.Context(), scope, id, period)
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("issue stats: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]bucketResponse, 0, len(buckets))
	for _, bucket := range buckets {
		items = append(items, bucketResponse{Bucket: bucket.Bucket.Format(time.RFC3339), Count: bucket.Count})
	}
	writeJSON(w, http.StatusOK, statsResponse{Period: string(period), Buckets: items})
}

// parseEventWindow reads statsPeriod/start/end the same way parseListFilters
// does for the issue stream, scoped down to one issue's own event pager
// rather than the full predicate set — duplicated rather than shared because
// parseListFilters also resolves status/level/assigned/tag predicates this
// endpoint has no use for.
//
// A trailing statsPeriod is deliberately resolved against wall-clock now() on
// every call, here as much as in parseListFilters: this function does not
// itself compute an end time, and window.Trailing is later expanded that way
// by windowClause (internal/storage/postgres/issue_query.go), which resolves
// the issue list's own trailing window identically. Callers that issue
// several requests across one paged walk — the event pager is exactly this —
// and want a window that does not drift as time passes between requests must
// resolve a relative period to absolute start/end themselves before calling,
// the way the web client's eventWindow.ts does; this endpoint has no way to
// tell "a fresh request" from "the next page of an old one" and so cannot
// make that call on the caller's behalf. statsPeriod is nonetheless kept —
// for symmetry with the issue-list endpoint, which accepts it with these
// identical per-request-resolution semantics — even though the dashboard
// deliberately never sends it, only the absolute bounds it froze itself.
func parseEventWindow(query url.Values) (issue.Window, error) {
	var window issue.Window
	if period := query.Get("statsPeriod"); period != "" {
		trailing, err := parseStatsPeriod(period)
		if err != nil {
			return issue.Window{}, err
		}
		window.Trailing = trailing
	}
	if start := query.Get("start"); start != "" {
		parsed, err := time.Parse(time.RFC3339, start)
		if err != nil {
			return issue.Window{}, fmt.Errorf("invalid start")
		}
		window.Start = parsed
	}
	if end := query.Get("end"); end != "" {
		parsed, err := time.Parse(time.RFC3339, end)
		if err != nil {
			return issue.Window{}, fmt.Errorf("invalid end")
		}
		window.End = parsed
	}
	if err := window.Validate(); err != nil {
		return issue.Window{}, err
	}
	return window, nil
}

// parseEventCursor decodes an opaque "received_at|event_id" token; empty input
// means the first page.
func parseEventCursor(token string) (*issue.EventCursor, error) {
	if token == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return nil, err
	}
	timestamp, rawID, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil, fmt.Errorf("malformed event cursor")
	}
	at, err := time.Parse(time.RFC3339Nano, timestamp)
	if err != nil {
		return nil, err
	}
	eventID, err := uuid.Parse(rawID)
	if err != nil {
		return nil, err
	}
	return &issue.EventCursor{ReceivedAt: at, EventID: eventID}, nil
}

func encodeEventCursor(cursor issue.EventCursor) string {
	token := cursor.ReceivedAt.Format(time.RFC3339Nano) + "|" + cursor.EventID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(token))
}

func toEventSummaryResponse(source issue.EventSummary) eventSummaryResponse {
	return eventSummaryResponse{
		EventID:     source.EventID.String(),
		Timestamp:   source.Timestamp.Format(time.RFC3339),
		ReceivedAt:  source.ReceivedAt.Format(time.RFC3339),
		Level:       source.Level,
		Environment: source.Environment,
		Release:     source.Release,
		Message:     source.Message,
	}
}

func toEventResponse(source issue.EventDetail) eventResponse {
	payload := source.Payload
	if len(payload) == 0 {
		payload = json.RawMessage("null")
	}
	return eventResponse{
		EventID:               source.EventID.String(),
		Timestamp:             source.Timestamp.Format(time.RFC3339),
		ReceivedAt:            source.ReceivedAt.Format(time.RFC3339),
		Level:                 source.Level,
		Environment:           source.Environment,
		Release:               source.Release,
		RequestID:             source.RequestID,
		Message:               source.Message,
		Payload:               payload,
		SymbolicatedException: toSymbolicatedExceptionResponse(source.SymbolicatedException),
	}
}

func toSymbolicatedExceptionResponse(source *sourcemap.Exception) *symbolicatedExceptionResponse {
	if source == nil {
		return nil
	}
	frames := make([]symbolicatedFrameResponse, 0, len(source.Frames))
	for _, frame := range source.Frames {
		frames = append(frames, toSymbolicatedFrameResponse(frame))
	}
	return &symbolicatedExceptionResponse{
		Type:   source.Type,
		Value:  source.Value,
		Frames: frames,
		Cause:  toSymbolicatedExceptionResponse(source.Cause),
	}
}

func toSymbolicatedFrameResponse(source sourcemap.Frame) symbolicatedFrameResponse {
	contextLines := make([]sourceContextLineResponse, 0, len(source.Context))
	for _, line := range source.Context {
		contextLines = append(contextLines, sourceContextLineResponse{Line: line.Line, Text: line.Text, Focus: line.Focus})
	}
	return symbolicatedFrameResponse{
		Generated: toSymbolicatedPositionResponse(source.Generated),
		Original:  toSymbolicatedPositionPointer(source.Original),
		Context:   contextLines,
	}
}

func toSymbolicatedPositionPointer(source *sourcemap.Position) *symbolicatedPositionResponse {
	if source == nil {
		return nil
	}
	position := toSymbolicatedPositionResponse(*source)
	return &position
}

func toSymbolicatedPositionResponse(source sourcemap.Position) symbolicatedPositionResponse {
	return symbolicatedPositionResponse{
		Function: source.Function,
		Filename: source.Filename,
		Line:     source.Line,
		Column:   source.Column,
		InApp:    source.InApp,
	}
}
