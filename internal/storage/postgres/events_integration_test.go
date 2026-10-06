//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/ingest"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestEventSink(t *testing.T) {
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

	projects := storagepostgres.NewProjectRepository(pool).Projects
	owner, err := projects.Create(ctx, project.CreateProject{Slug: "svc", Name: "Svc", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	sink := storagepostgres.NewEventSink(pool)

	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	fingerprint := strings.Repeat("a", 64)

	t.Run("creates an issue with counts, event rows, and tag facets", func(t *testing.T) {
		batch := storage.Batch{
			ProjectID: owner.ID,
			Releases:  []string{"svc@1.0.0"},
			Issues: []storage.IssueDelta{{
				Fingerprint: fingerprint,
				Title:       "boom",
				Culprit:     "svc.total",
				Level:       "error",
				Count:       2,
				FirstSeen:   base,
				LastSeen:    base,
				Tags: []storage.TagDelta{
					{Key: "service", Value: "billing", Count: 2, FirstSeen: base, LastSeen: base},
				},
			}},
			Events: []storage.EventRow{
				newEventRow(owner.ID, fingerprint, base),
				newEventRow(owner.ID, fingerprint, base),
			},
		}
		if err := sink.PersistBatch(ctx, batch); err != nil {
			t.Fatalf("persist first batch: %v", err)
		}

		issue := queryIssue(t, ctx, pool, owner.ID, fingerprint)
		if issue.timesSeen != 2 {
			t.Fatalf("times_seen = %d, want 2", issue.timesSeen)
		}
		if issue.status != "unresolved" {
			t.Fatalf("status = %q, want unresolved", issue.status)
		}
		if !issue.lastSeen.Equal(base) {
			t.Fatalf("last_seen = %v, want %v", issue.lastSeen, base)
		}
		if got := countEvents(t, ctx, pool, issue.id); got != 2 {
			t.Fatalf("event rows = %d, want 2", got)
		}
		if got := tagCount(t, ctx, pool, issue.id, "service", "billing"); got != 2 {
			t.Fatalf("service=billing count = %d, want 2", got)
		}
		if got := releaseCount(t, ctx, pool, owner.ID, "svc@1.0.0"); got != 1 {
			t.Fatalf("release rows = %d, want 1", got)
		}
	})

	t.Run("recurrence increments counts, advances last_seen, and reopens resolved issues", func(t *testing.T) {
		issue := queryIssue(t, ctx, pool, owner.ID, fingerprint)
		if _, err := pool.Exec(ctx, `UPDATE issues SET status = 'resolved' WHERE id = $1`, issue.id); err != nil {
			t.Fatalf("resolve issue: %v", err)
		}

		later := base.Add(time.Hour)
		batch := storage.Batch{
			ProjectID: owner.ID,
			Releases:  []string{"svc@1.0.0"},
			Issues: []storage.IssueDelta{{
				Fingerprint: fingerprint,
				Title:       "boom",
				Culprit:     "svc.total",
				Level:       "error",
				Count:       3,
				FirstSeen:   later,
				LastSeen:    later,
				Tags: []storage.TagDelta{
					{Key: "service", Value: "billing", Count: 3, FirstSeen: later, LastSeen: later},
				},
			}},
			Events: []storage.EventRow{
				newEventRow(owner.ID, fingerprint, later),
				newEventRow(owner.ID, fingerprint, later),
				newEventRow(owner.ID, fingerprint, later),
			},
		}
		if err := sink.PersistBatch(ctx, batch); err != nil {
			t.Fatalf("persist recurrence batch: %v", err)
		}

		reopened := queryIssue(t, ctx, pool, owner.ID, fingerprint)
		if reopened.timesSeen != 5 {
			t.Fatalf("times_seen = %d, want 5", reopened.timesSeen)
		}
		if reopened.status != "unresolved" {
			t.Fatalf("status = %q, want unresolved (auto-reopen)", reopened.status)
		}
		if !reopened.lastSeen.Equal(later) {
			t.Fatalf("last_seen = %v, want %v", reopened.lastSeen, later)
		}
		if !reopened.firstSeen.Equal(base) {
			t.Fatalf("first_seen = %v, want unchanged %v", reopened.firstSeen, base)
		}
		if got := countEvents(t, ctx, pool, reopened.id); got != 5 {
			t.Fatalf("event rows = %d, want 5", got)
		}
		if got := tagCount(t, ctx, pool, reopened.id, "service", "billing"); got != 5 {
			t.Fatalf("service=billing count = %d, want 5", got)
		}
	})

	t.Run("keeps ignored issues ignored on recurrence", func(t *testing.T) {
		ignoredFingerprint := strings.Repeat("b", 64)
		first := storage.Batch{
			ProjectID: owner.ID,
			Issues: []storage.IssueDelta{{
				Fingerprint: ignoredFingerprint, Title: "noise", Level: "warning",
				Count: 1, FirstSeen: base, LastSeen: base,
			}},
			Events: []storage.EventRow{newEventRow(owner.ID, ignoredFingerprint, base)},
		}
		if err := sink.PersistBatch(ctx, first); err != nil {
			t.Fatalf("persist ignored seed: %v", err)
		}
		seed := queryIssue(t, ctx, pool, owner.ID, ignoredFingerprint)
		if _, err := pool.Exec(ctx, `UPDATE issues SET status = 'ignored' WHERE id = $1`, seed.id); err != nil {
			t.Fatalf("ignore issue: %v", err)
		}

		second := storage.Batch{
			ProjectID: owner.ID,
			Issues: []storage.IssueDelta{{
				Fingerprint: ignoredFingerprint, Title: "noise", Level: "warning",
				Count: 4, FirstSeen: base, LastSeen: base.Add(time.Hour),
			}},
			Events: []storage.EventRow{newEventRow(owner.ID, ignoredFingerprint, base.Add(time.Hour))},
		}
		if err := sink.PersistBatch(ctx, second); err != nil {
			t.Fatalf("persist ignored recurrence: %v", err)
		}

		after := queryIssue(t, ctx, pool, owner.ID, ignoredFingerprint)
		if after.status != "ignored" {
			t.Fatalf("status = %q, want ignored", after.status)
		}
		if after.timesSeen != 5 {
			t.Fatalf("times_seen = %d, want 5", after.timesSeen)
		}
	})

	t.Run("collapses tag values past the per-key cap into <other> across flushes", func(t *testing.T) {
		// The guard reads the pre-statement snapshot, so it caps values seen in
		// earlier flushes: seed the key to its limit, then a later flush of new
		// values must funnel into the <other> bucket.
		capFingerprint := strings.Repeat("c", 64)
		seed := storage.Batch{
			ProjectID: owner.ID,
			Issues:    []storage.IssueDelta{{Fingerprint: capFingerprint, Title: "capped", Level: "error", Count: 200, FirstSeen: base, LastSeen: base, Tags: userTags(0, 200, base)}},
			Events:    []storage.EventRow{newEventRow(owner.ID, capFingerprint, base)},
		}
		if err := sink.PersistBatch(ctx, seed); err != nil {
			t.Fatalf("persist cap seed: %v", err)
		}
		issue := queryIssue(t, ctx, pool, owner.ID, capFingerprint)
		if seeded := distinctTagValues(t, ctx, pool, issue.id, "user"); seeded != 200 {
			t.Fatalf("seeded distinct user values = %d, want 200", seeded)
		}

		overflow := storage.Batch{
			ProjectID: owner.ID,
			Issues:    []storage.IssueDelta{{Fingerprint: capFingerprint, Title: "capped", Level: "error", Count: 10, FirstSeen: base, LastSeen: base, Tags: userTags(200, 210, base)}},
			Events:    []storage.EventRow{newEventRow(owner.ID, capFingerprint, base)},
		}
		if err := sink.PersistBatch(ctx, overflow); err != nil {
			t.Fatalf("persist cap overflow: %v", err)
		}

		if distinct := distinctTagValues(t, ctx, pool, issue.id, "user"); distinct != 201 {
			t.Fatalf("distinct user values = %d, want 201 (200 seeded + <other>)", distinct)
		}
		if got := tagCount(t, ctx, pool, issue.id, "user", "<other>"); got != 10 {
			t.Fatalf("<other> bucket count = %d, want 10 overflow values", got)
		}
	})
}

type issueRow struct {
	id        int64
	timesSeen int64
	usersSeen int64
	status    string
	firstSeen time.Time
	lastSeen  time.Time
}

func userTags(start, end int, at time.Time) []storage.TagDelta {
	tags := make([]storage.TagDelta, 0, end-start)
	for index := start; index < end; index++ {
		tags = append(tags, storage.TagDelta{
			Key: "user", Value: "u" + strconv.Itoa(index), Count: 1, FirstSeen: at, LastSeen: at,
		})
	}
	return tags
}

func newEventRow(projectID int64, fingerprint string, at time.Time) storage.EventRow {
	return storage.EventRow{
		ProjectID:   projectID,
		EventID:     uuid.New(),
		Timestamp:   at,
		ReceivedAt:  at,
		Level:       "error",
		Environment: "production",
		Message:     "boom",
		Fingerprint: fingerprint,
		Payload:     json.RawMessage(`{"message":"boom"}`),
	}
}

func queryIssue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, fingerprint string) issueRow {
	t.Helper()
	var issue issueRow
	err := pool.QueryRow(ctx, `
		SELECT id, times_seen, users_seen, status::text, first_seen, last_seen
		FROM issues WHERE project_id = $1 AND fingerprint = $2
	`, projectID, fingerprint).Scan(&issue.id, &issue.timesSeen, &issue.usersSeen, &issue.status, &issue.firstSeen, &issue.lastSeen)
	if err != nil {
		t.Fatalf("query issue: %v", err)
	}
	issue.firstSeen = issue.firstSeen.UTC()
	issue.lastSeen = issue.lastSeen.UTC()
	return issue
}

