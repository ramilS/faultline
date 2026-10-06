package api

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
)

func TestRequireSessionRejectsMissingMalformedDuplicateAndInvalidCookies(t *testing.T) {
	tests := []struct {
		name          string
		cookieHeaders []string
		authErr       error
		wantCalls     int
	}{
		{"missing", nil, nil, 0},
		{"malformed value", []string{SessionCookieName + "=\"unterminated"}, nil, 0},
		{"missing equals before session", []string{SessionCookieName + "; " + SessionCookieName + "=token"}, nil, 0},
		{"malformed unrelated before session", []string{"unrelated; " + SessionCookieName + "=token"}, nil, 0},
		{"duplicate", []string{SessionCookieName + "=one; " + SessionCookieName + "=two"}, nil, 0},
		{"duplicate across headers", []string{SessionCookieName + "=one", SessionCookieName + "=two"}, nil, 0},
		{"expired or revoked", []string{SessionCookieName + "=token"}, auth.ErrInvalidSession, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &authServiceStub{authErr: test.authErr}
			handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
			reached := false
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
			for _, cookie := range test.cookieHeaders {
				request.Header.Add("Cookie", cookie)
			}

			handler.RequireSession(next).ServeHTTP(recorder, request)

			assertJSONError(t, recorder, http.StatusUnauthorized, "authentication required")
			if reached || len(service.authTokens) != test.wantCalls {
				t.Fatalf("reached/calls = %v/%d", reached, len(service.authTokens))
			}
		})
	}
}

func TestRequireSessionAcceptsOneStrictlyValidCookie(t *testing.T) {
	service := &authServiceStub{authUser: auth.User{ID: 11}}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	request.Header.Set("Cookie", "theme=dark; "+SessionCookieName+"=token")

	handler.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || len(service.authTokens) != 1 || service.authTokens[0] != "token" {
		t.Fatalf("status/tokens = %d/%#v", recorder.Code, service.authTokens)
	}
}

func TestRequireSessionAcceptsQuotedUnrelatedCookieValue(t *testing.T) {
	service := &authServiceStub{authUser: auth.User{ID: 11}}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	request.Header.Set("Cookie", `theme="dark"; `+SessionCookieName+"=token")

	handler.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || len(service.authTokens) != 1 {
		t.Fatalf("status/tokens = %d/%#v", recorder.Code, service.authTokens)
	}
}

func TestRequireSessionRejectsMalformedQuotedCookieValues(t *testing.T) {
	for _, cookie := range []string{
		`theme="dark; ` + SessionCookieName + `=token`,
		`theme="dark"extra; ` + SessionCookieName + `=token`,
		`theme="dark space"; ` + SessionCookieName + `=token`,
		`theme="dark,light"; ` + SessionCookieName + `=token`,
		`theme="dark\light"; ` + SessionCookieName + `=token`,
		`theme="dark\"light"; ` + SessionCookieName + `=token`,
	} {
		t.Run(cookie, func(t *testing.T) {
			service := &authServiceStub{authUser: auth.User{ID: 11}}
			handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
			request.Header.Set("Cookie", cookie)

			handler.RequireSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("protected handler reached")
			})).ServeHTTP(recorder, request)

			assertJSONError(t, recorder, http.StatusUnauthorized, "authentication required")
			if len(service.authTokens) != 0 {
				t.Fatalf("authenticate calls = %d, want 0", len(service.authTokens))
			}
		})
	}
}

func TestRequireSessionRejectsQuotedSessionToken(t *testing.T) {
	service := &authServiceStub{authUser: auth.User{ID: 11}}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	request.Header.Set("Cookie", SessionCookieName+`="token"`)

	handler.RequireSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler reached")
	})).ServeHTTP(recorder, request)

	assertJSONError(t, recorder, http.StatusUnauthorized, "authentication required")
	if len(service.authTokens) != 0 {
		t.Fatalf("authenticate calls = %d, want 0", len(service.authTokens))
	}
}

