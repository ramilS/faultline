package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

var mapReceivedAt = time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)

func TestMapSentryEventReadsCoreFields(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"timestamp":"2026-08-08T10:00:00Z",
		"level":"warning",
		"environment":"staging",
		"release":"web@1.2.3",
		"logger":"checkout",
		"message":"card declined",
		"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}}
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if event.EventID.String() != "9ec79c33-ec99-42ab-8353-589fcb2e04dc" {
		t.Fatalf("got event id %s", event.EventID)
	}
	if !event.Timestamp.Equal(time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("got timestamp %s", event.Timestamp)
	}
	if event.Level != "warning" || event.Environment != "staging" || event.Logger != "checkout" {
		t.Fatalf("got %+v", event)
	}
	if event.Release == nil || *event.Release != "web@1.2.3" {
		t.Fatalf("got release %v", event.Release)
	}
	if event.Message != "card declined" {
		t.Fatalf("got message %q", event.Message)
	}
	if event.RequestID == nil || *event.RequestID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id must become request_id, got %v", event.RequestID)
	}
	if string(event.Payload) != string(payload) {
		t.Fatalf("the stored payload must be the Sentry event verbatim")
	}
}

// TestMapSentryEventPayloadIsIndependentOfTheInputSlice pins the fix for the
// aliasing regression: MapSentryEvent's payload parameter aliases the whole
// decompressed envelope buffer (see readItemPayload in sentry_envelope.go),
// and Event.Payload is retained on pipeline.Item well past this call — so
// without an explicit copy, one small event keeps a buffer up to 20 MiB
// reachable for as long as the event sits on a dispatch channel.
func TestMapSentryEventPayloadIsIndependentOfTheInputSlice(t *testing.T) {
	original := `{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","level":"error","message":"boom"}`
	buffer := []byte(original)

	event, err := MapSentryEvent(buffer, mapReceivedAt)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	// Simulate the buffer being reused/overwritten after the call returns, the
	// way a caller holding only the item slice (not a copy) would observe.
	for i := range buffer {
		buffer[i] = 'x'
	}

	if string(event.Payload) != original {
		t.Fatalf("Event.Payload changed after the input buffer was mutated (got %q); it must be an independent copy", event.Payload)
	}
}

func TestMapSentryEventAcceptsUnixTimestamp(t *testing.T) {
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","timestamp":1786312800.5,"level":"error"}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	want := time.Unix(1786312800, 500000000).UTC()
	if !event.Timestamp.Equal(want) {
		t.Fatalf("got %s, want %s", event.Timestamp, want)
	}
}

func TestMapSentryEventDefaultsAbsentFields(t *testing.T) {
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc"}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if event.Level != "error" {
		t.Fatalf("got level %q, want the Sentry default error", event.Level)
	}
	if event.Environment != DefaultEnvironment {
		t.Fatalf("got environment %q", event.Environment)
	}
	if !event.Timestamp.Equal(mapReceivedAt) {
		t.Fatalf("an absent timestamp must fall back to receipt time, got %s", event.Timestamp)
	}
	if event.RequestID != nil {
		t.Fatalf("got request id %v, want nil", event.RequestID)
	}
}

func TestMapSentryEventReadsStructuredMessage(t *testing.T) {
	cases := []struct {
		name    string
		message string
		want    string
	}{
		{"plain string", `"boom"`, "boom"},
		{"formatted wins", `{"message":"user %s failed","params":["ada"],"formatted":"user ada failed"}`, "user ada failed"},
		{"falls back to message", `{"message":"user %s failed","params":["ada"]}`, "user %s failed"},
		// Neither a string nor a LogEntry object: ingestion accepts the event
		// with an empty message rather than rejecting it over a cosmetic field.
		{"bare number is accepted as an empty message", `42`, ""},
		{"bare bool is accepted as an empty message", `true`, ""},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","message":` + testCase.message + `}`)

			event, err := MapSentryEvent(payload, mapReceivedAt)

			if err != nil {
				t.Fatalf("map: %v", err)
			}
			if event.Message != testCase.want {
				t.Fatalf("got %q, want %q", event.Message, testCase.want)
			}
		})
	}
}

func TestMapSentryEventCoercesTagValues(t *testing.T) {
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","tags":{"a":"one","b":2,"c":true,"d":{"x":1},"e":[1],"f":null}}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	want := map[string]string{"a": "one", "b": "2", "c": "true"}
	if len(event.Tags) != len(want) {
		t.Fatalf("got tags %v, want %v", event.Tags, want)
	}
	for key, value := range want {
		if event.Tags[key] != value {
			t.Fatalf("tag %q: got %q, want %q", key, event.Tags[key], value)
		}
	}
}

