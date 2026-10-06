package ingest

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/grouping"
)

// sentryEventWithChain builds an event whose exception.values run oldest-first,
// which is the order Sentry sends.
func sentryEventWithChain() []byte {
	return []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[
			{"type":"IOError","value":"disk gone"},
			{"type":"RepositoryError","value":"load failed"},
			{"type":"CheckoutError","value":"could not check out"}
		]}
	}`)
}

func TestMapSentryEventTakesTheLastExceptionValueAsOutermost(t *testing.T) {
	event, err := MapSentryEvent(sentryEventWithChain(), mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if event.Exception == nil {
		t.Fatal("expected an exception")
	}
	// Sentry sorts values oldest-first, so the error a user thinks of as "the"
	// error is the last one. Taking values[0] would title every issue with the
	// innermost cause.
	if event.Exception.Type != "CheckoutError" {
		t.Fatalf("got %q, want CheckoutError", event.Exception.Type)
	}
	if event.Exception.Value != "could not check out" {
		t.Fatalf("got %q", event.Exception.Value)
	}
}

func TestMapSentryEventReversesFrameOrder(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[{"type":"CheckoutError","value":"boom","stacktrace":{"frames":[
			{"module":"cmd","function":"main","in_app":true},
			{"module":"server","function":"handle","in_app":true},
			{"module":"checkout","function":"charge","in_app":true}
		]}}]}
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	// Sentry orders frames caller-to-callee, so the error site is last. Our
	// grouping takes the leading frames and the first in-app frame as culprit,
	// so an unreversed slice makes every culprit the process entry point.
	got := grouping.Fingerprint(grouping.GroupingInput{Exception: event.Exception})
	if got.Culprit != "checkout.charge" {
		t.Fatalf("got culprit %q, want checkout.charge", got.Culprit)
	}
}

// maxInAppFramesForTest mirrors grouping's unexported maxInAppFrames (16):
// groupingFrames keeps only the leading maxInAppFrames in-app frames, in
// callee-first order. A stack one frame deeper than this cap is what makes
// truncation itself the thing that would notice an unreversed frame list.
const maxInAppFramesForTest = 16

// withCallStack builds a Sentry-ordered (caller-to-callee) frame list: sixteen
// identical callers, then the varying error site last. Sentry order puts the
// site at the deepest index; only a correct reversal moves it to index 0,
// inside the cap, before groupingFrames truncates.
func withCallStack(site string) string {
	callers := make([]string, 0, maxInAppFramesForTest)
	for index := range maxInAppFramesForTest {
		callers = append(callers, fmt.Sprintf(`{"module":"pkg%d","function":"f%d","in_app":true}`, index, index))
	}
	frames := strings.Join(callers, ",") + fmt.Sprintf(`,{"module":"checkout","function":%q,"in_app":true}`, site)
	return `{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":[
		{"type":"CheckoutError","value":"boom","stacktrace":{"frames":[` + frames + `]}}]}}`
}

func TestFingerprintFollowsTheErrorSiteNotTheEntryPoint(t *testing.T) {
	first, err := MapSentryEvent([]byte(withCallStack("charge")), mapReceivedAt)
	if err != nil {
		t.Fatalf("map: %v", err)
	}
	second, err := MapSentryEvent([]byte(withCallStack("refund")), mapReceivedAt)
	if err != nil {
		t.Fatalf("map: %v", err)
	}

	firstPrint := grouping.Fingerprint(grouping.GroupingInput{Exception: first.Exception})
	secondPrint := grouping.Fingerprint(grouping.GroupingInput{Exception: second.Exception})

	// The stack is one frame deeper than grouping's in-app cap. Reversed
	// correctly, the site lands at index 0 and survives truncation, so the two
	// fingerprints differ. Left unreversed, the site lands at index 16 and is
	// the one frame truncation drops — both stacks truncate to the same 16
	// callers, and the fingerprints collapse to the same value.
	if firstPrint.Fingerprint == secondPrint.Fingerprint {
		t.Fatal("changing the error site must change the fingerprint; frames are reversed wrongly")
	}
}

func TestMapSentryEventFallsBackToFilenameWhenModuleIsAbsent(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":{"values":[{"type":"TypeError","value":"x is not a function","stacktrace":{"frames":[
			{"filename":"app://bundle.js","function":"render","in_app":true}
		]}}]}
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	// Browser SDKs send filename and no module; without the fallback every JS
	// frame would normalize to a bare function name and over-group.
	got := grouping.Fingerprint(grouping.GroupingInput{Exception: event.Exception})
	if got.Culprit != "app://bundle.js.render" {
		t.Fatalf("got culprit %q", got.Culprit)
	}
}

func TestMapSentryEventAcceptsAbsentException(t *testing.T) {
	for _, payload := range []string{
		`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc"}`,
		`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":[]}}`,
	} {
		event, err := MapSentryEvent([]byte(payload), mapReceivedAt)
		if err != nil {
			t.Fatalf("map %s: %v", payload, err)
		}
		if event.Exception != nil {
			t.Fatalf("expected no exception for %s", payload)
		}
	}
}

