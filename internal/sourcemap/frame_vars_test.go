package sourcemap

import (
	"encoding/json"
	"testing"
)

func exceptionWithFrameVars(vars string) json.RawMessage {
	return json.RawMessage(`{"exception":{"values":[{"type":"KeyError","value":"cart","stacktrace":{"frames":[` +
		`{"filename":"/app/shop/cart.py","function":"load","lineno":88,"in_app":true,"vars":` + vars + `}` +
		`]}}]}}`)
}

func TestResolveCarriesFrameVariablesFromTheStoredPayload(t *testing.T) {
	resolver := newTestResolver(t, &fakeMapRepository{maps: map[string]Map{}})

	got, err := resolver.Resolve(t.Context(), EventInput{ProjectID: 4, Payload: exceptionWithFrameVars(`{"user_id":42,"password":"[Filtered]"}`)})

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	var vars map[string]any
	if err := json.Unmarshal(got.Frames[0].Vars, &vars); err != nil {
		t.Fatalf("vars = %s: %v", got.Frames[0].Vars, err)
	}
	if vars["user_id"] != float64(42) || vars["password"] != "[Filtered]" || len(vars) != 2 {
		t.Fatalf("vars = %v", vars)
	}
}

func TestResolveCarriesFrameVariablesOnASymbolicatedFrame(t *testing.T) {
	repo := &fakeMapRepository{maps: map[string]Map{"web@1|/assets/app.js": fixtureMap(t, 1)}}
	exception := mappedException()
	exception["stacktrace"].(map[string]any)["frames"].([]any)[0].(map[string]any)["vars"] = map[string]any{"cart": nil}

	got, err := newTestResolver(t, repo).Resolve(t.Context(), EventInput{ProjectID: 4, Release: "web@1", Payload: browserPayload(t, exception)})

	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Frames[0].Original == nil || string(got.Frames[0].Vars) != `{"cart":null}` {
		t.Fatalf("frame = %+v vars=%s", got.Frames[0], got.Frames[0].Vars)
	}
}

func TestResolveDropsFrameVariablesThatAreNotAnObject(t *testing.T) {
	for _, vars := range []string{`null`, `{}`, `[1,2]`, `"text"`, `42`, ` { } `} {
		got, err := newTestResolver(t, &fakeMapRepository{}).Resolve(t.Context(), EventInput{ProjectID: 4, Payload: exceptionWithFrameVars(vars)})
		if err != nil {
			t.Fatalf("vars %s: Resolve: %v", vars, err)
		}
		if len(got.Frames) != 1 || got.Frames[0].Generated.Line != 88 {
			t.Fatalf("vars %s: the frame must still resolve: %+v", vars, got)
		}
		if got.Frames[0].Vars != nil {
			t.Fatalf("vars %s: kept %s", vars, got.Frames[0].Vars)
		}
	}
}
