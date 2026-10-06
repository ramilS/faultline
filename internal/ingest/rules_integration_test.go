//go:build integration

package ingest_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/pipeline"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// postEventOrFail posts a minimal single-event Sentry envelope through
// handler and fails the test unless the ingest pipeline accepts it (200).
func postEventOrFail(t *testing.T, handler *ingest.Handler, projectID int64, publicKey, eventID, logger, message string) {
	t.Helper()
	eventPayload := `{"event_id":"` + eventID + `","timestamp":"2026-07-25T00:00:00Z","level":"error","logger":"` + logger + `","message":"` + message + `"}`
	body := "{}\n{\"type\":\"event\"}\n" + eventPayload + "\n"
	projectIDPath := strconv.FormatInt(projectID, 10)
	request := httptest.NewRequest(http.MethodPost, "/api/"+projectIDPath+"/envelope/?sentry_key="+publicKey, bytes.NewReader([]byte(body)))
	request.SetPathValue("project_id", projectIDPath)
	recorder := httptest.NewRecorder()
	handler.ServeSentry(recorder, request)
	if recorder.Code != 200 {
		t.Fatalf("ingest %s: status = %d body = %s", eventID, recorder.Code, recorder.Body.String())
	}
}

// TestRuleDrivenIngestRoutesMatchingEventToAliasedIssue is the primary
// completion-boundary proof for the rules half of the spec: a rule created in
// Postgres changes where a real HTTP-decoded event lands, while a
// non-matching event's deterministic path is untouched. It never recomputes
// the alias hash independently -- it drives it through the same
// grouping.ParseTemplate/Resolve path production rule evaluation uses -- and
// it asserts on the persisted events' issue_id linkage, not just issue counts.
func TestRuleDrivenIngestRoutesMatchingEventToAliasedIssue(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('rules-e2e', 'Rules E2E', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	const publicKey = "rules-e2e-key"
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_keys (project_id, public_key, rate_limit_rps, rate_limit_burst)
		VALUES ($1, $2, 200, 1000)
	`, projectID, publicKey); err != nil {
		t.Fatalf("seed ingest key: %v", err)
	}

	ruleRepository := storagepostgres.NewGroupingRuleRepository(pool)
	if _, err := ruleRepository.Create(ctx, projectID, "logger:billing", "billing-alias"); err != nil {
		t.Fatalf("create grouping rule: %v", err)
	}

	sink := storagepostgres.NewEventSink(pool)
	persistence := pipeline.New(sink, 1, time.Now, func(error) {})
	persistence.Start()
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer stopCancel()
		_ = persistence.Stop(stopCtx)
	}()

	// The rule above is created before this cache is ever constructed, so
	// there is no stale pre-existing entry to worry about (Hazard 2): the
	// very first Get() for this project reads the rule fresh from Postgres.
	ruleCache := grouping.NewRuleCache(ruleRepository, time.Now)
	handler := ingest.NewHandlerWithOptions(ingest.HandlerOptions{
		Store:         storagepostgres.NewIngestKeyStore(pool),
		Now:           time.Now,
		ReportError:   func(error) {},
		Dispatch:      persistence.Dispatch,
		DispatchSpans: persistence.DispatchSpans,
		Rules:         ruleCache.Get,
	})

	const matchingEventID = "00000000-0000-4000-8000-000000000001"
	const nonMatchingEventID = "00000000-0000-4000-8000-000000000002"
	postEventOrFail(t, handler, projectID, publicKey, matchingEventID, "billing", "irrelevant text")
	postEventOrFail(t, handler, projectID, publicKey, nonMatchingEventID, "checkout", "unrelated issue")

	flushCtx, flushCancel := context.WithTimeout(ctx, 5*time.Second)
	defer flushCancel()
	if err := persistence.Flush(flushCtx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	aliasTemplate, err := grouping.ParseTemplate("billing-alias")
	if err != nil {
		t.Fatalf("parse alias template: %v", err)
	}
	wantAliasHash, ok := aliasTemplate.Resolve(grouping.EvaluationInput{})
	if !ok {
		t.Fatal("expected literal alias template to resolve")
	}
	wantDeterministic := grouping.Fingerprint(grouping.GroupingInput{Logger: "checkout", Message: "unrelated issue"}).Fingerprint

	var aliasIssueID, deterministicIssueID int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM issues WHERE project_id = $1 AND fingerprint = $2
	`, projectID, wantAliasHash).Scan(&aliasIssueID); err != nil {
		t.Fatalf("find alias-fingerprint issue: %v (rule did not create the aliased issue)", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT id FROM issues WHERE project_id = $1 AND fingerprint = $2
	`, projectID, wantDeterministic).Scan(&deterministicIssueID); err != nil {
		t.Fatalf("find deterministic-fingerprint issue: %v (non-matching event's baseline path was disturbed)", err)
	}
	if aliasIssueID == deterministicIssueID {
		t.Fatalf("alias issue id and deterministic issue id are both %d, want distinct issues", aliasIssueID)
	}

	// Assert the observable end state directly: which issue each persisted
	// event actually landed on, not a fingerprint string computed the same
	// way the production code computes it.
	var matchingEventIssueID, nonMatchingEventIssueID int64
	if err := pool.QueryRow(ctx, `
		SELECT issue_id FROM events WHERE project_id = $1 AND event_id = $2
	`, projectID, uuid.MustParse(matchingEventID)).Scan(&matchingEventIssueID); err != nil {
		t.Fatalf("matching event issue_id: %v", err)
	}
	if matchingEventIssueID != aliasIssueID {
		t.Fatalf("matching event issue_id = %d, want alias issue id %d", matchingEventIssueID, aliasIssueID)
	}
	if err := pool.QueryRow(ctx, `
		SELECT issue_id FROM events WHERE project_id = $1 AND event_id = $2
	`, projectID, uuid.MustParse(nonMatchingEventID)).Scan(&nonMatchingEventIssueID); err != nil {
		t.Fatalf("non-matching event issue_id: %v", err)
	}
	if nonMatchingEventIssueID != deterministicIssueID {
		t.Fatalf("non-matching event issue_id = %d, want deterministic issue id %d", nonMatchingEventIssueID, deterministicIssueID)
	}

	var aliasIssueCount, deterministicIssueCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1 AND fingerprint = $2`, projectID, wantAliasHash).Scan(&aliasIssueCount); err != nil || aliasIssueCount != 1 {
		t.Fatalf("alias-fingerprint issue count = %d, error = %v, want 1 (the rule-matching event)", aliasIssueCount, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1 AND fingerprint = $2`, projectID, wantDeterministic).Scan(&deterministicIssueCount); err != nil || deterministicIssueCount != 1 {
		t.Fatalf("deterministic-fingerprint issue count = %d, error = %v, want 1 (the non-matching event's path is untouched)", deterministicIssueCount, err)
	}
}

// TestRuleDrivenMergeRoutesFutureEventToTargetIssue is the completion-boundary
// proof for merge: an administrator merges two real issues, and a subsequent
// event whose signal originally mapped to the source now lands on the
// target -- through the real ingest pipeline, not by recomputing a
// fingerprint the same way production code does.
//
// This is the test that would catch the digest-mismatch hazard documented on
// grouping.MergeFingerprint: if that function ever hashed the joined template
// string instead of its split parts, MergeIssue would mint a target
// fingerprint that no future rule-evaluation could ever reproduce (see the
// discrimination proof in the task report). The event would then match no
// issue and the batch-writer upsert would silently create a brand-new issue
// instead of routing to target. Both assertions below -- the event's
// issue_id, and the total issue count staying unchanged -- are structured to
// fail in exactly that scenario.
func TestRuleDrivenMergeRoutesFutureEventToTargetIssue(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('merge-e2e', 'Merge E2E', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	const publicKey = "merge-e2e-key"
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_keys (project_id, public_key, rate_limit_rps, rate_limit_burst)
		VALUES ($1, $2, 200, 1000)
	`, projectID, publicKey); err != nil {
		t.Fatalf("seed ingest key: %v", err)
	}

	ruleRepository := storagepostgres.NewGroupingRuleRepository(pool)
	ruleCache := grouping.NewRuleCache(ruleRepository, time.Now)
	// Wiring ruleCache as the issue repository's CacheInvalidator is the
	// production configuration (see NewIssueRepository's doc comment and
	// command wiring): it is what makes MergeIssue's synchronous
	// Invalidate(projectID) call, after its transaction commits, actually
	// reach the same cache instance the ingest handler below reads through.
	issueRepository := storagepostgres.NewIssueRepository(pool, ruleCache)
	issueService := issue.NewService(issueRepository)

	sink := storagepostgres.NewEventSink(pool)
	persistence := pipeline.New(sink, 1, time.Now, func(error) {})
	persistence.Start()
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer stopCancel()
		_ = persistence.Stop(stopCtx)
	}()

	handler := ingest.NewHandlerWithOptions(ingest.HandlerOptions{
		Store:         storagepostgres.NewIngestKeyStore(pool),
		Now:           time.Now,
		ReportError:   func(error) {},
		Dispatch:      persistence.Dispatch,
		DispatchSpans: persistence.DispatchSpans,
		Rules:         ruleCache.Get,
	})

	flushCtx, flushCancel := context.WithTimeout(ctx, 5*time.Second)
	defer flushCancel()

	// Seed the source and target issues through the real ingest pipeline
	// (before any grouping rule exists), so their fingerprints are exactly
	// what deterministic ingest produced -- the test never recomputes them.
	// Ingesting these two also populates ruleCache's entry for this project
	// with the pre-merge (empty) rule set, which is the staleness hazard
	// Invalidate must clear below.
	postEventOrFail(t, handler, projectID, publicKey, "10000000-0000-4000-8000-000000000001", "source-signal", "source issue text")
	postEventOrFail(t, handler, projectID, publicKey, "10000000-0000-4000-8000-000000000002", "target-signal", "target issue text")
	if err := persistence.Flush(flushCtx); err != nil {
		t.Fatalf("flush seed events: %v", err)
	}

	sourceID := issueIDByTitle(t, ctx, pool, projectID, "source issue text")
	targetID := issueIDByTitle(t, ctx, pool, projectID, "target issue text")

	var issueCountBeforeMerge int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1`, projectID).Scan(&issueCountBeforeMerge); err != nil {
		t.Fatalf("count issues before merge: %v", err)
	}
	if issueCountBeforeMerge != 2 {
		t.Fatalf("issue count before merge = %d, want 2", issueCountBeforeMerge)
	}

	if _, err := issueService.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("merge issue: %v", err)
	}

	// No sleep, no manual cache poke: MergeIssue's own transaction already
	// invalidated ruleCache's entry for projectID (the merge-only
	// invalidation exception -- see grouping.RuleCache.Invalidate's doc comment). The
	// ingest below exercises exactly that production invalidation path.
	postEventOrFail(t, handler, projectID, publicKey, "10000000-0000-4000-8000-000000000003", "source-signal", "source issue text")
	if err := persistence.Flush(flushCtx); err != nil {
		t.Fatalf("flush post-merge event: %v", err)
	}

	var postMergeEventIssueID int64
	if err := pool.QueryRow(ctx, `
		SELECT issue_id FROM events WHERE project_id = $1 AND event_id = $2
	`, projectID, uuid.MustParse("10000000-0000-4000-8000-000000000003")).Scan(&postMergeEventIssueID); err != nil {
		t.Fatalf("post-merge event issue_id: %v", err)
	}
	if postMergeEventIssueID != targetID {
		t.Fatalf("post-merge event issue_id = %d, want target issue id %d (event did not route to the merge target)", postMergeEventIssueID, targetID)
	}

	var issueCountAfterMerge int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1`, projectID).Scan(&issueCountAfterMerge); err != nil {
		t.Fatalf("count issues after merge: %v", err)
	}
	if issueCountAfterMerge != issueCountBeforeMerge {
		t.Fatalf("issue count after merge = %d, want unchanged %d (a new issue was created instead of routing to target)", issueCountAfterMerge, issueCountBeforeMerge)
	}

	var sourceMergedInto int64
	if err := pool.QueryRow(ctx, `SELECT merged_into_id FROM issues WHERE id = $1`, sourceID).Scan(&sourceMergedInto); err != nil {
		t.Fatalf("source merged_into_id: %v", err)
	}
	if sourceMergedInto != targetID {
		t.Fatalf("source merged_into_id = %d, want %d", sourceMergedInto, targetID)
	}
}

