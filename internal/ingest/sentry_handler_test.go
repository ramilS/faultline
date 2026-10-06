package ingest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/spill"
)

const testPublicKey = "sentry-test-public-key"

// newTestSentryHandler wires a generous key store (project 1, effectively
// unlimited rate) so tests exercise ServeSentry's decode/dispatch path without
// tripping the rate limiter incidentally.
func newTestSentryHandler(t *testing.T) *Handler {
	t.Helper()
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}}
	return NewHandler(store, (&testClock{now: cacheBase}).Now, func(error) {}, nil, nil)
}

// newTestSentryHandlerWithRateLimit wires the same project-1 key store with a
// caller-controlled rate so tests can force a 429 deterministically.
func newTestSentryHandlerWithRateLimit(t *testing.T, rps, burst int) *Handler {
	t.Helper()
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: rps, RateLimitBurst: burst}}
	return NewHandler(store, (&testClock{now: cacheBase}).Now, func(error) {}, nil, nil)
}

const testEventPayload = `{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","level":"error","message":"boom"}`

func sentryEnvelopeBody() string {
	return "{\"event_id\":\"9ec79c33ec9942ab8353589fcb2e04dc\"}\n{\"type\":\"event\"}\n" + testEventPayload + "\n"
}

// sentryRequest builds a POST to the ingest route with the path project id
// populated the way ServeMux would.
func sentryRequest(projectID, key, body string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/"+projectID+"/envelope/?sentry_key="+key, strings.NewReader(body))
	request.SetPathValue("project_id", projectID)
	return request
}

func TestServeSentryAcceptsAnEnvelopeAndEchoesTheEventID(t *testing.T) {
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()

	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, sentryEnvelopeBody()))

	if recorder.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", recorder.Code, recorder.Body)
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "9ec79c33ec9942ab8353589fcb2e04dc" {
		t.Fatalf("got id %q", response.ID)
	}
}

func TestServeSentryRejectsMissingCredentials(t *testing.T) {
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/1/envelope/", strings.NewReader(sentryEnvelopeBody()))
	request.SetPathValue("project_id", "1")

	handler.ServeSentry(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", recorder.Code)
	}
}

func TestServeSentryRejectsAKeyBelongingToAnotherProject(t *testing.T) {
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()

	handler.ServeSentry(recorder, sentryRequest("999", testPublicKey, sentryEnvelopeBody()))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want 401", recorder.Code)
	}
	// The same body as an unknown key: distinguishing them would turn the
	// endpoint into an oracle for which project ids exist.
	if !strings.Contains(recorder.Body.String(), "invalid credentials") {
		t.Fatalf("got body %s", recorder.Body)
	}
}

func TestServeSentryRejectsNonNumericProjectID(t *testing.T) {
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()
	request := sentryRequest("v1", testPublicKey, sentryEnvelopeBody())

	handler.ServeSentry(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("the retired /api/v1/envelope path must fail closed, got %d", recorder.Code)
	}
}

func TestServeSentryDiscardsUnknownItemTypesWithoutFailing(t *testing.T) {
	body := "{}\n{\"type\":\"session\"}\n{\"sid\":\"abc\"}\n{\"type\":\"client_report\"}\n{}\n"
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()

	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, body))

	// An SDK that gets a 4xx for a session envelope retries it forever and
	// eventually drops the errors we do want.
	if recorder.Code != http.StatusOK {
		t.Fatalf("got %d, want 200: %s", recorder.Code, recorder.Body)
	}
}

func TestServeSentryReturns413ForAnOversizeEnvelope(t *testing.T) {
	body := "{}\n{\"type\":\"event\"}\n" + strings.Repeat("x", MaxCompressedBytes+1) + "\n"
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()

	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, body))

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("got %d, want 413", recorder.Code)
	}
}

func TestServeSentryReturns400ForAMalformedEnvelope(t *testing.T) {
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()

	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, "not an envelope\n"))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want 400", recorder.Code)
	}
}

