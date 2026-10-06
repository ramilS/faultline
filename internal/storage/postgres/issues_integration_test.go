//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/issue"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// issueTestEnv migrates a fresh TimescaleDB, seeds one project, and returns a
// pool plus the sink used to create issues/events through the real batch writer.
func issueTestEnv(t *testing.T) (context.Context, *pgxpool.Pool, int64) {
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

	var projectID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('web','Web','javascript') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	return ctx, pool, projectID
}

// testProjectSlug is the slug issueTestEnv seeds its one project under.
const testProjectSlug = "web"

// seedIssueOccurredAndReceived persists one event via the production batch
// writer so issues, events, releases, and tag facets are created exactly as
// ingestion would, with the event's occurrence instant (events.timestamp) and
// its arrival instant (events.received_at) set independently.
//
// The two columns mean different things and different predicates read
// different ones — the events table shows "Occurred" from timestamp while the
// pager's window narrows on received_at — so any test asserting WHICH column
// a predicate reads has to drive them apart. seedIssueAt is this with both
// instants the same, which is what every other caller wants.
//
// tags is only special-cased for the "release" key, which also registers the
// project release and populates the event's Release pointer — every other key
// (including "environment") is carried straight through as a tag facet.
func seedIssueOccurredAndReceived(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	projectID int64,
	fingerprint, title string,
	tags map[string]string,
	occurred, received time.Time,
) {
	t.Helper()
	sink := storagepostgres.NewEventSink(pool)
	tagDeltas := make([]storage.TagDelta, 0, len(tags))
	for key, value := range tags {
		tagDeltas = append(tagDeltas, storage.TagDelta{Key: key, Value: value, Count: 1, FirstSeen: occurred, LastSeen: occurred})
	}
	var releases []string
	var release *string
	if value, ok := tags["release"]; ok {
		releases = []string{value}
		release = &value
	}
	batch := storage.Batch{
		ProjectID: projectID,
		Releases:  releases,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: title, Culprit: "", Level: "error",
			Count: 1, FirstSeen: occurred, LastSeen: occurred,
			Tags: tagDeltas,
		}},
		Events: []storage.EventRow{{
			ProjectID: projectID, EventID: uuid.New(), Timestamp: occurred, ReceivedAt: received,
			Level: "error", Environment: tags["environment"], Message: title, Release: release,
			Fingerprint: fingerprint, Payload: json.RawMessage(`{"message":"` + title + `"}`),
		}},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("seed issue %s: %v", fingerprint, err)
	}
}

// seedIssueAt persists one event that occurred and arrived at the same
// instant — the shape every caller wants unless it is asserting which of the
// two columns a predicate reads.
func seedIssueAt(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, fingerprint, title string, tags map[string]string, seen time.Time) {
	t.Helper()
	seedIssueOccurredAndReceived(t, ctx, pool, projectID, fingerprint, title, tags, seen, seen)
}

// seedIssue is seedIssueAt for callers that don't care about a specific
// fingerprint or seen time: the fingerprint is derived from title and seen is
// now.
func seedIssue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, title string, tags map[string]string) {
	t.Helper()
	seedIssueAt(t, ctx, pool, projectID, hex64(title), title, tags, time.Now().UTC())
}

// seedIssueAtTimes persists one issue with one event per supplied timestamp
// through the production batch writer, so first_seen/last_seen and the
// issue_counts_hourly buckets are populated exactly as ingestion would.
func seedIssueAtTimes(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, title string, times []time.Time) {
	t.Helper()
	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64(title)
	events := make([]storage.EventRow, 0, len(times))
	for _, seen := range times {
		events = append(events, storage.EventRow{
			ProjectID: projectID, EventID: uuid.New(), Timestamp: seen, ReceivedAt: seen,
			Level: "error", Message: title, Fingerprint: fingerprint,
			Payload: json.RawMessage(`{"message":"` + title + `"}`),
		})
	}
	first, last := times[0], times[0]
	for _, seen := range times[1:] {
		if seen.Before(first) {
			first = seen
		}
		if seen.After(last) {
			last = seen
		}
	}
	batch := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: title, Culprit: "", Level: "error",
			Count: int64(len(times)), FirstSeen: first, LastSeen: last,
		}},
		Events: events,
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("seed issue at times %s: %v", fingerprint, err)
	}
}

// refreshAggregate materializes issue_counts_hourly for freshly-written
// events; without this, a just-seeded event sits outside the continuous
// aggregate's own refresh window and the window-CTE's aggregate join sees it
// as absent.
func refreshAggregate(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `CALL refresh_continuous_aggregate('issue_counts_hourly', NULL, NULL)`); err != nil {
		t.Fatalf("refresh issue_counts_hourly: %v", err)
	}
}

// An issue with events on Monday and Friday must NOT match a Wednesday window.
// The row-level predicate alone (first_seen <= end AND last_seen >= start)
// matches it, which is exactly why issue_counts_hourly is the authority.
func TestListAbsoluteWindowExcludesGapIssues(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	monday := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	wednesday := monday.Add(48 * time.Hour)
	friday := monday.Add(96 * time.Hour)

	seedIssueAtTimes(t, ctx, pool, projectID, "gapped", []time.Time{monday, friday})
	seedIssueAtTimes(t, ctx, pool, projectID, "present", []time.Time{wednesday})
	refreshAggregate(t, ctx, pool)

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Window:      issue.Window{Start: wednesday.Add(-time.Hour), End: wednesday.Add(time.Hour)},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "present" {
		titles := make([]string, 0, len(page.Issues))
		for _, listed := range page.Issues {
			titles = append(titles, listed.Title)
		}
		t.Fatalf("List returned %v, want only [present]", titles)
	}
}

func TestListTrailingWindowFiltersByLastSeen(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	seedIssueAtTimes(t, ctx, pool, projectID, "recent", []time.Time{time.Now().UTC().Add(-time.Hour)})
	seedIssueAtTimes(t, ctx, pool, projectID, "stale", []time.Time{time.Now().UTC().Add(-72 * time.Hour)})

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Window:      issue.Window{Trailing: 24 * time.Hour},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "recent" {
		t.Fatalf("List returned %d issues, want only recent", len(page.Issues))
	}
}

func TestListWindowedCountsReplaceFixedSparklines(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	now := time.Now().UTC()
	seedIssueAtTimes(t, ctx, pool, projectID, "alpha",
		[]time.Time{now.Add(-2 * time.Hour), now.Add(-time.Hour), now.Add(-30 * time.Minute)})
	refreshAggregate(t, ctx, pool)

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Window:      issue.Window{Trailing: 24 * time.Hour},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listed := page.Issues[0]
	if listed.PeriodCount != 3 {
		t.Fatalf("PeriodCount = %d, want 3", listed.PeriodCount)
	}
	if len(listed.Counts) != 24 {
		t.Fatalf("len(Counts) = %d, want 24 hourly buckets", len(listed.Counts))
	}
	// "Replace" means the legacy pair must be gone, not just that the new
	// fields are populated alongside it.
	if listed.Counts24h != [24]int64{} || listed.Counts14d != [14]int64{} {
		t.Fatalf("Counts24h/Counts14d = %v/%v, want both empty for a windowed request", listed.Counts24h, listed.Counts14d)
	}
}

func TestListWithoutWindowKeepsLegacySparklines(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	seedIssueAtTimes(t, ctx, pool, projectID, "alpha", []time.Time{time.Now().UTC().Add(-time.Hour)})
	refreshAggregate(t, ctx, pool)

	page, err := repository.List(ctx, issue.ListFilters{Scope: access.Unrestricted(), ProjectSlug: testProjectSlug, Limit: 50})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	listed := page.Issues[0]
	if len(listed.Counts) != 0 {
		t.Fatalf("Counts = %v, want empty for a windowless request", listed.Counts)
	}
	if listed.Counts24h == [24]int64{} || listed.Counts14d == [14]int64{} {
		t.Fatal("legacy sparklines are empty, want them populated for a windowless request")
	}
}

// TestListTrailingWindowCoversTheWindowsOpeningEdge is the regression test
// for the trailing-window blind spot: the old end-anchored, step-backward
// bucket generation truncated "now" down to a bucket boundary before
// stepping back by whole buckets, which shifted the entire series forward
// and left up to one bucket width uncovered at the window's opening edge. An
// issue whose only event fell in that gap was still listed (list membership
// is last_seen >= now()-Trailing, unaffected by sparkline bucketing) but
// reported PeriodCount 0 and an all-zero sparkline despite being the very
// issue the window matched.
func TestListTrailingWindowCoversTheWindowsOpeningEdge(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	trailing := 30 * 24 * time.Hour
	// One hour inside the window's opening edge — for a 30d trailing window
	// (24h buckets), the old anchoring's blind spot reached up to 24h deep.
	seeded := time.Now().UTC().Add(-trailing).Add(time.Hour)
	seedIssueAtTimes(t, ctx, pool, projectID, "edge", []time.Time{seeded})
	refreshAggregate(t, ctx, pool)

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Window:      issue.Window{Trailing: trailing},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 {
		t.Fatalf("List returned %d issues, want 1", len(page.Issues))
	}
	if got := page.Issues[0].PeriodCount; got != 1 {
		t.Fatalf("PeriodCount = %d, want 1 (count(*) over the window is 1)", got)
	}
}

