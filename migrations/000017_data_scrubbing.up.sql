-- Server-side data scrubbing (internal/scrub). On by default for every
-- project, existing ones included: events ingested after this migration have
-- secrets redacted from events.payload and events.message. Events stored
-- earlier are not rewritten.
ALTER TABLE projects ADD COLUMN scrub_data boolean NOT NULL DEFAULT true;
ALTER TABLE projects ADD COLUMN scrub_extra_fields text[] NOT NULL DEFAULT '{}';
ALTER TABLE projects ADD COLUMN scrub_safe_fields text[] NOT NULL DEFAULT '{}';
