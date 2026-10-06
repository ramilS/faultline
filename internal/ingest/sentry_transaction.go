package ingest

import (
	"encoding/json"
	"fmt"
	"math"
	"time"
)

type sentryTransaction struct {
	Name           string          `json:"transaction"`
	StartTimestamp json.RawMessage `json:"start_timestamp"`
	Timestamp      json.RawMessage `json:"timestamp"`
	Contexts       struct {
		Trace *struct {
			TraceID string `json:"trace_id"`
			SpanID  string `json:"span_id"`
			Status  string `json:"status"`
		} `json:"trace"`
	} `json:"contexts"`
	Spans []sentrySpan `json:"spans"`
}

type sentrySpan struct {
	SpanID         string          `json:"span_id"`
	ParentSpanID   *string         `json:"parent_span_id"`
	Op             string          `json:"op"`
	Description    string          `json:"description"`
	StartTimestamp json.RawMessage `json:"start_timestamp"`
	Timestamp      json.RawMessage `json:"timestamp"`
	Status         string          `json:"status"`
	Data           json.RawMessage `json:"data"`
}

// MapSentryTransaction converts a transaction event into the spans model. The
// trace id becomes request_id, which is what correlates these spans to the
// errors recorded during the same request.
func MapSentryTransaction(payload []byte) ([]Span, error) {
	var raw sentryTransaction
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("%w: transaction: %w", ErrInvalidEnvelope, err)
	}
	// A trace_id without a span_id is dropped exactly like a fully absent trace
	// context: the root span_id is the only id a child's parent_span_id can
	// point at, so a transaction that has one but not the other still leaves
	// every span unreachable — there is no root to correlate them under.
	if raw.Contexts.Trace == nil || raw.Contexts.Trace.TraceID == "" || raw.Contexts.Trace.SpanID == "" {
		return nil, nil
	}
	if len(raw.Spans)+1 > MaxSpans {
		return nil, fmt.Errorf("%w: more than %d spans", ErrTooLarge, MaxSpans)
	}
	requestID := raw.Contexts.Trace.TraceID
	if len(requestID) > MaxRequestIDBytes || len(raw.Contexts.Trace.SpanID) > MaxSpanIDBytes {
		return nil, fmt.Errorf("%w: trace identifiers", ErrInvalidEnvelope)
	}

	rootStart, rootEnd, err := sentrySpanBounds(raw.StartTimestamp, raw.Timestamp)
	if err != nil {
		return nil, err
	}
	name := raw.Name
	if name == "" {
		name = "transaction"
	}
	if len(name) > MaxSpanNameBytes {
		name = name[:MaxSpanNameBytes]
	}

	spans := make([]Span, 0, len(raw.Spans)+1)
	spans = append(spans, Span{
		RequestID:  requestID,
		SpanID:     raw.Contexts.Trace.SpanID,
		Name:       name,
		StartTime:  rootStart,
		DurationMS: durationMS(rootStart, rootEnd),
		Status:     sentrySpanStatus(raw.Contexts.Trace.Status),
		Attributes: json.RawMessage("{}"),
	})

	for _, child := range raw.Spans {
		mapped, err := mapSentryChildSpan(child, requestID)
		if err != nil {
			return nil, err
		}
		spans = append(spans, mapped)
	}
	return spans, nil
}

func mapSentryChildSpan(child sentrySpan, requestID string) (Span, error) {
	if child.SpanID == "" || len(child.SpanID) > MaxSpanIDBytes {
		return Span{}, fmt.Errorf("%w: span_id", ErrInvalidEnvelope)
	}
	if child.ParentSpanID != nil && (*child.ParentSpanID == "" || len(*child.ParentSpanID) > MaxSpanIDBytes) {
		return Span{}, fmt.Errorf("%w: parent_span_id", ErrInvalidEnvelope)
	}
	start, end, err := sentrySpanBounds(child.StartTimestamp, child.Timestamp)
	if err != nil {
		return Span{}, err
	}
	name := child.Description
	if name == "" {
		name = child.Op
	}
	if name == "" {
		name = "span"
	}
	if len(name) > MaxSpanNameBytes {
		name = name[:MaxSpanNameBytes]
	}
	attributes, err := validateSpanAttributes(child.Data)
	if err != nil {
		return Span{}, err
	}
	return Span{
		RequestID:    requestID,
		SpanID:       child.SpanID,
		ParentSpanID: child.ParentSpanID,
		Name:         name,
		StartTime:    start,
		DurationMS:   durationMS(start, end),
		Status:       sentrySpanStatus(child.Status),
		Attributes:   attributes,
	}, nil
}

func sentrySpanBounds(start, end json.RawMessage) (time.Time, time.Time, error) {
	zero := time.Time{}
	startTime, err := parseSentryTimestamp(start, zero)
	if err != nil {
		return zero, zero, err
	}
	endTime, err := parseSentryTimestamp(end, startTime)
	if err != nil {
		return zero, zero, err
	}
	return startTime, endTime, nil
}

func durationMS(start, end time.Time) float64 {
	elapsed := end.Sub(start).Seconds() * 1000
	if math.IsNaN(elapsed) || math.IsInf(elapsed, 0) || elapsed < 0 {
		return 0
	}
	return elapsed
}

// sentrySpanStatus narrows Sentry's status vocabulary to the three values the
// spans table stores. Anything unrecognised reads as an error: an unmapped
// status is far more likely to be a failure code than a success.
func sentrySpanStatus(status string) string {
	switch status {
	case "ok":
		return "ok"
	case "", "unset", "unknown":
		return "unset"
	default:
		return "error"
	}
}
