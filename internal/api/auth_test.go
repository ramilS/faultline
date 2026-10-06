package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
)

type authServiceStub struct {
	loginUser   auth.User
	loginToken  string
	loginErr    error
	loginCalls  int
	authUser    auth.User
	authSession auth.Session
	authErr     error
	authTokens  []string
	logoutErr   error
	logout      []string
}

func (s *authServiceStub) Login(context.Context, string, string) (auth.User, string, error) {
	s.loginCalls++
	return s.loginUser, s.loginToken, s.loginErr
}
func (s *authServiceStub) Authenticate(_ context.Context, token string) (auth.Session, auth.User, error) {
	s.authTokens = append(s.authTokens, token)
	return s.authSession, s.authUser, s.authErr
}
func (s *authServiceStub) Logout(_ context.Context, token string) error {
	s.logout = append(s.logout, token)
	return s.logoutErr
}

func TestLoginValidatesJSONRequest(t *testing.T) {
	tests := []struct {
		name, contentType, body string
	}{
		{"missing content type", "", `{}`},
		{"content type parameter", "application/json; charset=utf-8", `{}`},
		{"malformed", "application/json", `{`},
		{"unknown field", "application/json", `{"email":"a@example.com","password":"secret","extra":true}`},
		{"missing email", "application/json", `{"password":"secret"}`},
		{"missing password", "application/json", `{"email":"a@example.com"}`},
		{"second value", "application/json", `{"email":"a@example.com","password":"secret"} {}`},
		{"over limit", "application/json", `{"email":"a@example.com","password":"` + strings.Repeat("x", 16<<10) + `"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &authServiceStub{}
			handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
			recorder := httptest.NewRecorder()
			request := requestWithOrigin(http.MethodPost, "/api/v1/auth/login", test.body)
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}

			handler.Login(recorder, request)

			assertJSONError(t, recorder, http.StatusBadRequest, "invalid request")
			if service.loginCalls != 0 {
				t.Fatalf("login calls = %d, want 0", service.loginCalls)
			}
		})
	}
}

func TestLoginUsesGenericCredentialError(t *testing.T) {
	service := &authServiceStub{loginErr: auth.ErrInvalidCredentials}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := validLoginRequest()

	handler.Login(recorder, request)

	assertJSONError(t, recorder, http.StatusUnauthorized, "invalid credentials")
}

func TestLoginReturnsInternalErrorWithoutDetails(t *testing.T) {
	service := &authServiceStub{loginErr: errors.New("database secret")}
	var reported error
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{ReportError: func(err error) { reported = err }})
	recorder := httptest.NewRecorder()

	handler.Login(recorder, validLoginRequest())

	assertJSONError(t, recorder, http.StatusInternalServerError, "internal server error")
	if strings.Contains(recorder.Body.String(), "database secret") {
		t.Fatal("response leaked internal error")
	}
	if reported == nil || !strings.Contains(reported.Error(), "login") {
		t.Fatalf("reported error = %v", reported)
	}
}

func TestLoginThrottlingReturnsRetryAfter(t *testing.T) {
	service := &authServiceStub{loginErr: auth.ErrInvalidCredentials}
	limiter := newTestLimiter()
	handler := NewAuthHandler(service, limiter, &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	for range 5 {
		handler.Login(httptest.NewRecorder(), validLoginRequest())
	}
	recorder := httptest.NewRecorder()

	handler.Login(recorder, validLoginRequest())

	assertJSONError(t, recorder, http.StatusTooManyRequests, "too many login attempts")
	if got := recorder.Header().Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("Retry-After = %q, want positive seconds", got)
	}
}

func TestLoginAuthenticationBusyReturnsRetryAfterWithoutCookieOrReport(t *testing.T) {
	service := &authServiceStub{loginErr: auth.ErrAuthenticationBusy}
	var reported error
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{ReportError: func(err error) { reported = err }})
	recorder := httptest.NewRecorder()

	handler.Login(recorder, validLoginRequest())

	assertJSONError(t, recorder, http.StatusTooManyRequests, "authentication temporarily unavailable")
	if got := recorder.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
	if got := recorder.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("Set-Cookie = %#v, want none", got)
	}
	if reported != nil {
		t.Fatalf("reported error = %v, want nil", reported)
	}
}

func TestAuthEndpointResponsesArePrivateAndNotStored(t *testing.T) {
	tests := []struct {
		name string
		run  func(*AuthHandler, *httptest.ResponseRecorder)
	}{
		{"login error", func(h *AuthHandler, w *httptest.ResponseRecorder) {
			r := validLoginRequest()
			r.Header.Del("Content-Type")
			h.Login(w, r)
		}},
		{"login success", func(h *AuthHandler, w *httptest.ResponseRecorder) { h.Login(w, validLoginRequest()) }},
		{"logout error", func(h *AuthHandler, w *httptest.ResponseRecorder) {
			r := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/auth/logout", http.NoBody)
			r.Header.Set("Origin", "https://evil.example")
			h.Logout(w, r)
		}},
		{"logout no content", func(h *AuthHandler, w *httptest.ResponseRecorder) {
			h.Logout(w, requestWithOrigin(http.MethodPost, "/api/v1/auth/logout", ""))
		}},
		{"me error", func(h *AuthHandler, w *httptest.ResponseRecorder) {
			h.Me(w, httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody))
		}},
		{"me success", func(h *AuthHandler, w *httptest.ResponseRecorder) {
			r := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
			r = r.WithContext(ContextWithUser(r.Context(), auth.User{ID: 3}))
			h.Me(w, r)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &authServiceStub{loginUser: auth.User{ID: 1}, loginToken: "token"}
			handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
			recorder := httptest.NewRecorder()
			test.run(handler, recorder)
			if got := recorder.Header().Get("Cache-Control"); got != "private, no-store" {
				t.Fatalf("Cache-Control = %q, want private, no-store", got)
			}
		})
	}
}

func TestLoginSetsApprovedCookieAndReturnsUser(t *testing.T) {
	service := &authServiceStub{loginUser: auth.User{ID: 7, Email: "admin@example.com", Name: "Administrator", Role: auth.RoleOwner}, loginToken: "raw-token"}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{CookieSecure: true})
	recorder := httptest.NewRecorder()

	handler.Login(recorder, validLoginRequest())

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != `{"user":{"id":7,"email":"admin@example.com","name":"Administrator","role":"owner"}}`+"\n" {
		t.Fatalf("body = %q", got)
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %d, want 1", len(cookies))
	}
	cookie := cookies[0]
	if cookie.Name != SessionCookieName || cookie.Value != "raw-token" || cookie.Path != "/" || cookie.MaxAge != 7*24*60*60 || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %#v", cookie)
	}
}

func TestLogoutRevokesAndAlwaysClearsCookie(t *testing.T) {
	for _, test := range []struct {
		name, cookie string
		wantToken    string
	}{
		{"present", SessionCookieName + "=token", "token"},
		{"absent", "unrelated=value", ""},
		{"malformed", SessionCookieName + "=\"unterminated", ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := &authServiceStub{}
			handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{CookieSecure: true})
			recorder := httptest.NewRecorder()
			request := requestWithOrigin(http.MethodPost, "/api/v1/auth/logout", "")
			if test.cookie != "" {
				request.Header.Set("Cookie", test.cookie)
			}

			handler.Logout(recorder, request)

			if recorder.Code != http.StatusNoContent || recorder.Body.Len() != 0 {
				t.Fatalf("status/body = %d/%q", recorder.Code, recorder.Body.String())
			}
			if len(service.logout) != 1 || service.logout[0] != test.wantToken {
				t.Fatalf("logout tokens = %#v, want %q", service.logout, test.wantToken)
			}
			cookie := recorder.Result().Cookies()[0]
			if cookie.Name != SessionCookieName || cookie.Value != "" || cookie.MaxAge != -1 || !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" || cookie.SameSite != http.SameSiteLaxMode {
				t.Fatalf("clear cookie = %#v", cookie)
			}
		})
	}
}

func TestLogoutRejectsCrossOriginBeforeRevocationOrCookieMutation(t *testing.T) {
	service := &authServiceStub{}
	handler := NewAuthHandler(service, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{CookieSecure: true})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/auth/logout", http.NoBody)
	request.Header.Set("Origin", "https://evil.example")
	request.Header.Set("Cookie", SessionCookieName+"=token")

	handler.Logout(recorder, request)

	assertJSONError(t, recorder, http.StatusForbidden, "origin not allowed")
	if len(service.logout) != 0 {
		t.Fatalf("logout calls = %d, want 0", len(service.logout))
	}
	if got := recorder.Header().Values("Set-Cookie"); len(got) != 0 {
		t.Fatalf("Set-Cookie = %#v, want none", got)
	}
}

func TestCurrentUserResponseUsesContext(t *testing.T) {
	handler := NewAuthHandler(&authServiceStub{}, newTestLimiter(), &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", http.NoBody)
	request = request.WithContext(ContextWithUser(request.Context(), auth.User{ID: 3, Email: "a@example.com", Name: "A", Role: auth.RoleMember}))

	handler.Me(recorder, request)

	if got := recorder.Body.String(); got != `{"user":{"id":3,"email":"a@example.com","name":"A","role":"member"}}`+"\n" {
		t.Fatalf("body = %q", got)
	}
}

func validLoginRequest() *http.Request {
	request := requestWithOrigin(http.MethodPost, "/api/v1/auth/login", `{"email":"admin@example.com","password":"correct horse"}`)
	request.Header.Set("Content-Type", "application/json")
	request.RemoteAddr = "192.0.2.1:1234"
	return request
}

func requestWithOrigin(method, target, body string) *http.Request {
	request := httptest.NewRequest(method, "https://example.com"+target, strings.NewReader(body))
	request.Header.Set("Origin", "https://example.com")
	return request
}

func assertJSONError(t *testing.T, recorder *httptest.ResponseRecorder, status int, message string) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("status = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q", got)
	}
	if got := recorder.Body.String(); got != `{"error":"`+message+`"}`+"\n" {
		t.Fatalf("body = %q", got)
	}
}

type testClock struct{ now time.Time }

func (c testClock) Now() time.Time { return c.now }

func newTestLimiter() *auth.LoginLimiter {
	return auth.NewLoginLimiter(testClock{now: time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)})
}