func TestRequireSessionInjectsAuthenticatedUser(t *testing.T) {
	want := auth.User{ID: 9, Email: "user@example.com", Name: "User"}
	service := &authServiceStub{authUser: want}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	request.Header.Set("Cookie", SessionCookieName+"=token")

	handler.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := UserFromContext(r.Context())
		if !ok || got != want {
			t.Fatalf("context user = %#v, %v", got, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || len(service.authTokens) != 1 || service.authTokens[0] != "token" {
		t.Fatalf("status/tokens = %d/%#v", recorder.Code, service.authTokens)
	}
}

func TestRequireSessionReturnsInternalErrorForServiceFailure(t *testing.T) {
	service := &authServiceStub{authErr: errors.New("storage failure")}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	request.Header.Set("Cookie", SessionCookieName+"=token")

	handler.RequireSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})).ServeHTTP(recorder, request)

	assertJSONError(t, recorder, http.StatusInternalServerError, "internal server error")
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
}

func TestRequireSessionRejectsCrossOriginBeforeAuthentication(t *testing.T) {
	service := &authServiceStub{authErr: errors.New("must not be reached")}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/auth/me", http.NoBody)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Cookie", SessionCookieName+"=token")

	handler.RequireSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("protected handler reached")
	})).ServeHTTP(recorder, request)

	assertJSONError(t, recorder, http.StatusForbidden, "origin not allowed")
	if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q, want private, no-store", got)
	}
	if len(service.authTokens) != 0 {
		t.Fatalf("authenticate calls = %d, want 0", len(service.authTokens))
	}
}

func TestRequireSessionUsesServiceFiveMinuteTouchPolicy(t *testing.T) {
	now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
	repository := &touchSessionRepository{
		session: auth.Session{ExpiresAt: now.Add(time.Hour), LastSeenAt: now.Add(-5 * time.Minute)},
		user:    auth.User{ID: 4, Email: "user@example.com", Name: "User"},
	}
	service := auth.NewService(nil, repository, auth.ServiceOptions{Clock: testClock{now: now}})
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	request.Header.Set("Cookie", SessionCookieName+"="+token)

	handler.RequireSession(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := UserFromContext(r.Context()); !ok || user.ID != 4 {
			t.Fatalf("context user = %#v, %v", user, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	})).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusNoContent || repository.touches != 1 {
		t.Fatalf("status/touches = %d/%d, want 204/1", recorder.Code, repository.touches)
	}
}

type touchSessionRepository struct {
	session auth.Session
	user    auth.User
	touches int
}

func (*touchSessionRepository) Create(context.Context, auth.NewSession) error { return nil }
func (r *touchSessionRepository) FindByTokenHash(context.Context, [32]byte) (auth.Session, auth.User, error) {
	return r.session, r.user, nil
}
func (r *touchSessionRepository) Touch(context.Context, [32]byte, time.Time) error {
	r.touches++
	return nil
}
func (*touchSessionRepository) DeleteByTokenHash(context.Context, [32]byte) error { return nil }
func (*touchSessionRepository) DeleteExpired(context.Context, time.Time) error    { return nil }
func (*touchSessionRepository) DeleteByUserID(context.Context, int64) error       { return nil }

func (*touchSessionRepository) DeleteByUserIDExcept(context.Context, int64, [32]byte) error {
	return nil
}

func TestUnsafeRequestsRequireExactSameOrigin(t *testing.T) {
	tests := []struct {
		name, target, host, origin, forwarded string
		want                                  bool
	}{
		{"direct https", "https://example.com/path", "", "https://example.com", "", true},
		{"direct http", "http://example.com/path", "", "http://example.com", "", true},
		{"missing origin", "https://example.com/path", "", "", "", false},
		{"cross origin", "https://example.com/path", "", "https://evil.example", "", false},
		{"host case differs", "https://example.com/path", "", "https://EXAMPLE.com", "", false},
		{"port differs", "https://example.com:8443/path", "", "https://example.com", "", false},
		{"forwarded https", "http://internal/path", "public.example", "https://public.example", "https", true},
		{"first forwarded value", "http://internal/path", "public.example", "https://public.example", "https, http", true},
		{"invalid first forwarded value", "http://internal/path", "public.example", "https://public.example", "ftp, https", false},
		{"forwarded scheme mismatch", "http://internal/path", "public.example", "http://public.example", "https", false},
		{"origin has path", "https://example.com/path", "", "https://example.com/extra", "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, test.target, http.NoBody)
			if test.host != "" {
				request.Host = test.host
			}
			if test.origin != "" {
				request.Header.Set("Origin", test.origin)
			}
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-Proto", test.forwarded)
			}
			if got := SameOrigin(request); got != test.want {
				t.Fatalf("SameOrigin = %v, want %v", got, test.want)
			}
		})
	}
}

