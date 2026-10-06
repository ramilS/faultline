-- Backfills issue_tag_values with synthetic "environment" and "release" rows
-- derived from the events table's scalar columns. Before this migration,
-- issueDeltaFor only wrote user-supplied event tags, so the environment/
-- release issue filters and facet endpoints — which query issue_tag_values
-- exclusively — had nothing to match for events ingested before the pipeline
-- fix that accompanies this migration.
--
-- CAVEAT (retention): the events hypertable carries a 30-day retention policy
-- (see 000004_policies.up.sql), so this backfill can only recover facets for
-- events still within that window. Issues whose events have already aged out
-- keep incomplete environment/release facets until they receive new events
-- under the fixed pipeline.
--
-- CAVEAT (double counting): ON CONFLICT below is DO NOTHING, not the
-- accumulating DO UPDATE the batch writer uses for its own upserts (see
-- eventSink.upsertTagValues in internal/storage/postgres/events.go). That
-- writer's accumulation is safe because every call carries a fresh batch of
-- events that has never been counted before. This backfill instead re-reads
-- the *entire* events table, including rows the fixed pipeline may already
-- have written a tag for — e.g. during a rolling deploy where the new binary
-- is live and ingesting before this migration has run, or the migration runs
-- concurrently with it. Accumulating onto those rows would double-count
-- times_seen for the events they already cover. DO NOTHING accepts a
-- narrower, bounded risk instead: for any (issue_id, key, value) that already
-- exists when this INSERT runs, the existing row is left untouched and this
-- backfill's times_seen/first_seen/last_seen for that exact value are simply
-- dropped rather than merged in. That only ever loses the *historical* slice
-- of a count for a value that also received at least one post-fix event
-- before the backfill executed — the row's presence, which is all the
-- filters and facet endpoints actually key on, is unaffected either way.
-- This also makes the migration genuinely idempotent: re-running it can only
-- skip rows it already inserted, never inflate them.
--
-- CAVEAT (cardinality cap): the batch writer bounds distinct values per
-- (issue, key) to maxTagValuesPerKey (200 — see internal/storage/postgres/
-- events.go), collapsing overflow into the otherTagValue ("<other>") bucket
-- so a high-cardinality tag cannot grow issue_tag_values without bound. This
-- backfill enforces the same cap (the "200" and "<other>" literals below must
-- stay in sync with maxTagValuesPerKey/otherTagValue) by ranking each issue's
-- distinct values by event count and bucketing everything past the 200th
-- into "<other>". "environment" rarely has more than a handful of distinct
-- values so this is a formality there, but "release" on an
-- actively-deploying project can run to thousands, and an unbounded backfill
-- would defeat the very cap the live ingestion path enforces. Unlike the live
-- path — which keeps the first N values it ever sees and buckets later
-- arrivals — this backfill ranks by frequency and keeps each issue's 200
-- most-common values. That ordering difference is a deliberate,
-- backfill-only choice (a one-shot pass over history has no "arrival order"
-- worth preserving) and does not need to match the live path's behavior.

WITH environment_counts AS (
    SELECT project_id, issue_id, environment AS value,
           count(*) AS times_seen, min(timestamp) AS first_seen, max(timestamp) AS last_seen
    FROM events
    WHERE environment IS NOT NULL AND environment <> ''
    GROUP BY project_id, issue_id, environment
),
environment_ranked AS (
    SELECT *, row_number() OVER (
        PARTITION BY issue_id ORDER BY times_seen DESC, value
    ) AS value_rank
    FROM environment_counts
),
environment_bucketed AS (
    SELECT project_id, issue_id,
           CASE WHEN value_rank <= 200 THEN value ELSE '<other>' END AS value,
           times_seen, first_seen, last_seen
    FROM environment_ranked
)
INSERT INTO issue_tag_values (project_id, issue_id, key, value, times_seen, first_seen, last_seen)
SELECT project_id, issue_id, 'environment', value, sum(times_seen), min(first_seen), max(last_seen)
FROM environment_bucketed
GROUP BY project_id, issue_id, value
ON CONFLICT (issue_id, key, value) DO NOTHING;

WITH release_counts AS (
    SELECT project_id, issue_id, release AS value,
           count(*) AS times_seen, min(timestamp) AS first_seen, max(timestamp) AS last_seen
    FROM events
    WHERE release IS NOT NULL AND release <> ''
    GROUP BY project_id, issue_id, release
),
release_ranked AS (
    SELECT *, row_number() OVER (
        PARTITION BY issue_id ORDER BY times_seen DESC, value
    ) AS value_rank
    FROM release_counts
),
release_bucketed AS (
    SELECT project_id, issue_id,
           CASE WHEN value_rank <= 200 THEN value ELSE '<other>' END AS value,
           times_seen, first_seen, last_seen
    FROM release_ranked
)
INSERT INTO issue_tag_values (project_id, issue_id, key, value, times_seen, first_seen, last_seen)
SELECT project_id, issue_id, 'release', value, sum(times_seen), min(first_seen), max(last_seen)
FROM release_bucketed
GROUP BY project_id, issue_id, value
ON CONFLICT (issue_id, key, value) DO NOTHING;
