-- Restoring NOT NULL is impossible while passwordless accounts exist, and
-- there is no password to invent for them. Reverting the feature therefore
-- removes the accounts the feature created.
--
-- Every other reference to those accounts goes away on its own:
-- user_identities is dropped outright below, and sessions.user_id
-- (000005) is declared ON DELETE CASCADE. issues.assignee_id (000002) is
-- the exception — it is declared with no ON DELETE clause at all, so the
-- DELETE below would raise a foreign-key violation, abort the rest of this
-- file, and leave schema_migrations at "15 dirty" needing a manual force.
-- Clearing the assignments first is what keeps the rollback applicable.
DROP INDEX IF EXISTS user_identities_user_idx;
DROP TABLE IF EXISTS user_identities;

UPDATE issues SET assignee_id = NULL WHERE assignee_id IN (SELECT id FROM users WHERE password_hash IS NULL);

DELETE FROM users WHERE password_hash IS NULL;

ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
