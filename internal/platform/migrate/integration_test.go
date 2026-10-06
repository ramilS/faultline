//go:build integration

package migration_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	gomigrate "github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	migration "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/testdatabase"
	"github.com/ramilS/faultline/migrations"
)

func TestMigrate(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if err := migration.Run(databaseURL, migration.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if err := migration.Run(databaseURL, migration.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("idempotent migrate up: %v", err)
	}
	assertVersion(t, databaseURL, "17 clean\n")

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer pool.Close()

	t.Run("relational schema", func(t *testing.T) {
		var count int
		err := pool.QueryRow(ctx, `
			SELECT count(*)
			FROM pg_catalog.pg_tables
			WHERE schemaname = 'public'
			  AND tablename = ANY($1)
		`, []string{"users", "projects", "project_keys", "issues", "issue_tag_values", "issue_users", "releases", "grouping_rules", "events", "spans", "sessions", "artifact_tokens", "source_map_blobs", "source_map_refs", "upload_chunks", "groups", "group_members", "group_projects"}).Scan(&count)
		if err != nil || count != 18 {
			t.Fatalf("table count = %d, error = %v", count, err)
		}

		var enumValues string
		err = pool.QueryRow(ctx, `
			SELECT string_agg(e.enumlabel, ',' ORDER BY e.enumsortorder)
			FROM pg_enum e JOIN pg_type t ON t.oid = e.enumtypid
			WHERE t.typname = 'issue_status'
		`).Scan(&enumValues)
		if err != nil || enumValues != "unresolved,resolved,ignored" {
			t.Fatalf("issue_status = %q, error = %v", enumValues, err)
		}

		for _, index := range []string{"issues_list_idx", "itv_lookup_idx", "events_issue_idx", "events_request_idx", "spans_request_idx"} {
			var exists bool
			if err := pool.QueryRow(ctx, `SELECT to_regclass('public.' || $1) IS NOT NULL`, index).Scan(&exists); err != nil || !exists {
				t.Fatalf("index %s exists = %v, error = %v", index, exists, err)
			}
		}

		var mergedIntoDefinition string
		err = pool.QueryRow(ctx, `
			SELECT indexdef FROM pg_catalog.pg_indexes
			WHERE schemaname = 'public' AND indexname = 'issues_merged_into_idx'
		`).Scan(&mergedIntoDefinition)
		if err != nil || !strings.Contains(mergedIntoDefinition, "merged_into_id") || !strings.Contains(mergedIntoDefinition, "WHERE (merged_into_id IS NOT NULL)") {
			t.Fatalf("issues_merged_into_idx = %q, error = %v", mergedIntoDefinition, err)
		}

		var mergedIntoType string
		err = pool.QueryRow(ctx, `
			SELECT pg_catalog.format_type(a.atttypid, a.atttypmod)
			FROM pg_catalog.pg_attribute a
			WHERE a.attrelid = 'issues'::regclass AND a.attname = 'merged_into_id'
		`).Scan(&mergedIntoType)
		if err != nil || mergedIntoType != "bigint" {
			t.Fatalf("issues.merged_into_id type = %q, error = %v", mergedIntoType, err)
		}

		var indexCount int
		if err := pool.QueryRow(ctx, `
			SELECT count(*) FROM pg_indexes
			WHERE tablename = 'source_map_refs'
			  AND indexname IN ('source_map_refs_release_idx', 'source_map_refs_debug_idx')
		`).Scan(&indexCount); err != nil || indexCount != 2 {
			t.Fatalf("source_map_refs partial indexes = %d, error = %v", indexCount, err)
		}

		// The XOR CHECK is the invariant debug-id addressing depends on:
		// counting indexes only proves the lookup path exists; it says
		// nothing about whether the table still rejects an unaddressed or
		// doubly-addressed ref. Assert both degenerate shapes actually fail.
		var xorProjectID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO projects (slug, name, platform) VALUES ('source-map-refs-xor', 'XOR', 'other') RETURNING id
		`).Scan(&xorProjectID); err != nil {
			t.Fatalf("insert xor check project: %v", err)
		}
		var xorBlobID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO source_map_blobs (project_id, content_sha256, compressed_map, uncompressed_bytes)
			VALUES ($1, decode('aa', 'hex'), decode('bb', 'hex'), 1) RETURNING id
		`, xorProjectID).Scan(&xorBlobID); err != nil {
			t.Fatalf("insert xor check blob: %v", err)
		}

		_, err = pool.Exec(ctx, `
			INSERT INTO source_map_refs (blob_id, project_id) VALUES ($1, $2)
		`, xorBlobID, xorProjectID)
		assertCheckViolation(t, "ref with neither address", err)

		var xorReleaseID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO releases (project_id, version) VALUES ($1, 'xor-check@1') RETURNING id
		`, xorProjectID).Scan(&xorReleaseID); err != nil {
			t.Fatalf("insert xor check release: %v", err)
		}
		_, err = pool.Exec(ctx, `
			INSERT INTO source_map_refs (blob_id, project_id, release_id, generated_path, debug_id)
			VALUES ($1, $2, $3, '/assets/app.js', gen_random_uuid())
		`, xorBlobID, xorProjectID, xorReleaseID)
		assertCheckViolation(t, "ref with both addresses", err)
	})

	t.Run("session schema and cascade", func(t *testing.T) {
		var columns string
		err := pool.QueryRow(ctx, `
			SELECT string_agg(
				a.attname || ':' || pg_catalog.format_type(a.atttypid, a.atttypmod) || ':' || a.attnotnull,
				',' ORDER BY a.attnum
			)
			FROM pg_catalog.pg_attribute a
			WHERE a.attrelid = 'sessions'::regclass
			  AND a.attnum > 0
			  AND NOT a.attisdropped
		`).Scan(&columns)
		wantColumns := "id:bigint:true,user_id:bigint:true,token_hash:bytea:true,created_at:timestamp with time zone:true,expires_at:timestamp with time zone:true,last_seen_at:timestamp with time zone:true"
		if err != nil || columns != wantColumns {
			t.Fatalf("sessions columns = %q, error = %v", columns, err)
		}

		var checkDefinition string
		err = pool.QueryRow(ctx, `
			SELECT pg_get_constraintdef(oid)
			FROM pg_catalog.pg_constraint
			WHERE conrelid = 'sessions'::regclass AND contype = 'c'
		`).Scan(&checkDefinition)
		if err != nil || !strings.Contains(checkDefinition, "expires_at > created_at") {
			t.Fatalf("sessions check constraint = %q, error = %v", checkDefinition, err)
		}

		for name, fragments := range map[string][]string{
			"sessions_user_idx":   {"(user_id, expires_at DESC)"},
			"sessions_expiry_idx": {"(expires_at)"},
		} {
			var definition string
			err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_catalog.pg_indexes WHERE schemaname = 'public' AND indexname = $1`, name).Scan(&definition)
			if err != nil {
				t.Fatalf("find index %s: %v", name, err)
			}
			for _, fragment := range fragments {
				if !strings.Contains(definition, fragment) {
					t.Fatalf("index %s definition = %q, want fragment %q", name, definition, fragment)
				}
			}
		}

		var userID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, name, password_hash)
			VALUES ('session@example.com', 'Session User', 'password-hash')
			RETURNING id
		`).Scan(&userID); err != nil {
			t.Fatalf("insert session user: %v", err)
		}
		now := time.Now().UTC().Truncate(time.Second)
		tokenHash := bytes.Repeat([]byte{0xab}, 32)
		if _, err := pool.Exec(ctx, `
			INSERT INTO sessions (user_id, token_hash, created_at, expires_at, last_seen_at)
			VALUES ($1, $2, $3, $4, $3)
		`, userID, tokenHash, now, now.Add(7*24*time.Hour)); err != nil {
			t.Fatalf("insert session: %v", err)
		}

		var persistedUserID int64
		var persistedTokenHash []byte
		if err := pool.QueryRow(ctx, `SELECT user_id, token_hash FROM sessions WHERE user_id = $1`, userID).Scan(&persistedUserID, &persistedTokenHash); err != nil {
			t.Fatalf("read session: %v", err)
		}
		if persistedUserID != userID || !bytes.Equal(persistedTokenHash, tokenHash) {
			t.Fatalf("persisted session = user %d token %x", persistedUserID, persistedTokenHash)
		}

		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Fatalf("delete session user: %v", err)
		}
		var remaining int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, userID).Scan(&remaining); err != nil || remaining != 0 {
			t.Fatalf("sessions after user delete = %d, error = %v", remaining, err)
		}
	})

	t.Run("hypertables", func(t *testing.T) {
		for table, column := range map[string]string{"events": "received_at", "spans": "start_time"} {
			var gotColumn string
			var interval time.Duration
			err := pool.QueryRow(ctx, `
				SELECT column_name, time_interval
				FROM timescaledb_information.dimensions
				WHERE hypertable_schema = 'public' AND hypertable_name = $1
			`, table).Scan(&gotColumn, &interval)
			if err != nil || gotColumn != column || interval != 6*time.Hour {
				t.Fatalf("%s dimension = %s/%s, error = %v", table, gotColumn, interval, err)
			}
		}
	})

	t.Run("policies and continuous aggregate", func(t *testing.T) {
		assertColumnstore(t, pool, ctx, "events", "project_id,issue_id", "received_at", "DESC")
		assertColumnstore(t, pool, ctx, "spans", "project_id", "start_time", "")
		assertPolicyInterval(t, pool, ctx, "events", "policy_compression", "compress_after", 24*time.Hour)
		assertPolicyInterval(t, pool, ctx, "spans", "policy_compression", "compress_after", 24*time.Hour)
		assertPolicyInterval(t, pool, ctx, "events", "policy_retention", "drop_after", 30*24*time.Hour)
		assertPolicyInterval(t, pool, ctx, "spans", "policy_retention", "drop_after", 7*24*time.Hour)

		var materializedOnly bool
		err := pool.QueryRow(ctx, `
			SELECT materialized_only
			FROM timescaledb_information.continuous_aggregates
			WHERE view_schema = 'public' AND view_name = 'issue_counts_hourly'
		`).Scan(&materializedOnly)
		if err != nil || materializedOnly {
			t.Fatalf("continuous aggregate materialized_only = %v, error = %v", materializedOnly, err)
		}

		var schedule time.Duration
		err = pool.QueryRow(ctx, `
			SELECT schedule_interval
			FROM timescaledb_information.jobs
			WHERE proc_name = 'policy_refresh_continuous_aggregate'
			  AND hypertable_name = 'issue_counts_hourly'
		`).Scan(&schedule)
		if err != nil || schedule != 5*time.Minute {
			t.Fatalf("aggregate refresh schedule = %s, error = %v", schedule, err)
		}
		assertPolicyInterval(t, pool, ctx, "issue_counts_hourly", "policy_refresh_continuous_aggregate", "start_offset", 3*time.Hour)
		assertPolicyInterval(t, pool, ctx, "issue_counts_hourly", "policy_refresh_continuous_aggregate", "end_offset", 5*time.Minute)
	})

	t.Run("representative persistence", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		var projectID, issueID int64
		if err := pool.QueryRow(ctx, `INSERT INTO projects (slug, name, platform) VALUES ('billing', 'Billing', 'other') RETURNING id`).Scan(&projectID); err != nil {
			t.Fatalf("insert project: %v", err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO issues (project_id, fingerprint, title, first_seen, last_seen, times_seen)
			VALUES ($1, repeat('a', 64), 'failure', $2, $2, 1) RETURNING id
		`, projectID, now).Scan(&issueID); err != nil {
			t.Fatalf("insert issue: %v", err)
		}
		_, err := pool.Exec(ctx, `
			INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, request_id, message, payload)
			VALUES ($1, $2, '00000000-0000-4000-8000-000000000001', $3, $3, 'error', 'req-1', 'failure', '{"message":"failure"}')
		`, projectID, issueID, now)
		if err != nil {
			t.Fatalf("insert event: %v", err)
		}
		_, err = pool.Exec(ctx, `
			INSERT INTO spans (project_id, request_id, span_id, name, start_time, duration_ms)
			VALUES ($1, 'req-1', '0123456789abcdef', 'GET /billing', $2, 12.5)
		`, projectID, now)
		if err != nil {
			t.Fatalf("insert span: %v", err)
		}
		var count int64
		if err := pool.QueryRow(ctx, `SELECT n FROM issue_counts_hourly WHERE issue_id = $1`, issueID).Scan(&count); err != nil || count != 1 {
			t.Fatalf("hourly count = %d, error = %v", count, err)
		}
	})

	t.Run("session persistence and user delete cascade", func(t *testing.T) {
		now := time.Now().UTC().Truncate(time.Second)
		var userID, sessionID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, name, password_hash)
			VALUES ('session-admin@example.com', 'Session Admin', 'test-password-hash')
			RETURNING id
		`).Scan(&userID); err != nil {
			t.Fatalf("insert session user: %v", err)
		}
		if err := pool.QueryRow(ctx, `
			INSERT INTO sessions (user_id, token_hash, created_at, expires_at, last_seen_at)
			VALUES ($1, decode(repeat('ab', 32), 'hex'), $2::timestamptz, $2::timestamptz + INTERVAL '7 days', $2::timestamptz)
			RETURNING id
		`, userID, now).Scan(&sessionID); err != nil {
			t.Fatalf("insert session: %v", err)
		}

		var persistedUserID int64
		if err := pool.QueryRow(ctx, `SELECT user_id FROM sessions WHERE id = $1`, sessionID).Scan(&persistedUserID); err != nil || persistedUserID != userID {
			t.Fatalf("persisted session user ID = %d, error = %v", persistedUserID, err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
			t.Fatalf("delete session user: %v", err)
		}
		var count int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1`, sessionID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("cascaded session count = %d, error = %v", count, err)
		}
	})

	t.Run("rollback with columnstore chunks", func(t *testing.T) {
		var projectID, issueID int64
		if err := pool.QueryRow(ctx, `SELECT id FROM projects WHERE slug = 'billing'`).Scan(&projectID); err != nil {
			t.Fatalf("find project: %v", err)
		}
		if err := pool.QueryRow(ctx, `SELECT id FROM issues WHERE project_id = $1`, projectID).Scan(&issueID); err != nil {
			t.Fatalf("find issue: %v", err)
		}
		old := time.Now().UTC().Add(-48 * time.Hour)
		_, err := pool.Exec(ctx, `
			INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, message, payload)
			VALUES ($1, $2, '00000000-0000-4000-8000-000000000002', $3, $3, 'error', 'old failure', '{}')
		`, projectID, issueID, old)
		if err != nil {
			t.Fatalf("insert old event: %v", err)
		}
		_, err = pool.Exec(ctx, `
			INSERT INTO spans (project_id, request_id, span_id, name, start_time, duration_ms)
			VALUES ($1, 'req-old', 'fedcba9876543210', 'old span', $2, 2.5)
		`, projectID, old)
		if err != nil {
			t.Fatalf("insert old span: %v", err)
		}
		convertOldChunk(t, pool, ctx, "events")
		convertOldChunk(t, pool, ctx, "spans")
	})

	// Seeded last, immediately before the rollback loop: migration 000015's
	// down path deletes every passwordless account, and issues.assignee_id
	// (000002) references users(id) with no ON DELETE clause. Without the
	// UPDATE that clears those assignments first, the DELETE raises a
	// foreign-key violation, the file aborts part-applied, and
	// schema_migrations is left at "15 dirty". The rollback loop below is
	// what proves it does not.
	t.Run("passwordless account assigned an issue", func(t *testing.T) {
		var ssoUserID int64
		if err := pool.QueryRow(ctx, `
			INSERT INTO users (email, name, password_hash, role)
			VALUES ('sso-assignee@example.com', 'SSO Assignee', NULL, 'member')
			RETURNING id
		`).Scan(&ssoUserID); err != nil {
			t.Fatalf("insert passwordless user: %v", err)
		}
		if _, err := pool.Exec(ctx, `
			INSERT INTO user_identities (user_id, provider, subject)
			VALUES ($1, 'google', 'sub-rollback')
		`, ssoUserID); err != nil {
			t.Fatalf("insert identity: %v", err)
		}
		result, err := pool.Exec(ctx, `
			UPDATE issues SET assignee_id = $1
			WHERE id = (SELECT id FROM issues ORDER BY id LIMIT 1)
		`, ssoUserID)
		if err != nil {
			t.Fatalf("assign issue: %v", err)
		}
		if result.RowsAffected() != 1 {
			t.Fatalf("assigned %d issues, want 1 — the fixture must hold a real reference", result.RowsAffected())
		}
	})

	pool.Close()
	for range 17 {
		if err := migration.Run(databaseURL, migration.Down, &bytes.Buffer{}); err != nil {
			t.Fatalf("migrate down: %v", err)
		}
	}
	assertVersion(t, databaseURL, "0 clean\n")
	if err := migration.Run(databaseURL, migration.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up after rollback: %v", err)
	}
	assertVersion(t, databaseURL, "17 clean\n")
}

