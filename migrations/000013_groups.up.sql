CREATE TABLE groups (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL UNIQUE,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE group_members (
    group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    user_id  bigint NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);

CREATE TABLE group_projects (
    group_id   bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    project_id bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, project_id)
);

-- Serves the per-request scope lookup: every authenticated request by a
-- member resolves their reachable projects through this index.
CREATE INDEX group_members_user_idx ON group_members (user_id);

-- Serves the audit question "who can see this project", which is the first
-- thing asked after an incident and has no other index to answer it.
CREATE INDEX group_projects_project_idx ON group_projects (project_id);
