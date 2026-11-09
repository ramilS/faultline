//go:build integration

package ingest_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/pipeline"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

const cocoaFixtureKey = "cocoa-fixture-key"

// cocoaFixtureEventIDs are the event ids generate.sh writes into each build's
// envelope; posting the same crash again swaps in a fresh id.
var cocoaFixtureEventIDs = map[string]string{
	"build-a": "7a1c2b3d4e5f40718293a4b5c6d7e8f9",
	"build-b": "8b2d3c4e5f6041829304b5c6d7e8f9a0",
}

type nativeGroupingHarness struct {
	t           *testing.T
	ctx         context.Context
	pool        *pgxpool.Pool
	projectID   int64
	debugFiles  native.Repository
	persistence *pipeline.Pipeline
	handler     *ingest.Handler
	mu          sync.Mutex
	reported    []error
}

func newNativeGroupingHarness(t *testing.T) *nativeGroupingHarness {
	t.Helper()
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	harness := &nativeGroupingHarness{t: t, ctx: ctx, pool: pool, debugFiles: storagepostgres.NewNativeDebugFileRepository(pool)}
	if err := pool.QueryRow(ctx, `INSERT INTO projects (slug, name, platform) VALUES ('ios', 'iOS', 'other') RETURNING id`).Scan(&harness.projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_keys (project_id, public_key, rate_limit_rps, rate_limit_burst) VALUES ($1, $2, 200, 1000)
	`, harness.projectID, cocoaFixtureKey); err != nil {
		t.Fatalf("seed ingest key: %v", err)
	}

	harness.persistence = pipeline.New(storagepostgres.NewEventSink(pool), 1, time.Now, func(err error) { t.Errorf("persistence error: %v", err) })
	harness.persistence.Start()
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer stopCancel()
		_ = harness.persistence.Stop(stopCtx)
	})
	resolver := native.NewResolver(harness.debugFiles, nil)
	harness.handler = ingest.NewHandlerWithOptions(ingest.HandlerOptions{
		Store:         storagepostgres.NewIngestKeyStore(pool),
		Now:           time.Now,
		ReportError:   harness.report,
		Dispatch:      harness.persistence.Dispatch,
		DispatchSpans: harness.persistence.DispatchSpans,
		Symbols:       resolver.FunctionNames,
	})
	return harness
}

func (h *nativeGroupingHarness) report(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.reported = append(h.reported, err)
}

func (h *nativeGroupingHarness) uploadDebugFile(build string) {
	h.t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", "cocoa", build+".dwarf"))
	if err != nil {
		h.t.Fatal(err)
	}
	upload, err := native.DecodeUpload(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		h.t.Fatalf("decode %s debug file: %v", build, err)
	}
	if err := h.debugFiles.Put(h.ctx, h.projectID, upload); err != nil {
		h.t.Fatalf("store %s debug file: %v", build, err)
	}
}

func (h *nativeGroupingHarness) postCrash(build, exceptionType string) uuid.UUID {
	h.t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "cocoa", build+".envelope"))
	if err != nil {
		h.t.Fatal(err)
	}
	eventID := uuid.New()
	body := strings.ReplaceAll(string(data), cocoaFixtureEventIDs[build], strings.ReplaceAll(eventID.String(), "-", ""))
	if exceptionType != "" {
		body = strings.Replace(body, `"type":"EXC_BAD_ACCESS"`, `"type":"`+exceptionType+`"`, 1)
	}
	lines := strings.Split(body, "\n")
	lines[1] = `{"type":"event"}`
	body = strings.Join(lines, "\n")

	projectIDPath := strconv.FormatInt(h.projectID, 10)
	request := httptest.NewRequest(http.MethodPost, "/api/"+projectIDPath+"/envelope/?sentry_key="+cocoaFixtureKey, strings.NewReader(body))
	request.SetPathValue("project_id", projectIDPath)
	recorder := httptest.NewRecorder()
	h.handler.ServeSentry(recorder, request)
	if recorder.Code != http.StatusOK {
		h.t.Fatalf("post %s: status = %d body = %s", build, recorder.Code, recorder.Body.String())
	}
	flushCtx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	if err := h.persistence.Flush(flushCtx); err != nil {
		h.t.Fatalf("flush: %v", err)
	}
	return eventID
}

func (h *nativeGroupingHarness) issueOf(eventID uuid.UUID) int64 {
	h.t.Helper()
	var issueID int64
	if err := h.pool.QueryRow(h.ctx, `SELECT issue_id FROM events WHERE project_id = $1 AND event_id = $2`, h.projectID, eventID).Scan(&issueID); err != nil {
		h.t.Fatalf("issue of event %s: %v", eventID, err)
	}
	return issueID
}

func (h *nativeGroupingHarness) count(query string) int {
	h.t.Helper()
	var count int
	if err := h.pool.QueryRow(h.ctx, query, h.projectID).Scan(&count); err != nil {
		h.t.Fatalf("%s: %v", query, err)
	}
	return count
}

func (h *nativeGroupingHarness) releasesOf(issueID int64) []string {
	h.t.Helper()
	rows, err := h.pool.Query(h.ctx, `SELECT value FROM issue_tag_values WHERE issue_id = $1 AND key = 'release' ORDER BY value`, issueID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer rows.Close()
	var releases []string
	for rows.Next() {
		var release string
		if err := rows.Scan(&release); err != nil {
			h.t.Fatal(err)
		}
		releases = append(releases, release)
	}
	return releases
}

func (h *nativeGroupingHarness) noErrorsReported() {
	h.t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.reported) > 0 {
		h.t.Fatalf("reported %v", h.reported)
	}
}

func TestNativeCrashOfTwoBuildsWithDebugFilesLandsOnOneIssue(t *testing.T) {
	h := newNativeGroupingHarness(t)
	h.uploadDebugFile("build-a")
	h.uploadDebugFile("build-b")

	first := h.postCrash("build-a", "")
	second := h.postCrash("build-b", "")
	abort := h.postCrash("build-b", "EXC_CRASH")

	issueID := h.issueOf(first)
	if h.issueOf(second) != issueID {
		t.Fatal("build B's crash, at other offsets and on another line, must land on build A's issue")
	}
	if h.issueOf(abort) == issueID {
		t.Fatal("another exception type in the same function must be another issue")
	}
	if releases := h.releasesOf(issueID); !slices.Equal(releases, []string{"MyApp@1.0.build-a", "MyApp@1.0.build-b"}) {
		t.Fatalf("releases = %v, want both builds on the issue", releases)
	}
	var culprit string
	if err := h.pool.QueryRow(h.ctx, `SELECT culprit FROM issues WHERE id = $1`, issueID).Scan(&culprit); err != nil {
		t.Fatal(err)
	}
	if culprit != "submit_order" {
		t.Fatalf("culprit = %q, want the crashing function", culprit)
	}
	if aliases := h.count(`SELECT count(*) FROM issue_fingerprint_aliases WHERE project_id = $1`); aliases != 0 {
		t.Fatalf("aliases = %d, want none when no offset issue existed", aliases)
	}
	h.noErrorsReported()
}

func TestNativeCrashWithoutDebugFilesKeepsOneIssuePerBuild(t *testing.T) {
	h := newNativeGroupingHarness(t)

	first := h.postCrash("build-a", "")
	relaunch := h.postCrash("build-a", "")
	second := h.postCrash("build-b", "")

	if h.issueOf(first) != h.issueOf(relaunch) {
		t.Fatal("the same build's crash must keep landing on its offset issue")
	}
	if h.issueOf(first) == h.issueOf(second) {
		t.Fatal("without debug files the builds' offsets differ, so their issues do too")
	}
	h.noErrorsReported()
}

func TestDebugFileUploadedAfterTheFirstCrashKeepsItsIssue(t *testing.T) {
	h := newNativeGroupingHarness(t)
	beforeUpload := h.postCrash("build-a", "")
	offsetIssue := h.issueOf(beforeUpload)

	h.uploadDebugFile("build-a")
	afterUpload := h.postCrash("build-a", "")
	if h.issueOf(afterUpload) != offsetIssue {
		t.Fatal("the first named crash must land on the issue its offset fingerprint created")
	}
	if aliases := h.count(`SELECT count(*) FROM issue_fingerprint_aliases WHERE project_id = $1`); aliases != 1 {
		t.Fatalf("aliases = %d, want the named fingerprint aliased to the offset issue", aliases)
	}

	h.uploadDebugFile("build-b")
	nextBuild := h.postCrash("build-b", "")
	if h.issueOf(nextBuild) != offsetIssue {
		t.Fatal("the next build's crash must reach the offset issue through the alias")
	}
	if issues := h.count(`SELECT count(*) FROM issues WHERE project_id = $1`); issues != 1 {
		t.Fatalf("issues = %d, want 1", issues)
	}
	var timesSeen int
	if err := h.pool.QueryRow(h.ctx, `SELECT times_seen FROM issues WHERE id = $1`, offsetIssue).Scan(&timesSeen); err != nil {
		t.Fatal(err)
	}
	if timesSeen != 3 {
		t.Fatalf("times_seen = %d, want every crash counted on the issue", timesSeen)
	}
	h.noErrorsReported()
}

func TestUnreadableDebugFileStoresTheCrashOnItsOffsetIssue(t *testing.T) {
	h := newNativeGroupingHarness(t)
	unnamed := h.postCrash("build-a", "")
	h.uploadDebugFile("build-a")
	if _, err := h.pool.Exec(h.ctx, `UPDATE native_debug_file_blobs SET compressed_file = 'not gzip' WHERE project_id = $1`, h.projectID); err != nil {
		t.Fatal(err)
	}

	crash := h.postCrash("build-a", "")

	if h.issueOf(crash) != h.issueOf(unnamed) {
		t.Fatal("a crash whose debug file cannot be read must be stored on its offset issue")
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.reported) != 1 || !strings.Contains(h.reported[0].Error(), "read native debug file") {
		t.Fatalf("reported %v, want the unreadable debug file", h.reported)
	}
}

func TestIssueOfTheNamedFingerprintWinsOverTheOffsetIssue(t *testing.T) {
	h := newNativeGroupingHarness(t)
	offsetIssue := h.issueOf(h.postCrash("build-a", ""))
	h.uploadDebugFile("build-b")
	namedIssue := h.issueOf(h.postCrash("build-b", ""))
	if namedIssue == offsetIssue {
		t.Fatal("build B's named crash has no offset issue to join, so it must open its own")
	}

	h.uploadDebugFile("build-a")
	crash := h.postCrash("build-a", "")

	if h.issueOf(crash) != namedIssue {
		t.Fatal("a named crash must join the issue of its named fingerprint, not the offset issue")
	}
	if aliases := h.count(`SELECT count(*) FROM issue_fingerprint_aliases WHERE project_id = $1`); aliases != 0 {
		t.Fatalf("aliases = %d, want none: issues that already split stay apart", aliases)
	}
	h.noErrorsReported()
}