// assertCheckViolation fails the test unless err is a CHECK constraint
// violation (SQLSTATE 23514) — the failure mode a table's CHECK produces,
// as opposed to a NOT NULL, foreign key, or unique violation.
func assertCheckViolation(t *testing.T, label string, err error) {
	t.Helper()
	postgresError, ok := errors.AsType[*pgconn.PgError](err)
	if !ok || postgresError.Code != "23514" {
		t.Fatalf("%s: error = %v, want SQLSTATE 23514 (check_violation)", label, err)
	}
}

func convertOldChunk(t *testing.T, pool *pgxpool.Pool, ctx context.Context, table string) {
	t.Helper()
	var chunk string
	if err := pool.QueryRow(ctx, `SELECT show_chunks($1, older_than => now() - INTERVAL '24 hours')::text LIMIT 1`, table).Scan(&chunk); err != nil {
		t.Fatalf("find old %s chunk: %v", table, err)
	}
	if _, err := pool.Exec(ctx, `CALL convert_to_columnstore($1::regclass)`, chunk); err != nil {
		if postgresError, ok := errors.AsType[*pgconn.PgError](err); ok && postgresError.Code == "42710" {
			return
		}
		t.Fatalf("convert %s chunk %s to columnstore: %v", table, chunk, err)
	}
}

