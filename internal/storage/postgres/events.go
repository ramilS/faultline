package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/storage"
)

// maxTagValuesPerKey bounds the distinct values retained per (issue, key).
// Values beyond the cap collapse into otherTagValue so a high-cardinality tag
// cannot grow issue_tag_values without limit.
const maxTagValuesPerKey = 200

// otherTagValue is the bucket that absorbs tag values past maxTagValuesPerKey.
const otherTagValue = "<other>"

type eventSink struct {
	pool *pgxpool.Pool
}

var _ storage.EventSink = (*eventSink)(nil)

// NewEventSink returns a storage.EventSink that persists batches into the
// releases, issues, events, and issue_tag_values tables backed by pool.
func NewEventSink(pool *pgxpool.Pool) storage.EventSink {
	return &eventSink{pool: pool}
}

// PersistBatch writes one shard flush in a single transaction: it registers new
// releases, upserts issue counters (auto-reopening resolved issues), COPYs the
// event rows, COPYs the span rows, and accumulates tag facets. Callers must
// ensure each fingerprint appears at most once in Issues; ON CONFLICT DO UPDATE
// rejects a fingerprint that would be affected twice in the same statement.
func (sink *eventSink) PersistBatch(ctx context.Context, batch storage.Batch) error {
	if len(batch.Issues) == 0 && len(batch.Events) == 0 && len(batch.Spans) == 0 {
		return nil
	}

	tx, err := sink.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin persist batch: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := insertReleases(ctx, tx, batch); err != nil {
		return err
	}
	issueIDs, err := upsertIssues(ctx, tx, batch)
	if err != nil {
		return err
	}
	if err := copyEvents(ctx, tx, batch, issueIDs); err != nil {
		return err
	}
	if err := copySpans(ctx, tx, batch); err != nil {
		return err
	}
	if err := upsertTagValues(ctx, tx, batch, issueIDs); err != nil {
		return err
	}
	if err := upsertUserIdentities(ctx, tx, batch, issueIDs); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit persist batch: %w", err)
	}
	return nil
}

func insertReleases(ctx context.Context, tx pgx.Tx, batch storage.Batch) error {
	if len(batch.Releases) == 0 {
		return nil
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO releases (project_id, version)
		SELECT $1, version FROM unnest($2::text[]) AS version
		ON CONFLICT (project_id, version) DO NOTHING
	`, batch.ProjectID, batch.Releases)
	if err != nil {
		return fmt.Errorf("insert releases: %w", err)
	}
	return nil
}

// upsertIssues resolves each batch fingerprint to an issue id, folding in
// its counters. Ordinary fingerprints go through the normal
// INSERT ... ON CONFLICT below; fingerprints that belong to a tombstoned
// issue (a merge source) are redirected to the merge target instead — see
// resolveMergeTombstones. This is what keeps a merge-aliased signal from
// permanently reopening and re-counting into a tombstone the moment the
// grouping-rule cache lookup that would otherwise catch it is
// stale or never gets a chance to run (see grouping.EvaluateRules's doc
// comment on why a text-rule-derived fingerprint needs this structural
// backstop too).
func upsertIssues(ctx context.Context, tx pgx.Tx, batch storage.Batch) (map[string]int64, error) {
	issueIDs := make(map[string]int64, len(batch.Issues))
	if len(batch.Issues) == 0 {
		return issueIDs, nil
	}

	fingerprints := make([]string, len(batch.Issues))
	for index, issue := range batch.Issues {
		fingerprints[index] = issue.Fingerprint
	}
	tombstoneTargets, err := resolveMergeTombstones(ctx, tx, batch.ProjectID, fingerprints)
	if err != nil {
		return nil, err
	}

	fresh := make([]storage.IssueDelta, 0, len(batch.Issues))
	redirected := make(map[int64]storage.IssueDelta, len(tombstoneTargets))
	for _, issue := range batch.Issues {
		targetID, ok := tombstoneTargets[issue.Fingerprint]
		if !ok {
			fresh = append(fresh, issue)
			continue
		}
		issueIDs[issue.Fingerprint] = targetID
		if existing, ok := redirected[targetID]; ok {
			redirected[targetID] = storage.MergeIssueDelta(existing, issue)
		} else {
			redirected[targetID] = issue
		}
	}

	if len(redirected) > 0 {
		if err := applyTombstoneRedirectCounters(ctx, tx, redirected); err != nil {
			return nil, err
		}
	}
	if len(fresh) == 0 {
		return issueIDs, nil
	}

	count := len(fresh)
	insertFingerprints := make([]string, count)
	titles := make([]string, count)
	culprits := make([]string, count)
	levels := make([]string, count)
	firstSeen := make([]time.Time, count)
	lastSeen := make([]time.Time, count)
	timesSeen := make([]int64, count)
	for index, issue := range fresh {
		insertFingerprints[index] = issue.Fingerprint
		titles[index] = issue.Title
		culprits[index] = issue.Culprit
		levels[index] = issue.Level
		firstSeen[index] = issue.FirstSeen
		lastSeen[index] = issue.LastSeen
		timesSeen[index] = issue.Count
	}

	rows, err := tx.Query(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, culprit, level, first_seen, last_seen, times_seen)
		SELECT $1, fingerprint, title, culprit, level, first_seen, last_seen, times_seen
		FROM unnest($2::char(64)[], $3::text[], $4::text[], $5::text[], $6::timestamptz[], $7::timestamptz[], $8::bigint[])
			AS u(fingerprint, title, culprit, level, first_seen, last_seen, times_seen)
		ON CONFLICT (project_id, fingerprint) DO UPDATE
			SET last_seen  = GREATEST(issues.last_seen, EXCLUDED.last_seen),
			    times_seen = issues.times_seen + EXCLUDED.times_seen,
			    status     = CASE WHEN issues.status = 'resolved' THEN 'unresolved' ELSE issues.status END
		RETURNING id, fingerprint
	`, batch.ProjectID, insertFingerprints, titles, culprits, levels, firstSeen, lastSeen, timesSeen)
	if err != nil {
		return nil, fmt.Errorf("upsert issues: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			id          int64
			fingerprint string
		)
		if err := rows.Scan(&id, &fingerprint); err != nil {
			return nil, fmt.Errorf("scan upserted issue: %w", err)
		}
		issueIDs[fingerprint] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate upserted issues: %w", err)
	}
	return issueIDs, nil
}

