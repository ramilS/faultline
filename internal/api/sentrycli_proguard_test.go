package api

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"runtime/debug"
	"runtime/metrics"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nativedebug "github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/storage"
)

const (
	recordedMappingUUID = "cd395950-361a-563d-9df3-49aea740c6f6"
	recordedMappingSHA1 = "2c4a86fe85e18a7ffc127825adf217fa694a6878"
	proguardFixtureDir  = "../sentrycli/testdata/proguard/"
)

// fakeProguardMappings is an in-memory proguard.Repository. It refuses different
// bytes under a stored uuid the way the real one does, so the handlers'
// conflict path is reachable without a database.
type fakeProguardMappings struct {
	stored map[string]proguard.DecodedUpload
	puts   int
	putErr error
}

type fakeNativeDebugFiles struct {
	uploads []nativedebug.Upload
	err     error
}

func (f *fakeNativeDebugFiles) Put(_ context.Context, _ int64, upload nativedebug.Upload) error {
	if f.err != nil {
		return f.err
	}
	f.uploads = append(f.uploads, upload)
	return nil
}

func (*fakeNativeDebugFiles) Find(context.Context, int64, string) (nativedebug.Stored, error) {
	return nativedebug.Stored{}, nativedebug.ErrNotFound
}

func (f *fakeProguardMappings) Put(_ context.Context, _ int64, mappingUUID string, upload proguard.DecodedUpload) (bool, error) {
	f.puts++
	if f.putErr != nil {
		return false, f.putErr
	}
	if existing, ok := f.stored[mappingUUID]; ok {
		if existing.SHA256 != upload.SHA256 {
			return false, proguard.ErrConflict
		}
		return false, nil
	}
	if f.stored == nil {
		f.stored = map[string]proguard.DecodedUpload{}
	}
	f.stored[mappingUUID] = upload
	return true, nil
}

func (f *fakeProguardMappings) Find(context.Context, int64, string) (proguard.Stored, error) {
	return proguard.Stored{}, proguard.ErrNotFound
}

func recordedRequest(t *testing.T, name string) *http.Request {
	t.Helper()
	raw, err := os.ReadFile(proguardFixtureDir + name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	request, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(raw)))
	if err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	request.SetPathValue("org", "demo")
	request.SetPathValue("project", "demo")
	return request
}

func decodeAssemble(t *testing.T, body []byte) map[string]map[string]any {
	t.Helper()
	var decoded map[string]map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode assemble response %q: %v", body, err)
	}
	return decoded
}

// stageRecordedChunk puts the chunk sentry-cli 3.8.0 uploaded into the fake
// store by running its real request through UploadChunks.
func stageRecordedChunk(t *testing.T, handler *SentryCLIHandler) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.UploadChunks(recorder, recordedRequest(t, "chunked-3.8.0-chunk-upload-request.txt"))
	if recorder.Code != http.StatusOK {
		t.Fatalf("stage chunk status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestAssembleDebugFilesStoresTheRecordedChunkedUpload(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	stageRecordedChunk(t, handler)
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, recordedRequest(t, "chunked-3.8.0-assemble-request.txt"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}
	result := decodeAssemble(t, recorder.Body.Bytes())[recordedMappingSHA1]
	if result["state"] != "ok" {
		t.Fatalf("result = %v, want state ok", result)
	}
	if missing, _ := result["missingChunks"].([]any); len(missing) != 0 {
		t.Fatalf("missingChunks = %v, want none", missing)
	}
	if _, stored := fixture.mappings.stored[recordedMappingUUID]; !stored || fixture.mappings.puts != 1 {
		t.Fatalf("stored = %v puts = %d, want the recorded mapping stored once", fixture.mappings.stored, fixture.mappings.puts)
	}
	if fixture.chunks.deleted != 1 {
		t.Fatalf("deleted chunks = %d, want the consumed chunk removed", fixture.chunks.deleted)
	}
}

func TestAssembleDebugFilesStoresNativeUniversalContentOnce(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	nativeFiles := &fakeNativeDebugFiles{}
	handler.nativeFiles = nativeFiles
	want := nativedebug.Upload{UncompressedBytes: 6, Objects: []nativedebug.Object{
		{DebugID: "01000000-0000-0000-0000-000000000000", Index: 0},
		{DebugID: "02000000-0000-0000-0000-000000000000", Index: 1},
	}}
	handler.decodeNative = func(io.ReaderAt, int64) (nativedebug.Upload, error) { return want, nil }
	body := []byte("macho!")
	checksum := sentrycli.ComputeChecksum(body)
	fixture.chunks.staged[checksum] = body
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/difs/assemble/", strings.NewReader(`{"`+checksum.String()+`":{"name":"App","chunks":["`+checksum.String()+`"]}}`))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.SetPathValue("project", "demo")
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, request)

	result := decodeAssemble(t, recorder.Body.Bytes())[checksum.String()]
	if recorder.Code != http.StatusOK || result["state"] != "ok" {
		t.Fatalf("status=%d result=%v", recorder.Code, result)
	}
	if len(nativeFiles.uploads) != 1 || len(nativeFiles.uploads[0].Objects) != 2 {
		t.Fatalf("uploads=%#v", nativeFiles.uploads)
	}
	if fixture.chunks.deleted != 1 {
		t.Fatalf("deleted=%d", fixture.chunks.deleted)
	}
}