func assertColumnstore(t *testing.T, pool *pgxpool.Pool, ctx context.Context, table, wantSegment, wantOrderColumn, wantDirection string) {
	t.Helper()
	var segment, order string
	err := pool.QueryRow(ctx, `
		SELECT replace(replace(segmentby, '"', ''), ' ', ''), orderby
		FROM timescaledb_information.hypertable_columnstore_settings
		WHERE hypertable = $1::regclass
	`, table).Scan(&segment, &order)
	directionMatches := wantDirection == "" || strings.Contains(strings.ToUpper(order), wantDirection)
	if err != nil || segment != wantSegment || !strings.Contains(order, wantOrderColumn) || !directionMatches {
		t.Fatalf("%s columnstore = segment %q order %q, error = %v", table, segment, order, err)
	}
}

func assertPolicyInterval(t *testing.T, pool *pgxpool.Pool, ctx context.Context, table, procedure, key string, want time.Duration) {
	t.Helper()
	query := fmt.Sprintf(`
		SELECT (config->>'%s')::interval
		FROM timescaledb_information.jobs
		WHERE hypertable_schema = 'public' AND hypertable_name = $1 AND proc_name = $2
	`, key)
	var got time.Duration
	if err := pool.QueryRow(ctx, query, table, procedure).Scan(&got); err != nil || got != want {
		t.Fatalf("%s %s = %s, error = %v", table, procedure, got, err)
	}
}