// resolveMergeTombstones looks up, in one query, which of fingerprints
// currently belong to a tombstoned issue (merged_into_id set — i.e. a prior
// merge source) and returns fingerprint -> live target id for just those. A
// fingerprint absent from the result is either unseen or belongs to a live
// issue and goes through the normal upsert path. Merge's cascade repoint
// (issueRepository.MergeIssue) always flattens merged_into_id to point
// directly at the live head, so this single lookup never needs to chase a
// multi-hop chain.
func resolveMergeTombstones(ctx context.Context, tx pgx.Tx, projectID int64, fingerprints []string) (map[string]int64, error) {
	rows, err := tx.Query(ctx, `
		SELECT fingerprint, merged_into_id FROM issues
		WHERE project_id = $1 AND fingerprint = ANY($2) AND merged_into_id IS NOT NULL
	`, projectID, fingerprints)
	if err != nil {
		return nil, fmt.Errorf("resolve merge tombstones: %w", err)
	}
	defer rows.Close()

	targets := make(map[string]int64)
	for rows.Next() {
		var (
			fingerprint string
			targetID    int64
		)
		if err := rows.Scan(&fingerprint, &targetID); err != nil {
			return nil, fmt.Errorf("scan merge tombstone: %w", err)
		}
		targets[fingerprint] = targetID
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate merge tombstones: %w", err)
	}
	return targets, nil
}

// applyTombstoneRedirectCounters folds each redirected delta's counters
// directly into its live target issue by id — never through the
// (project_id, fingerprint) ON CONFLICT path, which would touch the
// tombstone row itself. One batched UPDATE regardless of how many
// fingerprints redirected to how many targets, so this stays cheap on the
// per-flush hot path.
func applyTombstoneRedirectCounters(ctx context.Context, tx pgx.Tx, redirected map[int64]storage.IssueDelta) error {
	ids := make([]int64, 0, len(redirected))
	timesSeen := make([]int64, 0, len(redirected))
	firstSeen := make([]time.Time, 0, len(redirected))
	lastSeen := make([]time.Time, 0, len(redirected))
	for targetID, delta := range redirected {
		ids = append(ids, targetID)
		timesSeen = append(timesSeen, delta.Count)
		firstSeen = append(firstSeen, delta.FirstSeen)
		lastSeen = append(lastSeen, delta.LastSeen)
	}
	_, err := tx.Exec(ctx, `
		UPDATE issues SET
			last_seen  = GREATEST(issues.last_seen, u.last_seen),
			first_seen = LEAST(issues.first_seen, u.first_seen),
			times_seen = issues.times_seen + u.times_seen,
			status     = CASE WHEN issues.status = 'resolved' THEN 'unresolved' ELSE issues.status END
		FROM unnest($1::bigint[], $2::bigint[], $3::timestamptz[], $4::timestamptz[]) AS u(id, times_seen, first_seen, last_seen)
		WHERE issues.id = u.id
	`, ids, timesSeen, firstSeen, lastSeen)
	if err != nil {
		return fmt.Errorf("apply tombstone redirect counters: %w", err)
	}
	return nil
}

