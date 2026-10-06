package ingest

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func corsRequest(method string) *http.Request {
	request := httptest.NewRequest(method, "/api/1/envelope/", http.NoBody)
	request.Header.Set("Origin", "https://app.example.com")
	return request
}

func TestPreflightAnswersWithoutReachingTheHandler(t *testing.T) {
	reached := false
	handler := WithCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, corsRequest(http.MethodOptions))

	if reached {
		t.Fatal("preflight reached the wrapped handler; it must be answered by the wrapper")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	if body := recorder.Body.String(); body != "" {
		t.Fatalf("body = %q, want empty", body)
	}
}

func TestPreflightAllowsExactlyWhatASentrySDKSends(t *testing.T) {
	handler := WithCORS(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, corsRequest(http.MethodOptions))

	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Methods": "POST, OPTIONS",
		"Access-Control-Allow-Headers": "Content-Type, Content-Encoding, X-Sentry-Auth",
		"Access-Control-Max-Age":       "86400",
	} {
		if got := recorder.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

// The endpoint authenticates on the path project id plus a Sentry public key
// (X-Sentry-Auth or ?sentry_key=) alone and reads no cookie. Pairing
// Allow-Credentials with a wildcard origin is rejected by browsers outright, and
// sending it at all would invite a cookie the endpoint must never depend on.
func TestCORSNeverAllowsCredentials(t *testing.T) {
	handler := WithCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))

	for _, method := range []string{http.MethodOptions, http.MethodPost} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, corsRequest(method))

		if got := recorder.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("%s: Access-Control-Allow-Credentials = %q, want unset", method, got)
		}
	}
}

func TestPostReachesTheHandlerAndCarriesCORSHeaders(t *testing.T) {
	handler := WithCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"9ec79c33ec9942ab8353589fcb2e04dc"}`))
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, corsRequest(http.MethodPost))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if body := recorder.Body.String(); body != `{"id":"9ec79c33ec9942ab8353589fcb2e04dc"}` {
		t.Fatalf("body = %q", body)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
}

// A cross-origin caller can only read Retry-After if it is explicitly exposed:
// it is not on the CORS response-header safelist. Without this the browser SDK's
// headers.get("Retry-After") returns null and its backoff silently ignores the
// delay the server asked for.
func TestThrottledResponseExposesRetryAfter(t *testing.T) {
	handler := WithCORS(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, corsRequest(http.MethodPost))

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", recorder.Code)
	}
	if got := recorder.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Retry-After") {
		t.Fatalf("Access-Control-Expose-Headers = %q, want it to include Retry-After", got)
	}
}

func TestBothRateLimitHeadersAreExposedToCrossOriginCallers(t *testing.T) {
	recorder := httptest.NewRecorder()
	handler := WithCORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/1/envelope/", http.NoBody))

	exposed := recorder.Header().Get("Access-Control-Expose-Headers")
	for _, want := range []string{"Retry-After", "X-Sentry-Rate-Limits"} {
		if !strings.Contains(exposed, want) {
			t.Fatalf("%s must be exposed or the SDK reads null for it; got %q", want, exposed)
		}
	}
}

func TestRejectedRequestStillCarriesCORSHeaders(t *testing.T) {
	handler := NewHandler(&fakeKeyStore{err: ErrKeyNotFound}, (&testClock{now: cacheBase}).Now, func(error) {}, nil, nil)

	recorder := httptest.NewRecorder()
	WithCORS(http.HandlerFunc(handler.ServeSentry)).ServeHTTP(recorder, sentryRequest("1", "ghost", ""))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want * on a 401", got)
	}
}