// TestBackfillEnvironmentReleaseTagsCapsRankedByFrequencyAndIsIdempotent covers
// Finding 3: nothing exercised migration 000010's own SQL beyond an empty
// database. It pins the 200-value cap, the "<other>" bucket arithmetic, and
// idempotence — the three behaviours the reviewer had verified by hand.
//
// It deliberately does not assert parity with the live ingestion path's
// bucketing (which keeps first-arrival values, not top-frequency ones): that
// divergence is a documented, accepted choice of the backfill, not a bug.
func TestBackfillEnvironmentReleaseTagsCapsRankedByFrequencyAndIsIdempotent(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	sourceDriver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	databaseDriver, err := pgxmigrate.WithInstance(database, &pgxmigrate.Config{})
	if err != nil {
		t.Fatalf("initialize migration database: %v", err)
	}
	runner, err := gomigrate.NewWithInstance("iofs", sourceDriver, "pgx5", databaseDriver)
	if err != nil {
		t.Fatalf("initialize migrations: %v", err)
	}
	defer func() { _, _ = runner.Close() }()

	// Apply every migration up to (but not including) the backfill under test,
	// so the events seeded below land in a database that still has the
	// pre-fix behavior migration 000010 exists to repair.
	if err := runner.Steps(9); err != nil {
		t.Fatalf("migrate to version 9: %v", err)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer pool.Close()

	var projectID, issueID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('backfill', 'Backfill', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("insert project: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, first_seen, last_seen, times_seen)
		VALUES ($1, repeat('b', 64), 'boom', $2, $2, 1) RETURNING id
	`, projectID, now).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}

	// Seed 205 distinct releases whose event counts strictly decrease from 205
	// down to 1 (release index i gets 205-i events). Every count is distinct,
	// so ranking by times_seen DESC is unambiguous: the 200 highest-count
	// releases (indices 0..199) are kept as-is and the 5 lowest-count ones
	// (indices 200..204, counts 5,4,3,2,1) must collapse into "<other>".
	const totalReleases = 205
	const maxKeptValues = 200
	base := now.Add(-24 * time.Hour)
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, environment, release, message, payload)
		SELECT
			$1, $2, gen_random_uuid(),
			$3::timestamptz + (release_index::text || ' hours')::interval + (occurrence::text || ' minutes')::interval,
			$3::timestamptz + (release_index::text || ' hours')::interval + (occurrence::text || ' minutes')::interval,
			'error', 'production',
			'release-' || lpad(release_index::text, 3, '0'),
			'boom', '{}'
		FROM generate_series(0, $4::int - 1) AS release_index
		CROSS JOIN LATERAL generate_series(1, $4::int - release_index) AS occurrence
	`, projectID, issueID, base, totalReleases); err != nil {
		t.Fatalf("seed release events: %v", err)
	}

	if err := runner.Steps(1); err != nil {
		t.Fatalf("migrate to version 10 (run backfill): %v", err)
	}

	// The 5 absorbed releases are indices 200..204 with counts 5,4,3,2,1: sum
	// 15, spanning from index 200's earliest event to index 204's only event.
	wantOtherTimesSeen := int64(5 + 4 + 3 + 2 + 1)
	wantOtherFirstSeen := base.Add(200*time.Hour + time.Minute)
	wantOtherLastSeen := base.Add(204*time.Hour + time.Minute)

	assertReleaseBackfill(t, ctx, pool, issueID, maxKeptValues+1, wantOtherTimesSeen, wantOtherFirstSeen, wantOtherLastSeen)

	// Re-running the backfill's raw SQL a second time (the same mechanism
	// golang-migrate used to apply it, via database/sql on the pgx driver, so
	// multi-statement files execute the same way) must be a true no-op: the
	// ON CONFLICT DO NOTHING upsert leaves every existing row untouched.
	content, err := migrations.FS.ReadFile("000010_backfill_environment_release_tags.up.sql")
	if err != nil {
		t.Fatalf("read backfill migration: %v", err)
	}
	if _, err := database.ExecContext(ctx, string(content)); err != nil {
		t.Fatalf("re-run backfill SQL: %v", err)
	}

	assertReleaseBackfill(t, ctx, pool, issueID, maxKeptValues+1, wantOtherTimesSeen, wantOtherFirstSeen, wantOtherLastSeen)
}

func assertReleaseBackfill(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64, wantRows int, wantOtherTimesSeen int64, wantOtherFirstSeen, wantOtherLastSeen time.Time) {
	t.Helper()

	var rowCount int
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FROM issue_tag_values WHERE issue_id = $1 AND key = 'release'
	`, issueID).Scan(&rowCount); err != nil {
		t.Fatalf("count release rows: %v", err)
	}
	if rowCount != wantRows {
		t.Fatalf("release rows = %d, want %d (200 kept values + \"<other>\")", rowCount, wantRows)
	}

	var otherTimesSeen int64
	var otherFirstSeen, otherLastSeen time.Time
	if err := pool.QueryRow(ctx, `
		SELECT times_seen, first_seen, last_seen
		FROM issue_tag_values
		WHERE issue_id = $1 AND key = 'release' AND value = '<other>'
	`, issueID).Scan(&otherTimesSeen, &otherFirstSeen, &otherLastSeen); err != nil {
		t.Fatalf("read <other> release row: %v", err)
	}
	if otherTimesSeen != wantOtherTimesSeen {
		t.Fatalf("<other> times_seen = %d, want %d (true sum of absorbed values)", otherTimesSeen, wantOtherTimesSeen)
	}
	if !otherFirstSeen.UTC().Equal(wantOtherFirstSeen) {
		t.Fatalf("<other> first_seen = %v, want %v (span of absorbed set)", otherFirstSeen.UTC(), wantOtherFirstSeen)
	}
	if !otherLastSeen.UTC().Equal(wantOtherLastSeen) {
		t.Fatalf("<other> last_seen = %v, want %v (span of absorbed set)", otherLastSeen.UTC(), wantOtherLastSeen)
	}
}

func TestMigrateUpPromotesFirstUserToOwner(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	sourceDriver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		t.Fatalf("open embedded migrations: %v", err)
	}
	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open migration database: %v", err)
	}
	databaseDriver, err := pgxmigrate.WithInstance(database, &pgxmigrate.Config{})
	if err != nil {
		t.Fatalf("initialize migration database: %v", err)
	}
	runner, err := gomigrate.NewWithInstance("iofs", sourceDriver, "pgx5", databaseDriver)
	if err != nil {
		t.Fatalf("initialize migrations: %v", err)
	}
	defer func() { _, _ = runner.Close() }()

	// Migrate to version 10, then seed three users before stepping to version 11.
	// Seeding before the migration is the point: promotion must work on an
	// existing database, not only on an empty one.
	if err := runner.Steps(10); err != nil {
		t.Fatalf("migrate to version 10: %v", err)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer pool.Close()

	// Seed three users in order, so the first one (with lowest id) is promoted to owner.
	for _, email := range []string{"first@example.com", "second@example.com", "third@example.com"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO users (email, name, password_hash) VALUES ($1, 'Seeded', 'x')
		`, email); err != nil {
			t.Fatalf("seed %s: %v", email, err)
		}
	}

	// Now step to version 11, which should run the promotion.
	if err := runner.Steps(1); err != nil {
		t.Fatalf("migrate to version 11: %v", err)
	}

	// Verify the first user is promoted to owner.
	var owners []string
	rows, err := pool.Query(ctx, `SELECT email FROM users WHERE role = 'owner' ORDER BY id`)
	if err != nil {
		t.Fatalf("query owners: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			t.Fatalf("scan: %v", err)
		}
		owners = append(owners, email)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}

	if len(owners) != 1 || owners[0] != "first@example.com" {
		t.Fatalf("owners = %v, want exactly [first@example.com]", owners)
	}
}

