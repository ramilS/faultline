package ingest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/sourcemap"
)

// defaultSentryLevel is the level Sentry assigns when an event omits one.
const defaultSentryLevel = "error"

type sentryEvent struct {
	EventID     string                                    `json:"event_id"`
	Timestamp   json.RawMessage                           `json:"timestamp"`
	Level       string                                    `json:"level"`
	Environment string                                    `json:"environment"`
	Release     *string                                   `json:"release"`
	Logger      string                                    `json:"logger"`
	Message     json.RawMessage                           `json:"message"`
	Tags        map[string]json.RawMessage                `json:"tags"`
	User        *sentryUser                               `json:"user"`
	Exception   *sentryExceptionContainer                 `json:"exception"`
	Threads     *sourcemap.ThreadValues[sentryStacktrace] `json:"threads"`
	Contexts    struct {
		Trace *struct {
			TraceID string `json:"trace_id"`
		} `json:"trace"`
	} `json:"contexts"`
}

// MapSentryEvent projects a Sentry event payload onto the internal event. The
// payload is retained byte-for-byte: downstream readers (symbolication, the
// raw-payload view, and features not yet built) consume fields this mapper
// deliberately ignores.
func MapSentryEvent(payload []byte, receivedAt time.Time) (Event, error) {
	return mapSentryEventInEnvelope(payload, "", receivedAt)
}

// mapSentryEventInEnvelope is MapSentryEvent for an event that arrived in an
// envelope: the PHP SDK leaves event_id out of the item payload and carries it
// only in the envelope header, so the header's id stands in when the payload
// has none.
func mapSentryEventInEnvelope(payload []byte, envelopeEventID string, receivedAt time.Time) (Event, error) {
	var raw sentryEvent
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Event{}, fmt.Errorf("%w: event: %w", ErrInvalidEnvelope, err)
	}

	eventID := raw.EventID
	if eventID == "" {
		eventID = envelopeEventID
	}
	id, err := uuid.Parse(eventID)
	if err != nil {
		return Event{}, fmt.Errorf("%w: event_id", ErrInvalidEnvelope)
	}

	timestamp, err := parseSentryTimestamp(raw.Timestamp, receivedAt)
	if err != nil {
		return Event{}, err
	}

	level := raw.Level
	if level == "" {
		level = defaultSentryLevel
	}
	if !validLevel(level) {
		return Event{}, fmt.Errorf("%w: level", ErrInvalidEnvelope)
	}

	message := parseSentryMessage(raw.Message)
	if len(message) > MaxMessageBytes {
		return Event{}, fmt.Errorf("%w: message exceeds %d bytes", ErrInvalidEnvelope, MaxMessageBytes)
	}
	if len(raw.Tags) > MaxTags {
		return Event{}, fmt.Errorf("%w: more than %d tags", ErrInvalidEnvelope, MaxTags)
	}

	environment := raw.Environment
	if environment == "" {
		environment = DefaultEnvironment
	}

	var requestID *string
	if raw.Contexts.Trace != nil && raw.Contexts.Trace.TraceID != "" {
		trace := raw.Contexts.Trace.TraceID
		if len(trace) > MaxRequestIDBytes {
			return Event{}, fmt.Errorf("%w: trace_id", ErrInvalidEnvelope)
		}
		requestID = &trace
	}

	exception, err := mapSentryException(raw.Exception, raw.Threads)
	if err != nil {
		return Event{}, err
	}

	identity, err := raw.User.identity()
	if err != nil {
		return Event{}, err
	}

	return Event{
		EventID:     id,
		Timestamp:   timestamp,
		Level:       level,
		Message:     message,
		Environment: environment,
		Release:     raw.Release,
		RequestID:   requestID,
		Logger:      raw.Logger,
		Tags:        coerceSentryTags(raw.Tags),
		User:        identity,
		Exception:   exception,
		// bytes.Clone, not the parameter directly: payload aliases the envelope's
		// whole decompressed buffer (see sentry_envelope.go's readItemPayload), and
		// this field is retained on pipeline.Item past the request — an uncloned
		// slice would keep up to 20 MiB reachable for as long as this one event sits
		// on a shard's dispatch channel.
		Payload: bytes.Clone(payload),
	}, nil
}