// TestListAbsoluteWindowExcludesEventsBeforeStart is the regression test for
// the non-exact-multiple absolute span bug: rounding the bucket count up
// used to place the resulting slop *before* Start, so an event preceding the
// window leaked into PeriodCount and the first sparkline bucket even though
// the membership CTE (bucket >= Start) already excludes that same event
// correctly — list and count disagreeing over what the window means.
func TestListAbsoluteWindowExcludesEventsBeforeStart(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(100 * time.Hour) // not a multiple of the 6h bucket bucketWidth(100h) picks
	before := start.Add(-time.Hour)
	inside := start.Add(time.Hour)
	seedIssueAtTimes(t, ctx, pool, projectID, "boundary", []time.Time{before, inside})
	refreshAggregate(t, ctx, pool)

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Window:      issue.Window{Start: start, End: end},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 {
		t.Fatalf("List returned %d issues, want 1", len(page.Issues))
	}
	listed := page.Issues[0]
	if listed.PeriodCount != 1 {
		t.Fatalf("PeriodCount = %d, want 1 (only the event inside [start, end))", listed.PeriodCount)
	}
	if listed.Counts[0] != 1 {
		t.Fatalf("Counts[0] = %d, want 1 (the pre-Start event must not land in the first bucket)", listed.Counts[0])
	}
}

// TestListAbsoluteWindowKeepsClockSkewedIssues pins why the absolute path
// carries no row-level prefilter beside its aggregate CTE. issues.first_seen /
// last_seen come from the client-supplied event timestamp; issue_counts_hourly
// buckets the server-stamped received_at. A client whose clock runs slow can
// therefore produce an event the aggregate places inside the window while
// last_seen sits before it. The CTE is the authority on membership, so the
// issue must be listed; a `last_seen >= start` prefilter would drop it.
func TestListAbsoluteWindowKeepsClockSkewedIssues(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	receivedAt := start.Add(30 * time.Minute) // server clock: inside the window
	clientTime := start.Add(-3 * time.Hour)   // client clock: three hours slow

	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64("skewed")
	batch := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "skewed", Culprit: "", Level: "error",
			Count: 1, FirstSeen: clientTime, LastSeen: clientTime,
		}},
		Events: []storage.EventRow{{
			ProjectID: projectID, EventID: uuid.New(), Timestamp: clientTime, ReceivedAt: receivedAt,
			Level: "error", Message: "skewed", Fingerprint: fingerprint,
			Payload: json.RawMessage(`{"message":"skewed"}`),
		}},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("seed skewed issue: %v", err)
	}
	refreshAggregate(t, ctx, pool)

	// Guard the premise: the skew must actually straddle the window boundary,
	// or the test would pass for the wrong reason.
	var lastSeen time.Time
	if err := pool.QueryRow(ctx,
		`SELECT last_seen FROM issues WHERE fingerprint = $1`, fingerprint).Scan(&lastSeen); err != nil {
		t.Fatalf("read last_seen: %v", err)
	}
	if !lastSeen.Before(start) {
		t.Fatalf("test setup: last_seen %v must be strictly before the window start %v", lastSeen, start)
	}

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Window:      issue.Window{Start: start, End: end},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "skewed" {
		t.Fatalf("List returned %+v, want the clock-skewed issue (the aggregate places it in-window)", page.Issues)
	}
	if got := page.Issues[0].PeriodCount; got != 1 {
		t.Fatalf("PeriodCount = %d, want 1", got)
	}
}

func TestIssueRepositoryListFiltersAndPaging(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("a1"), "Older", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-2*time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("b2"), "Newer", map[string]string{"environment": "staging", "release": "2.0.0"}, now.Add(-1*time.Hour))

	page, err := repository.List(ctx, issue.ListFilters{Scope: access.Unrestricted(), ProjectSlug: "web", Sort: issue.SortLastSeen, Limit: 1})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "Newer" {
		t.Fatalf("page 1 = %+v, want [Newer]", page.Issues)
	}
	if page.NextCursor == nil {
		t.Fatalf("page 1 next cursor = nil, want a cursor")
	}

	page2, err := repository.List(ctx, issue.ListFilters{Scope: access.Unrestricted(), ProjectSlug: "web", Sort: issue.SortLastSeen, Limit: 1, Cursor: page.NextCursor})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(page2.Issues) != 1 || page2.Issues[0].Title != "Older" {
		t.Fatalf("page 2 = %+v, want [Older]", page2.Issues)
	}
	if page2.NextCursor != nil {
		t.Fatalf("page 2 next cursor = %+v, want nil", page2.NextCursor)
	}

	filtered, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: "web",
		Predicates:  []issue.Predicate{{Field: issue.FieldEnvironment, Values: []string{"staging"}}},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("list env filter: %v", err)
	}
	if len(filtered.Issues) != 1 || filtered.Issues[0].Title != "Newer" {
		t.Fatalf("env filter = %+v, want [Newer]", filtered.Issues)
	}

	// The sparkline arrays are always fixed length and the recent event lands in
	// both the last 24h and last 14d windows for the "Newer" issue.
	var total24h int64
	for _, n := range filtered.Issues[0].Counts24h {
		total24h += n
	}
	if total24h != 1 {
		t.Fatalf("counts_24h total = %d, want 1", total24h)
	}
	var total int64
	for _, n := range filtered.Issues[0].Counts14d {
		total += n
	}
	if total != 1 {
		t.Fatalf("counts_14d total = %d, want 1", total)
	}
}

func hex64(seed string) string {
	out := make([]byte, 64)
	for i := range out {
		out[i] = '0'
	}
	copy(out, seed)
	return string(out)
}

