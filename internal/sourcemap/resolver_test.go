package sourcemap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type fakeMapRepository struct {
	maps       map[string]Map
	byDebugID  map[string]Map
	findErr    error
	calls      int
	debugCalls int
}

func (f *fakeMapRepository) Put(context.Context, int64, string, string, DecodedUpload) (Map, bool, error) {
	return Map{}, false, errors.New("unused")
}

func (f *fakeMapRepository) Find(_ context.Context, projectID int64, release, generatedPath string) (Map, error) {
	f.calls++
	if f.findErr != nil {
		return Map{}, f.findErr
	}
	m, ok := f.maps[release+"|"+generatedPath]
	if !ok {
		return Map{}, ErrNotFound
	}
	return m, nil
}

func (f *fakeMapRepository) FindByDebugID(_ context.Context, projectID int64, debugID string) (Map, error) {
	f.debugCalls++
	if f.findErr != nil {
		return Map{}, f.findErr
	}
	m, ok := f.byDebugID[debugIDKey(projectID, debugID)]
	if !ok {
		return Map{}, ErrNotFound
	}
	return m, nil
}

func (f *fakeMapRepository) PutBundle(context.Context, int64, string, []BundleEntry) error {
	return errors.New("unused")
}

// debugIDKey mirrors the real repository's project-scoped uniqueness: a debug
// id is only meaningful within the project that uploaded it, so the fake must
// key on both, not on the debug id alone. Keying on the debug id alone would
// let a resolver bug that passes the wrong project id go undetected by every
// test in this file.
func debugIDKey(projectID int64, debugID string) string {
	return fmt.Sprintf("%d|%s", projectID, debugID)
}

// browserPayload wraps a single exception in Sentry's values[] container —
// the wire shape events.payload actually stores — with one entry.
func browserPayload(t *testing.T, exception any) json.RawMessage {
	t.Helper()
	return sentryPayload(t, exception)
}

func mappedException() map[string]any {
	return map[string]any{
		"type":  "TypeError",
		"value": "cart is undefined",
		"stacktrace": map[string]any{
			"frames": []any{
				map[string]any{
					"filename": "https://cdn.test/assets/app.js",
					"function": "a",
					"lineno":   1,
					"colno":    100,
					"in_app":   true,
				},
			},
		},
	}
}

func newTestResolver(t *testing.T, repo Repository) Resolver {
	t.Helper()
	return NewResolver(repo, NewParsedMapCache(1<<20))
}

func TestResolveConvertsBrowserColumnsAndReturnsContext(t *testing.T) {
	repo := &fakeMapRepository{maps: map[string]Map{
		"web@1|/assets/app.js": fixtureMap(t, 1),
	}}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{
		ProjectID: 4,
		Release:   "web@1",
		Payload:   browserPayload(t, mappedException()),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || len(got.Frames) != 1 {
		t.Fatalf("Resolve = %#v", got)
	}
	frame := got.Frames[0]
	if frame.Generated.Line != 1 || frame.Generated.Column != 100 {
		t.Fatalf("generated position = %#v", frame.Generated)
	}
	if frame.Original == nil {
		t.Fatalf("expected symbolicated original, got nil")
	}
	if frame.Original.Filename != "src/checkout.ts" || frame.Original.Line != 42 || frame.Original.Column != 9 {
		t.Fatalf("original position = %#v", frame.Original)
	}
	if frame.Original.Function != "throwCheckoutError" {
		t.Fatalf("original function = %q", frame.Original.Function)
	}
	if len(frame.Context) == 0 {
		t.Fatalf("expected source context lines")
	}
	var focus *ContextLine
	for i := range frame.Context {
		if frame.Context[i].Focus {
			focus = &frame.Context[i]
		}
	}
	if focus == nil || focus.Line != 42 {
		t.Fatalf("focus line = %#v", focus)
	}
}

func TestResolvePreservesCauseChain(t *testing.T) {
	// Sentry expresses a cause chain as extra values[] entries, not a nested
	// "cause" pointer: values[0] is the oldest/innermost cause, and the root
	// (mappedException) is last.
	cause := map[string]any{
		"type":  "RangeError",
		"value": "root cause",
		"stacktrace": map[string]any{
			"frames": []any{
				map[string]any{
					"filename": "https://cdn.test/assets/app.js",
					"function": "b",
					"lineno":   1,
					"colno":    100,
					"in_app":   true,
				},
			},
		},
	}
	repo := &fakeMapRepository{maps: map[string]Map{
		"web@1|/assets/app.js": fixtureMap(t, 1),
	}}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: sentryPayload(t, cause, mappedException())})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Cause == nil || got.Cause.Type != "RangeError" {
		t.Fatalf("cause = %#v", got.Cause)
	}
	if got.Cause.Frames[0].Original == nil || got.Cause.Frames[0].Original.Line != 42 {
		t.Fatalf("cause frame original = %#v", got.Cause.Frames[0].Original)
	}
}