func TestMapSentryEventRejectsBadInput(t *testing.T) {
	oversizeMessageJSON, err := json.Marshal(strings.Repeat("a", MaxMessageBytes+1))
	if err != nil {
		t.Fatalf("marshal oversize message: %v", err)
	}

	oversizeTags := make(map[string]string, MaxTags+1)
	for i := 0; i <= MaxTags; i++ {
		oversizeTags[fmt.Sprintf("tag%d", i)] = "v"
	}
	oversizeTagsJSON, err := json.Marshal(oversizeTags)
	if err != nil {
		t.Fatalf("marshal oversize tags: %v", err)
	}

	oversizeTraceID := strings.Repeat("a", MaxRequestIDBytes+1)

	cases := []struct {
		name    string
		payload string
	}{
		{"not an object", `[]`},
		{"missing event id", `{"level":"error"}`},
		{"event id not hex", `{"event_id":"not-a-uuid"}`},
		{"unknown level", `{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","level":"catastrophe"}`},
		{"unparseable timestamp", `{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","timestamp":"yesterday"}`},
		{"message exceeds max bytes", fmt.Sprintf(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","message":%s}`, oversizeMessageJSON)},
		{"tags exceed max count", fmt.Sprintf(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","tags":%s}`, oversizeTagsJSON)},
		{"trace_id exceeds max bytes", fmt.Sprintf(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","contexts":{"trace":{"trace_id":%q}}}`, oversizeTraceID)},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := MapSentryEvent([]byte(testCase.payload), mapReceivedAt)

			if !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("got %v, want ErrInvalidEnvelope", err)
			}
		})
	}
}

func TestEnvelopeFromSentryItemsTakesEventIDFromEnvelopeHeaderWhenPayloadOmitsIt(t *testing.T) {
	envelope := SentryEnvelope{
		EventID: "cedd3cd83e2c41aaa60c45bc2b21cac2",
		Items:   []SentryItem{{Type: "event", Payload: []byte(`{"level":"error"}`)}},
	}

	parsed, err := EnvelopeFromSentryItems(envelope, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if got := parsed.Events[0].EventID.String(); got != "cedd3cd8-3e2c-41aa-a60c-45bc2b21cac2" {
		t.Fatalf("event id = %s, want the envelope header's", got)
	}
}

func TestEnvelopeFromSentryItemsPrefersThePayloadEventIDOverTheHeader(t *testing.T) {
	envelope := SentryEnvelope{
		EventID: "cedd3cd83e2c41aaa60c45bc2b21cac2",
		Items:   []SentryItem{{Type: "event", Payload: []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc"}`)}},
	}

	parsed, err := EnvelopeFromSentryItems(envelope, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if got := parsed.Events[0].EventID.String(); got != "9ec79c33-ec99-42ab-8353-589fcb2e04dc" {
		t.Fatalf("event id = %s, want the payload's", got)
	}
}

func TestEnvelopeFromSentryItemsRejectsAnEventWithNoEventIDAnywhere(t *testing.T) {
	envelope := SentryEnvelope{Items: []SentryItem{{Type: "event", Payload: []byte(`{"level":"error"}`)}}}

	_, err := EnvelopeFromSentryItems(envelope, mapReceivedAt)

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope", err)
	}
}

func TestMapSentryEventTakesFramesFromTheThreadWhenNoExceptionHasAStacktrace(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[{"type":"CheckoutError","value":"could not check out"}]},
		"threads":{"values":[{"id":"1","name":"main","stacktrace":{"frames":[
			{"function":"capture::main","in_app":true},
			{"function":"capture::charge_card","in_app":true},
			{"function":"sentry_core::hub::Hub::capture_error","in_app":false}
		]}}]}
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	frames := event.Exception.Frames
	if len(frames) != 3 || frames[0].Function != "sentry_core::hub::Hub::capture_error" || frames[2].Function != "capture::main" {
		t.Fatalf("frames = %+v, want the thread's frames callee-first", frames)
	}
}

func TestMapSentryEventPrefersTheExceptionStacktraceOverTheThreads(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[{"type":"E","value":"v","stacktrace":{"frames":[{"function":"own","in_app":true}]}}]},
		"threads":{"values":[{"stacktrace":{"frames":[{"function":"thread","in_app":true}]}}]}
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(event.Exception.Frames) != 1 || event.Exception.Frames[0].Function != "own" {
		t.Fatalf("frames = %+v, want the exception's own", event.Exception.Frames)
	}
}

func TestMapSentryEventIgnoresThreadStacktracesWhenSeveralThreadsHaveOneAndNoneCrashed(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[{"type":"E","value":"v"}]},
		"threads":{"values":[
			{"id":"1","stacktrace":{"frames":[{"function":"a","in_app":true}]}},
			{"id":"2","stacktrace":{"frames":[{"function":"b","in_app":true}]}}
		]}
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(event.Exception.Frames) != 0 {
		t.Fatalf("frames = %+v, want none: no thread is identifiable as the thrower", event.Exception.Frames)
	}
}

func TestMapSentryEventUsesTheCrashedThreadStacktrace(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[{"type":"E","value":"v"}]},
		"threads":{"values":[
			{"id":"1","stacktrace":{"frames":[{"function":"a","in_app":true}]}},
			{"id":"2","crashed":true,"stacktrace":{"frames":[{"function":"b","in_app":true}]}}
		]}
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(event.Exception.Frames) != 1 || event.Exception.Frames[0].Function != "b" {
		t.Fatalf("frames = %+v, want the crashed thread's", event.Exception.Frames)
	}
}

func TestMapSentryEventReadsThreadsAsABareArray(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[{"type":"E","value":"v"}]},
		"threads":[{"stacktrace":{"frames":[{"function":"a","in_app":true}]}}]
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(event.Exception.Frames) != 1 || event.Exception.Frames[0].Function != "a" {
		t.Fatalf("frames = %+v, want the thread's", event.Exception.Frames)
	}
}

func TestMapSentryEventIngestsAnEventWithMalformedThreads(t *testing.T) {
	for _, threads := range []string{`"x"`, `[1]`, `{"values":"x"}`} {
		payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":[{"type":"E","value":"v"}]},"threads":` + threads + `}`)

		event, err := MapSentryEvent(payload, mapReceivedAt)

		if err != nil {
			t.Fatalf("threads %s rejected the event: %v", threads, err)
		}
		if event.Exception == nil || len(event.Exception.Frames) != 0 {
			t.Fatalf("threads %s: exception = %+v, want it with no frames", threads, event.Exception)
		}
	}
}