func TestMapSentryEventRejectsTooManyFrames(t *testing.T) {
	frames := make([]byte, 0, MaxFrames*40)
	frames = append(frames, '[')
	for index := 0; index <= MaxFrames; index++ {
		if index > 0 {
			frames = append(frames, ',')
		}
		frames = append(frames, []byte(`{"module":"m","function":"f"}`)...)
	}
	frames = append(frames, ']')
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":[{"type":"E","stacktrace":{"frames":` + string(frames) + `}}]}}`)

	_, err := MapSentryEvent(payload, mapReceivedAt)

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope", err)
	}
}

// TestMapSentryEventRejectsTooManyFramesInAnEarlierCause pins the fix for the
// lost per-value frame cap: the ported decoder checked MaxFrames only against
// the outermost (last) exception value. An earlier, non-outermost cause with
// an oversize stacktrace passed validation and was stored verbatim, then
// walked in full by symbolication on every issue-detail render — an accepted
// request becoming a permanent per-view amplifier. The cap must apply to
// every value in the chain, not just the one grouping reads.
func TestMapSentryEventRejectsTooManyFramesInAnEarlierCause(t *testing.T) {
	frames := make([]byte, 0, MaxFrames*40)
	frames = append(frames, '[')
	for index := 0; index <= MaxFrames; index++ {
		if index > 0 {
			frames = append(frames, ',')
		}
		frames = append(frames, []byte(`{"module":"m","function":"f"}`)...)
	}
	frames = append(frames, ']')
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":[` +
		`{"type":"Inner","value":"root cause","stacktrace":{"frames":` + string(frames) + `}},` +
		`{"type":"Outer","value":"wrapped"}` +
		`]}}`)

	_, err := MapSentryEvent(payload, mapReceivedAt)

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope (an earlier cause's oversize stacktrace must be rejected too)", err)
	}
}

// TestMapSentryEventAcceptsExactlyMaxFrames pins the accept side of the frame
// cap: a strict off-by-one (>= MaxFrames instead of > MaxFrames) would reject
// this legitimate event, and the reject-side tests alone cannot catch that.
func TestMapSentryEventAcceptsExactlyMaxFrames(t *testing.T) {
	frames := make([]byte, 0, MaxFrames*40)
	frames = append(frames, '[')
	for index := range MaxFrames {
		if index > 0 {
			frames = append(frames, ',')
		}
		frames = append(frames, []byte(`{"module":"m","function":"f"}`)...)
	}
	frames = append(frames, ']')
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":[{"type":"E","stacktrace":{"frames":` + string(frames) + `}}]}}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if len(event.Exception.Frames) != MaxFrames {
		t.Fatalf("got %d frames, want %d", len(event.Exception.Frames), MaxFrames)
	}
}

