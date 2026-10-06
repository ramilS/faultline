package sourcemap

import (
	"encoding/json"
	"testing"
)

// goCapturedException is the exception member of a stored event captured from
// sentry-go: a bare array, oldest-first, with mechanism ids.
const goCapturedException = `[{"type":"*errors.errorString","value":"disk gone","mechanism":{"type":"chained","source":"unwrap","parent_id":0,"exception_id":1}},{"type":"*main.CheckoutError","value":"could not check out: disk gone","stacktrace":{"frames":[{"function":"main","module":"main","abs_path":"/work/main.go","lineno":30,"in_app":true},{"function":"runCheckout","module":"main","abs_path":"/work/main.go","lineno":23,"in_app":true},{"function":"chargeCard","module":"main","abs_path":"/work/main.go","lineno":18,"in_app":true}]},"mechanism":{"type":"generic","exception_id":0}}]`

func TestResolveAcceptsExceptionAsBareArrayFromTheGoSDK(t *testing.T) {
	resolver := newTestResolver(t, &fakeMapRepository{})
	payload := json.RawMessage(`{"exception":` + goCapturedException + `}`)

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Payload: payload})

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got == nil || got.Type != "*main.CheckoutError" {
		t.Fatalf("exception = %+v, want the outermost *main.CheckoutError", got)
	}
	if len(got.Frames) != 3 || got.Frames[0].Generated.Function != "chargeCard" {
		t.Fatalf("frames = %+v, want three frames with the error site first", got.Frames)
	}
	if got.Cause == nil || got.Cause.Type != "*errors.errorString" {
		t.Fatalf("cause = %+v", got.Cause)
	}
}

func TestExceptionValuesDecodesBothShapesAndRejectsOthers(t *testing.T) {
	for _, test := range []struct {
		name    string
		raw     string
		want    int
		wantErr bool
	}{
		{"values object", `{"values":[{"type":"A"},{"type":"B"}]}`, 2, false},
		{"bare array", `[{"type":"A"},{"type":"B"}]`, 2, false},
		{"empty array", `[]`, 0, false},
		{"empty object", `{}`, 0, false},
		{"values object ignores other keys", `{"values":[{"type":"A"}],"other":1}`, 1, false},
		{"null", `null`, 0, false},
		{"string", `"oops"`, 0, true},
		{"array of numbers", `[1]`, 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var decoded ExceptionValues[payloadException]

			err := json.Unmarshal([]byte(test.raw), &decoded)

			if (err != nil) != test.wantErr || (!test.wantErr && len(decoded.Values) != test.want) {
				t.Fatalf("got %d values, err %v; want %d values, error %v", len(decoded.Values), err, test.want, test.wantErr)
			}
		})
	}
}
