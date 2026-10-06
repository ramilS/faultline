//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/issue"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// listIssuesResponse mirrors the JSON shape api.listIssuesResponse produces,
// trimmed to the fields these tests assert on. It can't reference the
// unexported type directly from this external test package.
type listIssuesResponse struct {
	Issues []struct {
		Title       string  `json:"title"`
		Counts      []int64 `json:"counts"`
		PeriodCount *int64  `json:"period_count"`
		Counts24h   []int64 `json:"counts_24h"`
	} `json:"issues"`
}

// issueAPIEnv migrates a fresh TimescaleDB, seeds one project and one
// "current user" session, and wires a real IssueHandler over it so requests
// exercise the full parse-filter-and-query path, including assigned=me
// resolution from the session context.
type issueAPIEnv struct {
	t           *testing.T
	ctx         context.Context
	pool        *pgxpool.Pool
	projectID   int64
	handler     *api.IssueHandler
	sessionUser auth.User
	// sessionUserID is a convenience alias for &sessionUser.ID, so tests can
	// pass it directly as an assignee to seedIssue.
	sessionUserID *int64
}

func newIssueAPIEnv(t *testing.T) *issueAPIEnv {
	t.Helper()
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	var projectID int64
	if err := pool.QueryRow(ctx, `INSERT INTO projects (slug,name,platform) VALUES ('demo','Demo','javascript') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	service := issue.NewService(storagepostgres.NewIssueRepository(pool))
	handler := api.NewIssueHandler(service, api.Options{})

	environment := &issueAPIEnv{t: t, ctx: ctx, pool: pool, projectID: projectID, handler: handler}
	environment.sessionUser = environment.seedUser("caller@example.com", "Caller")
	environment.sessionUserID = &environment.sessionUser.ID
	return environment
}

func (e *issueAPIEnv) seedUser(email, name string) auth.User {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, `
		INSERT INTO users (email, name, password_hash) VALUES ($1, $2, 'x') RETURNING id
	`, email, name).Scan(&id); err != nil {
		e.t.Fatalf("seed user %s: %v", email, err)
	}
	return auth.User{ID: id, Email: email, Name: name}
}

// seedIssue persists one issue via the production batch writer and, if
// assigneeID is non-nil, assigns it directly through a SQL UPDATE — the
// service's Assign path is exercised elsewhere; here the assignment only
// needs to exist for the list filter under test to find.
func (e *issueAPIEnv) seedIssue(title, level string, assigneeID *int64) {
	e.t.Helper()
	fingerprint := fill64(title)
	now := time.Now().UTC()
	sink := storagepostgres.NewEventSink(e.pool)
	batch := storage.Batch{
		ProjectID: e.projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: title, Level: level, Count: 1, FirstSeen: now, LastSeen: now,
		}},
		Events: []storage.EventRow{{
			ProjectID: e.projectID, EventID: uuid.New(), Timestamp: now, ReceivedAt: now,
			Level: level, Message: title, Fingerprint: fingerprint,
			Payload: json.RawMessage(`{"message":"` + title + `"}`),
		}},
	}
	if err := sink.PersistBatch(e.ctx, batch); err != nil {
		e.t.Fatalf("seed issue %s: %v", title, err)
	}
	if assigneeID == nil {
		return
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE issues SET assignee_id = $1 WHERE fingerprint = $2`, *assigneeID, fingerprint); err != nil {
		e.t.Fatalf("assign issue %s: %v", title, err)
	}
}

// get issues a list request as the environment's session user, the same way
// RequireSession would have populated the context in production.
func (e *issueAPIEnv) get(path string) *httptest.ResponseRecorder {
	return e.getAs(e.sessionUser, path)
}

func (e *issueAPIEnv) getAs(user auth.User, path string) *httptest.ResponseRecorder {
	e.t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request.SetPathValue("slug", "demo")
	request = request.WithContext(api.ContextWithUser(request.Context(), user))
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	e.handler.ListIssues(recorder, request)
	return recorder
}

// getWithoutSession bypasses ContextWithUser entirely, modeling a request
// that reaches the handler with no authenticated identity at all.
func (e *issueAPIEnv) getWithoutSession(path string) *httptest.ResponseRecorder {
	e.t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	request.SetPathValue("slug", "demo")
	e.handler.ListIssues(recorder, request)
	return recorder
}

