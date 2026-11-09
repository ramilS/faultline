CREATE TABLE issue_views (
    user_id   bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issue_id  bigint      NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    viewed_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, issue_id)
);

CREATE INDEX issue_views_issue_idx ON issue_views (issue_id);