func TestAssembleDebugFilesKeepsSharedChunksUntilEveryFileIsStored(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	nativeFiles := &fakeNativeDebugFiles{}
	handler.nativeFiles = nativeFiles
	handler.decodeNative = func(io.ReaderAt, int64) (nativedebug.Upload, error) {
		return nativedebug.Upload{Objects: []nativedebug.Object{{DebugID: "01000000-0000-0000-0000-000000000000"}}}, nil
	}
	shared, firstTail, secondTail := []byte("shared"), []byte("first"), []byte("second")
	sharedSum, firstTailSum, secondTailSum := sentrycli.ComputeChecksum(shared), sentrycli.ComputeChecksum(firstTail), sentrycli.ComputeChecksum(secondTail)
	maps.Copy(fixture.chunks.staged, map[sentrycli.Checksum][]byte{sharedSum: shared, firstTailSum: firstTail, secondTailSum: secondTail})
	firstSum := sentrycli.ComputeChecksum(append(append([]byte{}, shared...), firstTail...))
	secondSum := sentrycli.ComputeChecksum(append(append([]byte{}, shared...), secondTail...))
	body, err := json.Marshal(map[string]debugFileAssembleItem{
		firstSum.String():  {Name: "First", Chunks: []string{sharedSum.String(), firstTailSum.String()}},
		secondSum.String(): {Name: "Second", Chunks: []string{sharedSum.String(), secondTailSum.String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/difs/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.SetPathValue("project", "demo")
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, request)

	results := decodeAssemble(t, recorder.Body.Bytes())
	if recorder.Code != http.StatusOK || results[firstSum.String()]["state"] != "ok" || results[secondSum.String()]["state"] != "ok" {
		t.Fatalf("status=%d results=%v", recorder.Code, results)
	}
	if len(nativeFiles.uploads) != 2 || fixture.chunks.deleted != 3 {
		t.Fatalf("uploads=%d deleted=%d", len(nativeFiles.uploads), fixture.chunks.deleted)
	}
}

func TestAssembleDebugFilesReportsNativeValidationConflictAndQuotaAsPerFileErrors(t *testing.T) {
	for _, test := range []struct {
		name              string
		decodeErr, putErr error
	}{
		{"malformed", nativedebug.ErrInvalidDebugFile, nil},
		{"conflict", nil, nativedebug.ErrConflict},
		{"quota", nil, storage.ErrArtifactQuotaExceeded},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, fixture := newSentryCLIFixture(t)
			nativeFiles := &fakeNativeDebugFiles{err: test.putErr}
			handler.nativeFiles = nativeFiles
			handler.decodeNative = func(io.ReaderAt, int64) (nativedebug.Upload, error) {
				return nativedebug.Upload{Objects: []nativedebug.Object{{DebugID: "01000000-0000-0000-0000-000000000000"}}}, test.decodeErr
			}
			body := []byte("native")
			checksum := sentrycli.ComputeChecksum(body)
			fixture.chunks.staged[checksum] = body
			request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/difs/assemble/", strings.NewReader(`{"`+checksum.String()+`":{"name":"App","chunks":["`+checksum.String()+`"]}}`))
			request.Header.Set("Authorization", "Bearer "+testArtifactToken)
			request.SetPathValue("project", "demo")
			recorder := httptest.NewRecorder()
			handler.AssembleDebugFiles(recorder, request)
			result := decodeAssemble(t, recorder.Body.Bytes())[checksum.String()]
			if recorder.Code != http.StatusOK || result["state"] != "error" {
				t.Fatalf("status=%d result=%v", recorder.Code, result)
			}
		})
	}
}

func TestAssembleDebugFilesAskForTheChunksItHasNot(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, recordedRequest(t, "chunked-3.8.0-assemble-request.txt"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (a 4xx would abort the CLI): %s", recorder.Code, recorder.Body.String())
	}
	result := decodeAssemble(t, recorder.Body.Bytes())[recordedMappingSHA1]
	missing, _ := result["missingChunks"].([]any)
	if result["state"] != "not_found" || len(missing) != 1 || missing[0] != recordedMappingSHA1 {
		t.Fatalf("result = %v, want not_found naming the chunk", result)
	}
	if fixture.mappings.puts != 0 {
		t.Fatal("nothing may be stored while chunks are missing")
	}
}

func TestAssembleDebugFilesRepeatedUploadIsStillOK(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	for range 2 {
		stageRecordedChunk(t, handler)
		recorder := httptest.NewRecorder()
		handler.AssembleDebugFiles(recorder, recordedRequest(t, "chunked-3.8.0-assemble-request.txt"))
		if state := decodeAssemble(t, recorder.Body.Bytes())[recordedMappingSHA1]["state"]; state != "ok" {
			t.Fatalf("state = %v, want ok on every upload", state)
		}
	}
	if len(fixture.mappings.stored) != 1 {
		t.Fatalf("stored %d mappings, want 1", len(fixture.mappings.stored))
	}
}

func TestAssembleDebugFilesReportsATerminalErrorForUnusableFiles(t *testing.T) {
	staged := []byte("com.acme.Cart -> a.a:\n    1:1:void run():3:3 -> a\n")
	stagedSum := sentrycli.ComputeChecksum(staged).String()
	notAMapping := []byte("this is not a mapping\n")
	notAMappingSum := sentrycli.ComputeChecksum(notAMapping).String()

	for _, test := range []struct {
		name string
		body []byte
		key  string
		item string
	}{
		{"name that is not a proguard uuid", staged, stagedSum, `{"name":"/proguard/nope.txt","chunks":["` + stagedSum + `"]}`},
		{"name of another kind of file", staged, stagedSum, `{"name":"libapp.so","chunks":["` + stagedSum + `"]}`},
		{"content that is not a mapping", notAMapping, notAMappingSum, `{"name":"/proguard/` + recordedMappingUUID + `.txt","chunks":["` + notAMappingSum + `"]}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, fixture := newSentryCLIFixture(t)
			fixture.chunks.staged[sentrycli.ComputeChecksum(test.body)] = test.body
			request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/difs/assemble/", strings.NewReader(`{"`+test.key+`":`+test.item+`}`))
			request.Header.Set("Authorization", "Bearer "+testArtifactToken)
			request.SetPathValue("project", "demo")
			recorder := httptest.NewRecorder()

			handler.AssembleDebugFiles(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
			}
			result := decodeAssemble(t, recorder.Body.Bytes())[test.key]
			if result["state"] != "error" || result["detail"] == nil || result["detail"] == "" {
				t.Fatalf("result = %v, want state error with a detail", result)
			}
			if fixture.mappings.puts != 0 {
				t.Fatal("an unusable file must not be stored")
			}
		})
	}
}

func TestAssembleDebugFilesReportsAConflictingUuid(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	other, err := proguard.DecodeUpload(strings.NewReader("com.other.Thing -> z:\n"))
	if err != nil {
		t.Fatalf("DecodeUpload: %v", err)
	}
	fixture.mappings.stored = map[string]proguard.DecodedUpload{recordedMappingUUID: other}
	stageRecordedChunk(t, handler)
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, recordedRequest(t, "chunked-3.8.0-assemble-request.txt"))

	if state := decodeAssemble(t, recorder.Body.Bytes())[recordedMappingSHA1]["state"]; state != "error" {
		t.Fatalf("state = %v, want error for different bytes under a taken uuid", state)
	}
}

func TestAssembleDebugFilesRefusesAProjectTheTokenDoesNotOwn(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	stageRecordedChunk(t, handler)
	request := recordedRequest(t, "chunked-3.8.0-assemble-request.txt")
	request.SetPathValue("project", otherProjectSlug)
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", recorder.Code)
	}
	if fixture.mappings.puts != 0 {
		t.Fatal("a foreign project must not be written to")
	}
}

func TestAssembleDebugFilesRequiresAnArtifactToken(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	request := recordedRequest(t, "chunked-3.8.0-assemble-request.txt")
	request.Header.Del("Authorization")
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, request)

	if recorder.Code != http.StatusUnauthorized || fixture.mappings.puts != 0 {
		t.Fatalf("status = %d puts = %d, want 401 and no write", recorder.Code, fixture.mappings.puts)
	}
}

func TestAssembleDebugFilesRejectsTooManyFilesAndChunks(t *testing.T) {
	handler, _ := newSentryCLIFixture(t)
	var items []string
	for i := range maxDebugFilesPerAssemble + 1 {
		key := strings.Repeat("a", 39) + string(rune('a'+i))
		items = append(items, `"`+key+`":{"name":"/proguard/`+recordedMappingUUID+`.txt","chunks":[]}`)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/difs/assemble/", strings.NewReader("{"+strings.Join(items, ",")+"}"))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.SetPathValue("project", "demo")
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestAssembleDebugFilesReportsAnExceededStorageQuota(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	fixture.mappings.putErr = fmt.Errorf("put proguard mapping: %w", storage.ErrArtifactQuotaExceeded)
	stageRecordedChunk(t, handler)
	recorder := httptest.NewRecorder()

	handler.AssembleDebugFiles(recorder, recordedRequest(t, "chunked-3.8.0-assemble-request.txt"))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 with a terminal error state: %s", recorder.Code, recorder.Body.String())
	}
	result := decodeAssemble(t, recorder.Body.Bytes())[recordedMappingSHA1]
	if result["state"] != "error" || !strings.Contains(fmt.Sprint(result["detail"]), "quota") {
		t.Fatalf("result = %v, want an error state naming the quota", result)
	}
}

func TestUploadLegacyDebugFileRejectsAnExceededStorageQuota(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	fixture.mappings.putErr = fmt.Errorf("put proguard mapping: %w", storage.ErrArtifactQuotaExceeded)
	recorder := httptest.NewRecorder()

	handler.UploadLegacyDebugFile(recorder, recordedRequest(t, "legacy-2.58.6-request.txt"))

	if recorder.Code != http.StatusRequestEntityTooLarge || !strings.Contains(recorder.Body.String(), "quota") {
		t.Fatalf("status = %d body = %s, want 413 naming the quota", recorder.Code, recorder.Body.String())
	}
}

func TestUploadLegacyDebugFileStoresTheRecordedUpload(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	recorder := httptest.NewRecorder()

	handler.UploadLegacyDebugFile(recorder, recordedRequest(t, "legacy-2.58.6-request.txt"))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", recorder.Code, recorder.Body.String())
	}
	if _, stored := fixture.mappings.stored[recordedMappingUUID]; !stored {
		t.Fatalf("stored = %v, want the mapping under %s", fixture.mappings.stored, recordedMappingUUID)
	}
	// sentry-cli 2.58.6 parses the reply as a JSON array of debug files and
	// fails on an object or on a missing objectName (internal/sentrycli/testdata/proguard/README.md).
	var files []map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &files); err != nil {
		t.Fatalf("reply %q is not a JSON array: %v", recorder.Body.String(), err)
	}
	if len(files) != 1 {
		t.Fatalf("reply has %d files, want 1", len(files))
	}
	for _, field := range []string{"id", "debugId", "objectName", "cpuName", "sha1", "size", "dateCreated", "headers"} {
		if _, present := files[0][field]; !present {
			t.Errorf("reply file lacks %q, which sentry-cli requires: %v", field, files[0])
		}
	}
	if files[0]["debugId"] != recordedMappingUUID || files[0]["sha1"] != recordedMappingSHA1 {
		t.Fatalf("reply file = %v, want debugId %s and sha1 %s", files[0], recordedMappingUUID, recordedMappingSHA1)
	}
}

func TestUploadLegacyDebugFileRejectsAnOversizedBody(t *testing.T) {
	handler, fixture := newSentryCLIFixture(t)
	oversized := multipartUpload(t, "file", make([]byte, legacyUploadLimit+1))
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/dsyms/", bytes.NewReader(oversized.data))
	request.Header.Set("Content-Type", oversized.contentType)
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.SetPathValue("project", "demo")
	recorder := httptest.NewRecorder()

	handler.UploadLegacyDebugFile(recorder, request)

	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", recorder.Code, recorder.Body.String())
	}
	if fixture.mappings.puts != 0 {
		t.Fatal("an oversized upload must not be stored")
	}
}

func TestUploadLegacyDebugFileRejectsBadUploads(t *testing.T) {
	notZip := multipartUpload(t, "file", []byte("not a zip"))
	wrongPart := multipartUpload(t, "other", []byte("x"))

	for _, test := range []struct {
		name   string
		mutate func(*http.Request)
		body   *multipartBody
		want   int
	}{
		{"a body that is not a zip", nil, notZip, http.StatusBadRequest},
		{"no file part", nil, wrongPart, http.StatusBadRequest},
		{"no token", func(r *http.Request) { r.Header.Del("Authorization") }, notZip, http.StatusUnauthorized},
		{"a project the token does not own", func(r *http.Request) { r.SetPathValue("project", otherProjectSlug) }, notZip, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, fixture := newSentryCLIFixture(t)
			request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/dsyms/", bytes.NewReader(test.body.data))
			request.Header.Set("Content-Type", test.body.contentType)
			request.Header.Set("Authorization", "Bearer "+testArtifactToken)
			request.SetPathValue("project", "demo")
			if test.mutate != nil {
				test.mutate(request)
			}
			recorder := httptest.NewRecorder()

			handler.UploadLegacyDebugFile(recorder, request)

			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, test.want, recorder.Body.String())
			}
			if fixture.mappings.puts != 0 {
				t.Fatal("a rejected upload must not be stored")
			}
		})
	}
}

type multipartBody struct {
	data        []byte
	contentType string
}

func multipartUpload(t *testing.T, field string, content []byte) *multipartBody {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	part, err := writer.CreateFormFile(field, "upload")
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	return &multipartBody{data: buffer.Bytes(), contentType: writer.FormDataContentType()}
}

// A 90 MB R8 mapping once OOM-killed a 384 MiB container: assembling it held
// the staged chunks, their concatenation and an io.ReadAll copy of that at the
// same time. The staged chunks are the floor; whatever assembling adds on top
// must not grow with the size of the mapping.
func TestAssembleDebugFilesAddsNoMappingSizedCopyToTheStagedChunks(t *testing.T) {
	const mappingBytes = 32 << 20
	handler, fixture := newSentryCLIFixture(t)
	mapping := syntheticMapping(mappingBytes)
	chunkSize := int(sentrycli.DefaultChunkOptions("").ChunkSize)
	var chunks []string
	for start := 0; start < len(mapping); start += chunkSize {
		chunk := mapping[start:min(start+chunkSize, len(mapping))]
		checksum := sentrycli.ComputeChecksum(chunk)
		fixture.chunks.staged[checksum] = chunk
		chunks = append(chunks, checksum.String())
	}
	key := sentrycli.ComputeChecksum(mapping).String()
	body, err := json.Marshal(map[string]debugFileAssembleItem{key: {Name: "/proguard/" + recordedMappingUUID + ".txt", Chunks: chunks}})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/difs/assemble/", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+testArtifactToken)
	request.SetPathValue("project", "demo")
	recorder := httptest.NewRecorder()

	growth := peakHeapGrowth(func() { handler.AssembleDebugFiles(recorder, request) })

	if state := decodeAssemble(t, recorder.Body.Bytes())[key]["state"]; recorder.Code != http.StatusOK || state != "ok" {
		t.Fatalf("status = %d state = %v, want 200 ok: %s", recorder.Code, state, recorder.Body.String())
	}
	if stored := fixture.mappings.stored[recordedMappingUUID]; stored.UncompressedBytes != int64(len(mapping)) {
		t.Fatalf("stored %d bytes, want the whole %d byte mapping", stored.UncompressedBytes, len(mapping))
	}
	// The compressed copy the repository keeps, with its buffer's growth, is
	// inherent and comes to about half of this mapping; one more uncompressed
	// copy of any kind is the whole mapping and lands well past the limit.
	if limit := uint64(mappingBytes * 3 / 4); growth > limit {
		t.Fatalf("assembling grew the heap by %d MiB over the staged chunks, want under %d MiB", growth>>20, limit>>20)
	}
}

// syntheticMapping returns a valid mapping of at least size bytes whose class
// names never repeat, so it does not compress to nothing.
func syntheticMapping(size int) []byte {
	var mapping bytes.Buffer
	mapping.Grow(size + 256)
	for index := 0; mapping.Len() < size; index++ {
		fmt.Fprintf(&mapping, "com.acme.feature%d.Screen%d -> a.b%d:\n    1:4:void render(int):%d:%d -> a\n", index%97, index, index, index%500, index%500+3)
	}
	return mapping.Bytes()
}

// peakHeapGrowth runs work while sampling the live heap and returns how far it
// rose above where it stood before. GC is made aggressive for the duration so
// the samples track live data rather than garbage awaiting collection.
func peakHeapGrowth(work func()) uint64 {
	defer debug.SetGCPercent(debug.SetGCPercent(5))
	runtime.GC()
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	baseline := sample[0].Value.Uint64()

	var peak atomic.Uint64
	done, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		local := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
		for {
			metrics.Read(local)
			if value := local[0].Value.Uint64(); value > peak.Load() {
				peak.Store(value)
			}
			select {
			case <-done:
				return
			case <-time.After(time.Millisecond):
			}
		}
	}()
	work()
	close(done)
	<-sampled
	return max(peak.Load(), baseline) - baseline
}
