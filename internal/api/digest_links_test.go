package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/digestlink"
	"github.com/ramilS/faultline/internal/issue"
)

type stubDigestLinkService struct {
	raw          string
	expiresAt    time.Time
	createErr    error
	createdTTL   string
	createdBy    *int64
	createdIssue int64
	target       digestlink.Target
	resolveErr   error
	resolvedRaw  string
	revokedIssue int64
	revokeErr    error
}

func (s *stubDigestLinkService) Create(_ context.Context, _ access.Scope, issueID int64, createdBy *int64, ttl string) (string, time.Time, error) {
	s.createdIssue, s.createdBy, s.createdTTL = issueID, createdBy, ttl
	return s.raw, s.expiresAt, s.createErr
}

func (s *stubDigestLinkService) Resolve(_ context.Context, raw string) (digestlink.Target, error) {
	s.resolvedRaw = raw
	return s.target, s.resolveErr
}

func (s *stubDigestLinkService) RevokeAll(_ context.Context, _ access.Scope, issueID int64) error {
	s.revokedIssue = issueID
	return s.revokeErr
}

func createLinkRequest(body string) *http.Request {
	request := newIssueRequest(http.MethodPost, "/api/v1/issues/5/digest-links", body)
	request.SetPathValue("id", "5")
	return request.WithContext(ContextWithUser(request.Context(), auth.User{ID: 7}))
}

func TestCreateDigestLinkReturnsAnAbsoluteURLOnce(t *testing.T) {
	links := &stubDigestLinkService{raw: "fl_dig_secret", expiresAt: time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)}
	handler := NewDigestLinkHandler(links, digestService(), "", Options{})
	recorder := httptest.NewRecorder()

	handler.Create(recorder, createLinkRequest(`{"ttl":"1h"}`))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := map[string]string{"url": "http://example.test/api/v1/digest-links/fl_dig_secret", "expires_at": "2026-11-21T10:00:00Z"}
	if !reflect.DeepEqual(body, want) {
		t.Fatalf("body = %v, want %v", body, want)
	}
	if links.createdIssue != 5 || links.createdTTL != "1h" || links.createdBy == nil || *links.createdBy != 7 {
		t.Fatalf("created issue=%d ttl=%q by=%v", links.createdIssue, links.createdTTL, links.createdBy)
	}
}

func TestCreateDigestLinkPrefersTheConfiguredPublicURL(t *testing.T) {
	links := &stubDigestLinkService{raw: "fl_dig_secret"}
	handler := NewDigestLinkHandler(links, digestService(), "https://faultline.example.com/", Options{})
	recorder := httptest.NewRecorder()

	handler.Create(recorder, createLinkRequest(`{"ttl":"7d"}`))

	if !strings.Contains(recorder.Body.String(), `"url":"https://faultline.example.com/api/v1/digest-links/fl_dig_secret"`) {
		t.Fatalf("body = %s", recorder.Body)
	}
}

func TestCreateDigestLinkMapsServiceErrors(t *testing.T) {
	for name, testCase := range map[string]struct {
		err  error
		want int
	}{
		"invalid ttl":    {digestlink.ErrInvalidTTL, http.StatusBadRequest},
		"issue in scope": {digestlink.ErrNotFound, http.StatusNotFound},
		"storage":        {errors.New("boom"), http.StatusInternalServerError},
	} {
		handler := NewDigestLinkHandler(&stubDigestLinkService{createErr: testCase.err}, digestService(), "", Options{})
		recorder := httptest.NewRecorder()

		handler.Create(recorder, createLinkRequest(`{"ttl":"30d"}`))

		if recorder.Code != testCase.want {
			t.Errorf("%s: status = %d, want %d", name, recorder.Code, testCase.want)
		}
	}
}

func TestCreateDigestLinkRejectsAnUnknownField(t *testing.T) {
	links := &stubDigestLinkService{}
	handler := NewDigestLinkHandler(links, digestService(), "", Options{})
	recorder := httptest.NewRecorder()

	handler.Create(recorder, createLinkRequest(`{"ttl":"1h","issue_id":9}`))

	if recorder.Code != http.StatusBadRequest || links.createdTTL != "" {
		t.Fatalf("status = %d, service called = %v", recorder.Code, links.createdTTL != "")
	}
}

