//go:build integration

package ingest_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/pipeline"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// withUnrestrictedScope stamps the request context with the scope
// RequireSession would have resolved, so a test calling a scoped handler
// directly — bypassing that middleware — exercises the same request the
// handler expects in production instead of tripping scopeOrRefuse's
// fail-closed 500.
func withUnrestrictedScope(request *http.Request) *http.Request {
	return request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
}

func TestEnvelopePersistsAcceptedEventEndToEnd(t *testing.T) {
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

	repositories := storagepostgres.NewProjectRepository(pool)
	owner, err := repositories.Projects.Create(ctx, project.CreateProject{Slug: "svc", Name: "Svc", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	const publicKey = "ingest-public-key"
	if _, err := repositories.Keys.Create(ctx, project.CreateKey{ProjectID: owner.ID, PublicKey: publicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}); err != nil {
		t.Fatalf("create ingest key: %v", err)
	}

	persistence := pipeline.New(storagepostgres.NewEventSink(pool), 1, time.Now, func(err error) {
		t.Errorf("persistence error: %v", err)
	})
	persistence.Start()
	handler := ingest.NewHandler(storagepostgres.NewIngestKeyStore(pool), time.Now, func(err error) {
		t.Errorf("ingest error: %v", err)
	}, persistence.Dispatch, persistence.DispatchSpans)

	eventPayload := `{"event_id":"b1c2d3e4-f5a6-4b8c-9d0e-1f2a3b4c5d6e","level":"error",` +
		`"release":"svc@1.0.0","message":"boom","tags":{"service":"billing"},` +
		`"exception":{"values":[{"type":"IllegalStateException","stacktrace":{"frames":[` +
		`{"module":"com.acme.Svc","function":"run","in_app":true}]}}]}}`
	body := "{}\n{\"type\":\"event\"}\n" + eventPayload + "\n"
	projectID := strconv.FormatInt(owner.ID, 10)
	path := "/api/" + projectID + "/envelope/?sentry_key=" + publicKey
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.SetPathValue("project_id", projectID)
	recorder := httptest.NewRecorder()
	handler.ServeSentry(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", recorder.Code, recorder.Body.String())
	}

	// Stopping forces the final flush, making the write observable.
	if err := persistence.Stop(ctx); err != nil {
		t.Fatalf("stop pipeline: %v", err)
	}

	var (
		issues     int64
		events     int64
		timesSeen  int64
		releases   int64
		tagRecords int64
	)
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1`, owner.ID).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE project_id = $1`, owner.ID).Scan(&events); err != nil {
		t.Fatalf("count events: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT times_seen FROM issues WHERE project_id = $1`, owner.ID).Scan(&timesSeen); err != nil {
		t.Fatalf("issue times_seen: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM releases WHERE project_id = $1 AND version = $2`, owner.ID, "svc@1.0.0").Scan(&releases); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM issue_tag_values v
		JOIN issues i ON i.id = v.issue_id
		WHERE i.project_id = $1 AND v.key = 'service' AND v.value = 'billing'
	`, owner.ID).Scan(&tagRecords); err != nil {
		t.Fatalf("count tag values: %v", err)
	}

	if issues != 1 {
		t.Fatalf("issues = %d, want 1", issues)
	}
	if events != 1 {
		t.Fatalf("events = %d, want 1", events)
	}
	if timesSeen != 1 {
		t.Fatalf("times_seen = %d, want 1", timesSeen)
	}
	if releases != 1 {
		t.Fatalf("releases = %d, want 1", releases)
	}
	if tagRecords != 1 {
		t.Fatalf("service=billing tag rows = %d, want 1", tagRecords)
	}
}

// TestEnvironmentAndReleaseFlowThroughPipelineToFiltersAndFacets is the
// end-to-end pin for the environment/release filter fix: issueDeltaFor now
// synthesizes "environment" and "release" tag deltas from the event's
// reserved scalar columns, so they must reach issue_tag_values through the
// real ingest -> pipeline -> storage path (not a hand-built storage.Batch)
// and from there satisfy the issue-list filters and facet endpoints.
func TestEnvironmentAndReleaseFlowThroughPipelineToFiltersAndFacets(t *testing.T) {
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

	repositories := storagepostgres.NewProjectRepository(pool)
	owner, err := repositories.Projects.Create(ctx, project.CreateProject{Slug: "facet-svc", Name: "Facet Svc", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	const publicKey = "facet-ingest-key"
	if _, err := repositories.Keys.Create(ctx, project.CreateKey{ProjectID: owner.ID, PublicKey: publicKey, RateLimitRPS: 1000, RateLimitBurst: 1000}); err != nil {
		t.Fatalf("create ingest key: %v", err)
	}

	persistence := pipeline.New(storagepostgres.NewEventSink(pool), 1, time.Now, func(err error) {
		t.Errorf("persistence error: %v", err)
	})
	persistence.Start()
	handler := ingest.NewHandler(storagepostgres.NewIngestKeyStore(pool), time.Now, func(err error) {
		t.Errorf("ingest error: %v", err)
	}, persistence.Dispatch, persistence.DispatchSpans)

	postEvent := func(eventID, environment, release, exceptionType string) {
		t.Helper()
		var releaseField string
		if release != "" {
			releaseField = `"release":"` + release + `",`
		}
		eventPayload := `{"event_id":"` + eventID + `","level":"error",` +
			`"environment":"` + environment + `",` + releaseField +
			`"message":"boom",` +
			`"exception":{"values":[{"type":"` + exceptionType + `","stacktrace":{"frames":[` +
			`{"module":"com.acme.Svc","function":"run","in_app":true}]}}]}}`
		body := "{}\n{\"type\":\"event\"}\n" + eventPayload + "\n"
		projectID := strconv.FormatInt(owner.ID, 10)
		path := "/api/" + projectID + "/envelope/?sentry_key=" + publicKey
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		request.SetPathValue("project_id", projectID)
		recorder := httptest.NewRecorder()
		handler.ServeSentry(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("ingest status = %d, want 200: %s", recorder.Code, recorder.Body.String())
		}
	}

	// Event A: explicit environment + release, own exception type so it
	// fingerprints into its own issue.
	postEvent("b1c2d3e4-f5a6-4b8c-9d0e-1f2a3b4c5d6e", "production", "1.2.3", "ProductionError")
	// Event B: a different environment and no release field at all — proves a
	// nil release produces no release tag row rather than an empty-value one.
	postEvent("c2d3e4f5-a6b7-4c9d-8e1f-2a3b4c5d6e7f", "staging", "", "StagingError")

	if err := persistence.Stop(ctx); err != nil {
		t.Fatalf("stop pipeline: %v", err)
	}

	service := issue.NewService(storagepostgres.NewIssueRepository(pool))
	issueHandler := api.NewIssueHandler(service, api.Options{})
	facetHandler := api.NewFacetHandler(service, api.Options{})
	const slug = "facet-svc"

	getIssueTitles := func(query string) []string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+slug+"/issues?"+query, http.NoBody)
		request.SetPathValue("slug", slug)
		request = withUnrestrictedScope(request)
		issueHandler.ListIssues(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("list issues (%s) status = %d: %s", query, recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Issues []struct {
				Title string `json:"title"`
			} `json:"issues"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode list issues (%s): %v", query, err)
		}
		titles := make([]string, 0, len(payload.Issues))
		for _, listed := range payload.Issues {
			titles = append(titles, listed.Title)
		}
		return titles
	}

	if got := getIssueTitles("environment=production"); len(got) != 1 {
		t.Fatalf("environment=production issues = %v, want exactly one", got)
	}
	if got := getIssueTitles("environment=nonexistent"); len(got) != 0 {
		t.Fatalf("environment=nonexistent issues = %v, want none", got)
	}
	if got := getIssueTitles("release=1.2.3"); len(got) != 1 {
		t.Fatalf("release=1.2.3 issues = %v, want exactly one", got)
	}
	if got := getIssueTitles("release=9.9.9"); len(got) != 0 {
		t.Fatalf("release=9.9.9 issues = %v, want none", got)
	}

	getTagKeys := func() []string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+slug+"/tags", http.NoBody)
		request.SetPathValue("slug", slug)
		request = withUnrestrictedScope(request)
		facetHandler.TagKeys(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("tag keys status = %d: %s", recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Tags []struct {
				Key string `json:"key"`
			} `json:"tags"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode tag keys: %v", err)
		}
		keys := make([]string, 0, len(payload.Tags))
		for _, tag := range payload.Tags {
			keys = append(keys, tag.Key)
		}
		return keys
	}
	// TagKeys excludes the reserved environment/release keys: they have
	// dedicated filter controls and query parameters, so listing them here
	// too would let the generic tag picker build a `tag=environment:x` filter
	// that silently shadows `environment=x`.
	tagKeys := getTagKeys()
	for _, key := range tagKeys {
		if key == "environment" || key == "release" {
			t.Fatalf("tag keys = %v, must not include the reserved environment/release keys", tagKeys)
		}
	}

	getTagValues := func(key string) []string {
		t.Helper()
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+slug+"/tags/"+key+"/values", http.NoBody)
		request.SetPathValue("slug", slug)
		request.SetPathValue("key", key)
		request = withUnrestrictedScope(request)
		facetHandler.TagValues(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("tag values (%s) status = %d: %s", key, recorder.Code, recorder.Body.String())
		}
		var payload struct {
			Values []struct {
				Value string `json:"value"`
			} `json:"values"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
			t.Fatalf("decode tag values (%s): %v", key, err)
		}
		values := make([]string, 0, len(payload.Values))
		for _, value := range payload.Values {
			values = append(values, value.Value)
		}
		return values
	}

	environmentValues := getTagValues("environment")
	wantEnvironments := map[string]bool{"production": true, "staging": true}
	if len(environmentValues) != 2 {
		t.Fatalf("environment values = %v, want [production staging]", environmentValues)
	}
	for _, value := range environmentValues {
		if !wantEnvironments[value] {
			t.Fatalf("environment values = %v, want only production/staging", environmentValues)
		}
	}

	releaseValues := getTagValues("release")
	if len(releaseValues) != 1 || releaseValues[0] != "1.2.3" {
		t.Fatalf("release values = %v, want exactly [1.2.3] (staging's nil release must not appear)", releaseValues)
	}

	// Directly confirm at the storage layer that the nil-release event's
	// issue produced no "release" row at all, rather than one with an empty
	// value that a naive fix might have written.
	var stagingReleaseRows int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM issue_tag_values v
		JOIN issues i ON i.id = v.issue_id
		JOIN events e ON e.issue_id = i.id
		WHERE i.project_id = $1 AND v.key = 'release' AND e.environment = 'staging'
	`, owner.ID).Scan(&stagingReleaseRows); err != nil {
		t.Fatalf("count staging release rows: %v", err)
	}
	if stagingReleaseRows != 0 {
		t.Fatalf("staging release rows = %d, want 0 (nil release must not create a tag row)", stagingReleaseRows)
	}
}