// seedIssueWithUsers persists one issue with userCount distinct user
// identities via the production batch writer, so issues.users_seen is
// populated exactly as ingestion would (see upsertUserIdentities) rather than
// being poked in directly. Returns the issue's id.
func seedIssueWithUsers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, title string, userCount int) int64 {
	t.Helper()
	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64(title)
	seen := time.Now().UTC()

	users := make([]storage.UserDelta, 0, userCount)
	for i := range userCount {
		users = append(users, storage.UserDelta{
			Key:       "id",
			Value:     fmt.Sprintf("%s-user-%d", title, i),
			Count:     1,
			FirstSeen: seen,
			LastSeen:  seen,
		})
	}
	batch := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: title, Culprit: "", Level: "error",
			Count: 1, FirstSeen: seen, LastSeen: seen,
			Users: users,
		}},
		Events: []storage.EventRow{{
			ProjectID: projectID, EventID: uuid.New(), Timestamp: seen, ReceivedAt: seen,
			Level: "error", Message: title, Fingerprint: fingerprint,
			Payload: json.RawMessage(`{"message":"` + title + `"}`),
		}},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("seed issue with users %s: %v", fingerprint, err)
	}

	var issueID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM issues WHERE project_id = $1 AND fingerprint = $2`,
		projectID, fingerprint).Scan(&issueID); err != nil {
		t.Fatalf("lookup issue id for %s: %v", fingerprint, err)
	}
	return issueID
}

// TestListUsersSeenSortBreaksTiesOnID pins the tie-break requirement for a
// non-unique sort key: users_seen is not unique across issues — a saturated
// count pins every busy issue at the same floor — so paging by it must break
// ties on id, or a page boundary between two equal values skips or repeats an
// issue. Three issues share users_seen 5, 5, 2 (the duplicate is the whole
// point of the fixture); every issue must appear exactly once across a
// limit-2 keyset walk.
func TestListUsersSeenSortBreaksTiesOnID(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	idAlpha := seedIssueWithUsers(t, ctx, pool, projectID, "Alpha", 5)
	idBravo := seedIssueWithUsers(t, ctx, pool, projectID, "Bravo", 5)
	idCharlie := seedIssueWithUsers(t, ctx, pool, projectID, "Charlie", 2)

	seen := make(map[int64]int)
	var cursor *issue.Cursor
	for page := range 5 {
		result, err := repository.List(ctx, issue.ListFilters{
			Scope:       access.Unrestricted(),
			ProjectSlug: testProjectSlug, Sort: issue.SortUsersSeen, Limit: 2, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("list page %d: %v", page, err)
		}
		for _, listed := range result.Issues {
			seen[listed.ID]++
		}
		if result.NextCursor == nil {
			break
		}
		cursor = result.NextCursor
	}

	for _, id := range []int64{idAlpha, idBravo, idCharlie} {
		if seen[id] != 1 {
			t.Fatalf("issue %d appeared %d times across pages, want exactly once (seen = %+v)", id, seen[id], seen)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("saw %d distinct issues, want exactly 3 (seen = %+v)", len(seen), seen)
	}
}

func TestIssueRepositoryDetailAndTriage(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()
	seedIssueAt(t, ctx, pool, projectID, hex64("c3"), "Boom", map[string]string{"environment": "production", "release": "3.0.0"}, now)

	page, err := repository.List(ctx, issue.ListFilters{Scope: access.Unrestricted(), ProjectSlug: "web", Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	id := page.Issues[0].ID

	detail, err := repository.Detail(ctx, access.Unrestricted(), id)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	var spark14dTotal int64
	for _, n := range detail.Issue.Counts14d {
		spark14dTotal += n
	}
	if spark14dTotal < 1 {
		t.Fatalf("detail counts_14d total = %d, want >= 1 (sparklines must be attached)", spark14dTotal)
	}
	if detail.Issue.Title != "Boom" {
		t.Fatalf("detail title = %q, want Boom", detail.Issue.Title)
	}
	if len(detail.Environments) != 1 || detail.Environments[0] != "production" {
		t.Fatalf("environments = %+v, want [production]", detail.Environments)
	}
	if len(detail.Releases) != 1 || detail.Releases[0] != "3.0.0" {
		t.Fatalf("releases = %+v, want [3.0.0]", detail.Releases)
	}

	if err := repository.UpdateStatus(ctx, access.Unrestricted(), id, issue.StatusResolved); err != nil {
		t.Fatalf("update status: %v", err)
	}
	resolved, err := repository.Detail(ctx, access.Unrestricted(), id)
	if err != nil {
		t.Fatalf("detail after resolve: %v", err)
	}
	if resolved.Issue.Status != issue.StatusResolved {
		t.Fatalf("status = %q, want resolved", resolved.Issue.Status)
	}

	var userID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash) VALUES ('a@x.io','Ada','x') RETURNING id
	`).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	if err := repository.Assign(ctx, access.Unrestricted(), id, &userID); err != nil {
		t.Fatalf("assign: %v", err)
	}
	assigned, err := repository.Detail(ctx, access.Unrestricted(), id)
	if err != nil {
		t.Fatalf("detail after assign: %v", err)
	}
	if assigned.Issue.Assignee == nil || assigned.Issue.Assignee.Name != "Ada" {
		t.Fatalf("assignee = %+v, want Ada", assigned.Issue.Assignee)
	}
	if err := repository.Assign(ctx, access.Unrestricted(), id, nil); err != nil {
		t.Fatalf("unassign: %v", err)
	}
	unassigned, err := repository.Detail(ctx, access.Unrestricted(), id)
	if err != nil {
		t.Fatalf("detail after unassign: %v", err)
	}
	if unassigned.Issue.Assignee != nil {
		t.Fatalf("assignee after unassign = %+v, want nil", unassigned.Issue.Assignee)
	}

	missing := int64(999999)
	if err := repository.Assign(ctx, access.Unrestricted(), id, &missing); !errors.Is(err, issue.ErrInvalidAssignee) {
		t.Fatalf("assign unknown user err = %v, want ErrInvalidAssignee", err)
	}
	if _, err := repository.Detail(ctx, access.Unrestricted(), 424242); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("detail missing err = %v, want ErrNotFound", err)
	}
	if err := repository.UpdateStatus(ctx, access.Unrestricted(), 424243, issue.StatusResolved); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("update status of missing issue err = %v, want ErrNotFound", err)
	}
	if err := repository.Assign(ctx, access.Unrestricted(), 424244, nil); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("assign missing issue err = %v, want ErrNotFound", err)
	}
}

// TestIssueRepositoryDetailExcludesReservedKeysFromGenericFacets covers
// Finding 1: environment/release rows in issue_tag_values must populate only
// Detail.Environments/Detail.Releases, never also the generic Detail.Tags
// list — otherwise the issue-detail response (and the UI panel that renders
// both) shows each value twice.
func TestIssueRepositoryDetailExcludesReservedKeysFromGenericFacets(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()
	seedIssueAt(t, ctx, pool, projectID, hex64("dup"), "Duplicated", map[string]string{
		"environment": "production",
		"release":     "5.0.0",
		"plan":        "pro",
	}, now)

	page, err := repository.List(ctx, issue.ListFilters{Scope: access.Unrestricted(), ProjectSlug: "web", Limit: 50})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	id := page.Issues[0].ID

	detail, err := repository.Detail(ctx, access.Unrestricted(), id)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}

	if len(detail.Environments) != 1 || detail.Environments[0] != "production" {
		t.Fatalf("environments = %+v, want [production]", detail.Environments)
	}
	if len(detail.Releases) != 1 || detail.Releases[0] != "5.0.0" {
		t.Fatalf("releases = %+v, want [5.0.0]", detail.Releases)
	}
	for _, facet := range detail.Tags {
		if facet.Key == "environment" || facet.Key == "release" {
			t.Fatalf("tags = %+v, must not repeat the dedicated environment/release keys", detail.Tags)
		}
	}
	if len(detail.Tags) != 1 || detail.Tags[0].Key != "plan" {
		t.Fatalf("tags = %+v, want exactly the non-reserved [plan] facet", detail.Tags)
	}
}

func TestIssueRepositoryEventsAndStats(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()
	seedIssueAt(t, ctx, pool, projectID, hex64("d4"), "Kaboom", map[string]string{"environment": "production", "release": "4.0.0"}, now)

	page, _ := repository.List(ctx, issue.ListFilters{Scope: access.Unrestricted(), ProjectSlug: "web", Limit: 50})
	id := page.Issues[0].ID

	events, err := repository.Events(ctx, access.Unrestricted(), issue.EventQuery{IssueID: id, Limit: 50})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(events.Events) != 1 || events.Events[0].Message != "Kaboom" {
		t.Fatalf("events = %+v, want one Kaboom", events.Events)
	}

	latest, err := repository.LatestEvent(ctx, access.Unrestricted(), id)
	if err != nil {
		t.Fatalf("latest event: %v", err)
	}
	if len(latest.Payload) == 0 {
		t.Fatalf("latest payload is empty")
	}
	if latest.ProjectID != projectID {
		t.Fatalf("latest project id = %d, want %d", latest.ProjectID, projectID)
	}

	byID, err := repository.Event(ctx, access.Unrestricted(), latest.EventID)
	if err != nil {
		t.Fatalf("event by id: %v", err)
	}
	if byID.EventID != latest.EventID {
		t.Fatalf("event by id = %s, want %s", byID.EventID, latest.EventID)
	}

	buckets, err := repository.Stats(ctx, access.Unrestricted(), id, issue.Period24h)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if len(buckets) != 24 {
		t.Fatalf("24h buckets = %d, want 24", len(buckets))
	}
	var total int64
	for _, b := range buckets {
		total += b.Count
	}
	if total != 1 {
		t.Fatalf("24h total = %d, want 1", total)
	}

	if _, err := repository.LatestEvent(ctx, access.Unrestricted(), 987654); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("latest of missing issue err = %v, want ErrNotFound", err)
	}
}

func TestEventsOldestOrderPagesForwardFromTheFirstOccurrence(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	// Seeded out of chronological order so a passing test cannot be an accident
	// of insertion sequence: an unordered scan would return them 3, 0, 4, 1, 2.
	base := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	times := make([]time.Time, 0, 5)
	for _, offset := range []int{3, 0, 4, 1, 2} {
		times = append(times, base.Add(time.Duration(offset)*time.Minute))
	}
	seedIssueAtTimes(t, ctx, pool, projectID, "Ascending", times)
	id := issueIDForFingerprint(t, ctx, pool, hex64("Ascending"))

	page, err := repository.Events(ctx, access.Unrestricted(), issue.EventQuery{IssueID: id, Limit: 2, Order: issue.EventOrderOldest})
	if err != nil {
		t.Fatalf("events: %v", err)
	}

	if len(page.Events) != 2 {
		t.Fatalf("len(Events) = %d, want 2", len(page.Events))
	}
	if !page.Events[0].ReceivedAt.Equal(base) {
		t.Fatalf("first event = %v, want the earliest %v", page.Events[0].ReceivedAt, base)
	}
	if !page.Events[1].ReceivedAt.After(page.Events[0].ReceivedAt) {
		t.Fatal("oldest order must ascend")
	}
	if page.NextCursor == nil {
		t.Fatal("NextCursor = nil, want a cursor with three events remaining")
	}
}