func TestResolveLeavesFrameUnmappedWhenMapAbsent(t *testing.T) {
	repo := &fakeMapRepository{maps: map[string]Map{}}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: browserPayload(t, mappedException())})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Frames[0].Original != nil {
		t.Fatalf("expected nil original for absent map, got %#v", got.Frames[0].Original)
	}
}

func TestResolveSymbolicatesWithoutContentThenNoContext(t *testing.T) {
	// A valid map with a mapping but no sourcesContent yields an original
	// position but no context lines.
	raw := []byte(`{"version":3,"file":"app.js","sources":["src/checkout.ts"],"names":["throwCheckoutError"],"mappings":"mGAyCQA"}`)
	m := gzipMap(t, 7, raw, int64(len(raw)))
	repo := &fakeMapRepository{maps: map[string]Map{"web@1|/assets/app.js": m}}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: browserPayload(t, mappedException())})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	original := got.Frames[0].Original
	if original == nil || original.Line != 42 {
		t.Fatalf("expected symbolicated original, got %#v", original)
	}
	if len(got.Frames[0].Context) != 0 {
		t.Fatalf("expected no context without sourcesContent, got %#v", got.Frames[0].Context)
	}
}

func TestResolveReturnsNilWithoutException(t *testing.T) {
	resolver := newTestResolver(t, &fakeMapRepository{maps: map[string]Map{}})
	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: json.RawMessage(`{"message":"no exception here"}`)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil exception, got %#v", got)
	}
}

