//go:build integration

package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/auth"
)

func (e *issueAPIEnv) postViewAs(user auth.User, id int64) *httptest.ResponseRecorder {
	e.t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/issues/"+strconv.FormatInt(id, 10)+"/view", http.NoBody)
	request.SetPathValue("id", strconv.FormatInt(id, 10))
	request = request.WithContext(api.ContextWithUser(request.Context(), user))
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	e.handler.RecordView(recorder, request)
	return recorder
}

func (e *issueAPIEnv) issueID(title string) int64 {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM issues WHERE title = $1`, title).Scan(&id); err != nil {
		e.t.Fatalf("issue id for %s: %v", title, err)
	}
	return id
}

func rawUnseenByTitle(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status = %d body %s", recorder.Code, recorder.Body.String())
	}
	var payload struct {
		Issues []map[string]any `json:"issues"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode: %v (body %s)", err, recorder.Body.String())
	}
	out := make(map[string]any, len(payload.Issues))
	for _, item := range payload.Issues {
		out[item["title"].(string)] = item["unseen"]
	}
	return out
}

func TestIssueViewMarksTheListSeenOnlyForTheViewer(t *testing.T) {
	env := newIssueAPIEnv(t)
	other := env.seedUser("other@example.com", "Other")
	env.seedIssue("Opened", "error", nil)
	env.seedIssue("Untouched", "error", nil)

	if recorder := env.postViewAs(env.sessionUser, env.issueID("Opened")); recorder.Code != http.StatusNoContent {
		t.Fatalf("view status = %d body %s", recorder.Code, recorder.Body.String())
	}

	caller := rawUnseenByTitle(t, env.get("/api/v1/projects/demo/issues"))
	if caller["Opened"] != false || caller["Untouched"] != true {
		t.Fatalf("caller unseen = %v, want Opened=false Untouched=true", caller)
	}
	stranger := rawUnseenByTitle(t, env.getAs(other, "/api/v1/projects/demo/issues"))
	if stranger["Opened"] != true || stranger["Untouched"] != true {
		t.Fatalf("other member unseen = %v, want both true", stranger)
	}
}

func TestIssueListOnlyUnseenParamCombinesWithLevel(t *testing.T) {
	env := newIssueAPIEnv(t)
	env.seedIssue("Seen error", "error", nil)
	env.seedIssue("Unseen error", "error", nil)
	env.seedIssue("Unseen warning", "warning", nil)
	if recorder := env.postViewAs(env.sessionUser, env.issueID("Seen error")); recorder.Code != http.StatusNoContent {
		t.Fatalf("view status = %d", recorder.Code)
	}

	got := rawUnseenByTitle(t, env.get("/api/v1/projects/demo/issues?level=error&unseen=true"))
	if len(got) != 1 || got["Unseen error"] != true {
		t.Fatalf("unseen = %v, want just Unseen error", got)
	}
}

func TestIssueViewAnswersNotFound(t *testing.T) {
	env := newIssueAPIEnv(t)
	env.seedIssue("Source", "error", nil)
	env.seedIssue("Target", "error", nil)
	source := env.issueID("Source")
	if _, err := env.pool.Exec(env.ctx, `UPDATE issues SET merged_into_id = $1 WHERE id = $2`, env.issueID("Target"), source); err != nil {
		t.Fatalf("tombstone source: %v", err)
	}

	for name, id := range map[string]int64{"merged source": source, "missing issue": source + 1000} {
		t.Run(name, func(t *testing.T) {
			if recorder := env.postViewAs(env.sessionUser, id); recorder.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", recorder.Code, recorder.Body.String())
			}
		})
	}
	var rows int
	if err := env.pool.QueryRow(env.ctx, `SELECT count(*) FROM issue_views`).Scan(&rows); err != nil {
		t.Fatalf("count views: %v", err)
	}
	if rows != 0 {
		t.Fatalf("issue_views rows = %d, want 0", rows)
	}
}