func TestRevokeDigestLinks(t *testing.T) {
	for name, testCase := range map[string]struct {
		err  error
		want int
	}{
		"revoked":   {nil, http.StatusNoContent},
		"not found": {digestlink.ErrNotFound, http.StatusNotFound},
	} {
		links := &stubDigestLinkService{revokeErr: testCase.err}
		handler := NewDigestLinkHandler(links, digestService(), "", Options{})
		recorder := httptest.NewRecorder()
		request := newIssueRequest(http.MethodDelete, "/api/v1/issues/5/digest-links", "")
		request.SetPathValue("id", "5")

		handler.RevokeAll(recorder, request)

		if recorder.Code != testCase.want || links.revokedIssue != 5 {
			t.Errorf("%s: status = %d, revoked issue %d", name, recorder.Code, links.revokedIssue)
		}
	}
}

func serveLinkDigest(links *stubDigestLinkService, issues *stubIssueService, target string) *httptest.ResponseRecorder {
	handler := NewDigestLinkHandler(links, issues, "", Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, target, http.NoBody)
	request.SetPathValue("token", "fl_dig_secret")
	handler.Digest(recorder, request)
	return recorder
}

func TestDigestLinkServesTheIssueDigestWithinTheLinksProject(t *testing.T) {
	issues := digestService()
	links := &stubDigestLinkService{target: digestlink.Target{IssueID: 5, ProjectID: 9}}

	recorder := serveLinkDigest(links, issues, "/api/v1/digest-links/fl_dig_secret")

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "# Issue 5 · web\n") {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if links.resolvedRaw != "fl_dig_secret" {
		t.Errorf("resolved %q", links.resolvedRaw)
	}
	if !reflect.DeepEqual(issues.lastDetailScope, access.Limited([]int64{9})) {
		t.Errorf("detail scope = %+v, want project 9 only", issues.lastDetailScope)
	}
	for header, want := range map[string]string{
		"Content-Type":           "text/markdown; charset=utf-8",
		"X-Content-Type-Options": "nosniff",
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := recorder.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestDigestLinkThatResolvesToNothingIsAPlainNotFound(t *testing.T) {
	merged := int64(8)
	for name, testCase := range map[string]struct {
		links  *stubDigestLinkService
		issues func() *stubIssueService
		target string
	}{
		"unknown or expired": {
			links:  &stubDigestLinkService{resolveErr: digestlink.ErrNotFound},
			issues: digestService,
			target: "/api/v1/digest-links/fl_dig_secret",
		},
		"merged issue": {
			links: &stubDigestLinkService{target: digestlink.Target{IssueID: 5, ProjectID: 9}},
			issues: func() *stubIssueService {
				service := digestService()
				service.detail.Issue.MergedIntoID = &merged
				return service
			},
			target: "/api/v1/digest-links/fl_dig_secret",
		},
		"issue gone": {
			links: &stubDigestLinkService{target: digestlink.Target{IssueID: 5, ProjectID: 9}},
			issues: func() *stubIssueService {
				service := digestService()
				service.err = issue.ErrNotFound
				return service
			},
			target: "/api/v1/digest-links/fl_dig_secret",
		},
		"event of another issue": {
			links: &stubDigestLinkService{target: digestlink.Target{IssueID: 5, ProjectID: 9}},
			issues: func() *stubIssueService {
				service := digestService()
				service.latest.IssueID = 6
				return service
			},
			target: "/api/v1/digest-links/fl_dig_secret?event=11111111-1111-4111-8111-111111111111",
		},
	} {
		recorder := serveLinkDigest(testCase.links, testCase.issues(), testCase.target)

		if recorder.Code != http.StatusNotFound || recorder.Body.String() != "{\"error\":\"not found\"}\n" {
			t.Errorf("%s: status = %d, body = %q", name, recorder.Code, recorder.Body)
		}
	}
}