func countEvents(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE issue_id = $1`, issueID).Scan(&count); err != nil {
		t.Fatalf("count events: %v", err)
	}
	return count
}

func tagCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64, key, value string) int64 {
	t.Helper()
	var count int64
	err := pool.QueryRow(ctx, `
		SELECT times_seen FROM issue_tag_values
		WHERE issue_id = $1 AND key = $2 AND value = $3
	`, issueID, key, value).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("tag count: %v", err)
	}
	return count
}

func distinctTagValues(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64, key string) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM issue_tag_values WHERE issue_id = $1 AND key = $2
	`, issueID, key).Scan(&count); err != nil {
		t.Fatalf("count distinct tag values: %v", err)
	}
	return count
}

func releaseCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, version string) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM releases WHERE project_id = $1 AND version = $2
	`, projectID, version).Scan(&count); err != nil {
		t.Fatalf("count releases: %v", err)
	}
	return count
}

func userTimesSeen(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64, key, value string) int64 {
	t.Helper()
	var count int64
	err := pool.QueryRow(ctx, `
		SELECT times_seen FROM issue_users
		WHERE issue_id = $1 AND user_key = $2 AND user_value = $3
	`, issueID, key, value).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatalf("user times_seen: %v", err)
	}
	return count
}

func countIssueUsers(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64) int64 {
	t.Helper()
	var count int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM issue_users WHERE issue_id = $1
	`, issueID).Scan(&count); err != nil {
		t.Fatalf("count issue_users: %v", err)
	}
	return count
}