// parseSentryTimestamp accepts both wire forms: an RFC 3339 string and a
// numeric Unix timestamp, which may be fractional.
func parseSentryTimestamp(raw json.RawMessage, fallback time.Time) (time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return fallback.UTC(), nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		parsed, err := time.Parse(time.RFC3339, text)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: timestamp", ErrInvalidEnvelope)
		}
		return parsed.UTC(), nil
	}
	var seconds float64
	if err := json.Unmarshal(raw, &seconds); err != nil {
		return time.Time{}, fmt.Errorf("%w: timestamp", ErrInvalidEnvelope)
	}
	return time.Unix(0, int64(seconds*float64(time.Second))).UTC(), nil
}

// parseSentryMessage reads either a bare string or a LogEntry object, whose
// formatted rendering is preferred over its template. A message that is
// neither shape (a bare number or bool, say) is accepted as empty rather than
// rejected: ingestion already treats tag coercion and duration clamping this
// way, and a client quirk on a field this cosmetic should not fail the whole
// event when the exception/stacktrace it came with is still usable.
func parseSentryMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text
	}
	var entry struct {
		Formatted string `json:"formatted"`
		Message   string `json:"message"`
	}
	if err := json.Unmarshal(raw, &entry); err != nil {
		return ""
	}
	if entry.Formatted != "" {
		return entry.Formatted
	}
	return entry.Message
}

// coerceSentryTags flattens scalar tag values to strings and drops structured
// ones. SDKs are not consistent about tag types and we cannot fix them from
// here; dropping a container is better than storing "[object Object]".
func coerceSentryTags(raw map[string]json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	tags := make(map[string]string, len(raw))
	for key, value := range raw {
		if text, ok := scalarTagValue(value); ok {
			tags[key] = text
		}
	}
	if len(tags) == 0 {
		return nil
	}
	return tags
}

func scalarTagValue(raw json.RawMessage) (string, bool) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return "", false
	}
	switch trimmed[0] {
	case '{', '[', 'n':
		return "", false
	case '"':
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return "", false
		}
		return text, true
	default:
		return string(trimmed), true
	}
}

type sentryExceptionContainer struct {
	Values []sentryExceptionValue `json:"values"`
}

// UnmarshalJSON accepts both wire shapes of the exception interface: the
// {"values": [...]} object and the bare array that the Go SDK sends.
func (c *sentryExceptionContainer) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(trimmed, &c.Values)
	}
	type plain sentryExceptionContainer
	return json.Unmarshal(data, (*plain)(c))
}

type sentryExceptionValue struct {
	Type       string            `json:"type"`
	Value      string            `json:"value"`
	Stacktrace *sentryStacktrace `json:"stacktrace"`
}

type sentryStacktrace struct {
	Frames []sentryFrame `json:"frames"`
}

type sentryFrame struct {
	Module   string `json:"module"`
	Filename string `json:"filename"`
	Function string `json:"function"`
	InApp    bool   `json:"in_app"`
	Package  string `json:"package"`
	AddrMode string `json:"addr_mode"`
	// Addresses are raw so that a value of the wrong JSON type cannot fail the
	// whole event; they only refine grouping and are read by parseFrameAddress.
	ImageAddr       json.RawMessage `json:"image_addr"`
	SymbolAddr      json.RawMessage `json:"symbol_addr"`
	InstructionAddr json.RawMessage `json:"instruction_addr"`
}