// TestServeSentryDoesNotChargeRateLimitBudgetOnADecodeFailure pins the
// decode-before-charge ordering: a hostile client must not be able to burn a
// victim key's rate budget with garbage. With burst 1, if the charge ran
// before the decode, the first (rejected) request would consume the only
// token and the second, valid request would come back 429 instead of 200.
func TestServeSentryDoesNotChargeRateLimitBudgetOnADecodeFailure(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{"malformed envelope", "not an envelope\n"},
		{"oversize envelope", "{}\n{\"type\":\"event\"}\n" + strings.Repeat("x", MaxCompressedBytes+1) + "\n"},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := newTestSentryHandlerWithRateLimit(t, 1, 1)

			rejected := httptest.NewRecorder()
			handler.ServeSentry(rejected, sentryRequest("1", testPublicKey, test.body))
			if rejected.Code != http.StatusBadRequest && rejected.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("got %d, want 400 or 413 for the decode failure: %s", rejected.Code, rejected.Body)
			}

			accepted := httptest.NewRecorder()
			handler.ServeSentry(accepted, sentryRequest("1", testPublicKey, sentryEnvelopeBody()))
			if accepted.Code != http.StatusOK {
				t.Fatalf("a decode failure must not consume the rate budget; got %d for the follow-up valid envelope: %s", accepted.Code, accepted.Body)
			}
		})
	}
}

// TestServeSentryAcceptsAGzipCompressedEnvelope pins the Content-Encoding
// wiring at ServeSentry's DecodeSentryEnvelope call: every real Sentry SDK
// gzips by default, so a regression in reading that header would reject all
// production traffic while a suite built only on uncompressed bodies stayed
// green. Asserting a dispatch call, not just the 200, proves the gzip body
// was actually decompressed and mapped, not merely accepted unread.
func TestServeSentryAcceptsAGzipCompressedEnvelope(t *testing.T) {
	recorder := &dispatchRecorder{}
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}}
	handler := NewHandler(store, (&testClock{now: cacheBase}).Now, func(error) {}, recorder.dispatch, nil)

	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(sentryEnvelopeBody())); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/1/envelope/?sentry_key="+testPublicKey, &compressed)
	request.SetPathValue("project_id", "1")
	request.Header.Set("Content-Encoding", "gzip")

	response := httptest.NewRecorder()
	handler.ServeSentry(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body)
	}
	if calls := recorder.snapshot(); len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1 (the gzip body must decompress and map)", len(calls))
	}
}

type spySpiller struct {
	appends [][]byte
	err     error
}

func (s *spySpiller) Append(_ int64, _ time.Time, envelope []byte) error {
	if s.err != nil {
		return s.err
	}
	s.appends = append(s.appends, envelope)
	return nil
}

// TestServeSentryDurableAppendFailureReturns503 pins the branch a generic
// (non-ErrSpillFull) Spiller.Append error takes: 503, not the 429 the
// spill-full case gets, since a 429 tells an SDK to retry a specific data
// category, which is the wrong instruction for "the disk is unwritable."
func TestServeSentryDurableAppendFailureReturns503(t *testing.T) {
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}}
	handler := NewHandlerWithOptions(HandlerOptions{
		Store:   store,
		Now:     (&testClock{now: cacheBase}).Now,
		Dedup:   NewDeduplicator(),
		Spiller: &spySpiller{err: errors.New("disk full")},
		Durable: true,
	})

	recorder := httptest.NewRecorder()
	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, sentryEnvelopeBody()))

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "durability unavailable") {
		t.Fatalf("body = %s, want it to name durability unavailable", recorder.Body)
	}
}

