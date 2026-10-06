package ingest

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/storage"
)

// validUUID is a fixed, valid event id reused by tests that don't care which
// id they get. distinctUUID mints a second (and third, ...) id that still
// parses, for tests that need events to be distinguishable for dedup.
const validUUID = "b1c2d3e4-f5a6-4b8c-9d0e-1f2a3b4c5d6e"

func distinctUUID(index int) string {
	return fmt.Sprintf("c2d3e4f5-a6b7-4c8d-9e0f-%012d", index)
}

func generousKey() KeyInfo {
	return KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}
}

type dispatchRecord struct {
	hint      string
	projectID int64
	item      pipeline.Item
}

type dispatchRecorder struct {
	mu    sync.Mutex
	calls []dispatchRecord
}

func (r *dispatchRecorder) dispatch(hint string, projectID int64, item pipeline.Item) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, dispatchRecord{hint: hint, projectID: projectID, item: item})
}

func (r *dispatchRecorder) snapshot() []dispatchRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]dispatchRecord(nil), r.calls...)
}

// exceptionEventJSON is a Sentry event item payload with an exception chain,
// its frames caller-first per the wire format (see groupingFramesFromSentry):
// the reversal it undergoes must land InvoiceService.total, the in_app frame,
// ahead of Thread.run for the fingerprint and culprit this test expects.
func exceptionEventJSON(eventID string) string {
	return `{"event_id":"` + eventID + `","level":"error",` +
		`"release":"billing@1.0.0","message":"total mismatch",` +
		`"tags":{"service":"billing"},` +
		`"exception":{"values":[{"type":"IllegalStateException","value":"total mismatch","stacktrace":{"frames":[` +
		`{"module":"java.lang.Thread","function":"run","in_app":false},` +
		`{"module":"com.acme.billing.InvoiceService","function":"total","in_app":true}]}}]}}`
}

func validSentryEventJSON(eventID string) string {
	return `{"event_id":"` + eventID + `","level":"error","message":"boom"}`
}

func sentryEventEnvelope(eventPayloads ...string) string {
	var builder strings.Builder
	builder.WriteString("{}\n")
	for _, payload := range eventPayloads {
		builder.WriteString("{\"type\":\"event\"}\n")
		builder.WriteString(payload)
		builder.WriteString("\n")
	}
	return builder.String()
}

func sentryEventAndTransactionEnvelope(eventPayload, transactionPayload string) string {
	return "{}\n{\"type\":\"event\"}\n" + eventPayload + "\n{\"type\":\"transaction\"}\n" + transactionPayload + "\n"
}

func newRecordingHandler(t *testing.T, store KeyStore) (*Handler, *dispatchRecorder) {
	t.Helper()
	recorder := &dispatchRecorder{}
	handler := NewHandler(store, (&testClock{now: cacheBase}).Now, func(error) {}, recorder.dispatch, nil)
	return handler, recorder
}

type spanRecord struct {
	projectID int64
	spans     []storage.SpanRow
}

type spanRecorder struct {
	mu    sync.Mutex
	calls []spanRecord
}

func (r *spanRecorder) dispatch(projectID int64, spans []storage.SpanRow) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, spanRecord{projectID: projectID, spans: spans})
}

func (r *spanRecorder) snapshot() []spanRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]spanRecord(nil), r.calls...)
}

func TestEnvelopeDispatchesAcceptedEventWithFingerprint(t *testing.T) {
	handler, recorder := newRecordingHandler(t, &fakeKeyStore{info: generousKey()})
	eventPayload := exceptionEventJSON(validUUID)

	response := httptest.NewRecorder()
	handler.ServeSentry(response, sentryRequest("1", testPublicKey, sentryEventEnvelope(eventPayload)))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}

	calls := recorder.snapshot()
	if len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1", len(calls))
	}

	decoded, err := MapSentryEvent([]byte(eventPayload), cacheBase)
	if err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	expected := grouping.Fingerprint(decoded.groupingInput())

	call := calls[0]
	if call.projectID != generousKey().ProjectID {
		t.Fatalf("project id = %d, want %d", call.projectID, generousKey().ProjectID)
	}
	if call.hint != expected.Fingerprint {
		t.Fatalf("hint = %q, want fingerprint %q", call.hint, expected.Fingerprint)
	}
	if call.item.Row.Fingerprint != expected.Fingerprint {
		t.Fatalf("fingerprint = %q, want %q", call.item.Row.Fingerprint, expected.Fingerprint)
	}
	if call.item.Title != expected.Title || call.item.Culprit != expected.Culprit {
		t.Fatalf("title/culprit = %q/%q, want %q/%q", call.item.Title, call.item.Culprit, expected.Title, expected.Culprit)
	}
	if call.item.Row.EventID != decoded.EventID {
		t.Fatalf("event id = %v, want %v", call.item.Row.EventID, decoded.EventID)
	}
	if call.item.Release != "billing@1.0.0" {
		t.Fatalf("release = %q, want billing@1.0.0", call.item.Release)
	}
	if call.item.Tags["service"] != "billing" {
		t.Fatalf("tags = %v", call.item.Tags)
	}
	if string(call.item.Row.Payload) == "" {
		t.Fatal("payload not carried into the dispatched item")
	}
	if !call.item.Row.ReceivedAt.Equal(cacheBase) {
		t.Fatalf("received_at = %v, want %v", call.item.Row.ReceivedAt, cacheBase)
	}
}

