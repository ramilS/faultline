package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/sourcemap"
)

// Envelope size and shape limits. Compressed and decompressed caps guard memory;
// the per-event caps bound the work a single admitted event can impose.
const (
	MaxCompressedBytes   = 2 << 20  // 2 MiB on the wire
	MaxDecompressedBytes = 20 << 20 // 20 MiB after gunzip
	MaxEvents            = 100
	MaxMessageBytes      = 8 << 10 // 8 KiB
	MaxFrames            = sourcemap.MaxThrowerFrames
	MaxTags              = 50
	MaxExceptionCauses   = 5
	// MaxUsersPerIssue bounds the distinct identities retained per issue. Past
	// the cap the aggregator stops inserting new rows and only increments ones
	// that already exist, so the count saturates instead of the table growing
	// without bound. Unlike tag values there is no "<other>" bucket: there is
	// no such person, so an inexact count is the honest degradation.
	MaxUsersPerIssue = 10000
	// MaxUserFieldBytes caps each of the four user fields. Over-long values are
	// rejected rather than truncated, because truncating an identity silently
	// merges two people into one counted user.
	MaxUserFieldBytes = 256

	MaxSpans              = 1000
	MaxSpanIDBytes        = 64
	MaxRequestIDBytes     = 256
	MaxSpanNameBytes      = 256
	MaxSpanAttributeBytes = 4 << 10 // 4 KiB of raw JSON per span
)

var (
	// ErrTooLarge reports that the envelope exceeded a size or count bound.
	ErrTooLarge = errors.New("envelope too large")
	// ErrInvalidEnvelope reports malformed JSON, spans, or invalid event fields.
	ErrInvalidEnvelope = errors.New("invalid envelope")
)

// RequestOptions carries per-request decoding hints from the HTTP boundary.
type RequestOptions struct {
	Gzip bool
}

// DefaultEnvironment labels events that omit an explicit environment.
const DefaultEnvironment = "production"

// Event is the validated subset of an envelope event retained after decoding,
// including the fields grouping and persistence consume and the byte-preserved
// raw event payload for JSONB storage.
type Event struct {
	EventID     uuid.UUID
	Timestamp   time.Time
	Level       string
	Message     string
	Environment string
	Release     *string
	RequestID   *string
	Logger      string
	Exception   *grouping.Exception
	Tags        map[string]string
	User        *EventUser
	Payload     json.RawMessage
}

// EventUser is the single identity an event contributes to its issue's
// distinct user count, already reduced to one key/value pair by the
// precedence rule in sentry_user.go.
type EventUser struct {
	Key   string
	Value string
}

// groupingInput adapts a decoded event to the input grouping.Fingerprint needs.
func (e Event) groupingInput() grouping.GroupingInput {
	return grouping.GroupingInput{Logger: e.Logger, Message: e.Message, Exception: e.Exception}
}

// groupingRuleInput adapts a decoded event to the input grouping.EvaluateRules
// needs for text-field matcher evaluation and template substitution.
func (e Event) groupingRuleInput() grouping.EvaluationInput {
	errorType := ""
	if e.Exception != nil {
		errorType = e.Exception.Type
	}
	return grouping.EvaluationInput{ErrorType: errorType, Message: e.Message, Logger: e.Logger, Tags: e.Tags}
}

// Span is the validated subset of an envelope span retained after decoding. It
// correlates to events by RequestID and preserves its attribute bytes verbatim
// for JSONB storage.
type Span struct {
	RequestID    string
	SpanID       string
	ParentSpanID *string
	Name         string
	StartTime    time.Time
	DurationMS   float64
	Status       string
	Attributes   json.RawMessage
}

// Envelope is the validated result of decoding one ingestion envelope.
type Envelope struct {
	Events []Event
	Spans  []Span
}

// validateSpanAttributes defaults an absent attributes object to "{}" and
// rejects a present value that is not a bounded JSON object.
func validateSpanAttributes(raw json.RawMessage) (json.RawMessage, error) {
	if len(raw) == 0 {
		return json.RawMessage("{}"), nil
	}
	if len(raw) > MaxSpanAttributeBytes {
		return nil, fmt.Errorf("%w: span attributes exceed %d bytes", ErrInvalidEnvelope, MaxSpanAttributeBytes)
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("%w: span attributes must be an object", ErrInvalidEnvelope)
	}
	return raw, nil
}

func validLevel(level string) bool {
	switch level {
	case "fatal", "error", "warning", "info", "debug":
		return true
	default:
		return false
	}
}

func wrapDecodeError(err error) error {
	if errors.Is(err, ErrTooLarge) {
		return ErrTooLarge
	}
	// The HTTP body cap trips before cappedReader's, and its error carries neither
	// ErrTooLarge nor a wrapped chain past this point — classify it here or a
	// genuinely oversize request reads back as malformed content.
	if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
		return ErrTooLarge
	}
	if errors.Is(err, ErrInvalidEnvelope) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrInvalidEnvelope, err)
}

// cappedReader fails once its byte budget is exhausted, letting the decoder
// surface a size violation as ErrTooLarge. The exceeded flag stays reliable
// even when an intermediate reader (such as gzip) rewrites the error.
type cappedReader struct {
	reader    io.Reader
	remaining int64
	exceeded  bool
}

func (c *cappedReader) Read(p []byte) (int, error) {
	if c.remaining <= 0 {
		c.exceeded = true
		return 0, ErrTooLarge
	}
	if int64(len(p)) > c.remaining {
		p = p[:c.remaining]
	}
	n, err := c.reader.Read(p)
	c.remaining -= int64(n)
	return n, err
}
