//go:build integration

package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/ingest"
)

func (e *issueAPIEnv) patch(id int64, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPatch, "/api/v1/issues/"+strconv.FormatInt(id, 10), bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("id", strconv.FormatInt(id, 10))
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	e.handler.PatchIssue(recorder, request)
	return recorder
}

func (e *issueAPIEnv) issueIDByTitle(title string) int64 {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM issues WHERE title = $1`, title).Scan(&id); err != nil {
		e.t.Fatalf("find issue %s: %v", title, err)
	}
	return id
}

func TestPatchIssueIgnoresUntilMoreEventsAndReportsProgress(t *testing.T) {
	env := newIssueAPIEnv(t)
	env.seedIssue("Noisy", "error", nil)
	id := env.issueIDByTitle("Noisy")

	recorder := env.patch(id, `{"status":"ignored","ignore_until":{"events":100}}`)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Issue map[string]json.RawMessage `json:"issue"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got := string(body.Issue["status"]); got != `"ignored"` {
		t.Fatalf("issue.status = %s, want \"ignored\"", got)
	}
	if got, want := string(body.Issue["ignore_until"]), `{"kind":"events","until":null,"count":100,"so_far":0}`; got != want {
		t.Fatalf("issue.ignore_until = %s, want %s", got, want)
	}

	list := env.get("/api/v1/projects/demo/issues?status=ignored")
	var listed struct {
		Issues []map[string]json.RawMessage `json:"issues"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil || len(listed.Issues) != 1 {
		t.Fatalf("list = %s (%v)", list.Body.String(), err)
	}
	if got, want := string(listed.Issues[0]["ignore_until"]), `{"kind":"events","until":null,"count":100,"so_far":0}`; got != want {
		t.Fatalf("issues[0].ignore_until = %s, want %s", got, want)
	}
}

func TestPatchIssueRefusesAUsersTargetPastTheCapAndChangesNothing(t *testing.T) {
	env := newIssueAPIEnv(t)
	env.seedIssue("Crowded", "error", nil)
	id := env.issueIDByTitle("Crowded")
	if _, err := env.pool.Exec(env.ctx, `UPDATE issues SET users_seen = $2 WHERE id = $1`, id, ingest.MaxUsersPerIssue-50); err != nil {
		t.Fatalf("set users_seen: %v", err)
	}

	recorder := env.patch(id, `{"status":"ignored","ignore_until":{"users":100},"assignee_id":`+strconv.FormatInt(env.sessionUser.ID, 10)+`}`)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body %s)", recorder.Code, recorder.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil || !strings.HasPrefix(body["error"], "invalid ignore_until.users:") {
		t.Fatalf("body = %s, want an error naming ignore_until.users", recorder.Body.String())
	}
	var (
		status   string
		assignee *int64
	)
	if err := env.pool.QueryRow(env.ctx, `SELECT status::text, assignee_id FROM issues WHERE id = $1`, id).Scan(&status, &assignee); err != nil {
		t.Fatalf("read issue: %v", err)
	}
	if status != "unresolved" || assignee != nil {
		t.Fatalf("after a refused ignore, status = %q, assignee = %v; want unchanged", status, assignee)
	}
}