func TestEnvelopeDoesNotDispatchDuplicates(t *testing.T) {
	handler, recorder := newRecordingHandler(t, &fakeKeyStore{info: generousKey()})
	payload := sentryEventEnvelope(exceptionEventJSON(validUUID))

	first := httptest.NewRecorder()
	handler.ServeSentry(first, sentryRequest("1", testPublicKey, payload))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200: %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.ServeSentry(second, sentryRequest("1", testPublicKey, payload))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200: %s", second.Code, second.Body.String())
	}

	if calls := recorder.snapshot(); len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1 (duplicate not dispatched)", len(calls))
	}
}

func TestEnvelopeErrorPathsNeverDispatch(t *testing.T) {
	tooMany := make([]string, int(MaxEvents)+1)
	for index := range tooMany {
		tooMany[index] = validSentryEventJSON(distinctUUID(index))
	}
	for _, test := range []struct {
		name    string
		store   KeyStore
		key     string
		body    string
		wantErr int
	}{
		{"unauthorized missing key", &fakeKeyStore{info: generousKey()}, "", sentryEventEnvelope(validSentryEventJSON(validUUID)), http.StatusUnauthorized},
		{"unauthorized unknown key", &fakeKeyStore{err: ErrKeyNotFound}, "ghost", sentryEventEnvelope(validSentryEventJSON(validUUID)), http.StatusUnauthorized},
		{"too large", &fakeKeyStore{info: generousKey()}, testPublicKey, sentryEventEnvelope(tooMany...), http.StatusRequestEntityTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, recorder := newRecordingHandler(t, test.store)
			response := httptest.NewRecorder()
			handler.ServeSentry(response, sentryRequest("1", test.key, test.body))
			if response.Code != test.wantErr {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.wantErr, response.Body.String())
			}
			if calls := recorder.snapshot(); len(calls) != 0 {
				t.Fatalf("dispatch calls = %d, want 0 on error path", len(calls))
			}
		})
	}
}

func TestEnvelopeRateLimitNeverDispatches(t *testing.T) {
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1, RateLimitBurst: 1}}
	handler, recorder := newRecordingHandler(t, store)

	first := httptest.NewRecorder()
	handler.ServeSentry(first, sentryRequest("1", testPublicKey, sentryEventEnvelope(validSentryEventJSON(validUUID))))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200: %s", first.Code, first.Body.String())
	}

	limited := httptest.NewRecorder()
	handler.ServeSentry(limited, sentryRequest("1", testPublicKey, sentryEventEnvelope(validSentryEventJSON(distinctUUID(0)))))
	if limited.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", limited.Code)
	}

	// Only the first (accepted) event dispatched; the rate-limited one did not.
	if calls := recorder.snapshot(); len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1", len(calls))
	}
}

// TestZeroEventEnvelopeStillCostsRateLimitBudget pins the fix for envelopes
// that carry no "event" item — transaction-only, session, or a hand-crafted
// header-only body. rate.Limiter.ReserveN(now, 0) always succeeds with zero
// delay, so charging len(parsed.Events) directly let these requests bypass
// the limiter entirely even though a transaction still dispatches spans and,
// in durable mode, a zero-event envelope still costs a spill write.
func TestZeroEventEnvelopeStillCostsRateLimitBudget(t *testing.T) {
	store := &fakeKeyStore{info: KeyInfo{ProjectID: 1, PublicKey: testPublicKey, RateLimitRPS: 1, RateLimitBurst: 1}}
	handler, _ := newRecordingHandler(t, store)

	// sentryEventEnvelope() with no event payloads is a zero-item envelope:
	// just the header line, no "event" (or any other) item following it.
	first := httptest.NewRecorder()
	handler.ServeSentry(first, sentryRequest("1", testPublicKey, sentryEventEnvelope()))
	if first.Code != http.StatusOK {
		t.Fatalf("first status = %d, want 200: %s", first.Code, first.Body.String())
	}

	second := httptest.NewRecorder()
	handler.ServeSentry(second, sentryRequest("1", testPublicKey, sentryEventEnvelope()))
	if second.Code != http.StatusTooManyRequests {
		t.Fatalf("second status = %d, want 429: a zero-event envelope must still cost at least one rate-limit token", second.Code)
	}
}

