package sentrycli_test

import (
	"encoding/json"
	"testing"

	"github.com/ramilS/faultline/internal/sentrycli"
)

// sentry-cli reads these keys by name. Asserting on the marshalled JSON rather
// than the struct is the point: a renamed field compiles and silently makes
// every upload fail against a capability response the CLI cannot read.
func TestDefaultChunkOptionsMarshalToTheKeysSentryCLIReads(t *testing.T) {
	encoded, err := json.Marshal(sentrycli.DefaultChunkOptions("/api/0/organizations/demo/chunk-upload/"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		// url is mandatory: sentry-cli 2.58.6 cannot deserialize
		// ChunkServerOptions without it and aborts the whole upload with
		// "could not parse JSON response" before any chunk is sent.
		"url":       "/api/0/organizations/demo/chunk-upload/",
		"chunkSize": float64(8388608), "chunksPerRequest": float64(64),
		"maxFileSize": float64(209715200), "maxRequestSize": float64(33554432),
		"concurrency": float64(4), "hashAlgorithm": "sha1",
	}
	for key, value := range want {
		if decoded[key] != value {
			t.Fatalf("%s = %v, want %v", key, decoded[key], value)
		}
	}
	if got := decoded["compression"]; !jsonEqual(got, []any{"gzip"}) {
		t.Fatalf("compression = %v, want [gzip]", got)
	}
	// accept is how sentry-cli picks its upload path, not a menu of what we
	// support. Dropping either artifact_bundles_v2 or release_files makes the
	// CLI fall back to the legacy release-file endpoints, which this server
	// does not implement, and the upload then fails with no chunk ever
	// reaching /chunk-upload/.
	wantAccept := []any{"artifact_bundles", "artifact_bundles_v2", "release_files", "debug_files"}
	if got := decoded["accept"]; !jsonEqual(got, wantAccept) {
		t.Fatalf("accept = %v, want %v", got, wantAccept)
	}
}

func jsonEqual(got any, want []any) bool {
	list, ok := got.([]any)
	if !ok || len(list) != len(want) {
		return false
	}
	for i := range want {
		if list[i] != want[i] {
			return false
		}
	}
	return true
}
