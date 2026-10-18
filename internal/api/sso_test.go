package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/auth"
)

// flowStateCookie mirrors the JSON shape sso.go's unexported flowState
// encodes into the cookie. Declaring it here, independently of the
// production type, pins the cookie's wire shape: renaming a field in
// flowState without updating this struct breaks the test instead of both
// sides silently drifting together.
type flowStateCookie struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
}

func decodeFlowStateCookie(t *testing.T, value string) flowStateCookie {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		t.Fatalf("cookie value is not base64url: %v", err)
	}
	var decoded flowStateCookie
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("cookie value is not JSON flow state: %v", err)
	}
	return decoded
}

type stubSSO struct {
	lastState        string
	lastNonce        string
	lastChallenge    string
	claims           auth.IdentityClaims
	exchangeErr      error
	exchanges        int
	exchangeDeadline time.Time
}

func (s *stubSSO) AuthCodeURL(state, nonce, challenge string) string {
	s.lastState, s.lastNonce, s.lastChallenge = state, nonce, challenge
	return "https://idp.example/auth?state=" + state
}

func (s *stubSSO) Exchange(ctx context.Context, _, _, _ string) (auth.IdentityClaims, error) {
	s.exchanges++
	s.exchangeDeadline, _ = ctx.Deadline()
	return s.claims, s.exchangeErr
}

type stubIdentityLogins struct {
	user  auth.User
	token string
	err   error
	calls int
}

func (s *stubIdentityLogins) LoginWithIdentity(_ context.Context, _ string, _ auth.IdentityClaims) (auth.User, string, error) {
	s.calls++
	return s.user, s.token, s.err
}

