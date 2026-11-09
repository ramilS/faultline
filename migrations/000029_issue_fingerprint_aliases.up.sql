CREATE TABLE issue_fingerprint_aliases (
    project_id  bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fingerprint char(64) NOT NULL,
    issue_id    bigint NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, fingerprint)
);

CREATE INDEX issue_fingerprint_aliases_issue_idx ON issue_fingerprint_aliases(issue_id);
