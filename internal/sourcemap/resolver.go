package sourcemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	gosourcemap "github.com/go-sourcemap/sourcemap"
)

const (
	contextRadius       = 5
	maxContextLineBytes = 2 << 10
	maxContextFrames    = 20
)

type resolver struct {
	repo  Repository
	cache *ParsedMapCache
}

// NewResolver builds a read-time symbolication resolver backed by a source-map
// repository and a parsed-map cache.
func NewResolver(repo Repository, cache *ParsedMapCache) Resolver {
	return &resolver{repo: repo, cache: cache}
}

type payloadFrame struct {
	Filename string `json:"filename"`
	Function string `json:"function"`
	Lineno   *int   `json:"lineno"`
	Colno    *int   `json:"colno"`
	InApp    bool   `json:"in_app"`
}

type payloadStacktrace struct {
	Frames []payloadFrame `json:"frames"`
}

type payloadException struct {
	Type       string             `json:"type"`
	Value      string             `json:"value"`
	Stacktrace *payloadStacktrace `json:"stacktrace"`
	Cause      *payloadException  `json:"-"`
}

// payloadDebugImage deliberately declares no "type" field. Narrowing real SDK
// input on a "sourcemap" literal would drop every image whose type string the
// capture does not happen to carry; code_file and debug_id are the two values
// the index is built from and the only two read.
type payloadDebugImage struct {
	CodeFile string `json:"code_file"`
	DebugID  string `json:"debug_id"`
}

// debugIDIndex maps a normalized generated-file path to the debug id the SDK
// reported for it. Built once per Resolve: every frame of a stack usually
// names one of a handful of files, and rebuilding it per frame would reparse
// the same array for each.
func debugIDIndex(images []payloadDebugImage) map[string]string {
	if len(images) == 0 {
		return nil
	}
	index := make(map[string]string, len(images))
	for _, image := range images {
		if image.DebugID == "" || image.CodeFile == "" {
			continue
		}
		normalized, err := NormalizePath(image.CodeFile)
		if err != nil {
			continue
		}
		index[normalized] = image.DebugID
	}
	return index
}

// contextCandidate references a resolved in-app frame whose source context can
// be attached after the whole tree is built.
type contextCandidate struct {
	frames   []Frame
	index    int
	consumer *gosourcemap.Consumer
	source   string
	line     int
}

// resolution carries the per-Resolve state threaded through the exception tree.
// Only the first infrastructure failure is kept: every frame of a stack usually
// shares one generated file, so an unbounded slice would repeat the same cause
// once per frame without telling an operator anything more.
type resolution struct {
	candidates []contextCandidate
	debugIDs   map[string]string
	failure    error
}

func (s *resolution) recordFailure(err error) {
	if s.failure == nil {
		s.failure = err
	}
}

func (r *resolver) Resolve(ctx context.Context, in EventInput) (*Exception, error) {
	// debug_meta is decoded as raw bytes here, not into a typed field: a type
	// mismatch anywhere inside it must not fail this Unmarshal call, because
	// encoding/json reports a mismatch for the whole call even when the
	// sibling exception field decoded perfectly fine.
	var payload struct {
		Exception *ExceptionValues[payloadException] `json:"exception"`
		Threads   *ThreadValues[payloadStacktrace]   `json:"threads"`
		DebugMeta json.RawMessage                    `json:"debug_meta"`
	}
	if err := json.Unmarshal(in.Payload, &payload); err != nil {
		return nil, fmt.Errorf("parse event payload: %w", err)
	}
	if payload.Exception == nil || len(payload.Exception.Values) == 0 {
		return nil, nil
	}
	values := payload.Exception.Values
	if outermost := &values[len(values)-1]; outermost.Stacktrace == nil && !slices.ContainsFunc(values, hasStacktrace) {
		if thrower := payload.Threads.ThrowerStacktrace(); thrower != nil && len(thrower.Frames) <= MaxThrowerFrames {
			outermost.Stacktrace = thrower
		}
	}
	root := sentryExceptionChain(values)

	var state resolution
	state.debugIDs = parseDebugIDs(payload.DebugMeta)
	exception := r.resolveException(ctx, in, root, &state)
	attachContext(state.candidates)
	return exception, state.failure
}