func TestSSOStartRedirectsAndSetsOneStateCookie(t *testing.T) {
	provider := &stubSSO{}
	handler := api.NewSSOHandler(provider, &stubIdentityLogins{}, nil, auth.AllowedDomains{"example.com"}, api.Options{CookieSecure: true})

	recorder := httptest.NewRecorder()
	handler.Start(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/start", http.NoBody))

	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", recorder.Code)
	}
	if !strings.HasPrefix(recorder.Header().Get("Location"), "https://idp.example/auth") {
		t.Fatalf("Location = %q", recorder.Header().Get("Location"))
	}
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("set %d cookies, want 1", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly || !cookie.Secure {
		t.Fatalf("cookie must be HttpOnly and Secure: %+v", cookie)
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/api/v1/auth/sso" {
		t.Fatalf("Path = %q, want the flow's own path", cookie.Path)
	}
	if provider.lastNonce == "" || provider.lastChallenge == "" {
		t.Fatalf("nonce = %q, challenge = %q, both must be minted", provider.lastNonce, provider.lastChallenge)
	}

	decoded := decodeFlowStateCookie(t, cookie.Value)
	if decoded.Verifier == provider.lastChallenge {
		t.Fatal("the cookie carries the PKCE challenge; it must carry the verifier, which the challenge is derived from")
	}
	wantChallenge := sha256.Sum256([]byte(decoded.Verifier))
	if provider.lastChallenge != base64.RawURLEncoding.EncodeToString(wantChallenge[:]) {
		t.Fatalf("challenge = %q, want SHA-256(verifier) = %q", provider.lastChallenge, base64.RawURLEncoding.EncodeToString(wantChallenge[:]))
	}
	if decoded.State != provider.lastState {
		t.Fatalf("cookie state = %q, redirect state = %q", decoded.State, provider.lastState)
	}
	if decoded.Nonce != provider.lastNonce {
		t.Fatalf("cookie nonce = %q, redirect nonce = %q", decoded.Nonce, provider.lastNonce)
	}
}

func TestSSOStartMintsFreshValuesEachTime(t *testing.T) {
	provider := &stubSSO{}
	handler := api.NewSSOHandler(provider, &stubIdentityLogins{}, nil, auth.AllowedDomains{"example.com"}, api.Options{})

	handler.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/start", http.NoBody))
	firstState, firstNonce, firstChallenge := provider.lastState, provider.lastNonce, provider.lastChallenge
	handler.Start(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/start", http.NoBody))

	if firstState == provider.lastState || firstState == "" {
		t.Fatalf("state was reused across sign-ins: %q", firstState)
	}
	if firstNonce == provider.lastNonce || firstNonce == "" {
		t.Fatalf("nonce was reused across sign-ins: %q", firstNonce)
	}
	if firstChallenge == provider.lastChallenge || firstChallenge == "" {
		t.Fatalf("PKCE challenge was reused across sign-ins: %q", firstChallenge)
	}
}

func startFlow(t *testing.T, handler *api.SSOHandler) *http.Cookie {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.Start(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/start", http.NoBody))
	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Start set %d cookies, want 1", len(cookies))
	}
	return cookies[0]
}

func TestSSOCallbackSignsInAndClearsFlowCookie(t *testing.T) {
	provider := &stubSSO{claims: auth.IdentityClaims{
		Subject: "sub-1", Email: "person@example.com", EmailVerified: true, HostedDomain: "example.com",
	}}
	logins := &stubIdentityLogins{token: "session-token"}
	handler := api.NewSSOHandler(provider, logins, nil, auth.AllowedDomains{"example.com"}, api.Options{})
	cookie := startFlow(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/callback?code=abc&state="+provider.lastState, http.NoBody)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.Callback(recorder, request)

	if recorder.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302", recorder.Code)
	}
	var session, flow *http.Cookie
	for _, set := range recorder.Result().Cookies() {
		switch set.Name {
		case api.SessionCookieName:
			session = set
		case "faultline_sso_state":
			flow = set
		}
	}
	if session == nil || session.Value != "session-token" {
		t.Fatalf("session cookie = %+v", session)
	}
	// Written as the literal number of seconds in 12 hours, deliberately not
	// as auth.SSOSessionLifetime/time.Second: deriving it from the same
	// constant the handler uses would make this assertion unable to fail. A
	// cookie that outlives its session shows the user as signed in until the
	// next request 401s.
	if session.MaxAge != 12*60*60 {
		t.Fatalf("session cookie MaxAge = %d, want 43200 (12h, the SSO session lifetime)", session.MaxAge)
	}
	if session.Path != "/" || !session.HttpOnly || session.SameSite != http.SameSiteLaxMode {
		t.Fatalf("session cookie attributes = %+v", session)
	}
	if flow == nil || flow.MaxAge >= 0 {
		t.Fatalf("the flow cookie must be cleared, got %+v", flow)
	}
	if logins.calls != 1 {
		t.Fatalf("LoginWithIdentity called %d times, want 1", logins.calls)
	}
}

// assertFlowCookieCleared fails unless the response re-sets the flow cookie
// with a negative MaxAge on the same path Start used. A regression that ran
// clearFlowCookie only on the success path would defeat the anti-replay
// guarantee: a rejected attempt's cookie would still validate a retry. A
// cookie cleared on a different Path is equally useless — the browser keeps
// the original alive alongside it.
func assertFlowCookieCleared(t *testing.T, recorder *httptest.ResponseRecorder) {
	t.Helper()
	for _, set := range recorder.Result().Cookies() {
		if set.Name != "faultline_sso_state" {
			continue
		}
		if set.MaxAge >= 0 {
			t.Fatalf("flow cookie MaxAge = %d, want < 0 (cleared)", set.MaxAge)
		}
		if set.Path != "/api/v1/auth/sso" {
			t.Fatalf("flow cookie Path = %q, want %q (the path it was set with)", set.Path, "/api/v1/auth/sso")
		}
		return
	}
	t.Fatal("response did not clear the flow cookie")
}

func TestSSOCallbackRejectsMismatchedState(t *testing.T) {
	provider := &stubSSO{claims: auth.IdentityClaims{Subject: "sub-1", Email: "p@example.com", EmailVerified: true, HostedDomain: "example.com"}}
	logins := &stubIdentityLogins{}
	handler := api.NewSSOHandler(provider, logins, nil, auth.AllowedDomains{"example.com"}, api.Options{})
	cookie := startFlow(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/callback?code=abc&state=forged", http.NoBody)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.Callback(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if logins.calls != 0 {
		t.Fatal("a forged state reached the login service")
	}
	assertFlowCookieCleared(t, recorder)
}

func TestSSOCallbackRejectsMissingFlowCookie(t *testing.T) {
	provider := &stubSSO{}
	logins := &stubIdentityLogins{}
	handler := api.NewSSOHandler(provider, logins, nil, auth.AllowedDomains{"example.com"}, api.Options{})

	recorder := httptest.NewRecorder()
	handler.Callback(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/callback?code=abc&state=whatever", http.NoBody))

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if logins.calls != 0 {
		t.Fatal("a flow with no cookie reached the login service")
	}
}

func TestSSOCallbackRejectsForeignHostedDomain(t *testing.T) {
	provider := &stubSSO{claims: auth.IdentityClaims{
		Subject: "sub-1", Email: "person@other.example", EmailVerified: true, HostedDomain: "other.example",
	}}
	logins := &stubIdentityLogins{}
	handler := api.NewSSOHandler(provider, logins, nil, auth.AllowedDomains{"example.com"}, api.Options{})
	cookie := startFlow(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/callback?code=abc&state="+provider.lastState, http.NoBody)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.Callback(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if logins.calls != 0 {
		t.Fatal("a foreign hosted domain reached the login service")
	}
	assertFlowCookieCleared(t, recorder)
}

// TestSSOCallbackReportsAnUnconfiguredLoginService is what makes
// auth.ErrSSONotConfigured's export honest: the doc comment says internal/api
// recognizes it by identity, and this is the caller that does. The response
// stays a bare 500 — a misconfigured deployment must not be described to an
// unauthenticated caller — but the operator's report names the wiring bug.
func TestSSOCallbackReportsAnUnconfiguredLoginService(t *testing.T) {
	provider := &stubSSO{claims: auth.IdentityClaims{
		Subject: "sub-1", Email: "person@example.com", EmailVerified: true, HostedDomain: "example.com",
	}}
	var reported []error
	handler := api.NewSSOHandler(provider, &stubIdentityLogins{err: auth.ErrSSONotConfigured}, nil, auth.AllowedDomains{"example.com"}, api.Options{
		ReportError: func(err error) { reported = append(reported, err) },
	})
	cookie := startFlow(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/callback?code=abc&state="+provider.lastState, http.NoBody)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	handler.Callback(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", recorder.Code)
	}
	if len(reported) != 1 || !errors.Is(reported[0], auth.ErrSSONotConfigured) {
		t.Fatalf("reported = %v, want one error wrapping auth.ErrSSONotConfigured", reported)
	}
	if !strings.Contains(reported[0].Error(), "identity repository") {
		t.Fatalf("reported error = %q, want it to name the missing wiring", reported[0])
	}
}

// TestSSOCallbackBoundsTheExchangeDeadline pins the deadline on the one
// outbound call this codebase makes from an unauthenticated route. The
// request context carries none of its own, so without it a blackholed route
// to the provider holds a goroutine and a socket indefinitely.
func TestSSOCallbackBoundsTheExchangeDeadline(t *testing.T) {
	provider := &stubSSO{claims: auth.IdentityClaims{
		Subject: "sub-1", Email: "person@example.com", EmailVerified: true, HostedDomain: "example.com",
	}}
	handler := api.NewSSOHandler(provider, &stubIdentityLogins{token: "t"}, nil, auth.AllowedDomains{"example.com"}, api.Options{})
	cookie := startFlow(t, handler)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/callback?code=abc&state="+provider.lastState, http.NoBody)
	request.AddCookie(cookie)
	handler.Callback(httptest.NewRecorder(), request)

	if provider.exchangeDeadline.IsZero() {
		t.Fatal("Exchange received a context with no deadline")
	}
	remaining := time.Until(provider.exchangeDeadline)
	if remaining <= 0 || remaining > 10*time.Second {
		t.Fatalf("exchange deadline is %s away, want a positive bound of at most 10s", remaining)
	}
}

// TestSSOCallbackRateLimitsExchangesPerAddress covers the resource-exhaustion
// path: the callback is unauthenticated and blocks on egress, so without a
// limit one client can collect flow cookies from Start and pin a goroutine
// and a socket per callback. The limit is per address, so a second client is
// unaffected by the first exhausting its bucket.
func TestSSOCallbackRateLimitsExchangesPerAddress(t *testing.T) {
	provider := &stubSSO{claims: auth.IdentityClaims{
		Subject: "sub-1", Email: "person@example.com", EmailVerified: true, HostedDomain: "example.com",
	}}
	limiter := auth.NewLoginLimiter(nil)
	handler := api.NewSSOHandler(provider, &stubIdentityLogins{token: "t"}, limiter, auth.AllowedDomains{"example.com"}, api.Options{})

	attempt := func(remoteAddr string) *httptest.ResponseRecorder {
		cookie := startFlow(t, handler)
		request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/sso/callback?code=abc&state="+provider.lastState, http.NoBody)
		request.RemoteAddr = remoteAddr
		request.AddCookie(cookie)
		recorder := httptest.NewRecorder()
		handler.Callback(recorder, request)
		return recorder
	}

	const burst = 5
	for index := range burst {
		if recorder := attempt("198.51.100.7:4000"); recorder.Code != http.StatusFound {
			t.Fatalf("attempt %d status = %d, want 302 within the burst allowance", index+1, recorder.Code)
		}
	}
	if provider.exchanges != burst {
		t.Fatalf("exchanges = %d, want %d", provider.exchanges, burst)
	}

	refused := attempt("198.51.100.7:4000")
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("status past the allowance = %d, want 429", refused.Code)
	}
	if refused.Header().Get("Retry-After") == "" {
		t.Fatal("a 429 must carry Retry-After, as the password path does")
	}
	if provider.exchanges != burst {
		t.Fatalf("exchanges = %d, want %d — a refused callback must not reach the provider", provider.exchanges, burst)
	}

	if other := attempt("203.0.113.9:4000"); other.Code != http.StatusFound {
		t.Fatalf("second address status = %d, want 302 — the limit must be per address", other.Code)
	}
}
