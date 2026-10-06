ALTER TABLE events SET (
    timescaledb.enable_columnstore = true,
    timescaledb.segmentby = 'project_id,issue_id',
    timescaledb.orderby = 'received_at DESC'
);

CALL add_columnstore_policy('events', after => INTERVAL '24 hours');
SELECT add_retention_policy('events', INTERVAL '30 days');

ALTER TABLE spans SET (
    timescaledb.enable_columnstore = true,
    timescaledb.segmentby = 'project_id',
    timescaledb.orderby = 'start_time'
);

CALL add_columnstore_policy('spans', after => INTERVAL '24 hours');
SELECT add_retention_policy('spans', INTERVAL '7 days');

CREATE MATERIALIZED VIEW issue_counts_hourly
WITH (timescaledb.continuous) AS
SELECT issue_id,
       time_bucket('1 hour', received_at) AS bucket,
       count(*) AS n
FROM events
GROUP BY issue_id, bucket
WITH NO DATA;

ALTER MATERIALIZED VIEW issue_counts_hourly SET (timescaledb.materialized_only = false);

SELECT add_continuous_aggregate_policy(
    'issue_counts_hourly',
    start_offset => INTERVAL '3 hours',
    end_offset => INTERVAL '5 minutes',
    schedule_interval => INTERVAL '5 minutes'
);