// sentryPayload wraps values in the {"exception":{"values":[...]}} container
// Sentry sends, as opposed to browserPayload's single {"exception":{...}}.
func sentryPayload(t *testing.T, values ...any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"exception": map[string]any{"values": values},
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func TestResolveSymbolicatesSentryExceptionValues(t *testing.T) {
	// Sentry order: oldest-first for values, caller-to-callee for frames.
	// values[0] is the innermost/oldest cause; its frame is the (only) error site.
	innermost := map[string]any{
		"type":  "RangeError",
		"value": "root cause",
		"stacktrace": map[string]any{
			"frames": []any{
				map[string]any{
					"filename": "https://cdn.test/assets/app.js",
					"function": "b",
					"lineno":   1,
					"colno":    100,
					"in_app":   true,
				},
			},
		},
	}
	// values[1] is the outermost exception a user thinks of as "the error".
	// Its frames are caller-first, error-site-last in Sentry order: "callerFn"
	// (unmapped) then "a" (mapped by fixtureMap) at the tail.
	outermost := map[string]any{
		"type":  "TypeError",
		"value": "cart is undefined",
		"stacktrace": map[string]any{
			"frames": []any{
				map[string]any{
					"filename": "https://cdn.test/assets/caller.js",
					"function": "callerFn",
					"lineno":   5,
					"colno":    1,
					"in_app":   true,
				},
				map[string]any{
					"filename": "https://cdn.test/assets/app.js",
					"function": "a",
					"lineno":   1,
					"colno":    100,
					"in_app":   true,
				},
			},
		},
	}

	repo := &fakeMapRepository{maps: map[string]Map{
		"web@1|/assets/app.js": fixtureMap(t, 1),
	}}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{
		ProjectID: 4,
		Release:   "web@1",
		Payload:   sentryPayload(t, innermost, outermost),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil {
		t.Fatalf("expected resolved exception, got nil")
	}

	// The outermost exception is the LAST values[] entry, not the first.
	if got.Type != "TypeError" {
		t.Fatalf("outermost type = %q, want TypeError (values[len-1]); a wrong pick or reversed chain gives RangeError", got.Type)
	}
	if len(got.Frames) != 2 {
		t.Fatalf("Frames = %#v, want 2", got.Frames)
	}

	// The error site was last in Sentry's frame order; the resolved frame slice
	// must put it first. Only the error-site frame ("a") has a source map entry,
	// so its Original position is the discriminator between reversed/unreversed.
	errorSite := got.Frames[0]
	if errorSite.Generated.Function != "a" {
		t.Fatalf("frames[0] function = %q, want %q (error site first)", errorSite.Generated.Function, "a")
	}
	if errorSite.Original == nil || errorSite.Original.Line != 42 {
		t.Fatalf("frames[0] original = %#v, want symbolicated line 42", errorSite.Original)
	}
	caller := got.Frames[1]
	if caller.Generated.Function != "callerFn" {
		t.Fatalf("frames[1] function = %q, want %q (caller last)", caller.Generated.Function, "callerFn")
	}
	if caller.Original != nil {
		t.Fatalf("frames[1] original = %#v, want nil (no map entry for caller.js)", caller.Original)
	}

	// The cause chain must run outermost -> innermost, matching resolveException's
	// existing recursion.
	if got.Cause == nil || got.Cause.Type != "RangeError" {
		t.Fatalf("cause = %#v, want RangeError (values[0])", got.Cause)
	}
}

func TestResolveRejectsMalformedPayload(t *testing.T) {
	resolver := newTestResolver(t, &fakeMapRepository{maps: map[string]Map{}})
	_, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: json.RawMessage(`{not json`)})
	if err == nil {
		t.Fatalf("expected error for malformed payload")
	}
}

// TestResolveReportsRepositoryFailureButStillDegrades proves a genuine
// repository error (DB down, say) is surfaced to the caller via Resolve's
// error return, while the frame itself still degrades to an unmapped
// original position rather than aborting the whole exception tree.
func TestResolveReportsRepositoryFailureButStillDegrades(t *testing.T) {
	repo := &fakeMapRepository{findErr: errors.New("connection refused")}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: browserPayload(t, mappedException())})
	if err == nil {
		t.Fatalf("expected repository failure to be reported, got nil error")
	}
	if got == nil || len(got.Frames) != 1 {
		t.Fatalf("expected the exception to still be returned, got %#v", got)
	}
	if got.Frames[0].Original != nil {
		t.Fatalf("expected unmapped frame on repository failure, got %#v", got.Frames[0].Original)
	}
}

// TestResolveLeavesFrameUnmappedWhenMapAbsentDoesNotReportFailure documents
// the counterpart: an expected "no map for this release/path" miss is not an
// infrastructure failure and must not surface as a Resolve error, or every
// unmapped frame in production would look like an outage to an operator.
func TestResolveLeavesFrameUnmappedWhenMapAbsentDoesNotReportFailure(t *testing.T) {
	repo := &fakeMapRepository{maps: map[string]Map{}}
	resolver := newTestResolver(t, repo)

	_, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: browserPayload(t, mappedException())})
	if err != nil {
		t.Fatalf("expected ErrNotFound to degrade silently, got %v", err)
	}
}

