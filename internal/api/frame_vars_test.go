package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/sourcemap"
)

func TestLatestEventSerializesFrameVariablesUnderTheVarsKey(t *testing.T) {
	service := &stubIssueService{}
	service.latest = issue.EventDetail{
		EventID: uuid.New(),
		Payload: json.RawMessage(`{}`),
		SymbolicatedException: &sourcemap.Exception{Type: "KeyError", Frames: []sourcemap.Frame{
			{Generated: sourcemap.Position{Filename: "/app/shop/cart.py", Line: 88}, Vars: json.RawMessage(`{"user_id":42,"password":"[Filtered]"}`)},
			{Generated: sourcemap.Position{Filename: "/app/shop/views.py", Line: 12}},
		}},
	}
	recorder := httptest.NewRecorder()
	request := newIssueRequest(http.MethodGet, "/api/v1/issues/5/events/latest", "")
	request.SetPathValue("id", "5")

	NewIssueHandler(service, Options{}).LatestEvent(recorder, request)

	var body struct {
		Event struct {
			SymbolicatedException struct {
				Frames []map[string]json.RawMessage `json:"frames"`
			} `json:"symbolicated_exception"`
		} `json:"event"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	frames := body.Event.SymbolicatedException.Frames
	if len(frames) != 2 {
		t.Fatalf("frames = %s", recorder.Body.String())
	}
	if got := string(frames[0]["vars"]); got != `{"user_id":42,"password":"[Filtered]"}` {
		t.Fatalf(`frames[0]["vars"] = %s`, got)
	}
	if got, present := frames[1]["vars"]; !present || string(got) != "null" {
		t.Fatalf(`frames[1]["vars"] = %s (present %v), want null`, got, present)
	}
}
