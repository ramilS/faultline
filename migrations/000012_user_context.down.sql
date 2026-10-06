--
-- WARNING: this rollback is destructive and not reversible.
--
-- Dropping issue_users discards every distinct-user count ever aggregated;
-- re-applying the migration starts from zero, and the counts cannot be
-- rebuilt, because events store the raw payload but the aggregation that
-- turned payloads into per-issue identities is not replayed on migrate up.
-- Dropping projects.store_user_ip also forgets which projects had opted in to
-- aggregating IP addresses: after a down-then-up cycle every project is back to
-- the default of false, which is the safe direction, but a project that had
-- opted in will silently stop aggregating IP identities until someone
-- re-enables it.
DROP TABLE IF EXISTS issue_users;
ALTER TABLE issues DROP COLUMN IF EXISTS users_seen;
ALTER TABLE projects DROP COLUMN IF EXISTS store_user_ip;