const transactionWithChildSpanJSON = `{"transaction":"GET /checkout","start_timestamp":"2026-07-15T12:00:00Z",` +
	`"timestamp":"2026-07-15T12:00:01Z",` +
	`"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"a1b2c3d4e5f60718","status":"ok"}},` +
	`"spans":[{"span_id":"b2c3d4e5f6071829","parent_span_id":"a1b2c3d4e5f60718","op":"db.query",` +
	`"description":"SELECT * FROM orders","start_timestamp":"2026-07-15T12:00:00.100Z",` +
	`"timestamp":"2026-07-15T12:00:00.400Z","status":"ok"}]}`

func TestEnvelopeDispatchesSpans(t *testing.T) {
	recorder := &dispatchRecorder{}
	spans := &spanRecorder{}
	handler := NewHandler(&fakeKeyStore{info: generousKey()}, (&testClock{now: cacheBase}).Now, func(error) {}, recorder.dispatch, spans.dispatch)

	payload := sentryEventAndTransactionEnvelope(exceptionEventJSON(validUUID), transactionWithChildSpanJSON)
	response := httptest.NewRecorder()
	handler.ServeSentry(response, sentryRequest("1", testPublicKey, payload))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}

	calls := spans.snapshot()
	if len(calls) != 1 {
		t.Fatalf("span dispatch calls = %d, want 1", len(calls))
	}
	if calls[0].projectID != generousKey().ProjectID {
		t.Fatalf("project = %d", calls[0].projectID)
	}
	if len(calls[0].spans) != 2 {
		t.Fatalf("spans = %+v, want a root plus one child", calls[0].spans)
	}
	root, child := calls[0].spans[0], calls[0].spans[1]
	if root.SpanID != "a1b2c3d4e5f60718" {
		t.Fatalf("root span = %+v", root)
	}
	if child.SpanID != "b2c3d4e5f6071829" || child.ParentSpanID == nil || *child.ParentSpanID != "a1b2c3d4e5f60718" {
		t.Fatalf("child span = %+v", child)
	}
	for _, span := range calls[0].spans {
		if span.ProjectID != generousKey().ProjectID || span.RequestID != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("span row not stamped: %+v", span)
		}
	}
}

// ipIdentityEventJSON is a Sentry event item payload whose only user field is
// ip_address, alongside enough unrelated fields (level, message, release,
// tags) that TestHandlerDropsAnIPIdentityWhenTheProjectHasNotOptedIn can prove
// the toggle touches only the identity.
func ipIdentityEventJSON(eventID string) string {
	return `{"event_id":"` + eventID + `","level":"error","message":"boom",` +
		`"release":"billing@1.0.0","tags":{"service":"billing"},` +
		`"user":{"ip_address":"203.0.113.5"}}`
}

// idIdentityEventJSON pairs the same unrelated fields with an id-keyed
// identity, for the test proving the toggle leaves non-ip identities alone.
func idIdentityEventJSON(eventID string) string {
	return `{"event_id":"` + eventID + `","level":"error","message":"boom",` +
		`"release":"billing@1.0.0","tags":{"service":"billing"},` +
		`"user":{"id":"user-42"}}`
}

