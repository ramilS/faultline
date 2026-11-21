package api

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
)

func serveDigest(service *stubIssueService, target string) *httptest.ResponseRecorder {
	handler := NewIssueHandler(service, Options{})
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, target, "")
	request.SetPathValue("id", "5")
	handler.IssueDigest(recorder, request)
	return recorder
}

func digestService() *stubIssueService {
	return &stubIssueService{
		detail: issue.Detail{Issue: issue.Issue{ID: 5, ProjectSlug: "web", Title: "TypeError: boom", TimesSeen: 3}},
		latest: issue.EventDetail{
			EventSummary: issue.EventSummary{EventID: uuid.MustParse("11111111-1111-4111-8111-111111111111"), Message: "latest boom"},
			IssueID:      5,
		},
	}
}

func TestIssueDigestRendersTheIssueWithItsLatestEvent(t *testing.T) {
	recorder := serveDigest(digestService(), "/api/v1/issues/5/digest")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body)
	}
	if got := recorder.Header().Get("Content-Type"); got != "text/markdown; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := recorder.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
	if got := recorder.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{"# Issue 5 · web\n", "title: TypeError: boom\n", "## Event 11111111-1111-4111-8111-111111111111\n", "message: latest boom\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("digest lacks %q:\n%s", want, body)
		}
	}
}

func TestIssueDigestRendersTheRequestedEvent(t *testing.T) {
	service := digestService()

	recorder := serveDigest(service, "/api/v1/issues/5/digest?event=11111111-1111-4111-8111-111111111111")

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !reflect.DeepEqual(service.lastEventScope, access.Unrestricted()) || !reflect.DeepEqual(service.lastLatestEventScope, access.Scope{}) {
		t.Fatalf("digest read the latest event instead of the requested one")
	}
}

func TestIssueDigestRefusesAnEventOfAnotherIssue(t *testing.T) {
	service := digestService()
	service.latest.IssueID = 6

	recorder := serveDigest(service, "/api/v1/issues/5/digest?event=11111111-1111-4111-8111-111111111111")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
	if strings.Contains(recorder.Body.String(), "latest boom") {
		t.Fatalf("digest leaked another issue's event")
	}
}

func TestIssueDigestRejectsAMalformedEventID(t *testing.T) {
	recorder := serveDigest(digestService(), "/api/v1/issues/5/digest?event=nope")

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestIssueDigestOfAnIssueOutsideScopeIsNotFound(t *testing.T) {
	service := digestService()
	service.err = issue.ErrNotFound

	recorder := serveDigest(service, "/api/v1/issues/5/digest")

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}