func TestEventsOldestOrderCursorDoesNotRepeatOrSkip(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	base := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	times := make([]time.Time, 0, 5)
	for offset := range 5 {
		times = append(times, base.Add(time.Duration(offset)*time.Minute))
	}
	seedIssueAtTimes(t, ctx, pool, projectID, "Walked", times)
	id := issueIDForFingerprint(t, ctx, pool, hex64("Walked"))

	var walked []time.Time
	var cursor *issue.EventCursor
	// Bounded so a keyset that never advances fails as a wrong count rather
	// than hanging the suite.
	for range 10 {
		page, err := repository.Events(ctx, access.Unrestricted(), issue.EventQuery{
			IssueID: id, Limit: 2, Order: issue.EventOrderOldest, Cursor: cursor,
		})
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		for _, event := range page.Events {
			walked = append(walked, event.ReceivedAt)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}

	// The keyset comparison flips with the order. Reusing the newest-first
	// comparison under an ascending ORDER BY yields either an empty second page
	// or an endless walk, and both look like "paging works" on a single page.
	if len(walked) != 5 {
		t.Fatalf("walked %d events, want 5 with no repeats or skips", len(walked))
	}
	for index := 1; index < len(walked); index++ {
		if !walked[index].After(walked[index-1]) {
			t.Fatalf("walk is not strictly ascending at %d: %v", index, walked)
		}
	}
}

func TestEventsNewestOrderCursorStillDescendsWithoutRepeats(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	base := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	times := make([]time.Time, 0, 5)
	for offset := range 5 {
		times = append(times, base.Add(time.Duration(offset)*time.Minute))
	}
	seedIssueAtTimes(t, ctx, pool, projectID, "Descended", times)
	id := issueIDForFingerprint(t, ctx, pool, hex64("Descended"))

	var walked []time.Time
	var cursor *issue.EventCursor
	for range 10 {
		// Order left unset on purpose: the default walk is the contract every
		// caller written before this parameter existed relies on.
		page, err := repository.Events(ctx, access.Unrestricted(), issue.EventQuery{IssueID: id, Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("events: %v", err)
		}
		for _, event := range page.Events {
			walked = append(walked, event.ReceivedAt)
		}
		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}

	if len(walked) != 5 {
		t.Fatalf("walked %d events, want 5 with no repeats or skips", len(walked))
	}
	for index := 1; index < len(walked); index++ {
		if !walked[index].Before(walked[index-1]) {
			t.Fatalf("walk is not strictly descending at %d: %v", index, walked)
		}
	}
}

func TestEventsFiltersByEnvironment(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()
	fingerprint := hex64("MultiEnv")
	seedIssueAt(t, ctx, pool, projectID, fingerprint, "MultiEnv", map[string]string{"environment": "production"}, now)
	seedIssueAt(t, ctx, pool, projectID, fingerprint, "MultiEnv", map[string]string{"environment": "staging"}, now.Add(time.Minute))

	page, err := repository.Events(ctx, access.Unrestricted(), issue.EventQuery{
		IssueID:     issueIDForFingerprint(t, ctx, pool, fingerprint),
		Limit:       50,
		Environment: "staging",
	})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].Environment != "staging" {
		t.Fatalf("events = %+v, want exactly the staging event", page.Events)
	}
}

// TestEventsFiltersByTrailingWindow is the only guard on WHICH column the
// window predicate narrows on, so the two events below are deliberately
// crossed: each falls inside the 24h window by one column and outside it by
// the other. Filtering on received_at (correct) keeps "LateArrival";
// filtering on timestamp keeps "LateReport" instead, and the assertions here
// fail.
func TestEventsFiltersByTrailingWindow(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()
	fingerprint := hex64("OldAndNew")
	old := now.Add(-48 * time.Hour)
	recent := now.Add(-time.Hour)
	// Occurred two days ago, arrived an hour ago: inside the window.
	seedIssueOccurredAndReceived(t, ctx, pool, projectID, fingerprint, "LateArrival", nil, old, recent)
	// Occurred an hour ago, arrived two days ago: outside the window.
	seedIssueOccurredAndReceived(t, ctx, pool, projectID, fingerprint, "LateReport", nil, recent, old)

	page, err := repository.Events(ctx, access.Unrestricted(), issue.EventQuery{
		IssueID: issueIDForFingerprint(t, ctx, pool, fingerprint),
		Limit:   50,
		Window:  issue.Window{Trailing: 24 * time.Hour},
	})
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	if len(page.Events) != 1 {
		t.Fatalf("events = %+v, want exactly the event RECEIVED in the last 24h", page.Events)
	}
	if page.Events[0].Message != "LateArrival" {
		t.Fatalf("event = %q, want LateArrival — the window must narrow on received_at, not timestamp", page.Events[0].Message)
	}
	if page.Events[0].Timestamp.Sub(old).Abs() > time.Second {
		t.Fatalf("event timestamp = %v, want ~%v (the surviving event occurred outside the window)", page.Events[0].Timestamp, old)
	}
}

func TestIssueRepositoryMergeIssue(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("source"), "Source Issue", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("target"), "Target Issue", map[string]string{"environment": "production", "release": "1.0.0"}, now)

	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("target"))

	merged, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID)
	if err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}
	if merged.Issue.ID != targetID || merged.Issue.TimesSeen != 2 {
		t.Fatalf("merged detail = %+v", merged.Issue)
	}
	if merged.Issue.FirstSeen.Sub(now.Add(-time.Hour)).Abs() > time.Second {
		t.Fatalf("merged first_seen = %v, want ~%v", merged.Issue.FirstSeen, now.Add(-time.Hour))
	}

	var eventCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE issue_id = $1`, targetID).Scan(&eventCount); err != nil || eventCount != 2 {
		t.Fatalf("target events = %d, error = %v, want 2", eventCount, err)
	}

	var sourceMergedInto *int64
	if err := pool.QueryRow(ctx, `SELECT merged_into_id FROM issues WHERE id = $1`, sourceID).Scan(&sourceMergedInto); err != nil || sourceMergedInto == nil || *sourceMergedInto != targetID {
		t.Fatalf("source merged_into_id = %v, error = %v, want %d", sourceMergedInto, err, targetID)
	}

	var newFingerprint string
	if err := pool.QueryRow(ctx, `SELECT fingerprint FROM issues WHERE id = $1`, targetID).Scan(&newFingerprint); err != nil || newFingerprint != grouping.MergeFingerprint(targetID) {
		t.Fatalf("target fingerprint = %q, error = %v, want %q", newFingerprint, err, grouping.MergeFingerprint(targetID))
	}

	var aliasRuleCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM grouping_rules WHERE project_id = $1 AND template = $2 AND matcher IN ($3, $4)
	`, projectID, grouping.MergeTemplate(targetID), "fingerprint:"+hex64("source"), "fingerprint:"+hex64("target")).Scan(&aliasRuleCount); err != nil || aliasRuleCount != 2 {
		t.Fatalf("alias rule count = %d, error = %v, want 2", aliasRuleCount, err)
	}

	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); !errors.Is(err, issue.ErrAlreadyMerged) {
		t.Fatalf("re-merge error = %v, want ErrAlreadyMerged", err)
	}
}

// TestIssueRepositoryMergeIssueSkipsSelfReferentialAliasOnRepeatTarget covers
// Finding 2's dead-rule bug: merging a second, fresh issue into a target that
// is already a merge target from an earlier merge must NOT insert the
// target-side alias rule this time, because target.fingerprint already
// equals grouping.MergeFingerprint(target's own id) — a value no ingested
// event can ever produce deterministically, so a rule matching it would be
// permanent, unreachable dead weight evaluated on every future event.
func TestIssueRepositoryMergeIssueSkipsSelfReferentialAliasOnRepeatTarget(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("repeat-a"), "A", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-2*time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("repeat-b"), "B", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("repeat-d"), "D", map[string]string{"environment": "production", "release": "1.0.0"}, now)

	a := issueIDForFingerprint(t, ctx, pool, hex64("repeat-a"))
	b := issueIDForFingerprint(t, ctx, pool, hex64("repeat-b"))
	d := issueIDForFingerprint(t, ctx, pool, hex64("repeat-d"))

	// First merge into b: fresh on both sides, so both aliases are inserted
	// (2 rows), exactly like TestIssueRepositoryMergeIssue.
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), a, b); err != nil {
		t.Fatalf("merge a into b: %v", err)
	}

	var countAfterFirst int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM grouping_rules WHERE project_id = $1 AND template = $2
	`, projectID, grouping.MergeTemplate(b)).Scan(&countAfterFirst); err != nil || countAfterFirst != 2 {
		t.Fatalf("alias rule count after first merge = %d, error = %v, want 2", countAfterFirst, err)
	}

	// Second, unrelated merge into the SAME target b: b.fingerprint is
	// already grouping.MergeFingerprint(b), so the target-side alias this
	// time must be skipped — only the fresh source-side alias for d is new.
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), d, b); err != nil {
		t.Fatalf("merge d into b: %v", err)
	}

	var countAfterSecond int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM grouping_rules WHERE project_id = $1 AND template = $2
	`, projectID, grouping.MergeTemplate(b)).Scan(&countAfterSecond); err != nil || countAfterSecond != 3 {
		t.Fatalf("alias rule count after second merge = %d, error = %v, want 3 (2 + 1 new source-side, target-side skipped)", countAfterSecond, err)
	}

	var selfReferentialCount int
	selfMatcher := "fingerprint:" + grouping.MergeFingerprint(b)
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM grouping_rules WHERE project_id = $1 AND matcher = $2
	`, projectID, selfMatcher).Scan(&selfReferentialCount); err != nil || selfReferentialCount != 0 {
		t.Fatalf("self-referential alias rows (matcher = %q) = %d, error = %v, want 0", selfMatcher, selfReferentialCount, err)
	}
}

// TestIssueRepositoryMergeIssueEnforcesRuleCap covers Finding 2's second half:
// merge must not silently bypass MaxRulesPerProject. A project already at the
// cap must have its merge fail outright with grouping.ErrTooManyRules, rather
// than quietly inserting past the bound that guards ingest's per-event
// rule-evaluation cost.
func TestIssueRepositoryMergeIssueEnforcesRuleCap(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("cap-source"), "Cap Source", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("cap-target"), "Cap Target", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("cap-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("cap-target"))

	// Fill the project to one below the cap: a fresh merge would insert 2
	// alias rules (neither side is self-referential), pushing the total to
	// MaxRulesPerProject+1.
	const filledRuleCount = grouping.MaxRulesPerProject - 1
	for position := 1; position <= filledRuleCount; position++ {
		matcher := fmt.Sprintf("logger:filler-%d", position)
		if _, err := pool.Exec(ctx, `
			INSERT INTO grouping_rules (project_id, position, matcher, template) VALUES ($1, $2, $3, 'filler')
		`, projectID, position, matcher); err != nil {
			t.Fatalf("seed filler rule %d: %v", position, err)
		}
	}

	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); !errors.Is(err, grouping.ErrTooManyRules) {
		t.Fatalf("merge at cap error = %v, want ErrTooManyRules", err)
	}

	// The failed merge must not have partially applied: no alias rules
	// inserted, source not tombstoned.
	var totalRuleCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM grouping_rules WHERE project_id = $1`, projectID).Scan(&totalRuleCount); err != nil || totalRuleCount != filledRuleCount {
		t.Fatalf("rule count after rejected merge = %d, error = %v, want unchanged %d", totalRuleCount, err, filledRuleCount)
	}
	detail, err := repository.Detail(ctx, access.Unrestricted(), sourceID)
	if err != nil {
		t.Fatalf("detail after rejected merge: %v", err)
	}
	if detail.Issue.MergedIntoID != nil {
		t.Fatalf("source tombstoned after cap-rejected merge: merged_into_id = %v", detail.Issue.MergedIntoID)
	}
}