// TestMapSentryEventAcceptsExactlyMaxExceptionCausesPlusOne pins the accept
// side of the exception-chain cap: a strict off-by-one (dropping the +1, or
// using >= instead of >) would reject this legitimate chain, and the
// reject-side test alone cannot catch that.
func TestMapSentryEventAcceptsExactlyMaxExceptionCausesPlusOne(t *testing.T) {
	values := make([]byte, 0)
	values = append(values, '[')
	for index := range MaxExceptionCauses + 1 {
		if index > 0 {
			values = append(values, ',')
		}
		values = append(values, []byte(`{"type":"E","value":"v"}`)...)
	}
	values = append(values, ']')
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":` + string(values) + `}}`)

	_, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
}

func TestMapSentryEventRejectsTooManyExceptionValues(t *testing.T) {
	values := make([]byte, 0)
	values = append(values, '[')
	for index := 0; index <= MaxExceptionCauses+1; index++ {
		if index > 0 {
			values = append(values, ',')
		}
		values = append(values, []byte(`{"type":"E","value":"v"}`)...)
	}
	values = append(values, ']')
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":` + string(values) + `}}`)

	_, err := MapSentryEvent(payload, mapReceivedAt)

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope", err)
	}
}

func nativeFramePayload(frame string) []byte {
	return []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":{"values":[
		{"type":"EXC_BAD_ACCESS","value":"boom","stacktrace":{"frames":[` + frame + `]}}]}}`)
}

func nativeFingerprint(t *testing.T, frame string) grouping.Result {
	t.Helper()
	event, err := MapSentryEvent(nativeFramePayload(frame), mapReceivedAt)
	if err != nil {
		t.Fatalf("map %s: %v", frame, err)
	}
	return grouping.Fingerprint(grouping.GroupingInput{Exception: event.Exception})
}

func TestNativeFramesGroupByImageOffsetNotByLoadAddress(t *testing.T) {
	const pkg = `"package":"/private/var/x/MyApp.app/MyApp","in_app":true`
	first := nativeFingerprint(t, `{`+pkg+`,"image_addr":"0x104a50000","symbol_addr":"0x104a5c000","instruction_addr":"0x104a5c3f8"}`)
	relaunch := nativeFingerprint(t, `{`+pkg+`,"image_addr":"0x108b20000","symbol_addr":"0x108b2c000","instruction_addr":"0x108b2c41c"}`)
	otherFunction := nativeFingerprint(t, `{`+pkg+`,"image_addr":"0x104a50000","symbol_addr":"0x104a5d000","instruction_addr":"0x104a5d010"}`)

	if first.Culprit != "MyApp+0xc000" {
		t.Fatalf("culprit = %q, want MyApp+0xc000", first.Culprit)
	}
	if first.Fingerprint != relaunch.Fingerprint {
		t.Fatal("a relaunch that only moved the image must keep the fingerprint")
	}
	if first.Fingerprint == otherFunction.Fingerprint {
		t.Fatal("a different function offset must change the fingerprint")
	}
}

func TestNativeFrameFallsBackToInstructionAddress(t *testing.T) {
	result := nativeFingerprint(t, `{"package":"/x/MyApp","in_app":true,"image_addr":"0x1000","instruction_addr":"0x1a2c"}`)

	if result.Culprit != "MyApp+0xa2c" {
		t.Fatalf("culprit = %q, want MyApp+0xa2c", result.Culprit)
	}
}

func TestNativeFrameOffsetAcceptsExplicitAbsoluteMode(t *testing.T) {
	for _, test := range []struct {
		name  string
		frame string
	}{
		{"explicit abs addr_mode", `{"package":"/x/MyApp","in_app":true,"addr_mode":"abs","image_addr":"0x1000","symbol_addr":"0x2000"}`},
		{"null symbol_addr uses the instruction address", `{"package":"/x/MyApp","in_app":true,"image_addr":"0x1000","symbol_addr":null,"instruction_addr":"0x2000"}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeFingerprint(t, test.frame).Culprit; got != "MyApp+0x1000" {
				t.Fatalf("culprit = %q, want MyApp+0x1000", got)
			}
		})
	}
}

