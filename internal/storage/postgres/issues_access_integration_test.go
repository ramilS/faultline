//go:build integration

package postgres_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestIssueListHonoursScope(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)

	web, _ := seedTwoProjectsWithAnIssueEach(t, pool)

	// Not merely "no rows": an empty page would read to the dashboard as a
	// project with nothing in it, which is a project the caller can see. The
	// slug has to resolve to nothing at all.
	t.Run("an out-of-scope project is not found", func(t *testing.T) {
		_, err := repository.List(ctx, issue.ListFilters{
			ProjectSlug: "secret",
			Scope:       access.Limited([]int64{web}),
			Limit:       50,
			Sort:        issue.SortLastSeen,
		})
		assertNotFound(t, err)
	})

	// The same answer a project that was never created gives, so the two are
	// indistinguishable to a caller probing slugs.
	t.Run("a nonexistent project is not found", func(t *testing.T) {
		_, err := repository.List(ctx, issue.ListFilters{
			ProjectSlug: "never-created",
			Scope:       access.Limited([]int64{web}),
			Limit:       50,
			Sort:        issue.SortLastSeen,
		})
		assertNotFound(t, err)
	})

	t.Run("the in-scope project still lists its issues", func(t *testing.T) {
		page, err := repository.List(ctx, issue.ListFilters{
			ProjectSlug: "web",
			Scope:       access.Limited([]int64{web}),
			Limit:       50,
			Sort:        issue.SortLastSeen,
		})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(page.Issues) != 1 {
			t.Fatalf("issues = %d, want 1", len(page.Issues))
		}
	})

	t.Run("tag facets do not leak an out-of-scope project's vocabulary", func(t *testing.T) {
		_, err := repository.TagKeys(ctx, access.Limited([]int64{web}), "secret")
		assertNotFound(t, err)
		_, err = repository.TagValues(ctx, access.Limited([]int64{web}), "secret", "browser", "", 10)
		assertNotFound(t, err)
	})

	// The two cases above cover an Unrestricted scope (TestTagKeysAndTagValues-
	// AreScopedByProject) and a Limited scope excluding the project. Neither
	// proves a Limited scope that *includes* the target project still returns
	// its real vocabulary rather than being wrongly filtered out by the same
	// predicate that excludes "secret" above.
	t.Run("tag facets return the in-scope project's real vocabulary", func(t *testing.T) {
		keys, err := repository.TagKeys(ctx, access.Limited([]int64{web}), "web")
		if err != nil {
			t.Fatalf("TagKeys: %v", err)
		}
		if len(keys) != 1 || keys[0].Key != "browser" {
			t.Fatalf("keys = %+v, want just [browser]", keys)
		}
		values, err := repository.TagValues(ctx, access.Limited([]int64{web}), "web", "browser", "", 10)
		if err != nil {
			t.Fatalf("TagValues: %v", err)
		}
		if len(values) != 1 || values[0].Value != "chrome" {
			t.Fatalf("values = %+v, want just [chrome]", values)
		}
	})

	// A project the caller does reach, asked for a key it has no values for,
	// must still succeed: the not-found answer above has to come from the
	// boundary, not from an empty result set.
	t.Run("an in-scope project with no matching values still succeeds", func(t *testing.T) {
		values, err := repository.TagValues(ctx, access.Limited([]int64{web}), "web", "no-such-key", "", 10)
		if err != nil {
			t.Fatalf("TagValues: %v", err)
		}
		if len(values) != 0 {
			t.Fatalf("tag values = %+v, want none", values)
		}
	})
}