func titles(response listIssuesResponse) []string {
	out := make([]string, 0, len(response.Issues))
	for _, item := range response.Issues {
		out = append(out, item.Title)
	}
	return out
}

func decodeListIssues(t *testing.T, recorder *httptest.ResponseRecorder) listIssuesResponse {
	t.Helper()
	var payload listIssuesResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (body %s)", err, recorder.Body.String())
	}
	return payload
}

func TestListIssuesFiltersByLevelAndAssignee(t *testing.T) {
	environment := newIssueAPIEnv(t)
	environment.seedIssue("alpha", "error", environment.sessionUserID)
	environment.seedIssue("beta", "warning", nil)

	response := environment.get("/api/v1/projects/demo/issues?level=error&assigned=me")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	payload := decodeListIssues(t, response)
	if len(payload.Issues) != 1 || payload.Issues[0].Title != "alpha" {
		t.Fatalf("got %v, want only alpha", titles(payload))
	}
}

// TestListIssuesCountsShapeFollowsWindowPresence closes the gap between
// toIssueResponse's shape switch (pinned at the marshal level) and
// Issue.Counts population (pinned at the repository level): until now nothing
// asserted the two actually meet over the wire — this file's decode struct
// only ever looked at `title`.
func TestListIssuesCountsShapeFollowsWindowPresence(t *testing.T) {
	environment := newIssueAPIEnv(t)
	environment.seedIssue("alpha", "error", nil)
	if _, err := environment.pool.Exec(environment.ctx, `CALL refresh_continuous_aggregate('issue_counts_hourly', NULL, NULL)`); err != nil {
		t.Fatalf("refresh issue_counts_hourly: %v", err)
	}

	windowed := decodeListIssues(t, environment.get("/api/v1/projects/demo/issues?statsPeriod=24h"))
	if len(windowed.Issues) != 1 || len(windowed.Issues[0].Counts) != 24 || windowed.Issues[0].PeriodCount == nil || windowed.Issues[0].Counts24h != nil {
		t.Fatalf("windowed issue = %+v, want 24 counts, a period_count, and no counts_24h", windowed.Issues[0])
	}

	windowless := decodeListIssues(t, environment.get("/api/v1/projects/demo/issues"))
	if len(windowless.Issues) != 1 || len(windowless.Issues[0].Counts24h) != 24 || windowless.Issues[0].Counts != nil || windowless.Issues[0].PeriodCount != nil {
		t.Fatalf("windowless issue = %+v, want 24 counts_24h and no counts/period_count", windowless.Issues[0])
	}
}

// TestListIssuesAssignedMeIsScopedToCallerSession is the end-to-end pin for
// this task's security requirement: two different sessions issuing the
// identical assigned=me request must each see only their own assignments,
// never the other session's.
func TestListIssuesAssignedMeIsScopedToCallerSession(t *testing.T) {
	environment := newIssueAPIEnv(t)
	other := environment.seedUser("other@example.com", "Other")

	environment.seedIssue("mine", "error", environment.sessionUserID)
	environment.seedIssue("theirs", "error", &other.ID)

	asCaller := environment.get("/api/v1/projects/demo/issues?assigned=me")
	if asCaller.Code != http.StatusOK {
		t.Fatalf("caller status = %d, want 200", asCaller.Code)
	}
	callerPayload := decodeListIssues(t, asCaller)
	if len(callerPayload.Issues) != 1 || callerPayload.Issues[0].Title != "mine" {
		t.Fatalf("caller got %v, want only mine", titles(callerPayload))
	}

	asOther := environment.getAs(other, "/api/v1/projects/demo/issues?assigned=me")
	if asOther.Code != http.StatusOK {
		t.Fatalf("other status = %d, want 200", asOther.Code)
	}
	otherPayload := decodeListIssues(t, asOther)
	if len(otherPayload.Issues) != 1 || otherPayload.Issues[0].Title != "theirs" {
		t.Fatalf("other got %v, want only theirs", titles(otherPayload))
	}
}

func TestListIssuesAssignedNoneReturnsUnassigned(t *testing.T) {
	environment := newIssueAPIEnv(t)
	environment.seedIssue("assigned", "error", environment.sessionUserID)
	environment.seedIssue("unassigned", "error", nil)

	response := environment.get("/api/v1/projects/demo/issues?assigned=none")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	payload := decodeListIssues(t, response)
	if len(payload.Issues) != 1 || payload.Issues[0].Title != "unassigned" {
		t.Fatalf("got %v, want only unassigned", titles(payload))
	}
}

