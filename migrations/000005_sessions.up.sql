CREATE TABLE sessions (
    id           bigserial PRIMARY KEY,
    user_id      bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE,
    created_at   timestamptz NOT NULL,
    expires_at   timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    CHECK (expires_at > created_at)
);

CREATE INDEX sessions_user_idx ON sessions (user_id, expires_at DESC);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at);