func TestMigrateUpAddsUserContextSchema(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()

	var projectID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ('p', 'P', 'other') RETURNING id
	`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	// The default must be false: a project that never opted in must not
	// aggregate IP identities.
	var storeUserIP bool
	if err := pool.QueryRow(ctx, `SELECT store_user_ip FROM projects WHERE id = $1`, projectID).Scan(&storeUserIP); err != nil {
		t.Fatalf("read store_user_ip: %v", err)
	}
	if storeUserIP {
		t.Fatal("store_user_ip = true, want false by default")
	}

	// user_key is constrained to the four precedence values; a fifth must fail.
	var issueID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, first_seen, last_seen)
		VALUES ($1, repeat('a', 64), 'Boom', now(), now()) RETURNING id
	`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("seed issue: %v", err)
	}

	var usersSeen int64
	if err := pool.QueryRow(ctx, `SELECT users_seen FROM issues WHERE id = $1`, issueID).Scan(&usersSeen); err != nil {
		t.Fatalf("read users_seen: %v", err)
	}
	if usersSeen != 0 {
		t.Fatalf("users_seen = %d, want 0 by default", usersSeen)
	}

	_, err := pool.Exec(ctx, `
		INSERT INTO issue_users (issue_id, user_key, user_value, first_seen, last_seen)
		VALUES ($1, 'nickname', 'nope', now(), now())
	`, issueID)
	if err == nil {
		t.Fatal("inserting user_key 'nickname' succeeded, want a CHECK violation")
	}

	// And the four allowed keys must all be accepted.
	for _, key := range []string{"id", "email", "username", "ip"} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO issue_users (issue_id, user_key, user_value, times_seen, first_seen, last_seen)
			VALUES ($1, $2, 'someone', 1, now(), now())
		`, issueID, key); err != nil {
			t.Fatalf("insert user_key %q: %v", key, err)
		}
	}
}

func assertVersion(t *testing.T, databaseURL, want string) {
	t.Helper()
	var output bytes.Buffer
	if err := migration.Run(databaseURL, migration.Version, &output); err != nil {
		t.Fatalf("migration version: %v", err)
	}
	if output.String() != want {
		t.Fatalf("migration version = %q, want %q", output.String(), want)
	}
}
