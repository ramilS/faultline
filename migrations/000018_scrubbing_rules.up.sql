-- Custom scrubbing rules (internal/scrub): per project, and one instance-wide
-- list that applies to every project, including those with scrubbing off.
ALTER TABLE projects ADD COLUMN scrub_rules jsonb NOT NULL DEFAULT '[]';

CREATE TABLE instance_scrubbing (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    rules     jsonb NOT NULL DEFAULT '[]'
);
INSERT INTO instance_scrubbing (singleton) VALUES (true);