// TestResolveReportsCacheFailureButStillDegrades proves a parse-time cache
// failure (corrupt or truncated stored map) is likewise surfaced rather than
// silently dropped, since it means the operator's map data is broken, not
// merely absent.
func TestResolveReportsCacheFailureButStillDegrades(t *testing.T) {
	corrupt := Map{ID: 1, CompressedMap: []byte("not gzip"), UncompressedBytes: 10}
	repo := &fakeMapRepository{maps: map[string]Map{"web@1|/assets/app.js": corrupt}}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: browserPayload(t, mappedException())})
	if err == nil {
		t.Fatalf("expected cache parse failure to be reported, got nil error")
	}
	if got == nil || got.Frames[0].Original != nil {
		t.Fatalf("expected unmapped frame on cache failure, got %#v", got)
	}
}

func TestResolveLimitsContextToTwentyInnermostFrames(t *testing.T) {
	frames := make([]any, 0, 25)
	for range 25 {
		frames = append(frames, map[string]any{
			"filename": "https://cdn.test/assets/app.js",
			"function": "f",
			"lineno":   1,
			"colno":    100,
			"in_app":   true,
		})
	}
	exception := map[string]any{
		"type":       "Error",
		"value":      "many frames",
		"stacktrace": map[string]any{"frames": frames},
	}
	repo := &fakeMapRepository{maps: map[string]Map{"web@1|/assets/app.js": fixtureMap(t, 1)}}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: browserPayload(t, exception)})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	withContext := 0
	for _, frame := range got.Frames {
		if len(frame.Context) > 0 {
			withContext++
		}
	}
	if withContext != 20 {
		t.Fatalf("expected 20 frames with context, got %d", withContext)
	}
	// The innermost frames (last in outer→inner order) must be the ones with context.
	if len(got.Frames[len(got.Frames)-1].Context) == 0 {
		t.Fatalf("innermost frame should have context")
	}
	if len(got.Frames[0].Context) != 0 {
		t.Fatalf("outermost frame should not have context when over the limit")
	}
}

