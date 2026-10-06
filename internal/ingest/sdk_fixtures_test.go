package ingest

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/sourcemap"
)

// sdkExpectation is what we know independently of Faultline about the error a
// capture program throws, so an expectation never restates our own output.
type sdkExpectation struct {
	SDKName         string   `json:"sdk_name"`
	OuterType       string   `json:"outer_type"`
	CulpritContains string   `json:"culprit_contains"`
	CulpritExcludes []string `json:"culprit_excludes"`
}

func TestCapturedSDKEnvelopesTitleGroupAndBlameCorrectly(t *testing.T) {
	expectFiles, err := filepath.Glob(filepath.Join("testdata", "sdk", "*", "*.expect.json"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(expectFiles) == 0 {
		t.Fatal("no captured SDK fixtures under testdata/sdk")
	}

	for _, expectFile := range expectFiles {
		base := strings.TrimSuffix(expectFile, ".expect.json")
		t.Run(filepath.Base(base), func(t *testing.T) {
			var want sdkExpectation
			raw, err := os.ReadFile(expectFile)
			if err != nil {
				t.Fatalf("read expectation: %v", err)
			}
			if err := json.Unmarshal(raw, &want); err != nil {
				t.Fatalf("parse expectation: %v", err)
			}

			first, firstEventID := capturedEventPayload(t, base+".run1.envelope")
			second, secondEventID := capturedEventPayload(t, base+".run2.envelope")

			assertSDKWireShape(t, first, want)
			firstResult := groupCaptured(t, first, firstEventID)
			secondResult := groupCaptured(t, second, secondEventID)

			if !strings.HasPrefix(firstResult.Title, want.OuterType+": ") {
				t.Fatalf("title = %q, want it to start with %q", firstResult.Title, want.OuterType+": ")
			}
			if !strings.Contains(firstResult.Culprit, want.CulpritContains) {
				t.Fatalf("culprit = %q, want it to contain %q", firstResult.Culprit, want.CulpritContains)
			}
			for _, excluded := range want.CulpritExcludes {
				if strings.Contains(firstResult.Culprit, excluded) {
					t.Fatalf("culprit = %q, must not contain %q", firstResult.Culprit, excluded)
				}
			}
			if firstResult.Fingerprint != secondResult.Fingerprint {
				t.Fatalf("two runs of the same program fingerprint differently: a volatile frame detail leaks into the key")
			}
		})
	}
}

func capturedEventPayload(t *testing.T, path string) (payload []byte, envelopeEventID string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	envelope, err := parseSentryEnvelope(data)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	for _, item := range envelope.Items {
		if item.Type == "event" {
			return item.Payload, envelope.EventID
		}
	}
	t.Fatalf("%s has no event item", path)
	return nil, ""
}

func groupCaptured(t *testing.T, payload []byte, envelopeEventID string) grouping.Result {
	t.Helper()
	event, err := mapSentryEventInEnvelope(payload, envelopeEventID, mapReceivedAt)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	return grouping.Fingerprint(grouping.GroupingInput{Exception: event.Exception})
}

// assertSDKWireShape names the keys Faultline reads as literal strings against
// the raw payload. Decoding into our own types and reading them back would stay
// green through a rename on both sides.
func assertSDKWireShape(t *testing.T, payload []byte, want sdkExpectation) {
	t.Helper()
	var raw struct {
		SDK struct {
			Name string `json:"name"`
		} `json:"sdk"`
		Exception json.RawMessage `json:"exception"`
		Threads   json.RawMessage `json:"threads"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		t.Fatalf("payload is not JSON: %v", err)
	}
	if !strings.HasPrefix(raw.SDK.Name, want.SDKName) {
		t.Fatalf("sdk.name = %q, want prefix %q: the fixture is not from the intended SDK", raw.SDK.Name, want.SDKName)
	}
	values := exceptionValues(t, raw.Exception)
	if len(values) == 0 {
		t.Fatal("exception values are missing or empty")
	}
	outermost := values[len(values)-1]
	var outerType string
	if err := json.Unmarshal(outermost["type"], &outerType); err != nil || outerType != want.OuterType {
		t.Fatalf("last exception.values[].type = %q, want %q (Sentry orders oldest-first)", outerType, want.OuterType)
	}
	var stack wireStacktrace
	if err := json.Unmarshal(outermost["stacktrace"], &stack); err != nil || len(stack.Frames) == 0 {
		stack = threadStacktrace(t, raw.Threads)
	}
	if len(stack.Frames) == 0 {
		t.Fatal("neither the outermost exception nor a thread carries stacktrace.frames")
	}
	sawInApp := false
	for _, frame := range stack.Frames {
		if frame.InApp != nil && *frame.InApp {
			sawInApp = true
		}
	}
	if !sawInApp {
		t.Fatal("no frame carries in_app:true; grouping would fall back to the first frames of the stack")
	}
}

// exceptionValues reads the exception interface in either wire shape: the
// {"values": [...]} object or the bare array the Go SDK sends.
func exceptionValues(t *testing.T, raw json.RawMessage) []map[string]json.RawMessage {
	t.Helper()
	var values []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err == nil {
		return values
	}
	var object struct {
		Values []map[string]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(raw, &object); err != nil {
		t.Fatalf("exception is neither an array nor a {values} object: %v", err)
	}
	return object.Values
}

type wireStacktrace struct {
	Frames []struct {
		InApp *bool `json:"in_app"`
	} `json:"frames"`
}

// threadStacktrace reads the stack the Rust SDK attaches to a captured error:
// it sits on the thrower's thread, not on the exception.
func threadStacktrace(t *testing.T, raw json.RawMessage) wireStacktrace {
	t.Helper()
	var threads sourcemap.ThreadValues[wireStacktrace]
	if len(raw) == 0 {
		return wireStacktrace{}
	}
	if err := json.Unmarshal(raw, &threads); err != nil {
		t.Fatalf("threads: %v", err)
	}
	if stack := threads.ThrowerStacktrace(); stack != nil {
		return *stack
	}
	return wireStacktrace{}
}
