//go:build integration

package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
)

const androidMappingUUID = "6f1c2a3b-4d5e-5f60-8a7b-9c0d1e2f3a4b"

const androidMapping = `com.example.player.SourceException -> bu4:
com.example.player.Source -> a.b:
    6:9:void open(int):30:33 -> b
`

type nothingResolved struct{}

func (nothingResolved) Resolve(context.Context, sourcemap.EventInput) (*sourcemap.Exception, error) {
	return nil, nil
}

// productionResolver chains the resolvers the way internal/command/run.go does.
func productionResolver(pool *pgxpool.Pool) sourcemap.Resolver {
	return native.NewResolver(
		storagepostgres.NewNativeDebugFileRepository(pool),
		proguard.NewResolver(storagepostgres.NewProguardMappingRepository(pool), nothingResolved{}),
	)
}

func newLabellingHarness(t *testing.T, resolverFor func(*pgxpool.Pool) sourcemap.Resolver, options issue.LabellerOptions) *nativeGroupingHarness {
	t.Helper()
	var labeller *issue.Labeller
	harness := startNativeGroupingHarness(t, func(pool *pgxpool.Pool, report func(error)) storage.EventSink {
		options.ReportError = report
		labeller = issue.NewLabeller(storagepostgres.NewIssueLabelStore(pool), options)
		return storagepostgres.NewEventSinkWithOptions(pool, storagepostgres.EventSinkOptions{AfterCommit: labeller.Offer})
	})
	ctx, cancel := context.WithCancel(harness.ctx)
	t.Cleanup(cancel)
	go labeller.Run(ctx, resolverFor(harness.pool))
	return harness
}

type issueLabels struct {
	fingerprint, title, culprit, displayTitle, displayCulprit string
}

func (h *nativeGroupingHarness) labelsOf(issueID int64) issueLabels {
	h.t.Helper()
	var labels issueLabels
	if err := h.pool.QueryRow(h.ctx, `
		SELECT fingerprint, title, culprit, display_title, display_culprit FROM issues WHERE id = $1
	`, issueID).Scan(&labels.fingerprint, &labels.title, &labels.culprit, &labels.displayTitle, &labels.displayCulprit); err != nil {
		h.t.Fatalf("labels of issue %d: %v", issueID, err)
	}
	return labels
}