func copyEvents(ctx context.Context, tx pgx.Tx, batch storage.Batch, issueIDs map[string]int64) error {
	if len(batch.Events) == 0 {
		return nil
	}
	columns := []string{
		"project_id", "issue_id", "event_id", "received_at", "timestamp",
		"level", "environment", "release", "request_id", "message", "payload",
	}
	rows := make([][]any, len(batch.Events))
	for index, event := range batch.Events {
		issueID, ok := issueIDs[event.Fingerprint]
		if !ok {
			return fmt.Errorf("event %s references unknown fingerprint %s", event.EventID, event.Fingerprint)
		}
		rows[index] = []any{
			event.ProjectID,
			issueID,
			pgtype.UUID{Bytes: [16]byte(event.EventID), Valid: true},
			event.ReceivedAt,
			event.Timestamp,
			event.Level,
			event.Environment,
			event.Release,
			event.RequestID,
			event.Message,
			event.Payload,
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"events"}, columns, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("copy events: %w", err)
	}
	return nil
}

func copySpans(ctx context.Context, tx pgx.Tx, batch storage.Batch) error {
	if len(batch.Spans) == 0 {
		return nil
	}
	columns := []string{
		"project_id", "request_id", "span_id", "parent_span_id",
		"name", "start_time", "duration_ms", "status", "attributes",
	}
	rows := make([][]any, len(batch.Spans))
	for index, span := range batch.Spans {
		attributes := span.Attributes
		if len(attributes) == 0 {
			attributes = json.RawMessage("{}")
		}
		rows[index] = []any{
			span.ProjectID,
			span.RequestID,
			span.SpanID,
			span.ParentSpanID,
			span.Name,
			span.StartTime,
			span.DurationMS,
			span.Status,
			attributes,
		}
	}
	if _, err := tx.CopyFrom(ctx, pgx.Identifier{"spans"}, columns, pgx.CopyFromRows(rows)); err != nil {
		return fmt.Errorf("copy spans: %w", err)
	}
	return nil
}

func upsertTagValues(ctx context.Context, tx pgx.Tx, batch storage.Batch, issueIDs map[string]int64) error {
	var (
		issueColumn []int64
		keys        []string
		values      []string
		counts      []int64
		firstSeen   []time.Time
		lastSeen    []time.Time
	)
	for _, issue := range batch.Issues {
		issueID, ok := issueIDs[issue.Fingerprint]
		if !ok {
			return fmt.Errorf("tag facet references unknown fingerprint %s", issue.Fingerprint)
		}
		for _, tag := range issue.Tags {
			issueColumn = append(issueColumn, issueID)
			keys = append(keys, tag.Key)
			values = append(values, tag.Value)
			counts = append(counts, tag.Count)
			firstSeen = append(firstSeen, tag.FirstSeen)
			lastSeen = append(lastSeen, tag.LastSeen)
		}
	}
	if len(issueColumn) == 0 {
		return nil
	}

	// resolved routes each incoming value to itself when it already exists or
	// the key is still under the cap, and to otherTagValue once the cap is hit.
	// Every subquery reads the pre-statement snapshot, so a single flush can
	// overshoot the cap slightly; the guard only bounds unbounded growth.
	_, err := tx.Exec(ctx, `
		WITH incoming AS (
			SELECT issue_id, key, value, times_seen, first_seen, last_seen
			FROM unnest($1::bigint[], $2::text[], $3::text[], $4::bigint[], $5::timestamptz[], $6::timestamptz[])
				AS u(issue_id, key, value, times_seen, first_seen, last_seen)
		),
		resolved AS (
			SELECT issue_id, key,
				CASE
					WHEN EXISTS (
						SELECT 1 FROM issue_tag_values existing
						WHERE existing.issue_id = incoming.issue_id
						  AND existing.key = incoming.key
						  AND existing.value = incoming.value
					) THEN value
					WHEN (
						SELECT count(*) FROM issue_tag_values existing
						WHERE existing.issue_id = incoming.issue_id
						  AND existing.key = incoming.key
					) >= $7 THEN $8
					ELSE value
				END AS value,
				times_seen, first_seen, last_seen
			FROM incoming
		)
		INSERT INTO issue_tag_values (project_id, issue_id, key, value, times_seen, first_seen, last_seen)
		SELECT $9, issue_id, key, value, sum(times_seen), min(first_seen), max(last_seen)
		FROM resolved
		GROUP BY issue_id, key, value
		ON CONFLICT (issue_id, key, value) DO UPDATE
			SET times_seen = issue_tag_values.times_seen + EXCLUDED.times_seen,
			    first_seen = LEAST(issue_tag_values.first_seen, EXCLUDED.first_seen),
			    last_seen  = GREATEST(issue_tag_values.last_seen, EXCLUDED.last_seen)
	`, issueColumn, keys, values, counts, firstSeen, lastSeen, maxTagValuesPerKey, otherTagValue, batch.ProjectID)
	if err != nil {
		return fmt.Errorf("upsert tag values: %w", err)
	}
	return nil
}

