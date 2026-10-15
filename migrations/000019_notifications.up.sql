-- Issue notifications (internal/notify). A channel is one destination a
-- project's new and regressed issues are sent to; target is a Telegram chat id
-- or a webhook URL, and secret the bot token or the webhook signing key. The
-- secret is never returned by the API, only whether one is set.
CREATE TABLE notification_channels (
    id                bigserial PRIMARY KEY,
    project_id        bigint NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name              text NOT NULL,
    kind              text NOT NULL CHECK (kind IN ('telegram', 'webhook')),
    target            text NOT NULL,
    secret            text NOT NULL DEFAULT '',
    enabled           boolean NOT NULL DEFAULT true,
    notify_new        boolean NOT NULL DEFAULT true,
    notify_regression boolean NOT NULL DEFAULT true,
    -- Empty means every environment.
    environments      text[] NOT NULL DEFAULT '{}',
    min_level         text NOT NULL DEFAULT 'debug' CHECK (min_level IN ('debug', 'info', 'warning', 'error', 'fatal')),
    last_success_at   timestamptz,
    last_error        text,
    last_error_at     timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notification_channels_project_idx ON notification_channels (project_id);

-- Pending deliveries only: a row is written in the same transaction that
-- created or reopened the issue, and deleted once it is delivered or has
-- exhausted its retries. next_attempt_at doubles as the claim lease.
CREATE TABLE notification_deliveries (
    id              bigserial PRIMARY KEY,
    channel_id      bigint NOT NULL REFERENCES notification_channels(id) ON DELETE CASCADE,
    issue_id        bigint NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    trigger         text NOT NULL CHECK (trigger IN ('new', 'regression')),
    environment     text NOT NULL DEFAULT '',
    attempts        int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX notification_deliveries_due_idx ON notification_deliveries (next_attempt_at);