func (h *nativeGroupingHarness) awaitLabels(issueID int64, done func(issueLabels) bool) issueLabels {
	h.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		labels := h.labelsOf(issueID)
		if done(labels) {
			return labels
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("issue %d labels = %+v, never reached the expected state", issueID, labels)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (h *nativeGroupingHarness) uploadMapping() {
	h.t.Helper()
	upload, err := proguard.DecodeUpload(strings.NewReader(androidMapping))
	if err != nil {
		h.t.Fatalf("decode mapping: %v", err)
	}
	if _, err := storagepostgres.NewProguardMappingRepository(h.pool).Put(h.ctx, h.projectID, androidMappingUUID, upload); err != nil {
		h.t.Fatalf("store mapping: %v", err)
	}
}

func (h *nativeGroupingHarness) postEvent(payload func(eventID string) string) uuid.UUID {
	h.t.Helper()
	eventID := uuid.New()
	hexID := strings.ReplaceAll(eventID.String(), "-", "")
	body := fmt.Sprintf("{\"event_id\":%q}\n{\"type\":\"event\"}\n%s\n", hexID, payload(hexID))
	projectIDPath := strconv.FormatInt(h.projectID, 10)
	request := httptest.NewRequest(http.MethodPost, "/api/"+projectIDPath+"/envelope/?sentry_key="+cocoaFixtureKey, strings.NewReader(body))
	request.SetPathValue("project_id", projectIDPath)
	recorder := httptest.NewRecorder()
	h.handler.ServeSentry(recorder, request)
	if recorder.Code != http.StatusOK {
		h.t.Fatalf("post event: status = %d body = %s", recorder.Code, recorder.Body.String())
	}
	flushCtx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	if err := h.persistence.Flush(flushCtx); err != nil {
		h.t.Fatalf("flush: %v", err)
	}
	return eventID
}

func androidCrash(eventID string) string {
	return fmt.Sprintf(`{"event_id":%q,"timestamp":%q,"platform":"java","level":"error","release":"player@1.0",`+
		`"exception":{"values":[{"type":"bu4","value":"Source error","stacktrace":{"frames":[`+
		`{"module":"a.b","function":"b","filename":"SourceFile","lineno":7,"in_app":true}]}}]},`+
		`"debug_meta":{"images":[{"type":"proguard","uuid":%q}]}}`,
		eventID, time.Now().UTC().Format(time.RFC3339), androidMappingUUID)
}

func browserError(eventID string) string {
	return fmt.Sprintf(`{"event_id":%q,"timestamp":%q,"platform":"javascript","level":"error","release":"web@1",`+
		`"exception":{"values":[{"type":"TypeError","value":"x is undefined","stacktrace":{"frames":[`+
		`{"filename":"https://shop.example/static/app.min.js","function":"n","lineno":1,"colno":42,"in_app":true}]}}]}}`,
		eventID, time.Now().UTC().Format(time.RFC3339))
}

func shown(raw, display string) string {
	if display != "" {
		return display
	}
	return raw
}

func TestNativeCrashWithItsDebugFileListsTheFunction(t *testing.T) {
	h := newLabellingHarness(t, productionResolver, issue.LabellerOptions{})
	h.uploadDebugFile("build-a")

	issueID := h.issueOf(h.postCrash("build-a", ""))

	page, err := issue.NewService(storagepostgres.NewIssueRepository(h.pool)).ListIssues(h.ctx, issue.ListFilters{
		Scope: access.Unrestricted(), ProjectSlug: "ios",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Issues) != 1 || page.Issues[0].ID != issueID {
		t.Fatalf("listed %+v, want the crash's issue", page.Issues)
	}
	if culprit := shown(page.Issues[0].Culprit, page.Issues[0].DisplayCulprit); culprit != "submit_order" {
		t.Fatalf("listed culprit = %q, want the app function", culprit)
	}
	h.noErrorsReported()
}

func TestDebugFileUploadedLaterLabelsTheIssueOnItsNextEvent(t *testing.T) {
	h := newLabellingHarness(t, productionResolver, issue.LabellerOptions{})
	issueID := h.issueOf(h.postCrash("build-a", ""))
	before := h.labelsOf(issueID)
	if !strings.Contains(before.culprit, "+0x") || before.displayCulprit != "" {
		t.Fatalf("labels before the debug file = %+v, want an offset culprit and no display label", before)
	}

	h.uploadDebugFile("build-a")
	if h.issueOf(h.postCrash("build-a", "")) != issueID {
		t.Fatal("the crash after the upload must stay on its issue")
	}

	after := h.awaitLabels(issueID, func(labels issueLabels) bool { return labels.displayCulprit != "" })
	if !strings.HasPrefix(after.displayCulprit, "submit_order") {
		t.Fatalf("display culprit = %q, want the crashing function", after.displayCulprit)
	}
	page, err := issue.NewService(storagepostgres.NewIssueRepository(h.pool)).ListIssues(h.ctx, issue.ListFilters{
		Scope: access.Unrestricted(), ProjectSlug: "ios",
	})
	if err != nil || len(page.Issues) != 1 || page.Issues[0].DisplayCulprit != after.displayCulprit {
		t.Fatalf("listed %+v (%v), want the issue with its display culprit", page.Issues, err)
	}
	if after.fingerprint != before.fingerprint || after.title != before.title || after.culprit != before.culprit {
		t.Fatalf("labels %+v changed the grouping columns of %+v", after, before)
	}
	h.noErrorsReported()
}

func TestDebugFileUploadedLaterLabelsTheIssueWhenItsPageResolvesAnEvent(t *testing.T) {
	h := newNativeGroupingHarness(t)
	issueID := h.issueOf(h.postCrash("build-a", ""))
	before := h.labelsOf(issueID)
	h.uploadDebugFile("build-a")
	service := issue.NewService(storagepostgres.NewIssueRepository(h.pool), productionResolver(h.pool))

	if _, err := service.LatestEvent(h.ctx, access.Unrestricted(), issueID); err != nil {
		t.Fatal(err)
	}

	after := h.labelsOf(issueID)
	if !strings.HasPrefix(after.displayCulprit, "submit_order (") {
		t.Fatalf("display culprit = %q, want the function and its file", after.displayCulprit)
	}
	if after.fingerprint != before.fingerprint || after.title != before.title || after.culprit != before.culprit {
		t.Fatalf("labels %+v changed the grouping columns of %+v", after, before)
	}
	detail, err := service.IssueDetail(h.ctx, access.Unrestricted(), issueID)
	if err != nil || detail.Issue.DisplayCulprit != after.displayCulprit {
		t.Fatalf("detail display culprit = %q (%v), want the refreshed one", detail.Issue.DisplayCulprit, err)
	}
}

func TestObfuscatedAndroidCrashWithItsMappingListsTheOriginalException(t *testing.T) {
	h := newLabellingHarness(t, productionResolver, issue.LabellerOptions{})
	h.uploadMapping()

	issueID := h.issueOf(h.postEvent(androidCrash))

	labels := h.awaitLabels(issueID, func(labels issueLabels) bool { return labels.displayTitle != "" })
	if labels.title != "bu4: Source error" || labels.displayTitle != "SourceException: Source error" {
		t.Fatalf("labels = %+v, want the raw title kept and the deobfuscated one displayed", labels)
	}
	if !strings.HasPrefix(labels.displayCulprit, "com.example.player.Source.open (") {
		t.Fatalf("display culprit = %q, want the retraced method", labels.displayCulprit)
	}
	h.noErrorsReported()
}

func TestUnobfuscatedEventKeepsItsRawLabels(t *testing.T) {
	h := newLabellingHarness(t, productionResolver, issue.LabellerOptions{})

	issueID := h.issueOf(h.postEvent(browserError))
	h.issueOf(h.postEvent(browserError))

	if labels := h.labelsOf(issueID); labels.displayTitle != "" || labels.displayCulprit != "" {
		t.Fatalf("labels = %+v, want no display labels for an event nothing symbolicated", labels)
	}
	h.noErrorsReported()
}

type blockingResolver struct {
	release chan struct{}
	calls   chan struct{}
}

func (r blockingResolver) Resolve(ctx context.Context, _ sourcemap.EventInput) (*sourcemap.Exception, error) {
	r.calls <- struct{}{}
	select {
	case <-r.release:
	case <-ctx.Done():
	}
	return nil, errors.New("symbol server unavailable")
}

func TestIngestCommitsWhileTheLabelResolverIsSlowOrFailing(t *testing.T) {
	resolver := blockingResolver{release: make(chan struct{}), calls: make(chan struct{}, 8)}
	h := newLabellingHarness(t, func(*pgxpool.Pool) sourcemap.Resolver { return resolver }, issue.LabellerOptions{QueueSize: 1})

	first := h.postEvent(browserError)
	select {
	case <-resolver.calls:
	case <-time.After(10 * time.Second):
		t.Fatal("the labeller never asked the resolver")
	}
	started := time.Now()
	second := h.postEvent(browserError)
	h.postEvent(browserError)
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("ingest took %v while the resolver hung", elapsed)
	}
	if h.count(`SELECT count(*) FROM events WHERE project_id = $1`) != 3 || h.issueOf(first) != h.issueOf(second) {
		t.Fatal("every event must be stored on its issue while the resolver hangs and the queue is full")
	}

	close(resolver.release)
	deadline := time.Now().Add(10 * time.Second)
	for {
		h.mu.Lock()
		reported := len(h.reported)
		h.mu.Unlock()
		if reported > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the resolver failure was never reported")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if labels := h.labelsOf(h.issueOf(first)); labels.displayTitle != "" || labels.displayCulprit != "" {
		t.Fatalf("labels = %+v, want the raw labels kept after a failed resolve", labels)
	}
}