// upsertUserIdentities writes each issue's distinct-user contributions into
// issue_users and refreshes issues.users_seen for every issue touched by this
// flush, all inside the caller's transaction — so a crash between the row
// write and the counter refresh is impossible and users_seen can never
// disagree with a fresh count(*) over the rows that produced it.
//
// This mirrors upsertTagValues's shape with one deliberate difference: past
// ingest.MaxUsersPerIssue there is no "<other>" bucket, because there is no
// such person. A (issue_id, user_key, user_value) already on file is always
// incremented; a value not yet on file is admitted only while the cap
// subquery — reading the pre-statement snapshot, like the tag-facet guard —
// finds the issue still under the cap. A value that misses admission is
// simply not inserted, so users_seen saturates into a floor rather than
// staying exact — an honest degradation rather than unbounded growth.
func upsertUserIdentities(ctx context.Context, tx pgx.Tx, batch storage.Batch, issueIDs map[string]int64) error {
	var (
		issueColumn []int64
		keys        []string
		values      []string
		counts      []int64
		firstSeen   []time.Time
		lastSeen    []time.Time
	)
	affectedIssues := make(map[int64]struct{})
	for _, issue := range batch.Issues {
		if len(issue.Users) == 0 {
			continue
		}
		issueID, ok := issueIDs[issue.Fingerprint]
		if !ok {
			return fmt.Errorf("user identity references unknown fingerprint %s", issue.Fingerprint)
		}
		for _, user := range issue.Users {
			issueColumn = append(issueColumn, issueID)
			keys = append(keys, user.Key)
			values = append(values, user.Value)
			counts = append(counts, user.Count)
			firstSeen = append(firstSeen, user.FirstSeen)
			lastSeen = append(lastSeen, user.LastSeen)
		}
		affectedIssues[issueID] = struct{}{}
	}
	if len(issueColumn) == 0 {
		return nil
	}

	_, err := tx.Exec(ctx, `
		WITH incoming AS (
			SELECT issue_id, key, value, times_seen, first_seen, last_seen
			FROM unnest($1::bigint[], $2::text[], $3::text[], $4::bigint[], $5::timestamptz[], $6::timestamptz[])
				AS u(issue_id, key, value, times_seen, first_seen, last_seen)
		),
		admitted AS (
			SELECT incoming.*
			FROM incoming
			WHERE EXISTS (
				SELECT 1 FROM issue_users existing
				WHERE existing.issue_id = incoming.issue_id
				  AND existing.user_key = incoming.key
				  AND existing.user_value = incoming.value
			)
			OR (
				SELECT count(*) FROM issue_users existing
				WHERE existing.issue_id = incoming.issue_id
			) < $7
		),
		grouped AS (
			SELECT issue_id, key, value,
				sum(times_seen) AS times_seen,
				min(first_seen) AS first_seen,
				max(last_seen)  AS last_seen
			FROM admitted
			GROUP BY issue_id, key, value
		)
		INSERT INTO issue_users (issue_id, user_key, user_value, times_seen, first_seen, last_seen)
		SELECT issue_id, key, value, times_seen, first_seen, last_seen
		FROM grouped
		ON CONFLICT (issue_id, user_key, user_value) DO UPDATE
			SET times_seen = issue_users.times_seen + EXCLUDED.times_seen,
			    first_seen = LEAST(issue_users.first_seen, EXCLUDED.first_seen),
			    last_seen  = GREATEST(issue_users.last_seen, EXCLUDED.last_seen)
	`, issueColumn, keys, values, counts, firstSeen, lastSeen, ingest.MaxUsersPerIssue)
	if err != nil {
		return fmt.Errorf("upsert user identities: %w", err)
	}

	affectedIDs := make([]int64, 0, len(affectedIssues))
	for issueID := range affectedIssues {
		affectedIDs = append(affectedIDs, issueID)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE issues SET users_seen = counted.n
		FROM (
			SELECT issue_id, count(*) AS n FROM issue_users
			WHERE issue_id = ANY($1::bigint[])
			GROUP BY issue_id
		) AS counted
		WHERE issues.id = counted.issue_id
	`, affectedIDs); err != nil {
		return fmt.Errorf("refresh users_seen: %w", err)
	}
	return nil
}