// TestPersistBatchCountsDistinctUsersPerIssue pins the honest-counting
// requirement: three events from two identities must leave users_seen at 2
// (not 3, the event count, and not 1, a single collapsed row) and one
// issue_users row per identity with the right times_seen split. This fails
// if the aggregator or the writer ever counts events instead of identities.
func TestPersistBatchCountsDistinctUsersPerIssue(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64("distinct-users-happy-path")
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	later := base.Add(time.Hour)

	batch := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "boom", Level: "error",
			Count: 3, FirstSeen: base, LastSeen: later,
			Users: []storage.UserDelta{
				{Key: "email", Value: "a@example.com", Count: 2, FirstSeen: base, LastSeen: later},
				{Key: "email", Value: "b@example.com", Count: 1, FirstSeen: later, LastSeen: later},
			},
		}},
		Events: []storage.EventRow{
			newEventRow(projectID, fingerprint, base),
			newEventRow(projectID, fingerprint, later),
			newEventRow(projectID, fingerprint, later),
		},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("persist batch: %v", err)
	}

	issue := queryIssue(t, ctx, pool, projectID, fingerprint)
	if issue.timesSeen != 3 {
		t.Fatalf("times_seen = %d, want 3", issue.timesSeen)
	}
	if issue.usersSeen != 2 {
		t.Fatalf("users_seen = %d, want 2 (distinct identities, not the event count)", issue.usersSeen)
	}
	if got := countIssueUsers(t, ctx, pool, issue.id); got != 2 {
		t.Fatalf("issue_users rows = %d, want 2", got)
	}
	if got := userTimesSeen(t, ctx, pool, issue.id, "email", "a@example.com"); got != 2 {
		t.Fatalf("a@example.com times_seen = %d, want 2", got)
	}
	if got := userTimesSeen(t, ctx, pool, issue.id, "email", "b@example.com"); got != 1 {
		t.Fatalf("b@example.com times_seen = %d, want 1", got)
	}
}

