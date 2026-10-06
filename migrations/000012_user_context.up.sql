CREATE TABLE issue_users (
    issue_id   bigint NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    user_key   text NOT NULL CHECK (user_key IN ('id', 'email', 'username', 'ip')),
    user_value text NOT NULL,
    times_seen bigint NOT NULL DEFAULT 0,
    first_seen timestamptz NOT NULL,
    last_seen  timestamptz NOT NULL,
    PRIMARY KEY (issue_id, user_key, user_value)
);

-- No separate index on issue_id: the primary key (issue_id, user_key,
-- user_value) already leads with it, so the aggregator's per-issue row count
-- and every `WHERE issue_id = $1` read use the PK index. An extra index would
-- add write cost on the ingest hot path for no read benefit.

ALTER TABLE issues ADD COLUMN users_seen bigint NOT NULL DEFAULT 0;

-- Storing an end user's IP address is a jurisdiction-dependent decision, so it
-- is off until a project opts in. The toggle only keeps ip out of the
-- issue_users aggregate (internal/ingest/dispatch.go's gateIPIdentity); a raw
-- event that carries user.ip_address still has it in events.payload
-- regardless of this setting.
ALTER TABLE projects ADD COLUMN store_user_ip boolean NOT NULL DEFAULT false;
