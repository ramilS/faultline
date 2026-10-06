//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// TestSourceMapUploadAndEventRead proves the full source-map loop against a real
// database: upload an immutable release-scoped map with a bearer token, persist
// a minified browser event at that release, then read the event back and see
// its frames symbolicated to their original source location and context.
func TestSourceMapUploadAndEventRead(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var projectID int64
	if err := pool.QueryRow(ctx, `INSERT INTO projects (slug,name,platform) VALUES ('web','Web','javascript') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// Create an artifact token and upload the checked-in fixture map for web@1.
	artifactService := artifactauth.NewService(storagepostgres.NewArtifactTokenRepository(pool), artifactauth.Options{})
	_, rawToken, err := artifactService.Create(ctx, projectID, "production CI")
	if err != nil {
		t.Fatalf("create artifact token: %v", err)
	}
	projectRepositories := storagepostgres.NewProjectRepository(pool)
	projectService := project.NewService(projectRepositories.Projects, projectRepositories.Keys)
	sourceMapRepository := storagepostgres.NewSourceMapRepository(pool)
	artifactHandler := api.NewArtifactHandler(artifactService, projectService, sourceMapRepository, api.Options{})

	fixture, err := os.ReadFile(filepath.Join("..", "sourcemap", "testdata", "app.js.map"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	uploadRecorder := httptest.NewRecorder()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/sourcemaps?release=web%401&path=%2Fassets%2Fapp.js", bytes.NewReader(fixture))
	uploadRequest.Header.Set("Authorization", "Bearer "+rawToken)
	uploadRequest.Header.Set("Content-Type", "application/json")
	uploadRequest.SetPathValue("slug", "web")
	artifactHandler.UploadSourceMap(uploadRecorder, uploadRequest)
	if uploadRecorder.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	// Persist a minified browser exception at the same release. The generated
	// frame at line 1, column 100 maps to src/checkout.ts:42:9 via the fixture.
	// The payload goes through the Sentry mapper rather than being hand-written:
	// events.payload stores the SDK envelope byte-for-byte, and symbolication
	// reads the exception out of it, so a payload the mapper would not accept
	// proves nothing about the read path.
	now := time.Now().UTC()
	envelope := []byte(`{"event_id":"` + uuid.New().String() + `","timestamp":"` + now.Format(time.RFC3339) + `","level":"error","environment":"production","release":"web@1","message":"TypeError",` +
		`"exception":{"values":[{"type":"TypeError","value":"cart is undefined","stacktrace":{"frames":[{"filename":"https://cdn.example/assets/app.js","function":"a","lineno":1,"colno":100,"in_app":true}]}}]}}`)
	mapped, err := ingest.MapSentryEvent(envelope, now)
	if err != nil {
		t.Fatalf("map sentry event: %v", err)
	}
	sink := storagepostgres.NewEventSink(pool)
	if err := sink.PersistBatch(ctx, storage.Batch{
		ProjectID: projectID, Releases: []string{*mapped.Release},
		Issues: []storage.IssueDelta{{Fingerprint: fill64("5a"), Title: "TypeError", Level: "error", Count: 1, FirstSeen: now, LastSeen: now,
			Tags: []storage.TagDelta{{Key: "environment", Value: "production", Count: 1, FirstSeen: now, LastSeen: now}}}},
		Events: []storage.EventRow{{ProjectID: projectID, EventID: mapped.EventID, Timestamp: mapped.Timestamp, ReceivedAt: now, Level: mapped.Level,
			Environment: mapped.Environment, Message: mapped.Message, Release: mapped.Release, Fingerprint: fill64("5a"), Payload: mapped.Payload}},
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// Read the latest event through a resolver-backed issue service.
	resolver := sourcemap.NewResolver(sourceMapRepository, sourcemap.NewParsedMapCache(128<<20))
	issueService := issue.NewService(storagepostgres.NewIssueRepository(pool), resolver)
	issueHandler := api.NewIssueHandler(issueService, api.Options{})

	listRecorder := httptest.NewRecorder()
	listRequest := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/issues", http.NoBody)
	listRequest.SetPathValue("slug", "web")
	listRequest = listRequest.WithContext(access.ContextWithScope(listRequest.Context(), access.Unrestricted()))
	issueHandler.ListIssues(listRecorder, listRequest)
	var listBody struct {
		Issues []struct {
			ID int64 `json:"id"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listBody); err != nil || len(listBody.Issues) != 1 {
		t.Fatalf("list body = %s err = %v", listRecorder.Body.String(), err)
	}

	eventRecorder := httptest.NewRecorder()
	eventRequest := httptest.NewRequest(http.MethodGet, "/api/v1/issues/1/events/latest", http.NoBody)
	eventRequest.SetPathValue("id", strconv.FormatInt(listBody.Issues[0].ID, 10))
	eventRequest = eventRequest.WithContext(access.ContextWithScope(eventRequest.Context(), access.Unrestricted()))
	issueHandler.LatestEvent(eventRecorder, eventRequest)
	if eventRecorder.Code != http.StatusOK {
		t.Fatalf("event status = %d body %s", eventRecorder.Code, eventRecorder.Body.String())
	}

	var eventBody struct {
		Event struct {
			SymbolicatedException *struct {
				Frames []struct {
					Original *struct {
						Function string `json:"function"`
						Filename string `json:"filename"`
						Line     int    `json:"line"`
						Column   int    `json:"column"`
					} `json:"original"`
					Context []struct {
						Line  int  `json:"line"`
						Focus bool `json:"focus"`
					} `json:"context"`
				} `json:"frames"`
			} `json:"symbolicated_exception"`
		} `json:"event"`
	}
	if err := json.Unmarshal(eventRecorder.Body.Bytes(), &eventBody); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	exception := eventBody.Event.SymbolicatedException
	if exception == nil || len(exception.Frames) != 1 {
		t.Fatalf("symbolicated event = %s", eventRecorder.Body.String())
	}
	original := exception.Frames[0].Original
	if original == nil || original.Filename != "src/checkout.ts" || original.Line != 42 || original.Column != 9 {
		t.Fatalf("original frame = %+v", original)
	}
	if original.Function != "throwCheckoutError" {
		t.Fatalf("original function = %q", original.Function)
	}
	if len(exception.Frames[0].Context) == 0 {
		t.Fatalf("expected source context, got none")
	}
	var focusLine int
	for _, line := range exception.Frames[0].Context {
		if line.Focus {
			focusLine = line.Line
		}
	}
	if focusLine != 42 {
		t.Fatalf("focus line = %d, want 42", focusLine)
	}
}