// issueIDByTitle finds the single issue row bearing title within projectID.
// Fingerprint() sets title to the event message's first line when the event
// carries no exception, so this locates a seeded issue by the exact text the
// test posted, without recomputing any fingerprint itself.
func issueIDByTitle(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, title string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM issues WHERE project_id = $1 AND title = $2
	`, projectID, title).Scan(&id); err != nil {
		t.Fatalf("find issue by title %q: %v", title, err)
	}
	return id
}

// TestRuleDrivenMergeRuleDerivedSourceSignalRoutesToTargetAfterMerge is a C-1
// regression test: unlike TestRuleDrivenMergeRoutesFutureEventToTargetIssue
// above (which seeds both merge participants before any grouping rule
// exists, so their fingerprints are plain deterministic hashes), this seeds
// the MERGE SOURCE's fingerprint through a pass-1 text rule. Before the C-1
// fix, EvaluateRules hard-returned on the pass-1 match and never ran pass 2,
// so a future event matching the same text rule kept resolving to the
// source's original (now-tombstoned) fingerprint; upsertIssues' old
// INSERT ... ON CONFLICT (project_id, fingerprint) had no merged_into_id
// predicate, so it silently bumped the tombstone's own counters instead of
// the target's.
//
// This scenario is satisfiable by either C-1 half alone: the evaluate.go
// pass-2 chain (the alias rule resolves the source's stale fingerprint to
// the target's live one), or the upsertIssues tombstone backstop (the
// tombstone row still carries the source's original fingerprint, so it has
// something to redirect from). See the target-derived mirror test below for
// the case that actually discriminates between the two.
func TestRuleDrivenMergeRuleDerivedSourceSignalRoutesToTargetAfterMerge(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('rule-source-merge-e2e', 'Rule Source Merge E2E', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	const publicKey = "rule-source-merge-e2e-key"
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_keys (project_id, public_key, rate_limit_rps, rate_limit_burst)
		VALUES ($1, $2, 200, 1000)
	`, projectID, publicKey); err != nil {
		t.Fatalf("seed ingest key: %v", err)
	}

	ruleRepository := storagepostgres.NewGroupingRuleRepository(pool)
	// A pass-1 text rule: every "billing" logger event, regardless of
	// message, resolves to this one literal fingerprint.
	if _, err := ruleRepository.Create(ctx, projectID, "logger:billing", "billing-alias"); err != nil {
		t.Fatalf("create grouping rule: %v", err)
	}

	ruleCache := grouping.NewRuleCache(ruleRepository, time.Now)
	issueRepository := storagepostgres.NewIssueRepository(pool, ruleCache)
	issueService := issue.NewService(issueRepository)

	sink := storagepostgres.NewEventSink(pool)
	persistence := pipeline.New(sink, 1, time.Now, func(error) {})
	persistence.Start()
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer stopCancel()
		_ = persistence.Stop(stopCtx)
	}()

	handler := ingest.NewHandlerWithOptions(ingest.HandlerOptions{
		Store:         storagepostgres.NewIngestKeyStore(pool),
		Now:           time.Now,
		ReportError:   func(error) {},
		Dispatch:      persistence.Dispatch,
		DispatchSpans: persistence.DispatchSpans,
		Rules:         ruleCache.Get,
	})

	flushCtx, flushCancel := context.WithTimeout(ctx, 5*time.Second)
	defer flushCancel()

	// sourceID's fingerprint comes from the rule above (pass 1), not from the
	// deterministic logger/message hash.
	postEventOrFail(t, handler, projectID, publicKey, "20000000-0000-4000-8000-000000000001", "billing", "irrelevant billing text")
	postEventOrFail(t, handler, projectID, publicKey, "20000000-0000-4000-8000-000000000002", "checkout", "target issue text")
	if err := persistence.Flush(flushCtx); err != nil {
		t.Fatalf("flush seed events: %v", err)
	}

	aliasTemplate, err := grouping.ParseTemplate("billing-alias")
	if err != nil {
		t.Fatalf("parse alias template: %v", err)
	}
	sourceFingerprint, ok := aliasTemplate.Resolve(grouping.EvaluationInput{})
	if !ok {
		t.Fatal("expected literal alias template to resolve")
	}
	var sourceID int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM issues WHERE project_id = $1 AND fingerprint = $2
	`, projectID, sourceFingerprint).Scan(&sourceID); err != nil {
		t.Fatalf("find rule-derived source issue: %v", err)
	}
	targetID := issueIDByTitle(t, ctx, pool, projectID, "target issue text")

	var sourceTimesSeenBeforeMerge int64
	if err := pool.QueryRow(ctx, `SELECT times_seen FROM issues WHERE id = $1`, sourceID).Scan(&sourceTimesSeenBeforeMerge); err != nil {
		t.Fatalf("source times_seen before merge: %v", err)
	}

	if _, err := issueService.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("merge issue: %v", err)
	}

	// A future event matching the same text rule must land on target, not the
	// now-tombstoned source -- this is C-1's core scenario.
	postEventOrFail(t, handler, projectID, publicKey, "20000000-0000-4000-8000-000000000003", "billing", "irrelevant billing text again")
	if err := persistence.Flush(flushCtx); err != nil {
		t.Fatalf("flush post-merge event: %v", err)
	}

	var postMergeEventIssueID int64
	if err := pool.QueryRow(ctx, `
		SELECT issue_id FROM events WHERE project_id = $1 AND event_id = $2
	`, projectID, uuid.MustParse("20000000-0000-4000-8000-000000000003")).Scan(&postMergeEventIssueID); err != nil {
		t.Fatalf("post-merge event issue_id: %v", err)
	}
	if postMergeEventIssueID != targetID {
		t.Fatalf("post-merge event issue_id = %d, want target issue id %d (rule-derived source signal did not route to target)", postMergeEventIssueID, targetID)
	}

	var sourceTimesSeenAfterMerge int64
	if err := pool.QueryRow(ctx, `SELECT times_seen FROM issues WHERE id = $1`, sourceID).Scan(&sourceTimesSeenAfterMerge); err != nil {
		t.Fatalf("source times_seen after merge: %v", err)
	}
	if sourceTimesSeenAfterMerge != sourceTimesSeenBeforeMerge {
		t.Fatalf("tombstoned source times_seen = %d, want unchanged %d (the post-merge event must not have bumped the tombstone's counters)", sourceTimesSeenAfterMerge, sourceTimesSeenBeforeMerge)
	}
}

// TestRuleDrivenMergeRuleDerivedTargetSignalRoutesToTargetNotADuplicate is
// C-1's discrimination proof at the integration level. Unlike the
// source-derived case above (satisfiable by either C-1 half, since the
// tombstoned row keeps the source's original fingerprint as something to
// redirect from), a rule-derived MERGE TARGET has its own issues row
// fingerprint overwritten by the merge transaction to
// grouping.MergeFingerprint(targetID). Nothing in the issues table retains
// the pre-merge value as a tombstone the upsertIssues backstop could
// redirect from -- the only way a future event matching the same text rule
// can still reach target is if EvaluateRules chains pass 2 onto pass 1's
// output and finds the target-side alias rule merge also inserts
// (fingerprint:<old target fingerprint> -> merged,issue-<targetID>).
//
// Reverting the evaluate.go chain alone (upsertIssues' tombstone backstop
// still in place) makes this test fail: the event's fingerprint stays at the
// stale pre-merge value, which now matches no row at all (it is not a
// tombstone's fingerprint -- it was target's OWN prior fingerprint, since
// overwritten), so the batch writer's ON CONFLICT DO UPDATE finds nothing to
// conflict with and INSERTs a brand new, duplicate issue.
func TestRuleDrivenMergeRuleDerivedTargetSignalRoutesToTargetNotADuplicate(t *testing.T) {
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
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('rule-target-merge-e2e', 'Rule Target Merge E2E', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	const publicKey = "rule-target-merge-e2e-key"
	if _, err := pool.Exec(ctx, `
		INSERT INTO project_keys (project_id, public_key, rate_limit_rps, rate_limit_burst)
		VALUES ($1, $2, 200, 1000)
	`, projectID, publicKey); err != nil {
		t.Fatalf("seed ingest key: %v", err)
	}

	ruleRepository := storagepostgres.NewGroupingRuleRepository(pool)
	if _, err := ruleRepository.Create(ctx, projectID, "logger:checkout", "checkout-alias"); err != nil {
		t.Fatalf("create grouping rule: %v", err)
	}

	ruleCache := grouping.NewRuleCache(ruleRepository, time.Now)
	issueRepository := storagepostgres.NewIssueRepository(pool, ruleCache)
	issueService := issue.NewService(issueRepository)

	sink := storagepostgres.NewEventSink(pool)
	persistence := pipeline.New(sink, 1, time.Now, func(error) {})
	persistence.Start()
	defer func() {
		stopCtx, stopCancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer stopCancel()
		_ = persistence.Stop(stopCtx)
	}()

	handler := ingest.NewHandlerWithOptions(ingest.HandlerOptions{
		Store:         storagepostgres.NewIngestKeyStore(pool),
		Now:           time.Now,
		ReportError:   func(error) {},
		Dispatch:      persistence.Dispatch,
		DispatchSpans: persistence.DispatchSpans,
		Rules:         ruleCache.Get,
	})

	flushCtx, flushCancel := context.WithTimeout(ctx, 5*time.Second)
	defer flushCancel()

	// The eventual MERGE TARGET's fingerprint comes from the rule above; the
	// source is an ordinary deterministic issue.
	postEventOrFail(t, handler, projectID, publicKey, "30000000-0000-4000-8000-000000000001", "checkout", "irrelevant checkout text")
	postEventOrFail(t, handler, projectID, publicKey, "30000000-0000-4000-8000-000000000002", "other-signal", "source issue text")
	if err := persistence.Flush(flushCtx); err != nil {
		t.Fatalf("flush seed events: %v", err)
	}

	aliasTemplate, err := grouping.ParseTemplate("checkout-alias")
	if err != nil {
		t.Fatalf("parse alias template: %v", err)
	}
	targetFingerprint, ok := aliasTemplate.Resolve(grouping.EvaluationInput{})
	if !ok {
		t.Fatal("expected literal alias template to resolve")
	}
	var targetID int64
	if err := pool.QueryRow(ctx, `
		SELECT id FROM issues WHERE project_id = $1 AND fingerprint = $2
	`, projectID, targetFingerprint).Scan(&targetID); err != nil {
		t.Fatalf("find rule-derived target issue: %v", err)
	}
	sourceID := issueIDByTitle(t, ctx, pool, projectID, "source issue text")

	var issueCountBeforeMerge int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1`, projectID).Scan(&issueCountBeforeMerge); err != nil {
		t.Fatalf("count issues before merge: %v", err)
	}
	if issueCountBeforeMerge != 2 {
		t.Fatalf("issue count before merge = %d, want 2", issueCountBeforeMerge)
	}

	// Merge source into the rule-derived target: target's issues row gets its
	// fingerprint rewritten away from targetFingerprint to
	// grouping.MergeFingerprint(targetID), and the target-side alias rule
	// (fingerprint:targetFingerprint -> merged,issue-<targetID>) becomes the
	// only path back to it for a future rule match.
	if _, err := issueService.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("merge issue: %v", err)
	}

	postEventOrFail(t, handler, projectID, publicKey, "30000000-0000-4000-8000-000000000003", "checkout", "another checkout event")
	if err := persistence.Flush(flushCtx); err != nil {
		t.Fatalf("flush post-merge event: %v", err)
	}

	var postMergeEventIssueID int64
	if err := pool.QueryRow(ctx, `
		SELECT issue_id FROM events WHERE project_id = $1 AND event_id = $2
	`, projectID, uuid.MustParse("30000000-0000-4000-8000-000000000003")).Scan(&postMergeEventIssueID); err != nil {
		t.Fatalf("post-merge event issue_id: %v", err)
	}
	if postMergeEventIssueID != targetID {
		t.Fatalf("post-merge event issue_id = %d, want target issue id %d (rule-derived target signal did not route back to target)", postMergeEventIssueID, targetID)
	}

	var issueCountAfterFlow int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issues WHERE project_id = $1`, projectID).Scan(&issueCountAfterFlow); err != nil {
		t.Fatalf("count issues after flow: %v", err)
	}
	if issueCountAfterFlow != issueCountBeforeMerge {
		t.Fatalf("issue count = %d, want unchanged %d (a duplicate issue was created instead of routing to the rule-derived target)", issueCountAfterFlow, issueCountBeforeMerge)
	}
}
