package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/ingest"
)

func TestEnvelopeRouteIsPublic(t *testing.T) {
	reached := false
	dependencies := authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})
	dependencies.EnvelopeHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", http.NoBody)
	// No session cookie and no Origin header: ingestion must not require them.

	New(dependencies).ServeHTTP(recorder, request)

	if !reached {
		t.Fatal("envelope route did not reach the ingestion handler")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
}

func TestEnvelopePreflightIsAnsweredWithoutSession(t *testing.T) {
	reached := false
	dependencies := authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})
	dependencies.EnvelopeHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/1/envelope/", http.NoBody)
	request.Header.Set("Origin", "https://app.example.com")
	request.Header.Set("Access-Control-Request-Method", "POST")
	request.Header.Set("Access-Control-Request-Headers", "x-sentry-auth")

	New(dependencies).ServeHTTP(recorder, request)

	if reached {
		t.Fatal("preflight reached the ingestion handler")
	}
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Headers"); !strings.Contains(got, "X-Sentry-Auth") {
		t.Fatalf("Access-Control-Allow-Headers = %q, want it to include X-Sentry-Auth", got)
	}
}

func TestEnvelopePostCarriesCORSHeaders(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})
	dependencies.EnvelopeHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", http.NoBody)
	request.Header.Set("Origin", "https://app.example.com")

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("Access-Control-Allow-Origin = %q, want *", got)
	}
	if got := recorder.Header().Get("Access-Control-Expose-Headers"); !strings.Contains(got, "Retry-After") {
		t.Fatalf("Access-Control-Expose-Headers = %q, want it to include Retry-After", got)
	}
}

// The wildcard belongs to ingestion alone. Session-authenticated routes carry
// ambient cookie authority, so allowing any origin to read their responses would
// turn every dashboard endpoint into a cross-origin data source.
func TestCORSIsScopedToIngestion(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})
	dependencies.EnvelopeHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})

	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/projects"},
		{http.MethodPost, "/api/v1/projects"},
		{http.MethodOptions, "/api/v1/projects"},
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPost, "/api/v1/auth/login"},
		{http.MethodGet, "/api/v1/issues/1"},
	} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(target.method, target.path, http.NoBody)
		request.Header.Set("Origin", "https://evil.example.com")

		New(dependencies).ServeHTTP(recorder, request)

		if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "" {
			t.Errorf("%s %s: Access-Control-Allow-Origin = %q, want unset", target.method, target.path, got)
		}
	}
}

func TestEnvelopePreflightAbsentWithoutEnvelopeHandler(t *testing.T) {
	dependencies := authDependencies(&authServiceStub{authErr: auth.ErrInvalidSession})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodOptions, "/api/1/envelope/", http.NoBody)

	New(dependencies).ServeHTTP(recorder, request)

	if recorder.Code == http.StatusNoContent {
		t.Fatal("preflight answered 204 without an ingestion handler wired")
	}
}

// TestRouterRegistersIngestAlongsideTheDashboardAPI's stub EnvelopeHandler
// always answers 200, so adding "/api/v1/envelope" to that table would only
// prove the stub was reached — not that the retired path fails closed, which
// is a property of ServeSentry itself (ProjectIDFromPath rejecting "v1" as
// non-numeric), not of the routing. Wiring the real handler here — with a
// key that would otherwise resolve and a well-formed envelope body that would
// otherwise be accepted — is what makes the assertion mean something: if
// ProjectIDFromPath ever stopped rejecting "v1", this request would sail
// through every other check and come back 200, not 401.
func TestRetiredEnvelopePathFailsClosedThroughTheRealHandler(t *testing.T) {
	envelopeHandler := ingest.NewHandler(appIngestKeyStoreStub{}, time.Now, func(error) {}, nil, nil)
	handler := New(Dependencies{EnvelopeHandler: http.HandlerFunc(envelopeHandler.ServeSentry)})

	body := "{\"event_id\":\"9ec79c33ec9942ab8353589fcb2e04dc\"}\n{\"type\":\"event\"}\n" +
		"{\"event_id\":\"9ec79c33ec9942ab8353589fcb2e04dc\",\"level\":\"error\",\"message\":\"boom\"}\n"
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/envelope?sentry_key=retired-path-key", strings.NewReader(body))

	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401: %s", recorder.Code, recorder.Body.String())
	}
}