// TestSentryCLIBundleSymbolicatesByReleaseAndPath spans the writer and the
// reader across every seam a real `sentry-cli sourcemaps upload` crosses:
// the captured bundle is staged as a chunk, assembled through the handler,
// stored by the real repository, and then read back by the resolver.
//
// The event carries NO debug_meta, so the debug-id address is unavailable and
// the release+path ref is the only way the map can be reached — exactly the
// shape a bundle uploaded without a prior `sourcemaps inject` produces. The
// frame names the minified file, "/app.js", because that is what a browser
// stack frame contains; a ref stored at the map's own "/app.js.map" is
// unreachable from here and symbolicates nothing, silently.
func TestSentryCLIBundleSymbolicatesByReleaseAndPath(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	var projectID int64
	if err := pool.QueryRow(ctx, `INSERT INTO projects (slug,name,platform) VALUES ('web','Web','javascript') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	artifactService := artifactauth.NewService(storagepostgres.NewArtifactTokenRepository(pool), artifactauth.Options{})
	_, rawToken, err := artifactService.Create(ctx, projectID, "bundler plugin")
	if err != nil {
		t.Fatalf("create artifact token: %v", err)
	}
	projectRepositories := storagepostgres.NewProjectRepository(pool)
	projectService := project.NewService(projectRepositories.Projects, projectRepositories.Keys)
	sourceMapRepository := storagepostgres.NewSourceMapRepository(pool)
	handler := api.NewSentryCLIHandler(
		artifactService, projectService,
		storagepostgres.NewUploadChunkStore(pool), sourceMapRepository,
		storagepostgres.NewReleaseStore(pool), storagepostgres.NewProguardMappingRepository(pool), api.Options{},
	)

	zipped, err := os.ReadFile(filepath.Join("..", "sentrycli", "testdata", "bundle.zip"))
	if err != nil {
		t.Fatalf("read bundle fixture: %v", err)
	}
	checksum := sentrycli.ComputeChecksum(zipped).String()

	var multipartBody bytes.Buffer
	writer := multipart.NewWriter(&multipartBody)
	part, err := writer.CreateFormFile("file", checksum)
	if err != nil {
		t.Fatalf("create chunk part: %v", err)
	}
	if _, err := part.Write(zipped); err != nil {
		t.Fatalf("write chunk part: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}

	uploadRecorder := httptest.NewRecorder()
	uploadRequest := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/chunk-upload/", &multipartBody)
	uploadRequest.Header.Set("Authorization", "Bearer "+rawToken)
	uploadRequest.Header.Set("Content-Type", writer.FormDataContentType())
	handler.UploadChunks(uploadRecorder, uploadRequest)
	if uploadRecorder.Code != http.StatusOK {
		t.Fatalf("chunk upload status = %d body %s", uploadRecorder.Code, uploadRecorder.Body.String())
	}

	assembleBody, err := json.Marshal(map[string]any{
		"checksum": checksum,
		"chunks":   []string{checksum},
		"projects": []string{"web"},
		"version":  "web@1.0.0",
	})
	if err != nil {
		t.Fatalf("marshal assemble body: %v", err)
	}
	assembleRecorder := httptest.NewRecorder()
	assembleRequest := httptest.NewRequest(http.MethodPost, "/api/0/organizations/demo/artifactbundle/assemble/", bytes.NewReader(assembleBody))
	assembleRequest.Header.Set("Authorization", "Bearer "+rawToken)
	assembleRequest.Header.Set("Content-Type", "application/json")
	handler.AssembleBundle(assembleRecorder, assembleRequest)
	if assembleRecorder.Code != http.StatusOK {
		t.Fatalf("assemble status = %d body %s", assembleRecorder.Code, assembleRecorder.Body.String())
	}

	// Generated line 3, column 142 is the call to boom() in the captured
	// minified app.js; the captured map resolves it to ../srcweb/app.js:7:10.
	payload := []byte(`{"exception":{"values":[{"type":"Error","value":"faultline sourcemap fixture",` +
		`"stacktrace":{"frames":[{"filename":"https://cdn.example/app.js","function":"e","lineno":3,"colno":142,"in_app":true}]}}]}}`)
	resolver := sourcemap.NewResolver(sourceMapRepository, sourcemap.NewParsedMapCache(128<<20))
	exception, err := resolver.Resolve(ctx, sourcemap.EventInput{ProjectID: projectID, Release: "web@1.0.0", Payload: payload})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if exception == nil || len(exception.Frames) != 1 {
		t.Fatalf("resolved exception = %+v, want one frame", exception)
	}
	original := exception.Frames[0].Original
	if original == nil {
		t.Fatalf("frame did not symbolicate: the bundle's release+path ref is unreachable from a frame naming /app.js")
	}
	if original.Filename != "../srcweb/app.js" || original.Line != 7 || original.Column != 10 {
		t.Fatalf("original = %+v, want ../srcweb/app.js:7:10", original)
	}
	if original.Function != "boom" {
		t.Fatalf("original function = %q, want boom", original.Function)
	}

	// The ref must be stored at the minified file's path, not the map's own.
	var storedPath string
	if err := pool.QueryRow(ctx, `
		SELECT generated_path FROM source_map_refs WHERE project_id = $1 AND debug_id IS NULL
	`, projectID).Scan(&storedPath); err != nil {
		t.Fatalf("read stored release+path ref: %v", err)
	}
	if storedPath != "/app.js" {
		t.Fatalf("stored generated_path = %q, want /app.js", storedPath)
	}
}
