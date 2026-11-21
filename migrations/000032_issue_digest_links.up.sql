CREATE TABLE issue_digest_links (
    id bigserial PRIMARY KEY,
    issue_id bigint NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    created_by bigint REFERENCES users (id) ON DELETE SET NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz
);

CREATE INDEX issue_digest_links_issue_id_idx ON issue_digest_links (issue_id);