func TestNativeFrameWithoutUsableAddressesGroupsByImageAlone(t *testing.T) {
	for _, test := range []struct {
		name  string
		frame string
	}{
		{"missing image address", `{"package":"/x/MyApp","in_app":true,"symbol_addr":"0x1000"}`},
		{"relative addressing", `{"package":"/x/MyApp","in_app":true,"addr_mode":"rel:abc","image_addr":"0x1000","symbol_addr":"0x2000"}`},
		{"malformed symbol address", `{"package":"/x/MyApp","in_app":true,"image_addr":"0x1000","symbol_addr":"zzz","instruction_addr":"0x2000"}`},
		{"address without a prefix", `{"package":"/x/MyApp","in_app":true,"image_addr":"0x1000","symbol_addr":"2000"}`},
		{"symbol below the image", `{"package":"/x/MyApp","in_app":true,"image_addr":"0x2000","symbol_addr":"0x1000"}`},
		{"numeric address", `{"package":"/x/MyApp","in_app":true,"image_addr":4096,"symbol_addr":8192}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := nativeFingerprint(t, test.frame).Culprit; got != "MyApp" {
				t.Fatalf("culprit = %q, want MyApp", got)
			}
		})
	}
}

func TestNamedNativeFrameKeepsItsFunctionName(t *testing.T) {
	result := nativeFingerprint(t, `{"function":"viewDidLoad","package":"/x/MyApp","in_app":true,"image_addr":"0x1000","symbol_addr":"0x2000"}`)

	if result.Culprit != "viewDidLoad" {
		t.Fatalf("culprit = %q, want viewDidLoad", result.Culprit)
	}
}

func TestMapSentryEventAcceptsExceptionAsBareArray(t *testing.T) {
	payload := []byte(`{
		"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"exception":[
			{"type":"*errors.errorString","value":"disk gone"},
			{"type":"*main.CheckoutError","value":"could not check out","stacktrace":{"frames":[
				{"module":"main","function":"main","in_app":true},
				{"module":"main","function":"chargeCard","in_app":true}
			]}}
		]
	}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if event.Exception == nil || event.Exception.Type != "*main.CheckoutError" {
		t.Fatalf("got %+v, want the last array element as the outermost exception", event.Exception)
	}
	if len(event.Exception.Frames) != 2 {
		t.Fatalf("got %d frames, want 2", len(event.Exception.Frames))
	}
}

func TestMapSentryEventRejectsMalformedException(t *testing.T) {
	for _, exception := range []string{`"oops"`, `[1]`, `7`} {
		t.Run(exception, func(t *testing.T) {
			payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":` + exception + `}`)

			_, err := MapSentryEvent(payload, mapReceivedAt)

			if !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("got %v, want ErrInvalidEnvelope", err)
			}
		})
	}
}

func TestMapSentryEventRejectsTooManyFramesInABareArrayException(t *testing.T) {
	frames := strings.TrimSuffix(strings.Repeat(`{"module":"m","function":"f"},`, MaxFrames+1), ",")
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","exception":[{"type":"E","stacktrace":{"frames":[` + frames + `]}}]}`)

	_, err := MapSentryEvent(payload, mapReceivedAt)

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope", err)
	}
}

func TestMapSentryEventRejectsTooManyFramesInTheThrowerThread(t *testing.T) {
	frames := strings.Repeat(`{"function":"f"},`, MaxFrames) + `{"function":"f"}`
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",` +
		`"exception":{"values":[{"type":"E","value":"v"}]},` +
		`"threads":{"values":[{"stacktrace":{"frames":[` + frames + `]}}]}}`)

	_, err := MapSentryEvent(payload, mapReceivedAt)

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope", err)
	}
}