// TestIssueRepositoryMergeIssueFoldsTagFacets covers Finding 3: the tag-fold
// SQL had zero test assertions. Source and target are seeded with
// deliberately asymmetric windows on the SAME (key, value) — source is OLDER
// on first_seen and NEWER on last_seen than target — so a swapped
// LEAST/GREATEST computes the opposite of both and this test fails. A second
// tag present only on the source exercises the plain-insert branch alongside
// the ON CONFLICT branch the shared (key, value) row exercises.
func TestIssueRepositoryMergeIssueFoldsTagFacets(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("fold-source"), "Fold Source", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-3*time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("fold-target"), "Fold Target", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))

	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("fold-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("fold-target"))

	sourceFirstSeen := now.Add(-3 * time.Hour)
	sourceLastSeen := now.Add(-10 * time.Minute)
	targetFirstSeen := now.Add(-time.Hour)
	targetLastSeen := now.Add(-50 * time.Minute)
	setTagWindow(t, ctx, pool, sourceID, "environment", "production", 5, sourceFirstSeen, sourceLastSeen)
	setTagWindow(t, ctx, pool, targetID, "environment", "production", 3, targetFirstSeen, targetLastSeen)

	sourceOnlyFirstSeen := now.Add(-2 * time.Hour)
	sourceOnlyLastSeen := now.Add(-90 * time.Minute)
	insertTagValue(t, ctx, pool, sourceID, "browser", "firefox", 2, sourceOnlyFirstSeen, sourceOnlyLastSeen)

	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}

	var timesSeen int64
	var firstSeen, lastSeen time.Time
	if err := pool.QueryRow(ctx, `
		SELECT times_seen, first_seen, last_seen FROM issue_tag_values
		WHERE issue_id = $1 AND key = 'environment' AND value = 'production'
	`, targetID).Scan(&timesSeen, &firstSeen, &lastSeen); err != nil {
		t.Fatalf("folded environment tag: %v", err)
	}
	if timesSeen != 8 {
		t.Fatalf("folded times_seen = %d, want 8 (3+5)", timesSeen)
	}
	if firstSeen.Sub(sourceFirstSeen).Abs() > time.Second {
		t.Fatalf("folded first_seen = %v, want source's older %v (LEAST)", firstSeen, sourceFirstSeen)
	}
	if lastSeen.Sub(sourceLastSeen).Abs() > time.Second {
		t.Fatalf("folded last_seen = %v, want source's newer %v (GREATEST)", lastSeen, sourceLastSeen)
	}

	var browserCount int64
	var browserFirstSeen, browserLastSeen time.Time
	if err := pool.QueryRow(ctx, `
		SELECT times_seen, first_seen, last_seen FROM issue_tag_values
		WHERE issue_id = $1 AND key = 'browser' AND value = 'firefox'
	`, targetID).Scan(&browserCount, &browserFirstSeen, &browserLastSeen); err != nil {
		t.Fatalf("plain-inserted browser tag missing on target: %v", err)
	}
	if browserCount != 2 || browserFirstSeen.Sub(sourceOnlyFirstSeen).Abs() > time.Second || browserLastSeen.Sub(sourceOnlyLastSeen).Abs() > time.Second {
		t.Fatalf("browser tag = (%d, %v, %v), want (2, %v, %v)", browserCount, browserFirstSeen, browserLastSeen, sourceOnlyFirstSeen, sourceOnlyLastSeen)
	}

	var sourceTagCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue_tag_values WHERE issue_id = $1`, sourceID).Scan(&sourceTagCount); err != nil || sourceTagCount != 0 {
		t.Fatalf("source tag rows = %d, error = %v, want 0", sourceTagCount, err)
	}

	detail, err := repository.Detail(ctx, access.Unrestricted(), targetID)
	if err != nil {
		t.Fatalf("detail after merge: %v", err)
	}
	if len(detail.Environments) != 1 || detail.Environments[0] != "production" {
		t.Fatalf("merged detail environments = %+v, want [production]", detail.Environments)
	}
}

func setTagWindow(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64, key, value string, timesSeen int64, firstSeen, lastSeen time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		UPDATE issue_tag_values SET times_seen = $4, first_seen = $5, last_seen = $6
		WHERE issue_id = $1 AND key = $2 AND value = $3
	`, issueID, key, value, timesSeen, firstSeen, lastSeen); err != nil {
		t.Fatalf("set tag window (%d,%s,%s): %v", issueID, key, value, err)
	}
}