// TestPersistBatchSaturatesTheDistinctUserCountAtTheCap pins the saturating
// behaviour that distinguishes issue_users from issue_tag_values: past the
// cap, a brand-new identity is silently dropped rather than collapsed into an
// "<other>" bucket, while an identity already on file keeps incrementing.
// This fails if the writer buckets overflow (tag-facet behaviour) or lets the
// table grow past the cap instead of saturating it.
func TestPersistBatchSaturatesTheDistinctUserCountAtTheCap(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64("distinct-users-at-the-cap")
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)

	seed := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "capped", Level: "error",
			Count: 1, FirstSeen: base, LastSeen: base,
		}},
		Events: []storage.EventRow{newEventRow(projectID, fingerprint, base)},
	}
	if err := sink.PersistBatch(ctx, seed); err != nil {
		t.Fatalf("persist seed: %v", err)
	}
	issue := queryIssue(t, ctx, pool, projectID, fingerprint)

	// Seed MaxUsersPerIssue distinct identities with one statement rather than
	// one round trip each — 10000 individual inserts would make this test
	// unreasonably slow for no additional coverage; what matters is the state
	// the writer sees, not how it got there.
	const identityCap = ingest.MaxUsersPerIssue
	if _, err := pool.Exec(ctx, `
		INSERT INTO issue_users (issue_id, user_key, user_value, times_seen, first_seen, last_seen)
		SELECT $1, 'id', 'seed-' || s, 1, $2, $2
		FROM generate_series(1, $3) AS s
	`, issue.id, base, identityCap); err != nil {
		t.Fatalf("bulk-seed identities: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE issues SET users_seen = $2 WHERE id = $1`, issue.id, identityCap); err != nil {
		t.Fatalf("seed users_seen: %v", err)
	}

	later := base.Add(time.Hour)
	const existingValue = "seed-1"
	const newValue = "past-the-cap"
	overflow := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "capped", Level: "error",
			Count: 2, FirstSeen: later, LastSeen: later,
			Users: []storage.UserDelta{
				{Key: "id", Value: newValue, Count: 1, FirstSeen: later, LastSeen: later},
				{Key: "id", Value: existingValue, Count: 1, FirstSeen: later, LastSeen: later},
			},
		}},
		Events: []storage.EventRow{
			newEventRow(projectID, fingerprint, later),
			newEventRow(projectID, fingerprint, later),
		},
	}
	if err := sink.PersistBatch(ctx, overflow); err != nil {
		t.Fatalf("persist overflow: %v", err)
	}

	if got := userTimesSeen(t, ctx, pool, issue.id, "id", newValue); got != 0 {
		t.Fatalf("new identity past the cap times_seen = %d, want 0 (no row admitted)", got)
	}
	if got := userTimesSeen(t, ctx, pool, issue.id, "id", existingValue); got != 2 {
		t.Fatalf("existing identity times_seen = %d, want 2 (1 seeded + 1 incoming)", got)
	}
	if got := countIssueUsers(t, ctx, pool, issue.id); got != identityCap {
		t.Fatalf("issue_users rows = %d, want unchanged at the cap %d", got, identityCap)
	}
	after := queryIssue(t, ctx, pool, projectID, fingerprint)
	if after.usersSeen != identityCap {
		t.Fatalf("users_seen = %d, want unchanged at the cap %d", after.usersSeen, identityCap)
	}
	var otherRows int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM issue_users WHERE user_value = '<other>'`).Scan(&otherRows); err != nil {
		t.Fatalf("count <other> rows: %v", err)
	}
	if otherRows != 0 {
		t.Fatalf("<other> rows = %d, want 0 — there is no such person", otherRows)
	}
}

// TestPersistBatchWithoutAUserIdentityLeavesIssueUsersUntouched pins that an
// event with no identity is inert to the distinct-user aggregate: it must
// still advance times_seen, and must never fabricate an issue_users row or
// move users_seen. This fails if userDeltaFor (or the writer) ever treats a
// nil identity as a value to store.
func TestPersistBatchWithoutAUserIdentityLeavesIssueUsersUntouched(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64("distinct-users-nil-identity")
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)

	batch := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "no user", Level: "error",
			Count: 1, FirstSeen: base, LastSeen: base,
		}},
		Events: []storage.EventRow{newEventRow(projectID, fingerprint, base)},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("persist batch: %v", err)
	}

	issue := queryIssue(t, ctx, pool, projectID, fingerprint)
	if issue.timesSeen != 1 {
		t.Fatalf("times_seen = %d, want 1", issue.timesSeen)
	}
	if issue.usersSeen != 0 {
		t.Fatalf("users_seen = %d, want 0", issue.usersSeen)
	}
	if got := countIssueUsers(t, ctx, pool, issue.id); got != 0 {
		t.Fatalf("issue_users rows = %d, want 0", got)
	}
}

// TestPersistBatchTreatsSameValueUnderDifferentKeysAsDistinctIdentities pins
// the primary key's second dimension: the same literal value arriving as an
// id and as an email must count as two people, not one, because the person
// who typed "42" as a username is not provably the account whose id is "42".
// This fails if the writer (or its ON CONFLICT target) ever keys only on
// (issue_id, user_value).
func TestPersistBatchTreatsSameValueUnderDifferentKeysAsDistinctIdentities(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64("distinct-users-key-is-a-dimension")
	base := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)

	batch := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "same value, two keys", Level: "error",
			Count: 2, FirstSeen: base, LastSeen: base,
			Users: []storage.UserDelta{
				{Key: "id", Value: "shared", Count: 1, FirstSeen: base, LastSeen: base},
				{Key: "email", Value: "shared", Count: 1, FirstSeen: base, LastSeen: base},
			},
		}},
		Events: []storage.EventRow{
			newEventRow(projectID, fingerprint, base),
			newEventRow(projectID, fingerprint, base),
		},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("persist batch: %v", err)
	}

	issue := queryIssue(t, ctx, pool, projectID, fingerprint)
	if issue.usersSeen != 2 {
		t.Fatalf("users_seen = %d, want 2 (same value under a different key is a different identity)", issue.usersSeen)
	}
	if got := countIssueUsers(t, ctx, pool, issue.id); got != 2 {
		t.Fatalf("issue_users rows = %d, want 2", got)
	}
	if got := userTimesSeen(t, ctx, pool, issue.id, "id", "shared"); got != 1 {
		t.Fatalf("id=shared times_seen = %d, want 1", got)
	}
	if got := userTimesSeen(t, ctx, pool, issue.id, "email", "shared"); got != 1 {
		t.Fatalf("email=shared times_seen = %d, want 1", got)
	}
}

func TestTagValuesCarryProjectID(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	seedIssue(t, ctx, pool, projectID, "alpha", map[string]string{"browser": "Chrome"})

	var stored int64
	err := pool.QueryRow(ctx, `
		SELECT project_id FROM issue_tag_values WHERE key = 'browser' LIMIT 1
	`).Scan(&stored)
	if err != nil {
		t.Fatalf("read tag project_id: %v", err)
	}
	if stored != projectID {
		t.Fatalf("project_id = %d, want %d", stored, projectID)
	}
}
