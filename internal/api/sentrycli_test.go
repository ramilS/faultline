// This file is an internal test (package api, not api_test) because it reuses
// artifactServiceStub and sourceMapRepositoryStub from artifacts_test.go, which
// are unexported types in package api and invisible from an external test
// package.
package api

import (
	"archive/zip"
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/sourcemap"
)

const (
	testArtifactToken = "fl_art_test_token"
	testProjectSlug   = "demo"
	testProjectID     = int64(42)
	otherProjectSlug  = "other-project"
	otherProjectID    = int64(43)
)

// sentryCLIProjectResolverStub resolves a fixed set of slugs to distinct
// projects, unlike artifactProjectServiceStub (artifacts_test.go), which
// always returns the same project regardless of slug. The assemble
// authorization test needs a slug that resolves to a *different* project
// than the token's own, so a single-project fake cannot exercise it.
type sentryCLIProjectResolverStub struct {
	projects map[string]project.Project
}

func (s sentryCLIProjectResolverStub) FindProject(_ context.Context, _ access.Scope, slug string) (project.Project, error) {
	found, ok := s.projects[slug]
	if !ok {
		return project.Project{}, project.ErrNotFound
	}
	return found, nil
}

// fakeChunkStore is an in-memory sentrycli.ChunkStore. saved and deleted
// count chunks actually persisted or removed, so a test can assert a
// rejected upload wrote nothing without inspecting storage internals.
type fakeChunkStore struct {
	staged  map[sentrycli.Checksum][]byte
	saved   int
	deleted int
	sweeps  int
	saveErr error
	loadErr error
}

func newFakeChunkStore() *fakeChunkStore {
	return &fakeChunkStore{staged: make(map[sentrycli.Checksum][]byte)}
}

func (f *fakeChunkStore) Save(_ context.Context, _ int64, bodies map[sentrycli.Checksum][]byte) error {
	if f.saveErr != nil {
		return f.saveErr
	}
	for checksum, body := range bodies {
		f.staged[checksum] = body
		f.saved++
	}
	return nil
}

func (f *fakeChunkStore) Load(_ context.Context, _ int64, want []sentrycli.Checksum) (map[sentrycli.Checksum][]byte, error) {
	if f.loadErr != nil {
		return nil, f.loadErr
	}
	found := make(map[sentrycli.Checksum][]byte)
	for _, checksum := range want {
		if body, ok := f.staged[checksum]; ok {
			found[checksum] = body
		}
	}
	return found, nil
}

func (f *fakeChunkStore) Delete(_ context.Context, _ int64, used []sentrycli.Checksum) error {
	for _, checksum := range used {
		delete(f.staged, checksum)
		f.deleted++
	}
	return nil
}

func (f *fakeChunkStore) DeleteExpired(context.Context, time.Time) (int64, error) {
	f.sweeps++
	return 0, nil
}

// fakeReleaseStore is an in-memory sentrycli.ReleaseStore. upserted and
// finalized count calls actually made, so a test can assert an idempotent
// create or a rejected cross-project write without inspecting storage
// internals. lastFinalizedAt records the timestamp the most recent Finalize
// call carried, so a test can assert the handler parsed a captured timestamp
// exactly rather than merely accepting it.
type fakeReleaseStore struct {
	upserted             int
	finalized            int
	lastFinalizedAt      time.Time
	lastFinalizedVersion string
}

func (f *fakeReleaseStore) Upsert(context.Context, int64, string) error {
	f.upserted++
	return nil
}

func (f *fakeReleaseStore) Finalize(_ context.Context, _ int64, version string, at time.Time) error {
	f.finalized++
	f.lastFinalizedAt = at
	f.lastFinalizedVersion = version
	return nil
}

type sentryCLIFixture struct {
	artifacts  *artifactServiceStub
	projects   sentryCLIProjectResolverStub
	chunks     *fakeChunkStore
	sourceMaps *sourceMapRepositoryStub
	releases   *fakeReleaseStore
	mappings   *fakeProguardMappings
}

// newSentryCLIFixture wires a handler to counting fakes: testArtifactToken
// authenticates to testProjectSlug's project id, and otherProjectSlug
// resolves to a distinct project the token does not own.
func newSentryCLIFixture(t *testing.T) (*SentryCLIHandler, *sentryCLIFixture) {
	t.Helper()
	fixture := &sentryCLIFixture{
		artifacts: &artifactServiceStub{authenticated: artifactauth.Token{ProjectID: testProjectID}},
		projects: sentryCLIProjectResolverStub{projects: map[string]project.Project{
			testProjectSlug:  {ID: testProjectID, Slug: testProjectSlug},
			otherProjectSlug: {ID: otherProjectID, Slug: otherProjectSlug},
		}},
		chunks:     newFakeChunkStore(),
		sourceMaps: &sourceMapRepositoryStub{},
		releases:   &fakeReleaseStore{},
		mappings:   &fakeProguardMappings{},
	}
	handler := NewSentryCLIHandler(fixture.artifacts, fixture.projects, fixture.chunks, fixture.sourceMaps, fixture.releases, fixture.mappings, Options{})
	return handler, fixture
}

// The org segment is accepted and discarded — Faultline has no organizations,
// and authorization rests entirely on the artifact token. A
// handler that validated the segment would reject every correctly configured
// plugin.
func TestChunkUploadOptionsIgnoresTheOrgSegment(t *testing.T) {
	handler, _ := newSentryCLIFixture(t)
	for _, org := range []string{"demo", "anything-at-all"} {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/0/organizations/"+org+"/chunk-upload/", http.NoBody)
		request.SetPathValue("org", org)
		request.Header.Set("Authorization", "Bearer "+testArtifactToken)

		handler.ChunkUploadOptions(recorder, request)

		if recorder.Code != http.StatusOK {
			t.Fatalf("org %q status = %d, want 200", org, recorder.Code)
		}
		var options map[string]any
		if err := json.Unmarshal(recorder.Body.Bytes(), &options); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if options["hashAlgorithm"] != "sha1" {
			t.Fatalf("hashAlgorithm = %v, want sha1", options["hashAlgorithm"])
		}
		if options["url"] != "/api/0/organizations/"+org+"/chunk-upload/" {
			t.Fatalf("url = %v, want the request's own path", options["url"])
		}
	}
}

