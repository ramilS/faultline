CREATE TABLE events (
    project_id  bigint NOT NULL,
    issue_id    bigint NOT NULL,
    event_id    uuid NOT NULL,
    received_at timestamptz NOT NULL,
    timestamp   timestamptz NOT NULL,
    level       text NOT NULL,
    environment text NOT NULL DEFAULT 'production',
    release     text,
    request_id  text,
    message     text NOT NULL,
    payload     jsonb NOT NULL
);

SELECT create_hypertable('events', 'received_at', chunk_time_interval => INTERVAL '6 hours');
CREATE INDEX events_issue_idx ON events (issue_id, received_at DESC);
CREATE INDEX events_request_idx ON events (project_id, request_id, received_at DESC)
    WHERE request_id IS NOT NULL;

CREATE TABLE spans (
    project_id     bigint NOT NULL,
    request_id     text NOT NULL,
    span_id        text NOT NULL,
    parent_span_id text,
    name           text NOT NULL,
    start_time     timestamptz NOT NULL,
    duration_ms    double precision NOT NULL,
    status         text NOT NULL DEFAULT 'ok',
    attributes     jsonb NOT NULL DEFAULT '{}'::jsonb
);

SELECT create_hypertable('spans', 'start_time', chunk_time_interval => INTERVAL '6 hours');
CREATE INDEX spans_request_idx ON spans (project_id, request_id, start_time);