func TestIssueDetailReadsHonourScope(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)

	web, _ := seedTwoProjectsWithAnIssueEach(t, pool)
	foreignIssue := issueIDForProjectSlug(t, pool, "secret")
	foreignEvent := eventUUIDForProjectSlug(t, pool, "secret")
	scope := access.Limited([]int64{web})

	t.Run("Detail", func(t *testing.T) {
		_, err := repository.Detail(ctx, scope, foreignIssue)
		assertNotFound(t, err)
	})
	t.Run("Events", func(t *testing.T) {
		_, err := repository.Events(ctx, scope, issue.EventQuery{IssueID: foreignIssue, Limit: 10})
		assertNotFound(t, err)
	})
	// Events probes requireIssueInScope only when the page comes back empty
	// (issues.go:1141-1145), and every other Events test here uses an issue
	// that has events, so that probe's in-scope path has no positive control.
	// An empty-but-reachable page is a normal state — the last page of a
	// cursor walk, or an issue whose events have aged out of retention — so
	// this has to prove requireIssueInScope answers nil, not merely that the
	// query above answers ErrNotFound for a foreign issue.
	t.Run("Events on an in-scope issue with no events succeeds", func(t *testing.T) {
		emptyIssue := seedBareIssue(t, pool, web, strings.Repeat("c", 64), "No events yet")
		page, err := repository.Events(ctx, scope, issue.EventQuery{IssueID: emptyIssue, Limit: 10})
		if err != nil {
			t.Fatalf("Events: %v", err)
		}
		if len(page.Events) != 0 {
			t.Fatalf("events = %+v, want none", page.Events)
		}
	})
	t.Run("LatestEvent", func(t *testing.T) {
		_, err := repository.LatestEvent(ctx, scope, foreignIssue)
		assertNotFound(t, err)
	})
	t.Run("Event by uuid", func(t *testing.T) {
		_, err := repository.Event(ctx, scope, foreignEvent)
		assertNotFound(t, err)
	})
	t.Run("Timeline by uuid", func(t *testing.T) {
		spans, err := repository.Timeline(ctx, scope, foreignEvent)
		if err == nil && len(spans) != 0 {
			t.Fatalf("spans = %d, want none", len(spans))
		}
	})
	t.Run("IssueUsers", func(t *testing.T) {
		_, err := repository.IssueUsers(ctx, scope, foreignIssue, 10)
		assertNotFound(t, err)
	})
	t.Run("UpdateStatus refuses a foreign issue", func(t *testing.T) {
		err := repository.UpdateStatus(ctx, scope, foreignIssue, issue.StatusResolved)
		assertNotFound(t, err)
		assertStatusUnchanged(t, pool, foreignIssue, "unresolved")
	})
	t.Run("MergeIssue refuses a foreign participant", func(t *testing.T) {
		local := issueIDForProjectSlug(t, pool, "web")
		_, err := repository.MergeIssue(ctx, scope, foreignIssue, local)
		assertNotFound(t, err)
		_, err = repository.MergeIssue(ctx, scope, local, foreignIssue)
		assertNotFound(t, err)
	})
}

// issue_counts_hourly is a continuous aggregate with no project_id column, so
// this is the one read whose boundary is a CROSS JOIN against a scoped CTE
// rather than a column predicate. A leak here surfaces as another project's
// occurrence counts on a chart, with no other symptom.
func TestStatsHonoursScope(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)

	web, _ := seedTwoProjectsWithAnIssueEach(t, pool)
	foreignIssue := issueIDForProjectSlug(t, pool, "secret")

	// A chart of zeroes would still be a chart of an issue the caller cannot
	// reach, and indistinguishable from a quiet issue they can. The bucket
	// series is generated, so "no buckets at all" is the only answer that can
	// mean "no such issue".
	for _, period := range []issue.Period{issue.Period24h, issue.Period14d, issue.Period90d} {
		t.Run(string(period)+" is not found for a foreign issue", func(t *testing.T) {
			_, err := repository.Stats(ctx, access.Limited([]int64{web}), foreignIssue, period)
			assertNotFound(t, err)
		})
	}

	t.Run("an in-scope issue still returns its counts", func(t *testing.T) {
		local := issueIDForProjectSlug(t, pool, "web")
		buckets, err := repository.Stats(ctx, access.Limited([]int64{web}), local, issue.Period24h)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		total := int64(0)
		for _, bucket := range buckets {
			total += bucket.Count
		}
		if total == 0 {
			t.Fatal("in-scope issue returned no counts; the guard is too strict")
		}
	})

	// An empty restricted scope must not be confused with an unrestricted one:
	// Stats passes nil for projectIDs only when scope.IsUnrestricted() is
	// true. access.Limited(nil) is restricted (IsUnrestricted() is false) but
	// carries no project ids, so `= ANY($2)` is `= ANY('{}')` — false for
	// every row, including the caller's own project. If the nil/empty mapping
	// ever gets swapped, this is the test that catches it: every other
	// subtest above would still pass.
	t.Run("an empty restricted scope is not found even for one's own project", func(t *testing.T) {
		local := issueIDForProjectSlug(t, pool, "web")
		_, err := repository.Stats(ctx, access.Limited(nil), local, issue.Period24h)
		assertNotFound(t, err)
	})
}