func insertTagValue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64, key, value string, timesSeen int64, firstSeen, lastSeen time.Time) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue_tag_values (project_id, issue_id, key, value, times_seen, first_seen, last_seen)
		SELECT project_id, $1, $2, $3, $4, $5, $6 FROM issues WHERE id = $1
	`, issueID, key, value, timesSeen, firstSeen, lastSeen); err != nil {
		t.Fatalf("insert tag value (%d,%s,%s): %v", issueID, key, value, err)
	}
}

// countingRuleStore wraps a real grouping.RuleStore and counts calls, so the
// cache-invalidation test below can prove a re-read actually happened rather
// than just observing a returned value that could coincidentally match.
type countingRuleStore struct {
	inner grouping.RuleStore
	calls int
}

func (s *countingRuleStore) ListByProjectID(ctx context.Context, projectID int64) ([]grouping.Rule, error) {
	s.calls++
	return s.inner.ListByProjectID(ctx, projectID)
}

// TestIssueRepositoryMergeIssueInvalidatesRuleCache covers Finding 4: merge
// must synchronously invalidate the wired rule cache's entry for the
// affected project after its transaction commits, so ingest's next lookup
// re-reads the just-inserted alias rules instead of serving up to 30s of
// stale ones (which would route events from the source's signal onto the
// tombstoned source issue instead of the target).
func TestIssueRepositoryMergeIssueInvalidatesRuleCache(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	store := &countingRuleStore{inner: storagepostgres.NewGroupingRuleRepository(pool)}
	ruleCache := grouping.NewRuleCache(store, time.Now)
	repository := storagepostgres.NewIssueRepository(pool, ruleCache)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("cache-source"), "Cache Source", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("cache-target"), "Cache Target", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("cache-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("cache-target"))

	if _, err := ruleCache.Get(ctx, projectID); err != nil {
		t.Fatalf("prime cache: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls after priming = %d, want 1", store.calls)
	}

	// Still well within the cache's 30s TTL — without invalidation this next
	// Get would be served from the stale entry primed above.
	if _, err := ruleCache.Get(ctx, projectID); err != nil {
		t.Fatalf("Get before merge: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls before merge = %d, want 1 (still cached)", store.calls)
	}

	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}

	if _, err := ruleCache.Get(ctx, projectID); err != nil {
		t.Fatalf("Get after merge: %v", err)
	}
	if store.calls != 2 {
		t.Fatalf("store calls after merge = %d, want 2 (merge must invalidate synchronously on commit)", store.calls)
	}
}

func TestIssueRepositoryMergeIssueCascadesChainedMerges(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("a"), "A", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-2*time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("b"), "B", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("c"), "C", map[string]string{"environment": "production", "release": "1.0.0"}, now)

	a := issueIDForFingerprint(t, ctx, pool, hex64("a"))
	b := issueIDForFingerprint(t, ctx, pool, hex64("b"))
	c := issueIDForFingerprint(t, ctx, pool, hex64("c"))

	// A merges into B first.
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), a, b); err != nil {
		t.Fatalf("merge a into b: %v", err)
	}
	// B (now itself a merge target, holding A's alias rules) merges into C.
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), b, c); err != nil {
		t.Fatalf("merge b into c: %v", err)
	}

	var aMergedInto, bMergedInto *int64
	if err := pool.QueryRow(ctx, `SELECT merged_into_id FROM issues WHERE id = $1`, a).Scan(&aMergedInto); err != nil || aMergedInto == nil || *aMergedInto != c {
		t.Fatalf("a merged_into_id = %v, error = %v, want %d (flattened directly to c)", aMergedInto, err, c)
	}
	if err := pool.QueryRow(ctx, `SELECT merged_into_id FROM issues WHERE id = $1`, b).Scan(&bMergedInto); err != nil || bMergedInto == nil || *bMergedInto != c {
		t.Fatalf("b merged_into_id = %v, error = %v, want %d", bMergedInto, err, c)
	}

	var staleAliasCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM grouping_rules WHERE project_id = $1 AND template = $2
	`, projectID, grouping.MergeTemplate(b)).Scan(&staleAliasCount); err != nil || staleAliasCount != 0 {
		t.Fatalf("stale alias rules pointing at b = %d, error = %v, want 0 (repointed to c)", staleAliasCount, err)
	}

	// End-to-end: A's *original* fingerprint must still resolve, through the
	// repointed alias rule, all the way to C's minted fingerprint — proving a
	// future event from A's original signal actually reaches C, not a dead
	// link at B. This is what the chained repoint is for; checking
	// merged_into_id and the absence of a stale template (above) is not
	// sufficient on its own to prove future events route correctly.
	ruleRepository := storagepostgres.NewGroupingRuleRepository(pool)
	rules, err := ruleRepository.ListByProjectID(ctx, projectID)
	if err != nil {
		t.Fatalf("list grouping rules: %v", err)
	}
	deterministic := grouping.Result{Fingerprint: hex64("a"), Title: "A", Culprit: ""}
	evaluated := grouping.EvaluateRules(rules, grouping.EvaluationInput{}, deterministic)
	if evaluated.Fingerprint != grouping.MergeFingerprint(c) {
		t.Fatalf("evaluating a's original fingerprint through persisted rules = %q, want %q (c's minted fingerprint) — a future event from a would not reach c",
			evaluated.Fingerprint, grouping.MergeFingerprint(c))
	}

	var cFingerprint string
	if err := pool.QueryRow(ctx, `SELECT fingerprint FROM issues WHERE id = $1`, c).Scan(&cFingerprint); err != nil || cFingerprint != grouping.MergeFingerprint(c) {
		t.Fatalf("c fingerprint = %q, error = %v, want %q", cFingerprint, err, grouping.MergeFingerprint(c))
	}
}

func TestIssueRepositoryMergeIssueRejectsSameIssue(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()
	seedIssueAt(t, ctx, pool, projectID, hex64("self"), "Self", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	id := issueIDForFingerprint(t, ctx, pool, hex64("self"))

	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), id, id); !errors.Is(err, issue.ErrSameIssue) {
		t.Fatalf("MergeIssue(id, id) error = %v, want ErrSameIssue", err)
	}

	// No corruption from the rejected attempt: the issue must still be live.
	detail, err := repository.Detail(ctx, access.Unrestricted(), id)
	if err != nil {
		t.Fatalf("detail after rejected self-merge: %v", err)
	}
	if detail.Issue.MergedIntoID != nil {
		t.Fatalf("issue tombstoned itself after rejected self-merge: merged_into_id = %v", detail.Issue.MergedIntoID)
	}
}

func TestIssueRepositoryMergeIssueRejectsMissingOrAlreadyMergedOrCrossProject(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("live-source"), "Live Source", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("live-target"), "Live Target", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	seedIssueAt(t, ctx, pool, projectID, hex64("chain-a"), "Chain A", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-2*time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("chain-b"), "Chain B", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))

	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("live-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("live-target"))
	const missingID = int64(987654321)

	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), missingID, targetID); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("missing source error = %v, want ErrNotFound", err)
	}
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, missingID); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("missing target error = %v, want ErrNotFound", err)
	}

	var otherProjectID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('other','Other','javascript') RETURNING id
	`).Scan(&otherProjectID); err != nil {
		t.Fatalf("seed other project: %v", err)
	}
	seedIssueAt(t, ctx, pool, otherProjectID, hex64("other-project"), "Other Project Issue", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	otherProjectIssueID := issueIDForFingerprint(t, ctx, pool, hex64("other-project"))
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), otherProjectIssueID, targetID); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("cross-project source error = %v, want ErrNotFound (must not leak cross-project existence)", err)
	}

	chainA := issueIDForFingerprint(t, ctx, pool, hex64("chain-a"))
	chainB := issueIDForFingerprint(t, ctx, pool, hex64("chain-b"))
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), chainA, chainB); err != nil {
		t.Fatalf("merge chain-a into chain-b: %v", err)
	}
	// chainA is now tombstoned: using it as a source again must be rejected.
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), chainA, targetID); !errors.Is(err, issue.ErrAlreadyMerged) {
		t.Fatalf("already-merged source error = %v, want ErrAlreadyMerged", err)
	}
	// chainA is not a live head; using it as a *target* must also be rejected.
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, chainA); !errors.Is(err, issue.ErrAlreadyMerged) {
		t.Fatalf("already-merged target error = %v, want ErrAlreadyMerged", err)
	}
}

func TestIssueListExcludesTombstonedIssues(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("visible-source"), "Visible Source", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("visible-target"), "Visible Target", map[string]string{"environment": "production", "release": "1.0.0"}, now)

	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("visible-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("visible-target"))
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("merge: %v", err)
	}

	page, err := repository.List(ctx, issue.ListFilters{Scope: access.Unrestricted(), ProjectSlug: "web", Sort: issue.SortLastSeen, Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, listed := range page.Issues {
		if listed.ID == sourceID {
			t.Fatalf("tombstoned source issue %d appeared in list", sourceID)
		}
	}

	detail, err := repository.Detail(ctx, access.Unrestricted(), sourceID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if detail.Issue.MergedIntoID == nil || *detail.Issue.MergedIntoID != targetID {
		t.Fatalf("source detail MergedIntoID = %v, want %d", detail.Issue.MergedIntoID, targetID)
	}
}

func issueIDForFingerprint(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fingerprint string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `SELECT id FROM issues WHERE fingerprint = $1`, fingerprint).Scan(&id); err != nil {
		t.Fatalf("find id for fingerprint %s: %v", fingerprint, err)
	}
	return id
}

// convertAllEventChunksToColumnstore forces every chunk currently holding
// events onto the columnstore, exactly like migrations/000004_policies.up.sql's
// 24h add_columnstore_policy eventually would in production, without waiting
// on that scheduled policy. Chunks already converted (if_not_columnstore)
// are silently skipped.
func convertAllEventChunksToColumnstore(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `
		DO $$
		DECLARE c regclass;
		BEGIN
			FOR c IN SELECT show_chunks('events') LOOP
				CALL convert_to_columnstore(c, if_not_columnstore => true);
			END LOOP;
		END $$;
	`); err != nil {
		t.Fatalf("convert event chunks to columnstore: %v", err)
	}
	var compressedChunks int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM chunk_columnstore_stats('events') WHERE compression_status = 'Compressed'
	`).Scan(&compressedChunks); err != nil {
		t.Fatalf("count compressed chunks: %v", err)
	}
	if compressedChunks == 0 {
		t.Fatal("expected at least one compressed chunk after conversion -- test setup did not actually exercise the columnstore path")
	}
}