// TestHandlerDropsAnIPIdentityWhenTheProjectHasNotOptedIn pins the gate: an
// ip-keyed identity survives only when the project's key snapshot says
// StoreUserIP, and is cleared otherwise — without ever costing the event its
// place in the batch.
func TestHandlerDropsAnIPIdentityWhenTheProjectHasNotOptedIn(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		storeUserIP bool
		wantUser    bool
	}{
		{"opted in keeps the ip identity", true, true},
		{"default drops the ip identity", false, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			recorder := &dispatchRecorder{}
			settings := func(context.Context, int64) (ProjectSettings, error) {
				return ProjectSettings{StoreUserIP: testCase.storeUserIP}, nil
			}
			handler := NewHandlerWithOptions(HandlerOptions{
				Store: &fakeKeyStore{info: generousKey()}, Now: (&testClock{now: cacheBase}).Now,
				Dispatch: recorder.dispatch, Settings: settings,
			})
			eventPayload := ipIdentityEventJSON(validUUID)

			response := httptest.NewRecorder()
			handler.ServeSentry(response, sentryRequest("1", testPublicKey, sentryEventEnvelope(eventPayload)))
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
			}

			calls := recorder.snapshot()
			if len(calls) != 1 {
				t.Fatalf("dispatch calls = %d, want 1 (the event still counts once)", len(calls))
			}
			item := calls[0].item

			if testCase.wantUser {
				if item.User == nil || item.User.Key != userKeyIP || item.User.Value != "203.0.113.5" {
					t.Fatalf("user = %+v, want {Key: ip, Value: 203.0.113.5}", item.User)
				}
			} else if item.User != nil {
				t.Fatalf("user = %+v, want nil (project has not opted into store_user_ip)", item.User)
			}

			// Every other decoded field must be identical to an independent
			// decode of the same payload — a reference the toggle never
			// touches — so an implementation that dropped the whole event or
			// blanked unrelated fields would fail here regardless of which
			// case it broke.
			decoded, err := MapSentryEvent([]byte(eventPayload), cacheBase)
			if err != nil {
				t.Fatalf("decode reference: %v", err)
			}
			expected := grouping.Fingerprint(decoded.groupingInput())
			if item.Title != expected.Title || item.Culprit != expected.Culprit {
				t.Fatalf("title/culprit = %q/%q, want %q/%q", item.Title, item.Culprit, expected.Title, expected.Culprit)
			}
			if item.Row.Fingerprint != expected.Fingerprint {
				t.Fatalf("fingerprint = %q, want %q", item.Row.Fingerprint, expected.Fingerprint)
			}
			if item.Row.EventID != decoded.EventID {
				t.Fatalf("event id = %v, want %v", item.Row.EventID, decoded.EventID)
			}
			if item.Row.Level != decoded.Level || item.Row.Environment != decoded.Environment || item.Row.Message != decoded.Message {
				t.Fatalf("level/environment/message = %q/%q/%q, want %q/%q/%q",
					item.Row.Level, item.Row.Environment, item.Row.Message, decoded.Level, decoded.Environment, decoded.Message)
			}
			if item.Release != "billing@1.0.0" || item.Row.Release == nil || *item.Row.Release != "billing@1.0.0" {
				t.Fatalf("release = %q / %v, want billing@1.0.0", item.Release, item.Row.Release)
			}
			if item.Tags["service"] != "billing" {
				t.Fatalf("tags = %v, want service=billing", item.Tags)
			}
			if string(item.Row.Payload) != string(decoded.Payload) {
				t.Fatalf("payload = %q, want %q", item.Row.Payload, decoded.Payload)
			}
			if !item.Row.ReceivedAt.Equal(cacheBase) {
				t.Fatalf("received_at = %v, want %v", item.Row.ReceivedAt, cacheBase)
			}
		})
	}
}

// TestHandlerKeepsNonIPIdentitiesRegardlessOfTheToggle guards against a gate
// that drops every identity instead of only ip-keyed ones: an id identity
// must survive even with the project opted out of store_user_ip.
func TestHandlerKeepsNonIPIdentitiesRegardlessOfTheToggle(t *testing.T) {
	recorder := &dispatchRecorder{}
	settings := func(context.Context, int64) (ProjectSettings, error) { return ProjectSettings{}, nil }
	handler := NewHandlerWithOptions(HandlerOptions{
		Store: &fakeKeyStore{info: generousKey()}, Now: (&testClock{now: cacheBase}).Now,
		Dispatch: recorder.dispatch, Settings: settings,
	})

	response := httptest.NewRecorder()
	handler.ServeSentry(response, sentryRequest("1", testPublicKey, sentryEventEnvelope(idIdentityEventJSON(validUUID))))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}

	calls := recorder.snapshot()
	if len(calls) != 1 {
		t.Fatalf("dispatch calls = %d, want 1", len(calls))
	}
	user := calls[0].item.User
	if user == nil || user.Key != userKeyID || user.Value != "user-42" {
		t.Fatalf("user = %+v, want {Key: id, Value: user-42}", user)
	}
}

func TestEnvelopeWithoutSpansSkipsSpanDispatch(t *testing.T) {
	recorder := &dispatchRecorder{}
	spans := &spanRecorder{}
	handler := NewHandler(&fakeKeyStore{info: generousKey()}, (&testClock{now: cacheBase}).Now, func(error) {}, recorder.dispatch, spans.dispatch)

	response := httptest.NewRecorder()
	handler.ServeSentry(response, sentryRequest("1", testPublicKey, sentryEventEnvelope(exceptionEventJSON(validUUID))))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	if len(spans.snapshot()) != 0 {
		t.Fatalf("span dispatch should be skipped when no spans present")
	}
}