func TestListIssuesAssignedExplicitEmailOtherThanCaller(t *testing.T) {
	environment := newIssueAPIEnv(t)
	other := environment.seedUser("other@example.com", "Other")
	environment.seedIssue("mine", "error", environment.sessionUserID)
	environment.seedIssue("theirs", "error", &other.ID)

	response := environment.get("/api/v1/projects/demo/issues?assigned=other@example.com")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	payload := decodeListIssues(t, response)
	if len(payload.Issues) != 1 || payload.Issues[0].Title != "theirs" {
		t.Fatalf("got %v, want only theirs", titles(payload))
	}
}

// TestListIssuesNegatedAssignedMeIncludesUnassigned confirms assigneeClause's
// NULL guard applies through the full HTTP path too: "not assigned to me"
// must still surface unassigned issues, not just other people's.
func TestListIssuesNegatedAssignedMeIncludesUnassigned(t *testing.T) {
	environment := newIssueAPIEnv(t)
	environment.seedIssue("mine", "error", environment.sessionUserID)
	environment.seedIssue("unassigned", "error", nil)

	response := environment.get("/api/v1/projects/demo/issues?assigned=%21me")
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	payload := decodeListIssues(t, response)
	if len(payload.Issues) != 1 || payload.Issues[0].Title != "unassigned" {
		t.Fatalf("got %v, want only unassigned", titles(payload))
	}
}

func TestListIssuesAssignedMeFailsClosedWithoutSession(t *testing.T) {
	environment := newIssueAPIEnv(t)
	environment.seedIssue("alpha", "error", environment.sessionUserID)

	response := environment.getWithoutSession("/api/v1/projects/demo/issues?assigned=me")
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestIssueHandlerListAndTriageEndToEnd(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var projectID int64
	if err := pool.QueryRow(ctx, `INSERT INTO projects (slug,name,platform) VALUES ('web','Web','javascript') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	now := time.Now().UTC()
	release := "1.0.0"
	sink := storagepostgres.NewEventSink(pool)
	if err := sink.PersistBatch(ctx, storage.Batch{
		ProjectID: projectID, Releases: []string{release},
		Issues: []storage.IssueDelta{{Fingerprint: fill64("e5"), Title: "EndToEnd", Level: "error", Count: 1, FirstSeen: now, LastSeen: now,
			Tags: []storage.TagDelta{{Key: "environment", Value: "production", Count: 1, FirstSeen: now, LastSeen: now}}}},
		Events: []storage.EventRow{{ProjectID: projectID, EventID: uuid.New(), Timestamp: now, ReceivedAt: now, Level: "error",
			Environment: "production", Message: "EndToEnd", Release: &release, Fingerprint: fill64("e5"), Payload: json.RawMessage(`{"message":"EndToEnd"}`)}},
	}); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	service := issue.NewService(storagepostgres.NewIssueRepository(pool))
	handler := api.NewIssueHandler(service, api.Options{})

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/issues", http.NoBody)
	listRequest.SetPathValue("slug", "web")
	listRequest = listRequest.WithContext(access.ContextWithScope(listRequest.Context(), access.Unrestricted()))
	handler.ListIssues(listRecorder, listRequest)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list status = %d", listRecorder.Code)
	}
	var listBody struct {
		Issues []struct {
			ID int64 `json:"id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listBody); err != nil || len(listBody.Issues) != 1 {
		t.Fatalf("list body = %s err = %v", listRecorder.Body.String(), err)
	}
	id := listBody.Issues[0].ID

	patchRecorder := httptest.NewRecorder()
	patchRequest := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/1", bytes.NewBufferString(`{"status":"resolved"}`))
	patchRequest.Header.Set("Content-Type", "application/json")
	patchRequest.SetPathValue("id", strconv.FormatInt(id, 10))
	patchRequest = patchRequest.WithContext(access.ContextWithScope(patchRequest.Context(), access.Unrestricted()))
	handler.PatchIssue(patchRecorder, patchRequest)
	if patchRecorder.Code != http.StatusOK {
		t.Fatalf("patch status = %d body %s", patchRecorder.Code, patchRecorder.Body.String())
	}
}

func fill64(seed string) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = '0'
	}
	copy(out, seed)
	return string(out)
}