// parseDebugIDs decodes debug_meta.images into a path->debug-id index.
// debug_meta is untrusted SDK input reported alongside an exception tree that
// may otherwise be perfectly well-formed, so any decode failure here — the
// wrong type for images, a non-object debug_meta, a malformed image entry —
// degrades to "no debug ids available" rather than failing Resolve outright.
func parseDebugIDs(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var meta struct {
		Images []payloadDebugImage `json:"images"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return nil
	}
	return debugIDIndex(meta.Images)
}

// sentryExceptionChain converts Sentry's oldest-first values array into the
// outermost-first linked chain resolveException walks, reversing each frame
// slice on the way: Sentry orders frames caller-to-callee, and every consumer
// here expects the error site first.
func sentryExceptionChain(values []payloadException) *payloadException {
	var head *payloadException
	for index := range values {
		value := values[index]
		if value.Stacktrace != nil {
			value.Stacktrace = &payloadStacktrace{Frames: reversedFrames(value.Stacktrace.Frames)}
		}
		value.Cause = head
		head = &value
	}
	return head
}

func hasStacktrace(value payloadException) bool {
	return value.Stacktrace != nil
}

func reversedFrames(frames []payloadFrame) []payloadFrame {
	reversed := make([]payloadFrame, 0, len(frames))
	for _, frame := range slices.Backward(frames) {
		reversed = append(reversed, frame)
	}
	return reversed
}

func (r *resolver) resolveException(ctx context.Context, in EventInput, source *payloadException, state *resolution) *Exception {
	exception := &Exception{Type: source.Type, Value: source.Value}

	if source.Stacktrace != nil {
		frames := make([]Frame, len(source.Stacktrace.Frames))
		for i, payloadFrame := range source.Stacktrace.Frames {
			frame, candidate := r.resolveFrame(ctx, in, payloadFrame, state)
			frames[i] = frame
			if candidate != nil {
				candidate.frames = frames
				candidate.index = i
				state.candidates = append(state.candidates, *candidate)
			}
		}
		exception.Frames = frames
	}

	if source.Cause != nil {
		exception.Cause = r.resolveException(ctx, in, source.Cause, state)
	}
	return exception
}

func (r *resolver) resolveFrame(ctx context.Context, in EventInput, source payloadFrame, state *resolution) (Frame, *contextCandidate) {
	frame := Frame{Generated: Position{
		Function: source.Function,
		Filename: source.Filename,
		InApp:    source.InApp,
	}}
	if source.Lineno != nil {
		frame.Generated.Line = *source.Lineno
	}
	if source.Colno != nil {
		frame.Generated.Column = *source.Colno
	}
	if source.Lineno == nil || source.Colno == nil || source.Filename == "" {
		return frame, nil
	}
	generatedPath, err := NormalizePath(source.Filename)
	if err != nil {
		return frame, nil
	}
	stored, ok := r.lookup(ctx, in, generatedPath, state)
	if !ok {
		return frame, nil
	}
	consumer, err := r.cache.Get(ctx, stored)
	if err != nil {
		state.recordFailure(fmt.Errorf("parse source map: %w", err))
		return frame, nil
	}
	// go-sourcemap expects a one-based line and zero-based column, and returns a
	// one-based line with a zero-based column.
	sourceFile, name, line, column, ok := consumer.Source(*source.Lineno, *source.Colno-1)
	if !ok {
		return frame, nil
	}
	function := name
	if function == "" {
		function = source.Function
	}
	frame.Original = &Position{
		Function: function,
		Filename: sourceFile,
		Line:     line,
		Column:   column + 1,
		InApp:    source.InApp,
	}
	if !source.InApp {
		return frame, nil
	}
	return frame, &contextCandidate{consumer: consumer, source: sourceFile, line: line}
}

// lookup resolves one generated path to a stored map, preferring the debug id
// the event reported for that file. The release+path fallback is skipped when
// the event carries no release: it cannot match, and issuing it once per frame
// would be a guaranteed miss per frame.
func (r *resolver) lookup(ctx context.Context, in EventInput, generatedPath string, state *resolution) (Map, bool) {
	if debugID, ok := state.debugIDs[generatedPath]; ok {
		stored, err := r.repo.FindByDebugID(ctx, in.ProjectID, debugID)
		if err == nil {
			return stored, true
		}
		if !errors.Is(err, ErrNotFound) {
			state.recordFailure(fmt.Errorf("find source map by debug id: %w", err))
			return Map{}, false
		}
	}
	if in.Release == "" {
		return Map{}, false
	}
	stored, err := r.repo.Find(ctx, in.ProjectID, in.Release, generatedPath)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			state.recordFailure(fmt.Errorf("find source map: %w", err))
		}
		return Map{}, false
	}
	return stored, true
}

// attachContext fills source context for at most maxContextFrames resolved
// in-app frames, preferring the innermost frames across the whole tree.
func attachContext(candidates []contextCandidate) {
	remaining := maxContextFrames
	for i := len(candidates) - 1; i >= 0 && remaining > 0; i-- {
		candidate := candidates[i]
		lines := sourceContext(candidate.consumer, candidate.source, candidate.line)
		if len(lines) == 0 {
			continue
		}
		candidate.frames[candidate.index].Context = lines
		remaining--
	}
}

func sourceContext(consumer *gosourcemap.Consumer, source string, line int) []ContextLine {
	content := consumer.SourceContent(source)
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return nil
	}

	start := max(line-contextRadius, 1)
	end := min(line+contextRadius, len(lines))

	contextLines := make([]ContextLine, 0, end-start+1)
	for n := start; n <= end; n++ {
		contextLines = append(contextLines, ContextLine{
			Line:  n,
			Text:  truncateLine(lines[n-1], maxContextLineBytes),
			Focus: n == line,
		})
	}
	return contextLines
}

func truncateLine(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
