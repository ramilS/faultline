package sourcemap

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestThrowerStacktraceSelection(t *testing.T) {
	for _, test := range []struct {
		name string
		raw  string
		want string
	}{
		{"crashed thread wins", `{"values":[{"stacktrace":{"frames":[{"function":"a"}]}},{"crashed":true,"stacktrace":{"frames":[{"function":"b"}]}}]}`, "b"},
		{"single stacked thread", `{"values":[{"id":"1"},{"stacktrace":{"frames":[{"function":"a"}]}}]}`, "a"},
		{"several stacked and none crashed", `{"values":[{"stacktrace":{"frames":[{"function":"a"}]}},{"stacktrace":{"frames":[{"function":"b"}]}}]}`, ""},
		{"bare array", `[{"stacktrace":{"frames":[{"function":"a"}]}}]`, "a"},
		{"no thread has a stack", `{"values":[{"id":"1"}]}`, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var threads ThreadValues[payloadStacktrace]
			if err := json.Unmarshal([]byte(test.raw), &threads); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			got := threads.ThrowerStacktrace()

			if test.want == "" {
				if got != nil {
					t.Fatalf("got %+v, want none", got)
				}
				return
			}
			if got == nil || got.Frames[0].Function != test.want {
				t.Fatalf("got %+v, want frame %q", got, test.want)
			}
		})
	}
}

func TestThreadValuesNeverFailTheDecodeOfAMalformedInterface(t *testing.T) {
	for _, raw := range []string{`"x"`, `[1]`, `{"values":"x"}`, `null`} {
		var threads ThreadValues[payloadStacktrace]

		if err := json.Unmarshal([]byte(raw), &threads); err != nil {
			t.Fatalf("%s: unmarshal = %v, want nil", raw, err)
		}
		if threads.ThrowerStacktrace() != nil {
			t.Fatalf("%s: want no stacktrace", raw)
		}
	}
}

func TestResolveShowsTheThrowerThreadStackWhenTheExceptionCarriesNone(t *testing.T) {
	payload, err := os.ReadFile("testdata/rust_capture_error_event.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	resolver := newTestResolver(t, &fakeMapRepository{})

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Payload: payload})

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.Type != "CheckoutError" {
		t.Fatalf("exception = %+v, want the outermost CheckoutError", got)
	}
	inApp := map[string]bool{}
	for _, frame := range got.Frames {
		inApp[frame.Generated.Function] = frame.Generated.InApp
	}
	for _, function := range []string{"capture::charge_card", "capture::run_checkout"} {
		if !inApp[function] {
			t.Fatalf("frames lack an in-app %s: %+v", function, inApp)
		}
	}
	callee, caller := frameIndex(got.Frames, "capture::charge_card"), frameIndex(got.Frames, "capture::run_checkout")
	if callee >= caller {
		t.Fatalf("charge_card at %d must come before run_checkout at %d: the error site first", callee, caller)
	}
	if got.Cause == nil || got.Cause.Type != "Custom" || len(got.Cause.Frames) != 0 {
		t.Fatalf("cause = %+v, want the frameless Custom cause", got.Cause)
	}
}

func TestResolveKeepsTheExceptionsOwnStacktraceOverTheThreads(t *testing.T) {
	payload := json.RawMessage(`{"exception":{"values":[{"type":"E","stacktrace":{"frames":[{"function":"own","in_app":true}]}}]},` +
		`"threads":{"values":[{"stacktrace":{"frames":[{"function":"thread","in_app":true}]}}]}}`)
	resolver := newTestResolver(t, &fakeMapRepository{})

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Payload: payload})

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(got.Frames) != 1 || got.Frames[0].Generated.Function != "own" {
		t.Fatalf("frames = %+v, want the exception's own", got.Frames)
	}
}

func TestResolveIgnoresMalformedThreads(t *testing.T) {
	payload := json.RawMessage(`{"exception":{"values":[{"type":"E"}]},"threads":"x"}`)
	resolver := newTestResolver(t, &fakeMapRepository{})

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Payload: payload})

	if err != nil || got == nil || got.Type != "E" {
		t.Fatalf("got %+v, %v, want the exception despite malformed threads", got, err)
	}
}

func threadStackPayload(frameCount int) json.RawMessage {
	frames := strings.Repeat(`{"filename":"a.js","lineno":1,"colno":1},`, frameCount-1) +
		`{"filename":"a.js","lineno":1,"colno":1,"module":1}`
	return json.RawMessage(`{"exception":{"values":[{"type":"E"}]},"threads":{"values":[{"stacktrace":{"frames":[` + frames + `]}}]}}`)
}

func TestResolveDropsAThreadStackOverTheFrameCap(t *testing.T) {
	resolver := newTestResolver(t, &fakeMapRepository{})

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Payload: threadStackPayload(MaxThrowerFrames + 1)})

	if err != nil || got == nil || got.Type != "E" {
		t.Fatalf("got %+v, %v, want the exception", got, err)
	}
	if len(got.Frames) != 0 {
		t.Fatalf("got %d thread frames, want none for an over-cap stack", len(got.Frames))
	}
}

func TestResolveKeepsAThreadStackAtExactlyTheFrameCap(t *testing.T) {
	resolver := newTestResolver(t, &fakeMapRepository{})

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Payload: threadStackPayload(MaxThrowerFrames)})

	if err != nil || got == nil {
		t.Fatalf("got %+v, %v, want the exception", got, err)
	}
	if len(got.Frames) != MaxThrowerFrames {
		t.Fatalf("got %d thread frames, want %d", len(got.Frames), MaxThrowerFrames)
	}
}

func frameIndex(frames []Frame, function string) int {
	for index, frame := range frames {
		if frame.Generated.Function == function {
			return index
		}
	}
	return len(frames)
}
