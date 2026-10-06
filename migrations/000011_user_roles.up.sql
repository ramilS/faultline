ALTER TABLE users ADD COLUMN role text NOT NULL DEFAULT 'member'
    CHECK (role IN ('owner', 'member'));

ALTER TABLE users ADD COLUMN deactivated_at timestamptz;

-- An upgraded deployment must retain someone who can administer users. The
-- account created by `faultline user create` during bootstrap is the lowest id,
-- and promoting it is the only choice that does not require asking a human
-- mid-migration.
UPDATE users
SET role = 'owner'
WHERE id = (SELECT min(id) FROM users);

CREATE INDEX users_active_idx ON users (email) WHERE deactivated_at IS NULL;
