SELECT remove_continuous_aggregate_policy('issue_counts_hourly', if_exists => true);
DROP MATERIALIZED VIEW IF EXISTS issue_counts_hourly;

SELECT remove_retention_policy('spans', if_exists => true);
CALL remove_columnstore_policy('spans', if_exists => true);
DO $block$
DECLARE
    chunk_name regclass;
BEGIN
    FOR chunk_name IN SELECT show_chunks('spans')
    LOOP
        CALL convert_to_rowstore(chunk_name);
    END LOOP;
END
$block$;
ALTER TABLE spans SET (timescaledb.enable_columnstore = false);

SELECT remove_retention_policy('events', if_exists => true);
CALL remove_columnstore_policy('events', if_exists => true);
DO $block$
DECLARE
    chunk_name regclass;
BEGIN
    FOR chunk_name IN SELECT show_chunks('events')
    LOOP
        CALL convert_to_rowstore(chunk_name);
    END LOOP;
END
$block$;
ALTER TABLE events SET (timescaledb.enable_columnstore = false);