// imageOffset is the frame's offset inside its native image, which unlike the
// load address survives a relaunch. It prefers symbol_addr, the function start,
// so two call sites in one function share an offset. It is absent unless every
// address needed is an absolute 0x-prefixed hex string and the image starts at
// or below the frame.
func (f sentryFrame) imageOffset() *uint64 {
	if f.AddrMode != "" && f.AddrMode != "abs" {
		return nil
	}
	frameAddr := f.SymbolAddr
	if len(frameAddr) == 0 || string(frameAddr) == "null" {
		frameAddr = f.InstructionAddr
	}
	frame, frameOK := parseFrameAddress(frameAddr)
	image, imageOK := parseFrameAddress(f.ImageAddr)
	if !frameOK || !imageOK || frame < image {
		return nil
	}
	offset := frame - image
	return &offset
}

func parseFrameAddress(raw json.RawMessage) (uint64, bool) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	digits, found := strings.CutPrefix(text, "0x")
	if !found {
		return 0, false
	}
	address, err := strconv.ParseUint(digits, 16, 64)
	return address, err == nil
}

// mapSentryException projects Sentry's exception chain onto the grouping input.
// Sentry sorts exception.values oldest-first, so the outermost exception — the
// one grouping keys on — is the last element. The rest of the chain is not part
// of the grouping basis, but every value's frame count is still bounded here:
// the whole chain stays in the stored payload verbatim, where symbolication
// walks every value's frames on each issue-detail render, so an unbounded
// earlier cause is a permanent per-view amplifier, not just a grouping gap.
//
// The Rust SDK attaches the stack of a captured error to the current thread, not
// to the exception; when no exception carries a stacktrace, the thrower's thread
// stands in.
func mapSentryException(container *sentryExceptionContainer, threads *sourcemap.ThreadValues[sentryStacktrace]) (*grouping.Exception, error) {
	if container == nil || len(container.Values) == 0 {
		return nil, nil
	}
	if len(container.Values) > MaxExceptionCauses+1 {
		return nil, fmt.Errorf("%w: more than %d exception causes", ErrInvalidEnvelope, MaxExceptionCauses)
	}
	for _, value := range container.Values {
		if value.Stacktrace != nil && len(value.Stacktrace.Frames) > MaxFrames {
			return nil, fmt.Errorf("%w: more than %d frames", ErrInvalidEnvelope, MaxFrames)
		}
	}

	outermost := container.Values[len(container.Values)-1]
	mapped := &grouping.Exception{Type: outermost.Type, Value: outermost.Value}
	stacktrace := outermost.Stacktrace
	if stacktrace == nil && !container.anyStacktrace() {
		stacktrace = threads.ThrowerStacktrace()
		if stacktrace != nil && len(stacktrace.Frames) > MaxFrames {
			return nil, fmt.Errorf("%w: more than %d frames", ErrInvalidEnvelope, MaxFrames)
		}
	}
	if stacktrace == nil {
		return mapped, nil
	}
	mapped.Frames = groupingFramesFromSentry(stacktrace.Frames)
	return mapped, nil
}

func (c *sentryExceptionContainer) anyStacktrace() bool {
	return slices.ContainsFunc(c.Values, func(value sentryExceptionValue) bool { return value.Stacktrace != nil })
}

// groupingFramesFromSentry flips Sentry's caller-to-callee order into the
// callee-first order the grouping package expects: it takes the leading frames
// and reads the first in-app frame as the culprit, so an unreversed slice would
// make every issue's culprit the process entry point.
func groupingFramesFromSentry(frames []sentryFrame) []grouping.Frame {
	mapped := make([]grouping.Frame, 0, len(frames))
	for _, frame := range slices.Backward(frames) {
		module := frame.Module
		if module == "" {
			module = frame.Filename
		}
		mapped = append(mapped, grouping.Frame{
			Module:      module,
			Function:    frame.Function,
			InApp:       frame.InApp,
			Package:     frame.Package,
			ImageOffset: frame.imageOffset(),
		})
	}
	return mapped
}