func TestDirectRequestSchemeComesOnlyFromTLS(t *testing.T) {
	cleartextAbsoluteHTTPS := httptest.NewRequest(http.MethodPost, "https://example.com/path", http.NoBody)
	cleartextAbsoluteHTTPS.TLS = nil
	cleartextAbsoluteHTTPS.Header.Set("Origin", "http://example.com")
	if !SameOrigin(cleartextAbsoluteHTTPS) {
		t.Fatal("cleartext request with absolute https target did not use http transport scheme")
	}

	tlsAbsoluteHTTP := httptest.NewRequest(http.MethodPost, "http://example.com/path", http.NoBody)
	tlsAbsoluteHTTP.TLS = &tls.ConnectionState{}
	tlsAbsoluteHTTP.Header.Set("Origin", "https://example.com")
	if !SameOrigin(tlsAbsoluteHTTP) {
		t.Fatal("TLS request with absolute http target did not use https transport scheme")
	}
}

func TestSafeMethodsDoNotRequireOrigin(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		request := httptest.NewRequest(method, "https://example.com/path", http.NoBody)
		if !SameOrigin(request) {
			t.Fatalf("%s rejected", method)
		}
	}
}

func TestSameOriginMatrix(t *testing.T) {
	cases := []struct {
		name     string
		method   string
		host     string
		origin   string
		forward  string
		expected bool
	}{
		{"GET without origin is safe", http.MethodGet, "app.example", "", "", true},
		{"HEAD without origin is safe", http.MethodHead, "app.example", "", "", true},
		{"POST without origin is blocked", http.MethodPost, "app.example", "", "", false},
		{"POST with matching origin is allowed", http.MethodPost, "app.example", "http://app.example", "", true},
		{"POST with foreign origin is blocked", http.MethodPost, "app.example", "http://evil.example", "", false},
		{"POST with scheme mismatch is blocked", http.MethodPost, "app.example", "https://app.example", "", false},
		{"POST with forwarded https and https origin is allowed", http.MethodPost, "app.example", "https://app.example", "https", true},
		{"POST with origin carrying a path is blocked", http.MethodPost, "app.example", "http://app.example/x", "", false},
		{"POST with null origin is blocked", http.MethodPost, "app.example", "null", "", false},
		{"DELETE with matching origin is allowed", http.MethodDelete, "app.example", "http://app.example", "", true},
		{"DELETE with foreign origin is blocked", http.MethodDelete, "app.example", "http://evil.example", "", false},
		{"PATCH with foreign origin is blocked", http.MethodPatch, "app.example", "http://evil.example", "", false},
		{"PUT with foreign origin is blocked", http.MethodPut, "app.example", "http://evil.example", "", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, "/api/v1/projects", http.NoBody)
			request.Host = testCase.host
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			if testCase.forward != "" {
				request.Header.Set("X-Forwarded-Proto", testCase.forward)
			}
			if got := SameOrigin(request); got != testCase.expected {
				t.Fatalf("SameOrigin = %v, want %v", got, testCase.expected)
			}
		})
	}
}

func TestUnsafeRequestWithDuplicateOriginHeadersIsBlocked(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/projects", http.NoBody)
	request.Host = "app.example"
	request.Header.Add("Origin", "http://app.example")
	request.Header.Add("Origin", "http://evil.example")

	if SameOrigin(request) {
		t.Fatal("SameOrigin allowed a request carrying two Origin headers, want blocked")
	}
}

// TestUnsafeMethodsIgnoreFetchMetadata pins the divergence from
// net/http.CrossOriginProtection documented above SameOrigin: Sec-Fetch-Site
// never substitutes for an Origin match here, in either direction.
func TestUnsafeMethodsIgnoreFetchMetadata(t *testing.T) {
	cases := []struct {
		name     string
		site     string
		origin   string
		expected bool
	}{
		{"same-origin fetch metadata does not replace a missing Origin", "same-origin", "", false},
		{"user-initiated navigation does not replace a missing Origin", "none", "", false},
		{"cross-site fetch metadata does not override a matching Origin", "cross-site", "http://app.example", true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects", http.NoBody)
			request.Host = "app.example"
			request.Header.Set("Sec-Fetch-Site", testCase.site)
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			if got := SameOrigin(request); got != testCase.expected {
				t.Fatalf("SameOrigin with Sec-Fetch-Site=%q = %v, want %v", testCase.site, got, testCase.expected)
			}
		})
	}
}