// TestServeSentryDurableSpillFullReturns429 pins the sibling branch: a
// spill.ErrSpillFull Append error gets 429 with both Retry-After and the
// structured X-Sentry-Rate-Limits header, matching the ordinary rate-limit
// response shape so a compliant SDK backs off the same way for either cause.
func TestServeSentryDurableSpillFullReturns429(t *testing.T) {
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}}
	handler := NewHandlerWithOptions(HandlerOptions{
		Store:   store,
		Now:     (&testClock{now: cacheBase}).Now,
		Dedup:   NewDeduplicator(),
		Spiller: &spySpiller{err: spill.ErrSpillFull},
		Durable: true,
	})

	recorder := httptest.NewRecorder()
	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, sentryEnvelopeBody()))

	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", recorder.Code, recorder.Body)
	}
	if !strings.Contains(recorder.Body.String(), "spill full") {
		t.Fatalf("body = %s, want it to name spill full", recorder.Body)
	}
	if got := recorder.Header().Get("Retry-After"); got != "30" {
		t.Fatalf("Retry-After = %q, want 30", got)
	}
	if got := recorder.Header().Get("X-Sentry-Rate-Limits"); !strings.HasSuffix(got, "::key") {
		t.Fatalf("X-Sentry-Rate-Limits = %q, want <seconds>::key", got)
	}
}

func TestServeSentryEmitsBothRateLimitHeaders(t *testing.T) {
	handler := newTestSentryHandlerWithRateLimit(t, 1, 1)
	for range 5 {
		recorder := httptest.NewRecorder()
		handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, sentryEnvelopeBody()))
		if recorder.Code != http.StatusTooManyRequests {
			continue
		}
		if recorder.Header().Get("Retry-After") == "" {
			t.Fatal("Retry-After must be set")
		}
		limits := recorder.Header().Get("X-Sentry-Rate-Limits")
		if limits == "" || !strings.HasSuffix(limits, "::key") {
			t.Fatalf("got X-Sentry-Rate-Limits %q, want <seconds>::key", limits)
		}
		return
	}
	t.Fatal("expected a 429 within five attempts")
}

// TestServeSentryZeroItemEnvelopeSkipsSpillInDurableMode pins the fix for a
// header-only envelope (no items at all) in durable mode: it has nothing to
// spill or dispatch, so it must not pay for a durable WAL write and its fsync.
func TestServeSentryZeroItemEnvelopeSkipsSpillInDurableMode(t *testing.T) {
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}}
	spiller := &spySpiller{}
	handler := NewHandlerWithOptions(HandlerOptions{
		Store:   store,
		Now:     (&testClock{now: cacheBase}).Now,
		Dedup:   NewDeduplicator(),
		Spiller: spiller,
		Durable: true,
	})

	recorder := httptest.NewRecorder()
	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, sentryEventEnvelope()))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body)
	}
	if len(spiller.appends) != 0 {
		t.Fatalf("spill appends = %d, want 0 for a zero-item envelope", len(spiller.appends))
	}
}

// TestServeSentryFallsBackWhenHeaderEventIDIsNotHex pins the fix for echoing
// the envelope header's event_id unchecked: unlike an item's event_id (parsed
// with uuid.Parse), the header id had no format check, so a crafted header
// line could make the 200 response body arbitrarily large — a 2 MiB gzipped
// body with an oversize header id yields a response almost as large as the
// decompressed request.
func TestServeSentryFallsBackWhenHeaderEventIDIsNotHex(t *testing.T) {
	handler := newTestSentryHandler(t)
	recorder := httptest.NewRecorder()

	body := `{"event_id":"` + strings.Repeat("a", 5000) + `"}` + "\n" +
		`{"type":"event"}` + "\n" + testEventPayload + "\n"

	handler.ServeSentry(recorder, sentryRequest("1", testPublicKey, body))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body)
	}
	var response struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.ID != "9ec79c33ec9942ab8353589fcb2e04dc" {
		t.Fatalf("got id of length %d, want the mapped event's 32-hex id, not the malformed header value echoed verbatim", len(response.ID))
	}
}