// TestIssueRepositoryMergeIssueOnCompressedColumnstoreChunk is I-2's
// regression test. A prior note in this project's ledger claimed the events
// table had no compression/columnstore policy; that was wrong —
// migrations/000004_policies.up.sql enables the columnstore on events with
// segmentby = 'project_id,issue_id' and a 24h columnstore policy. Once a
// merged issue has more than 24h of history, its older events live in
// compressed segments that MergeIssue's `UPDATE events SET issue_id = ...`
// must rewrite.
//
// Verified behavior on timescale/timescaledb:latest-pg18 (this project's
// production image, matched by internal/testdatabase.StartTimescale): that
// UPDATE does not error or hang on compressed data by itself, but TimescaleDB
// enforces timescaledb.max_tuples_decompressed_per_dml_transaction (default
// 100,000) — a single DML statement decompressing more compressed tuples
// than that aborts with "tuple decompression limit exceeded by operation",
// which would abort this entire merge transaction. This test merges an issue
// with more compressed events than that default cap; the fix is a `SET LOCAL
// timescaledb.max_tuples_decompressed_per_dml_transaction = 0` scoped to
// just this transaction (see MergeIssue), since the UPDATE here is a
// deliberate, bounded, one-issue rewrite, not the runaway automatic one the
// cap exists to catch.
func TestIssueRepositoryMergeIssueOnCompressedColumnstoreChunk(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("cs-source"), "CS Source", map[string]string{"environment": "production", "release": "1.0.0"}, now.Add(-96*time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("cs-target"), "CS Target", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("cs-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("cs-target"))

	// Comfortably over timescaledb.max_tuples_decompressed_per_dml_transaction's
	// default cap (100,000), spread far enough apart to land across several
	// of events' 6-hour chunks once converted to columnstore.
	const bulkEventCount = 120_000
	oldStart := now.Add(-96 * time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, environment, message, payload)
		SELECT $1, $2, gen_random_uuid(), $3::timestamptz + (g * interval '1 second'), $3, 'error', 'production', 'columnstore bulk event ' || g, '{}'::jsonb
		FROM generate_series(1, $4) AS g
	`, projectID, sourceID, oldStart, bulkEventCount); err != nil {
		t.Fatalf("bulk-insert old events: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET times_seen = times_seen + $2 WHERE id = $1`, sourceID, bulkEventCount); err != nil {
		t.Fatalf("bump source times_seen: %v", err)
	}

	convertAllEventChunksToColumnstore(t, ctx, pool)

	repository := storagepostgres.NewIssueRepository(pool)
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("merge issue on compressed columnstore chunks: %v", err)
	}

	var movedEvents int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE issue_id = $1`, targetID).Scan(&movedEvents); err != nil {
		t.Fatalf("count target events: %v", err)
	}
	const wantTargetEvents = bulkEventCount + 2 // +1 source's own seeded event, +1 target's own seeded event
	if movedEvents != wantTargetEvents {
		t.Fatalf("target event count = %d, want %d (compressed events were not fully reassigned)", movedEvents, wantTargetEvents)
	}
	var strandedEvents int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE issue_id = $1`, sourceID).Scan(&strandedEvents); err != nil {
		t.Fatalf("count source events: %v", err)
	}
	if strandedEvents != 0 {
		t.Fatalf("source event count after merge = %d, want 0 (stranded on the tombstone)", strandedEvents)
	}
}

// TestIssueRepositoryMergeRefreshesHourlySparklinesForOldHistory is I-3's
// regression test. issue_counts_hourly's own continuous-aggregate refresh
// policy only ever re-materializes [now-3h, now-5min] on its schedule; once a
// bucket has been materialized ("settled") at some point, real-time
// aggregation (materialized_only = false) no longer recomputes it on the fly
// -- a later raw-table change underneath a settled bucket (exactly what
// merge's `UPDATE events SET issue_id = ...` does to old history) is
// provably invisible through the view until something explicitly refreshes
// that historical range again. This test settles the old bucket first (via
// an explicit refresh over a narrow window, standing in for the recurring
// policy having covered it long ago), then adds more raw events underneath
// it, confirms the settled view is stale for that addition, merges, and
// asserts the *target's* bucket reflects the full moved count afterward --
// which only MergeIssue's post-commit refresh (source.firstSeen through now)
// can produce, since nothing else ever revisits that historical range.
func TestIssueRepositoryMergeRefreshesHourlySparklinesForOldHistory(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	now := time.Now().UTC()
	oldTime := now.Add(-240 * time.Hour) // 10 days ago -- far outside the 3h refresh policy window

	seedIssueAt(t, ctx, pool, projectID, hex64("sparkline-source"), "Sparkline Source", map[string]string{"environment": "production", "release": "1.0.0"}, oldTime)
	seedIssueAt(t, ctx, pool, projectID, hex64("sparkline-target"), "Sparkline Target", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("sparkline-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("sparkline-target"))

	var oldBucket time.Time
	if err := pool.QueryRow(ctx, `SELECT time_bucket('1 hour', $1::timestamptz)`, oldTime).Scan(&oldBucket); err != nil {
		t.Fatalf("compute old bucket: %v", err)
	}

	// Settle the old bucket now, standing in for the recurring policy having
	// materialized it long ago when it was still within [now-3h, now-5min].
	if _, err := pool.Exec(ctx, `
		CALL refresh_continuous_aggregate('issue_counts_hourly', $1::timestamptz - interval '1 hour', $1::timestamptz + interval '2 hours')
	`, oldTime); err != nil {
		t.Fatalf("settle old bucket: %v", err)
	}
	var settledCount int64
	if err := pool.QueryRow(ctx, `
		SELECT n FROM issue_counts_hourly WHERE issue_id = $1 AND bucket = $2
	`, sourceID, oldBucket).Scan(&settledCount); err != nil {
		t.Fatalf("settled bucket count: %v", err)
	}
	if settledCount != 1 {
		t.Fatalf("settled bucket count = %d, want 1 (seedIssue's own event)", settledCount)
	}

	// Add more raw history underneath the now-settled bucket -- e.g. more
	// occurrences of the same long-lived issue -- and confirm the settled
	// view provably does not pick it up on its own (real-time aggregation
	// only covers data past the watermark, not retroactive changes below it).
	const extraOldEvents = 25
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, environment, message, payload)
		SELECT $1, $2, gen_random_uuid(), $3, $3, 'error', 'production', 'sparkline old event', '{}'::jsonb
		FROM generate_series(1, $4)
	`, projectID, sourceID, oldTime, extraOldEvents); err != nil {
		t.Fatalf("seed extra old events: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET times_seen = times_seen + $2 WHERE id = $1`, sourceID, extraOldEvents); err != nil {
		t.Fatalf("bump source times_seen: %v", err)
	}
	const wantMovedCount = extraOldEvents + 1 // +1 for seedIssue's own event

	var staleCount int64
	if err := pool.QueryRow(ctx, `
		SELECT n FROM issue_counts_hourly WHERE issue_id = $1 AND bucket = $2
	`, sourceID, oldBucket).Scan(&staleCount); err != nil {
		t.Fatalf("stale bucket count: %v", err)
	}
	if staleCount != 1 {
		t.Fatalf("stale bucket count = %d, want 1 (this test's premise requires the settled bucket to provably not auto-update)", staleCount)
	}

	// The refresh now runs in a background goroutine (see
	// scheduleMergedSparklineRefresh), so MergeIssue returning is no longer
	// proof it has happened. AfterMergeSparklineRefresh is the test-only hook
	// that fires once that goroutine finishes; wait on it (bounded, not a
	// sleep) instead of asserting anything about sparklines immediately after
	// MergeIssue returns.
	refreshDone := make(chan struct{})
	var reportedErr error
	repository := storagepostgres.NewIssueRepositoryWithOptions(pool, storagepostgres.IssueRepositoryOptions{
		ReportError:                func(err error) { reportedErr = err },
		AfterMergeSparklineRefresh: func() { close(refreshDone) },
	})
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("merge issue: %v", err)
	}
	select {
	case <-refreshDone:
	case <-time.After(10 * time.Second):
		t.Fatal("merge issue: background sparkline refresh did not complete in time")
	}
	if reportedErr != nil {
		t.Fatalf("merge issue reported a sparkline-refresh error: %v", reportedErr)
	}

	// The stale row from settling must be gone too, not merely superseded by
	// a second row under target -- refresh_continuous_aggregate recomputes
	// the bucket's rows from current data, it doesn't add to what was there.
	var staleSourceRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM issue_counts_hourly WHERE issue_id = $1 AND bucket = $2
	`, sourceID, oldBucket).Scan(&staleSourceRows); err != nil {
		t.Fatalf("post-merge source bucket rows: %v", err)
	}
	if staleSourceRows != 0 {
		t.Fatalf("post-merge issue_counts_hourly rows still under source's old id = %d, want 0", staleSourceRows)
	}

	var n int64
	if err := pool.QueryRow(ctx, `
		SELECT n FROM issue_counts_hourly WHERE issue_id = $1 AND bucket = $2
	`, targetID, oldBucket).Scan(&n); err != nil {
		t.Fatalf("post-merge target bucket: %v (issue_counts_hourly was never refreshed for the moved history)", err)
	}
	if n != wantMovedCount {
		t.Fatalf("post-merge target bucket n = %d, want %d (merged history not reflected in target's sparkline data)", n, wantMovedCount)
	}
}

// TestIssueRepositoryMergeRefreshWindowCoversReceivedAtEarlierThanFirstSeen is
// Minor 1's regression test. issues.first_seen comes straight from the
// client-supplied event timestamp (pipeline/aggregate.go's issueDeltaFor),
// while issue_counts_hourly buckets on the server-stamped received_at
// (migrations/000004_policies.up.sql). A client clock running more than an
// hour fast makes first_seen's bucket start later than the earliest moved
// received_at bucket, so a refresh window derived from first_seen alone would
// never reach that oldest bucket -- exactly the staleness this refresh exists
// to fix. This seeds that skew directly (seedIssue always pairs FirstSeen
// with the same instant as the event, so it can't reproduce this on its own)
// and asserts the merge's refresh still reaches the true earliest bucket.
func TestIssueRepositoryMergeRefreshWindowCoversReceivedAtEarlierThanFirstSeen(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	now := time.Now().UTC()

	// The server actually received the source's one event 10 days ago, but
	// the client's clock was more than an hour fast, so the timestamp it
	// reported -- and therefore issues.first_seen -- lands over 90 minutes
	// later, in the next hour bucket.
	receivedAt := now.Add(-240 * time.Hour)
	clientTimestamp := receivedAt.Add(90 * time.Minute)

	sink := storagepostgres.NewEventSink(pool)
	sourceFingerprint := hex64("clock-skew-source")
	if err := sink.PersistBatch(ctx, storage.Batch{
		ProjectID: projectID,
		Releases:  []string{"1.0.0"},
		Issues: []storage.IssueDelta{{
			Fingerprint: sourceFingerprint, Title: "Clock Skew Source", Culprit: "", Level: "error",
			Count: 1, FirstSeen: clientTimestamp, LastSeen: clientTimestamp,
		}},
		Events: []storage.EventRow{{
			ProjectID: projectID, EventID: uuid.New(), Timestamp: clientTimestamp, ReceivedAt: receivedAt,
			Level: "error", Environment: "production", Message: "Clock Skew Source",
			Fingerprint: sourceFingerprint, Payload: json.RawMessage(`{}`),
		}},
	}); err != nil {
		t.Fatalf("seed clock-skewed source issue: %v", err)
	}
	seedIssueAt(t, ctx, pool, projectID, hex64("clock-skew-target"), "Clock Skew Target", map[string]string{"environment": "production", "release": "1.0.0"}, now)
	sourceID := issueIDForFingerprint(t, ctx, pool, sourceFingerprint)
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("clock-skew-target"))

	var receivedBucket, firstSeenBucket time.Time
	if err := pool.QueryRow(ctx, `SELECT time_bucket('1 hour', $1::timestamptz)`, receivedAt).Scan(&receivedBucket); err != nil {
		t.Fatalf("compute received_at bucket: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT time_bucket('1 hour', $1::timestamptz)`, clientTimestamp).Scan(&firstSeenBucket); err != nil {
		t.Fatalf("compute first_seen bucket: %v", err)
	}
	if !receivedBucket.Before(firstSeenBucket) {
		t.Fatalf("test setup: received_at bucket %v must be strictly before first_seen bucket %v", receivedBucket, firstSeenBucket)
	}

	// Settle the true earliest (received_at) bucket now, standing in for the
	// recurring policy having materialized it long ago when it was still
	// within [now-3h, now-5min].
	if _, err := pool.Exec(ctx, `
		CALL refresh_continuous_aggregate('issue_counts_hourly', $1::timestamptz - interval '1 hour', $1::timestamptz + interval '2 hours')
	`, receivedAt); err != nil {
		t.Fatalf("settle received_at bucket: %v", err)
	}

	// Add more raw history under that now-settled bucket so the merge has
	// something new to surface.
	const extraOldEvents = 7
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, environment, message, payload)
		SELECT $1, $2, gen_random_uuid(), $3, $3, 'error', 'production', 'clock skew extra event', '{}'::jsonb
		FROM generate_series(1, $4)
	`, projectID, sourceID, receivedAt, extraOldEvents); err != nil {
		t.Fatalf("seed extra clock-skewed events: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET times_seen = times_seen + $2 WHERE id = $1`, sourceID, extraOldEvents); err != nil {
		t.Fatalf("bump source times_seen: %v", err)
	}
	const wantMovedCount = extraOldEvents + 1 // +1 for the originally seeded event

	refreshDone := make(chan struct{})
	var reportedErr error
	repository := storagepostgres.NewIssueRepositoryWithOptions(pool, storagepostgres.IssueRepositoryOptions{
		ReportError:                func(err error) { reportedErr = err },
		AfterMergeSparklineRefresh: func() { close(refreshDone) },
	})
	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("merge issue: %v", err)
	}
	select {
	case <-refreshDone:
	case <-time.After(10 * time.Second):
		t.Fatal("merge issue: background sparkline refresh did not complete in time")
	}
	if reportedErr != nil {
		t.Fatalf("merge issue reported a sparkline-refresh error: %v", reportedErr)
	}

	var n int64
	if err := pool.QueryRow(ctx, `
		SELECT n FROM issue_counts_hourly WHERE issue_id = $1 AND bucket = $2
	`, targetID, receivedBucket).Scan(&n); err != nil {
		t.Fatalf("post-merge target received_at bucket: %v (a refresh window derived from first_seen alone would miss this earlier bucket)", err)
	}
	if n != wantMovedCount {
		t.Fatalf("post-merge target received_at bucket n = %d, want %d (refresh window did not reach the earliest moved received_at bucket)", n, wantMovedCount)
	}
}