func assertNotFound(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("err = %v, want issue.ErrNotFound", err)
	}
}

// issueIDForProjectSlug returns the id of the single issue seeded for a
// project slug by seedTwoProjectsWithAnIssueEach.
func issueIDForProjectSlug(t *testing.T, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	var id int64
	mustScanRow(t, pool, []any{&id}, `
		SELECT i.id FROM issues i JOIN projects p ON p.id = i.project_id WHERE p.slug = $1
	`, slug)
	return id
}

// eventUUIDForProjectSlug returns the event_id of the single event seeded for
// a project slug by seedTwoProjectsWithAnIssueEach.
func eventUUIDForProjectSlug(t *testing.T, pool *pgxpool.Pool, slug string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	mustScanRow(t, pool, []any{&id}, `
		SELECT e.event_id FROM events e JOIN projects p ON p.id = e.project_id WHERE p.slug = $1
	`, slug)
	return id
}

// assertStatusUnchanged confirms a rejected UpdateStatus did not touch the
// row: not just that the call returned an error, but that the status column
// genuinely still reads want.
func assertStatusUnchanged(t *testing.T, pool *pgxpool.Pool, issueID int64, want string) {
	t.Helper()
	var status string
	mustScanRow(t, pool, []any{&status}, `SELECT status FROM issues WHERE id = $1`, issueID)
	if status != want {
		t.Fatalf("status = %q, want unchanged %q", status, want)
	}
}

// seedTwoProjectsWithAnIssueEach inserts two projects ("web" and "secret"),
// one issue per project, one issue_tag_values row per issue under the key
// "browser", and one event per issue — enough for a scope check to have
// something to wrongly include if the predicate were missing, and something
// to rightly include for the in-scope project. The event gives the
// uuid-keyed reads (Event, Timeline) a row to find or wrongly leak.
func seedTwoProjectsWithAnIssueEach(t *testing.T, pool *pgxpool.Pool) (web, secret int64) {
	t.Helper()

	mustScanRow(t, pool, []any{&web}, `
		INSERT INTO projects (slug, name, platform) VALUES ('web', 'Web', 'javascript') RETURNING id
	`)
	mustScanRow(t, pool, []any{&secret}, `
		INSERT INTO projects (slug, name, platform) VALUES ('secret', 'Secret', 'other') RETURNING id
	`)

	now := time.Now().UTC()
	seedIssueWithTag(t, pool, web, strings.Repeat("a", 64), "Web issue", now)
	seedIssueWithTag(t, pool, secret, strings.Repeat("b", 64), "Secret issue", now)

	return web, secret
}

// seedBareIssue seeds an issue row with no events and no tag values, so a
// caller can exercise a read against an issue that is reachable but has
// nothing to return — the state Events.requireIssueInScope must let through.
func seedBareIssue(t *testing.T, pool *pgxpool.Pool, projectID int64, fingerprint, title string) int64 {
	t.Helper()

	var issueID int64
	mustScanRow(t, pool, []any{&issueID}, `
		INSERT INTO issues (project_id, fingerprint, title, first_seen, last_seen)
		VALUES ($1, $2, $3, now(), now()) RETURNING id
	`, projectID, fingerprint, title)
	return issueID
}

func seedIssueWithTag(t *testing.T, pool *pgxpool.Pool, projectID int64, fingerprint, title string, seen time.Time) {
	t.Helper()

	var issueID int64
	mustScanRow(t, pool, []any{&issueID}, `
		INSERT INTO issues (project_id, fingerprint, title, first_seen, last_seen)
		VALUES ($1, $2, $3, $4, $4) RETURNING id
	`, projectID, fingerprint, title, seen)

	mustExec(t, pool, `
		INSERT INTO issue_tag_values (project_id, issue_id, key, value, times_seen, first_seen, last_seen)
		VALUES ($1, $2, 'browser', 'chrome', 1, $3, $3)
	`, projectID, issueID, seen)

	mustExec(t, pool, `
		INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, message, payload)
		VALUES ($1, $2, gen_random_uuid(), $3, $3, 'error', $4, '{}'::jsonb)
	`, projectID, issueID, seen, title)
}
