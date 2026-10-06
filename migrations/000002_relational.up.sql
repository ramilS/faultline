CREATE TABLE users (
    id            bigserial PRIMARY KEY,
    email         text NOT NULL UNIQUE,
    name          text NOT NULL,
    password_hash text NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE projects (
    id         bigserial PRIMARY KEY,
    slug       text NOT NULL UNIQUE,
    name       text NOT NULL,
    platform   text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE project_keys (
    id               bigserial PRIMARY KEY,
    project_id       bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    public_key       text NOT NULL UNIQUE,
    rate_limit_rps   int NOT NULL DEFAULT 200,
    rate_limit_burst int NOT NULL DEFAULT 1000,
    revoked_at       timestamptz
);

CREATE TYPE issue_status AS ENUM ('unresolved', 'resolved', 'ignored');

CREATE TABLE issues (
    id                  bigserial PRIMARY KEY,
    project_id          bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    fingerprint         char(64) NOT NULL,
    title               text NOT NULL,
    culprit             text NOT NULL DEFAULT '',
    level               text NOT NULL DEFAULT 'error',
    status              issue_status NOT NULL DEFAULT 'unresolved',
    assignee_id         bigint REFERENCES users(id),
    first_seen          timestamptz NOT NULL,
    last_seen           timestamptz NOT NULL,
    times_seen          bigint NOT NULL DEFAULT 0,
    resolved_in_release text,
    UNIQUE (project_id, fingerprint)
);

CREATE INDEX issues_list_idx ON issues (project_id, status, last_seen DESC);

CREATE TABLE issue_tag_values (
    issue_id   bigint NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    key        text NOT NULL,
    value      text NOT NULL,
    times_seen bigint NOT NULL DEFAULT 0,
    first_seen timestamptz NOT NULL,
    last_seen  timestamptz NOT NULL,
    PRIMARY KEY (issue_id, key, value)
);

CREATE INDEX itv_lookup_idx ON issue_tag_values (key, value, issue_id);

CREATE TABLE releases (
    id         bigserial PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    version    text NOT NULL,
    first_seen timestamptz NOT NULL DEFAULT now(),
    UNIQUE (project_id, version)
);

CREATE TABLE grouping_rules (
    id         bigserial PRIMARY KEY,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    position   int NOT NULL,
    matcher    text NOT NULL,
    template   text NOT NULL,
    UNIQUE (project_id, position)
);