func TestResolveWithoutReleaseReturnsUnmappedFrames(t *testing.T) {
	// Source maps are release-scoped, so an event with no release can never
	// match one. The frames it carries are still the point of the event, so
	// they come back unmapped rather than not at all — and the lookup that
	// could only miss is skipped.
	repo := &fakeMapRepository{}
	resolver := newTestResolver(t, repo)

	exception, err := resolver.Resolve(t.Context(), EventInput{
		ProjectID: 7,
		Release:   "",
		Payload:   browserPayload(t, mappedException()),
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if exception == nil {
		t.Fatal("expected an exception for a release-less event")
	}
	if len(exception.Frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(exception.Frames))
	}
	frame := exception.Frames[0]
	if frame.Original != nil {
		t.Fatalf("expected an unmapped frame, got original %#v", frame.Original)
	}
	if frame.Generated.Filename != "https://cdn.test/assets/app.js" || frame.Generated.Line != 1 {
		t.Fatalf("generated position = %#v", frame.Generated)
	}
	if repo.calls != 0 {
		t.Fatalf("repository lookups = %d, want 0 without a release", repo.calls)
	}
}

// debugMetaPayload wraps a single exception alongside a debug_meta.images[]
// entry, the shape captured from a real @sentry/browser event in
// internal/sentrycli/testdata/event-debug-meta.json.
func debugMetaPayload(t *testing.T, exception any, codeFile, debugID string) json.RawMessage {
	t.Helper()
	payload := map[string]any{
		"exception": map[string]any{"values": []any{exception}},
		"debug_meta": map[string]any{"images": []any{
			map[string]any{"type": "sourcemap", "code_file": codeFile, "debug_id": debugID},
		}},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return encoded
}

// A debug id in debug_meta must win over release+path, and must work with no
// release at all — that is the whole point of debug-id addressing.
func TestResolvePrefersDebugIDOverReleasePath(t *testing.T) {
	const debugID = "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f"
	repo := &fakeMapRepository{
		byDebugID: map[string]Map{debugIDKey(4, debugID): fixtureMap(t, 1)},
	}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{
		ProjectID: 4,
		Release:   "",
		Payload:   debugMetaPayload(t, mappedException(), "https://cdn.test/assets/app.js", debugID),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	original := got.Frames[0].Original
	if original == nil {
		t.Fatal("frame was not resolved by debug id")
	}
	if repo.calls != 0 {
		t.Fatalf("release+path lookups = %d, want 0", repo.calls)
	}
}

// An event whose debug id is unknown must still fall back to release+path —
// a partially migrated project has maps under both addresses.
func TestResolveFallsBackToReleasePathWhenDebugIDIsUnknown(t *testing.T) {
	repo := &fakeMapRepository{
		maps:      map[string]Map{"web@1|/assets/app.js": fixtureMap(t, 1)},
		byDebugID: map[string]Map{},
	}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{
		ProjectID: 4,
		Release:   "web@1",
		Payload:   debugMetaPayload(t, mappedException(), "https://cdn.test/assets/app.js", "aaaaaaaa-0000-0000-0000-000000000000"),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Frames[0].Original == nil {
		t.Fatal("frame was not resolved by the release+path fallback")
	}
	if repo.debugCalls != 1 {
		t.Fatalf("debug id lookups = %d, want 1", repo.debugCalls)
	}
}

// An event from a non-Sentry SDK carries no debug_meta and must not pay for a
// debug-id lookup on every frame.
func TestResolveSkipsDebugIDLookupWithoutDebugMeta(t *testing.T) {
	repo := &fakeMapRepository{maps: map[string]Map{"web@1|/assets/app.js": fixtureMap(t, 1)}}
	resolver := newTestResolver(t, repo)

	if _, err := resolver.Resolve(t.Context(), EventInput{
		ProjectID: 4, Release: "web@1", Payload: browserPayload(t, mappedException()),
	}); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if repo.debugCalls != 0 {
		t.Fatalf("debug id lookups = %d, want 0", repo.debugCalls)
	}
}

// TestResolveDebugIDLookupIsProjectScoped proves the resolver passes the
// event's own project id to FindByDebugID rather than, say, a zero value or
// some other project's id: a map stored under the debug id but a different
// project must miss and fall through to release+path, exactly as an unknown
// debug id would. Because fakeMapRepository.FindByDebugID now keys on
// projectID together with the debug id, a resolver that hard-coded
// or dropped the project id would make this map hit and skip the fallback —
// this test would then fail on repo.calls / the unmapped frame.
func TestResolveDebugIDLookupIsProjectScoped(t *testing.T) {
	const debugID = "1e2d0d0a-6d5f-4a2b-9d1e-0a0b0c0d0e0f"
	repo := &fakeMapRepository{
		// Stored under project 99, but the event below belongs to project 4.
		byDebugID: map[string]Map{debugIDKey(99, debugID): fixtureMap(t, 1)},
		maps:      map[string]Map{"web@1|/assets/app.js": fixtureMap(t, 1)},
	}
	resolver := newTestResolver(t, repo)

	got, err := resolver.Resolve(t.Context(), EventInput{
		ProjectID: 4,
		Release:   "web@1",
		Payload:   debugMetaPayload(t, mappedException(), "https://cdn.test/assets/app.js", debugID),
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if repo.debugCalls != 1 {
		t.Fatalf("debug id lookups = %d, want 1", repo.debugCalls)
	}
	if got.Frames[0].Original == nil {
		t.Fatal("expected the release+path fallback to resolve the frame")
	}
	if repo.calls != 1 {
		t.Fatalf("release+path lookups = %d, want 1 (fallback after cross-project miss)", repo.calls)
	}
}

// exceptionWithRawDebugMeta builds a payload with a well-formed exception (one
// frame, matching mappedException) alongside a caller-supplied raw debug_meta
// value. It is deliberately raw JSON rather than built through debugMetaPayload:
// the point is to exercise debug_meta shapes the typed Go builder cannot
// produce, exactly as a malformed or buggy SDK might send.
func exceptionWithRawDebugMeta(t *testing.T, rawDebugMeta string) json.RawMessage {
	t.Helper()
	payload := fmt.Sprintf(`{
		"exception": {"values": [{
			"type": "TypeError",
			"value": "cart is undefined",
			"stacktrace": {"frames": [{
				"filename": "https://cdn.test/assets/app.js",
				"function": "a",
				"lineno": 1,
				"colno": 100,
				"in_app": true
			}]}
		}]},
		"debug_meta": %s
	}`, rawDebugMeta)
	if !json.Valid([]byte(payload)) {
		t.Fatalf("invalid JSON test fixture (bad rawDebugMeta %q): %s", rawDebugMeta, payload)
	}
	return json.RawMessage(payload)
}

// A malformed debug_meta must never abort symbolication for the whole event:
// it is untrusted SDK input, and the exception tree beside it can be perfectly
// well-formed. Each case must still resolve via the release+path fallback,
// exactly as it would if debug_meta were absent entirely.
func TestResolveDegradesOnMalformedDebugMeta(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"images is not an array", `{"images":"not-an-array"}`},
		{"image element is not an object", `{"images":[123]}`},
		{"debug_id has the wrong type", `{"images":[{"debug_id":123}]}`},
		{"debug_meta itself is not an object", `"oops"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeMapRepository{maps: map[string]Map{"web@1|/assets/app.js": fixtureMap(t, 1)}}
			resolver := newTestResolver(t, repo)

			got, err := resolver.Resolve(t.Context(), EventInput{
				ProjectID: 4,
				Release:   "web@1",
				Payload:   exceptionWithRawDebugMeta(t, tc.raw),
			})
			if err != nil {
				t.Fatalf("Resolve returned %v; a malformed debug_meta must degrade to the release+path fallback, not fail the whole event", err)
			}
			if got == nil || len(got.Frames) != 1 {
				t.Fatalf("Resolve = %#v, want one resolved exception", got)
			}
			if got.Frames[0].Original == nil {
				t.Fatal("expected the release+path fallback to resolve the frame despite malformed debug_meta")
			}
		})
	}
}

// The debug_meta shape this resolver reads is not ours to define: it is what
// @sentry/browser puts on the wire. internal/sentrycli/testdata/ holds a
// capture of one, and this is the test that actually reads it — the
// hand-written debugMetaPayload cases above agree with our own reading of the
// shape on both sides and would keep passing if the SDK's real key names were
// something else entirely.
func TestResolveReadsDebugMetaFromTheCapturedBrowserEvent(t *testing.T) {
	const capturedDebugID = "003d8ac6-6080-5f78-8e37-8aa45ca5ee8a"
	const capturedCodeFile = "http://127.0.0.1:19000/app.js"

	debugMeta, err := os.ReadFile(filepath.Join("..", "sentrycli", "testdata", "event-debug-meta.json"))
	if err != nil {
		t.Fatalf("read captured debug_meta: %v", err)
	}
	exception, err := json.Marshal(map[string]any{
		"type":  "Error",
		"value": "faultline sourcemap fixture",
		"stacktrace": map[string]any{"frames": []any{map[string]any{
			"filename": capturedCodeFile, "function": "a", "lineno": 1, "colno": 100, "in_app": true,
		}}},
	})
	if err != nil {
		t.Fatalf("marshal exception: %v", err)
	}
	payload := json.RawMessage(`{"exception":{"values":[` + string(exception) + `]},"debug_meta":` + string(debugMeta) + `}`)

	repo := &fakeMapRepository{
		byDebugID: map[string]Map{debugIDKey(4, capturedDebugID): fixtureMap(t, 1)},
	}
	resolver := newTestResolver(t, repo)

	// No release: the release+path address cannot answer, so resolving at all
	// proves the debug id came out of the captured bytes.
	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Release: "", Payload: payload})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if repo.debugCalls != 1 {
		t.Fatalf("debug-id lookups = %d, want 1: the captured images[] entry was not read", repo.debugCalls)
	}
	if got == nil || len(got.Frames) != 1 || got.Frames[0].Original == nil {
		t.Fatalf("resolved = %#v, want one symbolicated frame", got)
	}
	if repo.calls != 0 {
		t.Fatalf("release+path lookups = %d, want 0", repo.calls)
	}
}
