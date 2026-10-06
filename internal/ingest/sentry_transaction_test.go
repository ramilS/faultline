package ingest

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const transactionPayload = `{
	"type":"transaction",
	"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
	"transaction":"GET /checkout",
	"start_timestamp":"2026-08-08T10:00:00Z",
	"timestamp":"2026-08-08T10:00:01.500Z",
	"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","status":"ok"}},
	"spans":[
		{"span_id":"b7ad6b7169203331","parent_span_id":"00f067aa0ba902b7","op":"db","description":"SELECT users",
		 "start_timestamp":"2026-08-08T10:00:00.100Z","timestamp":"2026-08-08T10:00:00.400Z",
		 "status":"internal_error","data":{"rows":3}}
	]
}`

func TestMapSentryTransactionBuildsRootAndChildSpans(t *testing.T) {
	spans, err := MapSentryTransaction([]byte(transactionPayload))

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want a root plus one child", len(spans))
	}

	root := spans[0]
	if root.RequestID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace_id must become request_id, got %q", root.RequestID)
	}
	if root.SpanID != "00f067aa0ba902b7" || root.ParentSpanID != nil {
		t.Fatalf("got root %+v", root)
	}
	if root.Name != "GET /checkout" {
		t.Fatalf("got root name %q", root.Name)
	}
	if !root.StartTime.Equal(time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("got root start %s", root.StartTime)
	}
	if root.DurationMS != 1500 {
		t.Fatalf("got root duration %v, want 1500", root.DurationMS)
	}
	if root.Status != "ok" {
		t.Fatalf("got root status %q", root.Status)
	}

	child := spans[1]
	if child.RequestID != root.RequestID {
		t.Fatalf("child must share the trace correlation key")
	}
	if child.ParentSpanID == nil || *child.ParentSpanID != "00f067aa0ba902b7" {
		t.Fatalf("got child parent %v", child.ParentSpanID)
	}
	if child.Name != "SELECT users" {
		t.Fatalf("description is preferred over op, got %q", child.Name)
	}
	if child.DurationMS != 300 {
		t.Fatalf("got child duration %v, want 300", child.DurationMS)
	}
	if string(child.Attributes) != `{"rows":3}` {
		t.Fatalf("got child attributes %s", child.Attributes)
	}
}

func TestMapSentryTransactionMapsSpanStatus(t *testing.T) {
	cases := []struct {
		sentry string
		want   string
	}{
		{"ok", "ok"},
		{"", "unset"},
		{"unset", "unset"},
		{"unknown", "unset"},
		{"internal_error", "error"},
		{"deadline_exceeded", "error"},
		{"permission_denied", "error"},
		{"cancelled", "error"},
	}

	for _, testCase := range cases {
		t.Run(testCase.sentry, func(t *testing.T) {
			// An unrecognised status is far likelier to be a failure code than a
			// success, so anything unmapped must read as an error.
			if got := sentrySpanStatus(testCase.sentry); got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestMapSentryTransactionFallsBackToOpForAnUnnamedSpan(t *testing.T) {
	payload := `{"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}},
		"transaction":"root","start_timestamp":0,"timestamp":1,
		"spans":[{"span_id":"b7ad6b7169203331","op":"http.client","start_timestamp":0,"timestamp":0.5}]}`

	spans, err := MapSentryTransaction([]byte(payload))

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if spans[1].Name != "http.client" {
		t.Fatalf("got %q, want the op as the fallback name", spans[1].Name)
	}
}

func TestMapSentryTransactionWithoutATraceContextYieldsNoSpans(t *testing.T) {
	spans, err := MapSentryTransaction([]byte(`{"transaction":"orphan","start_timestamp":0,"timestamp":1}`))

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	// request_id is the only thing correlating spans to events; without a
	// trace_id the rows would be unreachable, so dropping is the honest outcome.
	if len(spans) != 0 {
		t.Fatalf("got %d spans, want 0", len(spans))
	}
}

func TestMapSentryTransactionWithATraceIDButNoSpanIDYieldsNoSpans(t *testing.T) {
	spans, err := MapSentryTransaction([]byte(`{"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736"}},
		"transaction":"orphan","start_timestamp":0,"timestamp":1}`))

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	// A trace_id without a span_id is as unusable as no trace context at all:
	// the root span has no id to hand out as the parent correlation key for its
	// children, so this must drop silently, exactly like the fully absent case.
	if len(spans) != 0 {
		t.Fatalf("got %d spans, want 0 for a trace_id without a span_id", len(spans))
	}
}

func TestMapSentryTransactionClampsChildDurationWhenTimestampPrecedesStart(t *testing.T) {
	payload := `{"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}},
		"transaction":"root","start_timestamp":0,"timestamp":1,
		"spans":[{"span_id":"b7ad6b7169203331","op":"db","start_timestamp":5,"timestamp":1}]}`

	spans, err := MapSentryTransaction([]byte(payload))

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	// Clock skew between an SDK and its spans is real; a span whose reported end
	// precedes its start must not become a negative duration written to storage.
	if spans[1].DurationMS != 0 {
		t.Fatalf("got child duration %v, want 0 for a timestamp preceding start", spans[1].DurationMS)
	}
}

func TestMapSentryTransactionClampsRootDurationWhenTimestampPrecedesStart(t *testing.T) {
	payload := `{"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}},
		"transaction":"root","start_timestamp":5,"timestamp":1}`

	spans, err := MapSentryTransaction([]byte(payload))

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if spans[0].DurationMS != 0 {
		t.Fatalf("got root duration %v, want 0 for a timestamp preceding start", spans[0].DurationMS)
	}
}

func TestMapSentryTransactionRejectsTooManySpans(t *testing.T) {
	var builder strings.Builder
	builder.WriteString(`{"contexts":{"trace":{"trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7"}},`)
	builder.WriteString(`"transaction":"root","start_timestamp":0,"timestamp":1,"spans":[`)
	for index := range MaxSpans {
		if index > 0 {
			builder.WriteByte(',')
		}
		builder.WriteString(`{"span_id":"b7ad6b7169203331","op":"db","start_timestamp":0,"timestamp":0.5}`)
	}
	builder.WriteString(`]}`)

	// MaxSpans children plus the root is one over the cap, and the cap must trip
	// before the span slice is allocated.
	_, err := MapSentryTransaction([]byte(builder.String()))

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}