// An unauthenticated request must never reach storage.
func TestSentryCLIRoutesRequireAnArtifactToken(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/0/organizations/demo/chunk-upload/", http.NoBody)

	handler.ChunkUploadOptions(recorder, request)

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", recorder.Code)
	}
	if fixture.artifacts.authenticateCalls != 0 {
		t.Fatalf("authenticate calls = %d, want 0", fixture.artifacts.authenticateCalls)
	}
}

// Every entry of the assemble request's projects array must belong to the
// token's project. A token scoped to project A must not write bundles into B.
func TestAssembleRefusesAProjectTheTokenDoesNotOwn(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body, _ := json.Marshal(map[string]any{
		"checksum": "00", "chunks": []string{}, "projects": []string{otherProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// A project the token's own scope cannot even resolve (never mind own) must
// also be refused, not treated as a pass-through.
func TestAssembleRefusesAProjectThatDoesNotExist(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body, _ := json.Marshal(map[string]any{
		"checksum": "00", "chunks": []string{}, "projects": []string{"no-such-project"},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// An assemble request naming no project at all must be refused explicitly,
// not treated as "nothing to check, therefore authorized".
func TestAssembleRefusesAnEmptyProjectsArray(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body, _ := json.Marshal(map[string]any{
		"checksum": "00", "chunks": []string{}, "projects": []string{},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// Absent chunks must come back as missingChunks, not as an error: that field
// is how sentry-cli learns what to re-upload. Returning 4xx aborts the upload.
func TestAssembleReportsMissingChunks(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	absent := sentrycli.ComputeChecksum([]byte("never uploaded"))
	body, _ := json.Marshal(map[string]any{
		"checksum": sentrycli.ComputeChecksum([]byte("whole")).String(),
		"chunks":   []string{absent.String()},
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var decoded struct {
		State         string   `json:"state"`
		MissingChunks []string `json:"missingChunks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.State != "not_found" {
		t.Fatalf("state = %q, want not_found", decoded.State)
	}
	if len(decoded.MissingChunks) != 1 || decoded.MissingChunks[0] != absent.String() {
		t.Fatalf("missingChunks = %v, want [%s]", decoded.MissingChunks, absent)
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// AssembleBundle end to end, against the real sentry-cli-produced bundle
// fixture: this is what proves the "~" url form is normalized before
// storage (bundle.go's Unpack returns it un-normalized on purpose) and that
// a consumed chunk is deleted from staging afterward.
func TestAssembleStoresBundleWithNormalizedPathAndDeletesConsumedChunks(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	zipped, err := os.ReadFile("../sentrycli/testdata/bundle.zip")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	chunkChecksum := sentrycli.ComputeChecksum(zipped)
	fixture.chunks.staged[chunkChecksum] = zipped

	body, _ := json.Marshal(map[string]any{
		"checksum": chunkChecksum.String(),
		"chunks":   []string{chunkChecksum.String()},
		"projects": []string{testProjectSlug},
		"version":  "web@1.0.0",
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var decoded struct {
		State         string   `json:"state"`
		MissingChunks []string `json:"missingChunks"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.State != "ok" {
		t.Fatalf("state = %q, want ok", decoded.State)
	}
	if len(decoded.MissingChunks) != 0 {
		t.Fatalf("missingChunks = %v, want empty", decoded.MissingChunks)
	}
	if fixture.sourceMaps.bundleCalls != 1 || fixture.sourceMaps.bundleProject != testProjectID || fixture.sourceMaps.bundleVersion != "web@1.0.0" {
		t.Fatalf("PutBundle called with project=%d version=%q calls=%d, want project=%d version=web@1.0.0 calls=1",
			fixture.sourceMaps.bundleProject, fixture.sourceMaps.bundleVersion, fixture.sourceMaps.bundleCalls, testProjectID)
	}
	if len(fixture.sourceMaps.bundleEntries) != 1 {
		t.Fatalf("bundle entries = %d, want 1", len(fixture.sourceMaps.bundleEntries))
	}
	entry := fixture.sourceMaps.bundleEntries[0]
	if entry.DebugID != "003d8ac6-6080-5f78-8e37-8aa45ca5ee8a" {
		t.Fatalf("debug id = %q, want the file-level header id", entry.DebugID)
	}
	// The stored path must be the MINIFIED file's, "/app.js" — the address a
	// browser stack frame carries and the only one the resolver ever looks a
	// release+path ref up by. The map's own url, "~/app.js.map", normalizes
	// to "/app.js.map", which no frame will ever name. The leading slash is
	// separately the proof the "~" was stripped before normalizing, since
	// NormalizePath rejects anything without one.
	if entry.GeneratedPath != "/app.js" {
		t.Fatalf("generated path = %q, want /app.js (the minified file, not the map)", entry.GeneratedPath)
	}
	if _, staged := fixture.chunks.staged[chunkChecksum]; staged {
		t.Fatalf("consumed chunk %s still staged after a successful assemble", chunkChecksum)
	}
	if fixture.chunks.deleted != 1 {
		t.Fatalf("deleted chunks = %d, want 1", fixture.chunks.deleted)
	}
}

// A malformed bundle (not a zip at all) must map to 400 via errors.Is against
// ErrInvalidBundle, never by matching Unpack's error text.
func TestAssembleRejectsAMalformedBundleWith400(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	garbage := []byte("this is not a zip file")
	chunkChecksum := sentrycli.ComputeChecksum(garbage)
	fixture.chunks.staged[chunkChecksum] = garbage

	body, _ := json.Marshal(map[string]any{
		"checksum": chunkChecksum.String(),
		"chunks":   []string{chunkChecksum.String()},
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// A bundle whose single entry decompresses past the per-entry size guard
// wraps both ErrBundleTooLarge and ErrInvalidBundle (bundle.go) and must map
// to 413, distinguished from the plain-malformed case above by errors.Is,
// never by message text.
func TestAssembleRejectsAnOversizedEntryWith413(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	zipped := buildOversizedEntryBundle(t)
	chunkChecksum := sentrycli.ComputeChecksum(zipped)
	fixture.chunks.staged[chunkChecksum] = zipped

	body, _ := json.Marshal(map[string]any{
		"checksum": chunkChecksum.String(),
		"chunks":   []string{chunkChecksum.String()},
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// buildOversizedEntryBundle produces a valid zip whose one source-map entry
// decompresses past sentrycli's per-entry cap. The payload is all zeroes, so
// deflate compresses it to a few KB even though it decompresses past 50MiB —
// exactly the shape the per-entry guard exists to catch.
func buildOversizedEntryBundle(t *testing.T) []byte {
	t.Helper()
	const oversizeBytes = 50<<20 + 1

	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	manifestWriter, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	manifest := `{"files":{"files/app.js.map":{"type":"source_map","url":"~/app.js.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}}}`
	if _, err := manifestWriter.Write([]byte(manifest)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	entryWriter, err := writer.CreateHeader(&zip.FileHeader{Name: "files/app.js.map", Method: zip.Deflate})
	if err != nil {
		t.Fatalf("create entry: %v", err)
	}
	zero := make([]byte, 1<<20)
	for written := 0; written < oversizeBytes; written += len(zero) {
		chunk := zero
		if remaining := oversizeBytes - written; remaining < len(chunk) {
			chunk = zero[:remaining]
		}
		if _, err := entryWriter.Write(chunk); err != nil {
			t.Fatalf("write entry: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// A chunk whose body does not hash to the name it was uploaded under is
// either corruption or an attempt to poison another upload's content
// address.
func TestUploadChunksRejectsAChecksumThatDoesNotMatchItsBody(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	// "file" is the uncompressed part name; TestUploadChunksAcceptsGzipParts
	// does the same with "file_gzip" and a gzip body, since that is what
	// sentry-cli actually sends against the `compression: ["gzip"]` this
	// server advertises.
	part, err := writer.CreateFormFile("file", sentrycli.ComputeChecksum([]byte("declared")).String())
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write([]byte("actual, different")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
	if fixture.chunks.saved != 0 {
		t.Fatalf("saved chunks = %d, want 0", fixture.chunks.saved)
	}
}

// The sibling of the test above: sentry-cli actually sends chunks as
// "file_gzip" with a gzip body, because this server advertises
// compression: ["gzip"]. A parser that only accepts "file" would store
// nothing here and fail assembly later on a missing chunk.
func TestUploadChunksAcceptsGzipEncodedParts(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	content := []byte("a source map's worth of bytes")
	checksum := sentrycli.ComputeChecksum(content)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file_gzip", checksum.String())
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	gzipWriter := gzip.NewWriter(part)
	if _, err := gzipWriter.Write(content); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.chunks.saved != 1 {
		t.Fatalf("saved chunks = %d, want 1", fixture.chunks.saved)
	}
	if stored := fixture.chunks.staged[checksum]; !bytes.Equal(stored, content) {
		t.Fatalf("stored body = %q, want the decompressed content %q", stored, content)
	}
}

// A multipart part named neither "file" nor "file_gzip" is deliberately
// skipped, not accidentally: the upload must still succeed and store only
// the recognized chunk.
func TestUploadChunksSkipsUnrecognizedPartNames(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	content := []byte("chunk body")
	checksum := sentrycli.ComputeChecksum(content)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("org", "demo"); err != nil {
		t.Fatalf("write field: %v", err)
	}
	part, err := writer.CreateFormFile("file", checksum.String())
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.chunks.saved != 1 {
		t.Fatalf("saved chunks = %d, want 1 (only the recognized part)", fixture.chunks.saved)
	}
}

// The chunk-size cap applies to decompressed bytes, not the compressed
// stream: a gzip part that inflates past ChunkSize must be rejected even
// though its compressed size is tiny, or a small upload could exhaust memory
// well beyond the advertised budget.
func TestUploadChunksLimitsDecompressedGzipBytes(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	oversized := make([]byte, sentrycli.DefaultChunkOptions("").ChunkSize+1)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file_gzip", sentrycli.ComputeChecksum(oversized).String())
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	gzipWriter := gzip.NewWriter(part)
	if _, err := gzipWriter.Write(oversized); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.chunks.saved != 0 {
		t.Fatalf("saved chunks = %d, want 0", fixture.chunks.saved)
	}
}

// A request declaring more chunks than ChunksPerRequest allows is rejected
// outright, protecting storage from an unbounded number of tiny parts in one
// request.
func TestUploadChunksEnforcesChunksPerRequestLimit(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	limit := sentrycli.DefaultChunkOptions("").ChunksPerRequest

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i := 0; i <= limit; i++ {
		content := []byte{byte(i), byte(i >> 8)}
		part, err := writer.CreateFormFile("file", sentrycli.ComputeChecksum(content).String())
		if err != nil {
			t.Fatalf("create part %d: %v", i, err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write part %d: %v", i, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.chunks.saved != 0 {
		t.Fatalf("saved chunks = %d, want 0 (the whole request is rejected)", fixture.chunks.saved)
	}
}

// sentrycli.Concat sums a checksum's staged body length once
// per occurrence in chunks[], so naming one legitimately staged small chunk
// more times than a maximum-size bundle could ever require must be rejected
// before Load/Concat ever run — otherwise a ~1 MiB request can force an
// allocation far beyond any file this server would accept. The cap
// (MaxFileSize/ChunkSize = 25 at today's advertised values) is checked
// against len(chunks) alone, so this never touches storage.
func TestAssembleRejectsTooManyChunksInOneRequest(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	options := sentrycli.DefaultChunkOptions("")
	maxChunks := int(options.MaxFileSize / options.ChunkSize)

	chunkContent := []byte("a small legitimately staged chunk")
	checksum := sentrycli.ComputeChecksum(chunkContent)
	fixture.chunks.staged[checksum] = chunkContent

	// One more entry than the cap allows, all naming the same staged chunk —
	// exactly the shape that would otherwise multiply into an unbounded
	// allocation in sentrycli.Concat.
	chunkList := make([]string, maxChunks+1)
	for i := range chunkList {
		chunkList[i] = checksum.String()
	}
	// The checksum field is deliberately wrong: if this cap were absent, the
	// request would proceed to Concat and fail there instead (a different
	// error, proving Concat ran) rather than being rejected up front. See
	// the fix report for the paired RED capture.
	wrongTotal := sentrycli.ComputeChecksum([]byte("not what would actually be assembled"))

	body, _ := json.Marshal(map[string]any{
		"checksum": wrongTotal.String(),
		"chunks":   chunkList,
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error != "too many chunks in assemble request" {
		t.Fatalf("error = %q, want the count rejection (proves the cap ran before Concat, not the checksum-mismatch error Concat itself would produce)", decoded.Error)
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// A chunks[] list within the count cap but naming the same checksum twice
// must also be rejected — the count cap alone does not stop a
// short list from still multiplying a chunk's contribution to Concat's total.
func TestAssembleRejectsDuplicateChunkChecksums(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	chunkContent := []byte("a small legitimately staged chunk")
	checksum := sentrycli.ComputeChecksum(chunkContent)
	fixture.chunks.staged[checksum] = chunkContent

	// The "checksum" (total) field is deliberately wrong. If it matched the
	// doubled content, a pre-fix run would let Concat succeed and only fail
	// later at Unpack, landing on the same 400 status for an unrelated
	// reason and making this test pass whether or not duplicates were
	// rejected. A mismatched total instead makes the two code paths produce
	// distinguishable messages: reaching Concat (pre-fix) fails with a
	// checksum-mismatch message, while rejecting the duplicate up front
	// (post-fix) never gets that far.
	wrongTotal := sentrycli.ComputeChecksum([]byte("not the doubled content"))
	body, _ := json.Marshal(map[string]any{
		"checksum": wrongTotal.String(),
		"chunks":   []string{checksum.String(), checksum.String()},
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error != "duplicate chunk checksum in assemble request" {
		t.Fatalf("error = %q, want the duplicate rejection (proves it ran before Concat, not the checksum-mismatch error Concat itself would produce)", decoded.Error)
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// Independent of the count cap and de-duplication above, what is actually
// staged for the requested (distinct) checksums must not
// exceed MaxFileSize. This is a backstop against chunk bytes that reached
// storage some way other than UploadChunks' own ChunkSize-capped path — the
// two request-shape guards above cannot see that.
func TestAssembleRejectsStagedBytesExceedingMaxFileSize(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	options := sentrycli.DefaultChunkOptions("")
	oversized := make([]byte, options.MaxFileSize+1)
	checksum := sentrycli.ComputeChecksum(oversized)
	fixture.chunks.staged[checksum] = oversized

	body, _ := json.Marshal(map[string]any{
		"checksum": checksum.String(),
		"chunks":   []string{checksum.String()},
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// The ChunksPerRequest count must be checked BEFORE a part is opened, not after it has already paid its decompression cost. The excess
// (65th) part here carries a body that would itself fail to decompress; if
// the count check ran after buffering (the pre-fix order), that failure would
// surface as "invalid chunk upload". Checked before, the excess part is never
// opened, and the response names the count instead.
func TestUploadChunksChecksPartCountBeforeReadingTheNextPart(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	limit := sentrycli.DefaultChunkOptions("").ChunksPerRequest

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for i := range limit {
		content := []byte{byte(i), byte(i >> 8)}
		part, err := writer.CreateFormFile("file", sentrycli.ComputeChecksum(content).String())
		if err != nil {
			t.Fatalf("create part %d: %v", i, err)
		}
		if _, err := part.Write(content); err != nil {
			t.Fatalf("write part %d: %v", i, err)
		}
	}
	corrupt, err := writer.CreateFormFile("file_gzip", sentrycli.ComputeChecksum([]byte("x")).String())
	if err != nil {
		t.Fatalf("create corrupt part: %v", err)
	}
	if _, err := corrupt.Write([]byte("not gzip data at all")); err != nil {
		t.Fatalf("write corrupt part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error != "too many chunks in one request" {
		t.Fatalf("error = %q, want the count rejection — a decompression-failure message would mean the excess part was opened before the count was checked", decoded.Error)
	}
	if fixture.chunks.saved != 0 {
		t.Fatalf("saved chunks = %d, want 0", fixture.chunks.saved)
	}
}

// The part counter must count parts, not distinct checksums. bodies is keyed
// by checksum, so 65 parts that all repeat one checksum collapse to a single
// map entry and never grow len(bodies) past 1 — a counter keyed on that map
// would let this request through no matter how many parts arrived. ~1 MiB on
// the wire (gzip of all-zero content compresses to almost nothing) expands to
// 520 MiB, so what stops it and how early is load-bearing: the count guard
// must reject it before the 65th part is ever decompressed.
func TestUploadChunksCountsPartsRegardlessOfRepeatedChecksums(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	options := sentrycli.DefaultChunkOptions("")
	content := make([]byte, options.ChunkSize) // 8 MiB of zeros, at the per-part cap
	checksum := sentrycli.ComputeChecksum(content)

	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	if _, err := gzipWriter.Write(content); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	compressedBytes := compressed.Bytes() // a few KB: zeros compress extremely well

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	partCount := options.ChunksPerRequest + 1 // one more than the limit allows
	for i := range partCount {
		part, err := writer.CreateFormFile("file_gzip", checksum.String())
		if err != nil {
			t.Fatalf("create part %d: %v", i, err)
		}
		if _, err := part.Write(compressedBytes); err != nil {
			t.Fatalf("write part %d: %v", i, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	if body.Len() > 2<<20 {
		t.Fatalf("test payload = %d bytes, want it to stay small on the wire like the review's probe", body.Len())
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())

	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	var decoded struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Error != "too many chunks in one request" {
		t.Fatalf("error = %q, want the count rejection (proves repeated checksums no longer bypass the per-request part limit)", decoded.Error)
	}
	if fixture.chunks.saved != 0 {
		t.Fatalf("saved chunks = %d, want 0", fixture.chunks.saved)
	}
}

// A source map reaches storage under whichever of its two addresses survives
// conversion. Four manifest entries, exercising every combination
// bundleEntries' DebugID=="" && GeneratedPath=="" check can produce:
//   - files/app.js.map: debug id, and a minified entry naming it — both
//     addresses.
//   - files/legacy.js.map: named by a minified entry, but carrying no debug
//     id (no `sourcemaps inject` run — normal input, not a defect). A
//     regression to "no debug id -> drop" would silently start eating
//     legitimate un-injected bundles, which are the ones that depend on the
//     release+path address entirely.
//   - files/orphan.js.map: a debug id but no minified entry naming it, so no
//     generated path exists to store. It must still upload on its debug id.
//   - files/bad.js.map: named by a minified entry whose url does not
//     normalize (a bare "~"), and no debug id — neither address survives;
//     must be dropped, not passed to PutBundle, which requires at least one
//     and would otherwise 500 on input a client fully controls.
func TestAssembleDropsEntriesWithNoUsableAddress(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	sourceMap := `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`
	manifest := `{"files":{
		"files/app.js":{"type":"minified_source","url":"~/app.js","headers":{"sourcemap":"app.js.map"}},
		"files/app.js.map":{"type":"source_map","url":"~/app.js.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}},
		"files/legacy.js":{"type":"minified_source","url":"~/legacy.js","headers":{"sourcemap":"legacy.js.map"}},
		"files/legacy.js.map":{"type":"source_map","url":"~/legacy.js.map"},
		"files/orphan.js.map":{"type":"source_map","url":"~/orphan.js.map","headers":{"debug-id":"22222222-2222-2222-2222-222222222222"}},
		"files/bad.js":{"type":"minified_source","url":"~","headers":{"sourcemap":"~/bad.js.map"}},
		"files/bad.js.map":{"type":"source_map","url":"~/bad.js.map"}
	}}`
	zipped := buildBundleZip(t, manifest, map[string]string{
		"files/app.js.map":    sourceMap,
		"files/legacy.js.map": sourceMap,
		"files/orphan.js.map": sourceMap,
		"files/bad.js.map":    sourceMap,
	})
	chunkChecksum := sentrycli.ComputeChecksum(zipped)
	fixture.chunks.staged[chunkChecksum] = zipped

	body, _ := json.Marshal(map[string]any{
		"checksum": chunkChecksum.String(),
		"chunks":   []string{chunkChecksum.String()},
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleCalls != 1 {
		t.Fatalf("PutBundle calls = %d, want 1", fixture.sourceMaps.bundleCalls)
	}
	if len(fixture.sourceMaps.bundleEntries) != 3 {
		t.Fatalf("bundle entries = %d, want 3 (every addressable entry kept, the address-less one dropped rather than 500ing)", len(fixture.sourceMaps.bundleEntries))
	}
	byPath := make(map[string]sourcemap.BundleEntry, len(fixture.sourceMaps.bundleEntries))
	for _, entry := range fixture.sourceMaps.bundleEntries {
		byPath[entry.GeneratedPath] = entry
	}
	debugIDEntry, ok := byPath["/app.js"]
	if !ok {
		t.Fatalf("no surviving entry for /app.js: %+v", fixture.sourceMaps.bundleEntries)
	}
	if debugIDEntry.DebugID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("debug id entry's DebugID = %q, want the good entry's", debugIDEntry.DebugID)
	}
	urlOnlyEntry, ok := byPath["/legacy.js"]
	if !ok {
		t.Fatalf("no surviving entry for /legacy.js (a debug-id-less entry must survive when its minified url normalizes): %+v", fixture.sourceMaps.bundleEntries)
	}
	if urlOnlyEntry.DebugID != "" {
		t.Fatalf("url-only entry's DebugID = %q, want empty", urlOnlyEntry.DebugID)
	}
	orphanEntry, ok := byPath[""]
	if !ok {
		t.Fatalf("no surviving entry with an empty generated path (a map no minified entry names must still upload on its debug id): %+v", fixture.sourceMaps.bundleEntries)
	}
	if orphanEntry.DebugID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("orphan entry's DebugID = %q, want the orphan's", orphanEntry.DebugID)
	}
}

// buildBundleZip builds a minimal artifact bundle zip from a manifest and a
// set of entry contents, for scenarios buildOversizedEntryBundle's single-entry
// shape cannot express.
func buildBundleZip(t *testing.T, manifest string, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := zip.NewWriter(&buf)

	manifestWriter, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatalf("create manifest: %v", err)
	}
	if _, err := manifestWriter.Write([]byte(manifest)); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for name, content := range entries {
		entryWriter, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create entry %q: %v", name, err)
		}
		if _, err := entryWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write entry %q: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

const emptyMultipartContentType = "multipart/form-data; boundary=faultline-empty-boundary"

// emptyMultipart produces a valid multipart body with no parts, for tests
// that only need UploadChunks to authenticate and run its sweep without
// staging anything.
func emptyMultipart(t *testing.T) *bytes.Reader {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.SetBoundary("faultline-empty-boundary"); err != nil {
		t.Fatalf("set boundary: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return bytes.NewReader(body.Bytes())
}

// The plugin calls setCommits on its default configuration — `setCommits !==
// false` is true for undefined. In @sentry/bundler-plugins@10.74.0 that
// default also carries
// `shouldNotThrowOnFailure: true`, so a failing set-commits is logged and
// swallowed, not propagated. Accepting and discarding commit refs is therefore
// about not filling a user's build log with errors, not about keeping the build
// alive. Accepting them is still right; it is documented behaviour, not silent
// success. With no git repository sentry-cli sends no refs at all — it only reads /repos/ and /previous-with-commits/ and then
// fails locally with "could not find repository at '.'".
func TestUpdateReleaseAcceptsAndDiscardsCommitRefs(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"version":"web@1.0.0","projects":["` + testProjectSlug + `"],` +
		`"refs":[{"repository":"acme/app","commit":"deadbeef"}],"dateReleased":"2026-09-11T10:00:00Z"}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/0/organizations/demo/releases/web@1.0.0/", strings.NewReader(body))
	request.SetPathValue("version", "web@1.0.0")
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.UpdateRelease(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", recorder.Code, recorder.Body)
	}
	if fixture.releases.finalized != 1 {
		t.Fatalf("finalize calls = %d, want 1", fixture.releases.finalized)
	}
}

// The real finalize body carries a fractional-second timestamp,
// "2026-09-11T11:45:25.636386Z", unlike every other test here, which uses a whole-second one. time.RFC3339 does accept a
// fractional second even though the layout constant itself omits one, but
// this is the one input whose rejection would 400 every real build, so it
// must be pinned with the captured value verbatim, not a simplified one.
func TestUpdateReleaseAcceptsTheCapturedFractionalSecondTimestamp(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	const captured = "2026-09-11T11:45:25.636386Z"
	body := `{"projects":["` + testProjectSlug + `"],"dateReleased":"` + captured + `"}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/0/projects/demo/demo/releases/web@1.0.0/", strings.NewReader(body))
	request.SetPathValue("version", "web@1.0.0")
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.UpdateRelease(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body %s", recorder.Code, recorder.Body)
	}
	want, err := time.Parse(time.RFC3339, captured)
	if err != nil {
		t.Fatalf("parse captured timestamp: %v", err)
	}
	if !fixture.releases.lastFinalizedAt.Equal(want) {
		t.Fatalf("finalized at = %v, want %v (the captured timestamp, parsed)", fixture.releases.lastFinalizedAt, want)
	}
}

// Creating the same release twice is the plugin's normal behaviour on a
// rebuild. A conflict here fails the build for no reason.
func TestCreateReleaseIsIdempotent(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"version":"web@1.0.0","projects":["` + testProjectSlug + `"]}`
	for i := range 2 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/releases/", strings.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+testArtifactToken)
		request.Header.Set("Content-Type", "application/json")

		handler.CreateRelease(recorder, request)

		if recorder.Code != http.StatusCreated {
			t.Fatalf("attempt %d status = %d, want 201", i, recorder.Code)
		}
	}
	if fixture.releases.upserted != 2 {
		t.Fatalf("upsert calls = %d, want 2", fixture.releases.upserted)
	}
}

// The plugin sends dateStarted on every create body, so it must be a declared
// field rather than one the decoder merely tolerates: the value is discarded
// either way, but a reader needs to see that the server knows about it.
func TestCreateReleaseAcceptsDateStarted(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"version":"web@1.0.0","projects":["` + testProjectSlug + `"],"dateStarted":"2026-09-11T11:45:25.615580Z"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/releases/", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.CreateRelease(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201, body %s", recorder.Code, recorder.Body)
	}
	if fixture.releases.upserted != 1 {
		t.Fatalf("upsert calls = %d, want 1", fixture.releases.upserted)
	}
}

// An empty version must never reach the store: releases.version is NOT NULL
// with no emptiness check, so an unvalidated empty string becomes a real row
// occupying the (project_id, version) unique slot. UpdateRelease already
// rejects an empty version; CreateRelease must too.
func TestCreateReleaseRejectsAnEmptyVersion(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"projects":["` + testProjectSlug + `"]}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/releases/", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.CreateRelease(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.releases.upserted != 0 {
		t.Fatalf("upsert calls = %d, want 0 (an empty version must never reach the store)", fixture.releases.upserted)
	}
}

// A token for project A must never create a release naming project B in its
// body — the write always targets the token's own project, but a caller
// naming an unrelated project must still be refused rather than silently
// ignored.
func TestCreateReleaseRefusesAProjectTheTokenDoesNotOwn(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"version":"web@1.0.0","projects":["` + otherProjectSlug + `"]}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/"+otherProjectSlug+"/releases/", strings.NewReader(body))
	request.SetPathValue("project", otherProjectSlug)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.CreateRelease(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.releases.upserted != 0 {
		t.Fatalf("upsert calls = %d, want 0", fixture.releases.upserted)
	}
}

// Sibling of the above for finalize: a token for project A must never
// finalize a release naming project B, regardless of which {project} segment
// the URL carries.
func TestUpdateReleaseRefusesAProjectTheTokenDoesNotOwn(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"projects":["` + otherProjectSlug + `"],"dateReleased":"2026-09-11T10:00:00Z"}`
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/0/projects/demo/"+otherProjectSlug+"/releases/web@1.0.0/", strings.NewReader(body))
	request.SetPathValue("project", otherProjectSlug)
	request.SetPathValue("version", "web@1.0.0")
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.UpdateRelease(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.releases.finalized != 0 {
		t.Fatalf("finalize calls = %d, want 0", fixture.releases.finalized)
	}
}

// set-commits --auto reads /repos/ first; a non-array response makes
// sentry-cli silently skip the rest of set-commits. This asserts the literal
// wire shape — "[]" — not merely that a
// struct round-trips to an empty slice.
func TestListReposReturnsAnEmptyJSONArray(t *testing.T) {
	handler, _ := newSentryCLIFixture(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/0/organizations/demo/repos/", http.NoBody)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)

	handler.ListRepos(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != "[]" {
		t.Fatalf("body = %q, want the literal JSON array \"[]\" (an object or null makes sentry-cli skip set-commits)", got)
	}
}

// previous-with-commits must answer with a JSON object sentry-cli can parse
// as a release, even though Faultline tracks no release history to report.
func TestPreviousReleaseWithCommitsReturnsAReleaseObject(t *testing.T) {
	handler, _ := newSentryCLIFixture(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/0/organizations/demo/releases/web@1.0.0/previous-with-commits/", http.NoBody)
	request.SetPathValue("version", "web@1.0.0")
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)

	handler.PreviousReleaseWithCommits(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v (want a JSON object)", err)
	}
	if decoded["version"] != "web@1.0.0" {
		t.Fatalf("version = %v, want web@1.0.0", decoded["version"])
	}
}

// The GC must not run on every request: it is a table scan, and an upload
// sends many chunk requests back to back.
func TestChunkUploadSweepsAbandonedChunksAtMostOncePerInterval(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	for range 3 {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", emptyMultipart(t))
		request.Header.Set("Authorization", "Bearer "+testArtifactToken)
		request.Header.Set("Content-Type", emptyMultipartContentType)
		handler.UploadChunks(recorder, request)
	}
	if fixture.chunks.sweeps != 1 {
		t.Fatalf("sweeps = %d, want 1", fixture.chunks.sweeps)
	}
}

// A project over its chunk-staging ceiling is the caller's doing: the upload
// must come back 413 with a message naming the recovery, and must not be
// reported as an internal error for an operator to chase.
func TestUploadChunksReportsAFullStagingAreaAs413(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	var reported []error
	handler.reportError = func(err error) { reported = append(reported, err) }
	fixture.chunks.saveErr = fmt.Errorf("stage chunk: %w", sentrycli.ErrStagingQuotaExceeded)

	recorder := httptest.NewRecorder()
	handler.UploadChunks(recorder, newChunkUploadRequest(t, []byte("a chunk")))

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "staged upload chunks") {
		t.Fatalf("body = %s, want a message naming the staged chunks", recorder.Body.String())
	}
	if len(reported) != 0 {
		t.Fatalf("reported %v, want nothing: the caller is over quota, the server is not at fault", reported)
	}
}

// Every other Save failure is a server fault and keeps its 500, reported so
// an operator sees it.
func TestUploadChunksReportsAStorageFailureAs500(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	var reported []error
	handler.reportError = func(err error) { reported = append(reported, err) }
	fixture.chunks.saveErr = errors.New("connection refused")

	recorder := httptest.NewRecorder()
	handler.UploadChunks(recorder, newChunkUploadRequest(t, []byte("a chunk")))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if len(reported) != 1 {
		t.Fatalf("reported %v, want exactly one error", reported)
	}
}

// newChunkUploadRequest builds a well-formed single-part chunk upload, so a
// test about what Save returns is not also a test of multipart parsing.
func newChunkUploadRequest(t *testing.T, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", sentrycli.ComputeChecksum(content).String())
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &body)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

// Faultline does not own the sentry-cli protocol's schema: one additive field
// in a CLI patch release must not turn every upload into a 400, for a feature
// whose premise is that it works with no configuration. The declared fields
// must still take effect — leniency about unknown keys is not leniency about
// known ones.
func TestAssembleAcceptsAnUnknownFieldAndStillReadsTheDeclaredOnes(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	zipped, err := os.ReadFile("../sentrycli/testdata/bundle.zip")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	chunkChecksum := sentrycli.ComputeChecksum(zipped)
	fixture.chunks.staged[chunkChecksum] = zipped

	body, _ := json.Marshal(map[string]any{
		"checksum":          chunkChecksum.String(),
		"chunks":            []string{chunkChecksum.String()},
		"projects":          []string{testProjectSlug},
		"version":           "web@1.0.0",
		"aFieldWeNeverSaw":  "from a future sentry-cli",
		"anotherUnknownOne": map[string]any{"nested": true},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.sourceMaps.bundleVersion != "web@1.0.0" {
		t.Fatalf("stored version = %q, want web@1.0.0: the declared fields must still be read", fixture.sourceMaps.bundleVersion)
	}
}

// Leniency is about unknown keys only. A body carrying a second JSON value
// after the first is still malformed, and accepting it would mean the server
// and the client disagree about where the request ended.
func TestAssembleStillRejectsTrailingGarbage(t *testing.T) {
	zipped, err := os.ReadFile("../sentrycli/testdata/bundle.zip")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	checksum := sentrycli.ComputeChecksum(zipped)
	valid, _ := json.Marshal(map[string]any{
		"checksum": checksum.String(),
		"chunks":   []string{checksum.String()},
		"projects": []string{testProjectSlug},
		"version":  "web@1.0.0",
	})

	assemble := func(t *testing.T, body []byte) int {
		t.Helper()
		handler, fixture := newSentryCLIFixture(t)
		fixture.chunks.staged[checksum] = zipped
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer "+testArtifactToken)
		request.Header.Set("Content-Type", "application/json")
		handler.AssembleBundle(recorder, request)
		return recorder.Code
	}

	// The control: every field this body declares is valid and its chunk is
	// staged, so a 400 below can only come from what follows the first value.
	// Without it the test passes on a rejection the decoder never made.
	if code := assemble(t, valid); code != http.StatusOK {
		t.Fatalf("control status = %d, want 200: the body must be otherwise acceptable", code)
	}
	if code := assemble(t, append(append([]byte{}, valid...), ` {"second":"value"}`...)); code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}

// The same for the release endpoints, which the plugin calls on every build:
// an unknown key must not fail the build, and the version must still land.
func TestCreateReleaseAcceptsAnUnknownFieldAndStillReadsTheDeclaredOnes(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := []byte(`{"version":"web@2.0.0","projects":["` + testProjectSlug + `"],"newFieldFromAFutureCLI":42}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/releases/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.CreateRelease(recorder, request)

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.releases.upserted != 1 {
		t.Fatalf("upserts = %d, want 1", fixture.releases.upserted)
	}
	var decoded map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["version"] != "web@2.0.0" {
		t.Fatalf("echoed version = %q, want web@2.0.0", decoded["version"])
	}
}

func TestCreateReleaseStillRejectsTrailingGarbage(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := []byte(`{"version":"web@2.0.0"} {"second":"value"}`)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/releases/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.CreateRelease(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.releases.upserted != 0 {
		t.Fatalf("upserts = %d, want 0", fixture.releases.upserted)
	}
}

// Every other chunk-upload test builds its own multipart body, which means the
// bytes under test and the bytes the parser expects come from the same place.
// internal/sentrycli/testdata/chunk-upload-request.txt is a real sentry-cli
// 2.58.6 request captured off the wire — part name, disposition, boundary,
// gzip framing and all — and this is the test that puts those bytes through
// the handler.
func TestUploadChunksParsesTheCapturedSentryCLIRequest(t *testing.T) {
	const capturedChecksum = "b10e360e266f48c2c67d2a126076915dabaf6653"
	handler, fixture := newSentryCLIFixture(t)

	raw, err := os.ReadFile("../sentrycli/testdata/chunk-upload-request.txt")
	if err != nil {
		t.Fatalf("read captured request: %v", err)
	}
	request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("parse captured request: %v", err)
	}

	recorder := httptest.NewRecorder()
	handler.UploadChunks(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	checksum, err := sentrycli.ParseChecksum(capturedChecksum)
	if err != nil {
		t.Fatalf("parse checksum: %v", err)
	}
	body, staged := fixture.chunks.staged[checksum]
	if !staged {
		t.Fatalf("captured chunk was not staged under %s; staged %d chunks", capturedChecksum, len(fixture.chunks.staged))
	}
	// The filename is the SHA-1 of the DECOMPRESSED chunk: the part arrived
	// gzip-framed, so a handler that staged the compressed bytes would store
	// something that does not hash to the name it is filed under.
	if got := sentrycli.ComputeChecksum(body).String(); got != capturedChecksum {
		t.Fatalf("staged body hashes to %s, want %s", got, capturedChecksum)
	}
}

// The {version} path segment is the address of the release being finalized.
// A body naming a different version must not redirect the write: the caller
// asked to finalize A, and finalizing B instead marks the wrong release
// released with nothing anywhere reporting it.
func TestUpdateReleaseFinalizesThePathVersionNotTheBodyVersion(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"version":"web@2.0.0","projects":["` + testProjectSlug + `"]}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/0/projects/demo/demo/releases/web@1.0.0/", strings.NewReader(body))
	request.SetPathValue("version", "web@1.0.0")
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.UpdateRelease(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.releases.lastFinalizedVersion != "web@1.0.0" {
		t.Fatalf("finalized %q, want web@1.0.0 (the path segment)", fixture.releases.lastFinalizedVersion)
	}
	var decoded map[string]string
	if err := json.Unmarshal(recorder.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded["version"] != "web@1.0.0" {
		t.Fatalf("echoed version = %q, want web@1.0.0", decoded["version"])
	}
}

// The org-scoped finalize route carries no {version} segment on some
// sentry-cli paths, so the body is still the fallback address.
func TestUpdateReleaseFallsBackToTheBodyVersionWithNoPathSegment(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	body := `{"version":"web@2.0.0","projects":["` + testProjectSlug + `"]}`

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/api/0/organizations/demo/releases/", strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.UpdateRelease(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.releases.lastFinalizedVersion != "web@2.0.0" {
		t.Fatalf("finalized %q, want web@2.0.0 (the body, with no path segment to prefer)", fixture.releases.lastFinalizedVersion)
	}
}

// A chunk store that cannot read is a server fault: 500, reported, and no
// bundle written.
func TestAssembleReportsAChunkLoadFailureAs500(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	var reported []error
	handler.reportError = func(err error) { reported = append(reported, err) }
	fixture.chunks.loadErr = errors.New("connection refused")

	checksum := sentrycli.ComputeChecksum([]byte("anything")).String()
	body, _ := json.Marshal(map[string]any{
		"checksum": checksum, "chunks": []string{checksum}, "projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if len(reported) != 1 {
		t.Fatalf("reported %v, want exactly one error", reported)
	}
	if fixture.sourceMaps.bundleCalls != 0 {
		t.Fatalf("bundle writes = %d, want 0", fixture.sourceMaps.bundleCalls)
	}
}

// A bundle that unpacked cleanly but could not be stored must answer 500 and
// must NOT delete the chunks it consumed: the client's retry needs them.
func TestAssembleReportsAStoreFailureAs500AndKeepsTheChunks(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	var reported []error
	handler.reportError = func(err error) { reported = append(reported, err) }
	fixture.sourceMaps.bundleErr = errors.New("deadlock detected")

	zipped, err := os.ReadFile("../sentrycli/testdata/bundle.zip")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	chunkChecksum := sentrycli.ComputeChecksum(zipped)
	fixture.chunks.staged[chunkChecksum] = zipped

	body, _ := json.Marshal(map[string]any{
		"checksum": chunkChecksum.String(),
		"chunks":   []string{chunkChecksum.String()},
		"projects": []string{testProjectSlug},
	})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.Header.Set("Content-Type", "application/json")

	handler.AssembleBundle(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
	if len(reported) != 1 {
		t.Fatalf("reported %v, want exactly one error", reported)
	}
	if _, staged := fixture.chunks.staged[chunkChecksum]; !staged {
		t.Fatalf("chunk was deleted after a failed store; a retry has nothing left to assemble")
	}
}
