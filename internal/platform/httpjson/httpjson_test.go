package httpjson_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ramilS/faultline/internal/platform/httpjson"
)

// The header must be set before the status line is written. Reversing the two
// statements in Write leaves this the only assertion that fails: the body and
// the status code are unaffected, and net/http reports nothing.
//
// The assertion reads Result().Header, not recorder.Header(). The latter is
// the live map, which keeps accepting writes after WriteHeader and so cannot
// observe a header set too late -- it passes against the very bug this test
// exists to catch. Result().Header is the snapshot WriteHeader took, which is
// what a client actually receives.
func TestWriteSetsContentTypeBeforeStatus(t *testing.T) {
	recorder := httptest.NewRecorder()

	httpjson.Write(recorder, http.StatusTeapot, struct {
		Error string `json:"error"`
	}{Error: "brewing"})

	result := recorder.Result()
	defer result.Body.Close()

	if got := result.Header.Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	if result.StatusCode != http.StatusTeapot {
		t.Fatalf("status = %d, want %d", result.StatusCode, http.StatusTeapot)
	}
	var body map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body["error"] != "brewing" {
		t.Fatalf("body = %v, want error=brewing", body)
	}
}