func TestListFiltersByPredicates(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	seedIssue(t, ctx, pool, projectID, "alpha", map[string]string{"environment": "production"})
	seedIssue(t, ctx, pool, projectID, "beta", map[string]string{"environment": "staging"})

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Predicates: []issue.Predicate{
			{Field: issue.FieldEnvironment, Values: []string{"production"}},
		},
		Limit: 50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "alpha" {
		t.Fatalf("List returned %d issues, want only alpha", len(page.Issues))
	}
}

// seedUser inserts a user row and returns its id, for tests that need an
// assignee to filter by.
func seedUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash) VALUES ($1, $2, 'x') RETURNING id
	`, email, name).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

func TestListFiltersByAssigneeEmail(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	seedIssue(t, ctx, pool, projectID, "alpha", nil)
	seedIssue(t, ctx, pool, projectID, "beta", nil)

	userID := seedUser(t, ctx, pool, "assignee@x.io", "Assignee")
	alphaID := issueIDForFingerprint(t, ctx, pool, hex64("alpha"))
	if err := repository.Assign(ctx, access.Unrestricted(), alphaID, &userID); err != nil {
		t.Fatalf("assign: %v", err)
	}

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Predicates:  []issue.Predicate{{Field: issue.FieldAssignee, Values: []string{"assignee@x.io"}}},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "alpha" {
		t.Fatalf("List returned %d issues, want only alpha", len(page.Issues))
	}
}

func TestListFiltersByAssigneeNoneReturnsUnassigned(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	seedIssue(t, ctx, pool, projectID, "alpha", nil)
	seedIssue(t, ctx, pool, projectID, "beta", nil)

	userID := seedUser(t, ctx, pool, "assignee@x.io", "Assignee")
	alphaID := issueIDForFingerprint(t, ctx, pool, hex64("alpha"))
	if err := repository.Assign(ctx, access.Unrestricted(), alphaID, &userID); err != nil {
		t.Fatalf("assign: %v", err)
	}

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Predicates:  []issue.Predicate{{Field: issue.FieldAssignee, Values: []string{"none"}}},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "beta" {
		t.Fatalf("List returned %d issues, want only beta", len(page.Issues))
	}
}

// TestListNegatedAssigneeStillIncludesUnassigned pins the reason
// assigneeClause's NULL guard exists: negating a column comparison under SQL's
// three-valued logic would otherwise silently drop every unassigned row, even
// though "not assigned to X" should plainly include "assigned to nobody".
func TestListNegatedAssigneeStillIncludesUnassigned(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	seedIssue(t, ctx, pool, projectID, "alpha", nil)
	seedIssue(t, ctx, pool, projectID, "beta", nil)

	userID := seedUser(t, ctx, pool, "assignee@x.io", "Assignee")
	alphaID := issueIDForFingerprint(t, ctx, pool, hex64("alpha"))
	if err := repository.Assign(ctx, access.Unrestricted(), alphaID, &userID); err != nil {
		t.Fatalf("assign: %v", err)
	}

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Predicates:  []issue.Predicate{{Field: issue.FieldAssignee, Negate: true, Values: []string{"assignee@x.io"}}},
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "beta" {
		t.Fatalf("List returned %d issues, want only beta (unassigned, via the NULL guard)", len(page.Issues))
	}
}

func TestListNegatedPredicateExcludes(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	seedIssue(t, ctx, pool, projectID, "alpha", map[string]string{"environment": "production"})
	seedIssue(t, ctx, pool, projectID, "beta", map[string]string{"environment": "staging"})

	page, err := repository.List(ctx, issue.ListFilters{
		Scope:       access.Unrestricted(),
		ProjectSlug: testProjectSlug,
		Predicates: []issue.Predicate{
			{Field: issue.FieldEnvironment, Negate: true, Values: []string{"production"}},
		},
		Limit: 50,
	})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Title != "beta" {
		t.Fatalf("List returned %d issues, want only beta", len(page.Issues))
	}
}
