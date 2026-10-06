-- WARNING: this is a destructive rollback, not a reversible one.
--
-- Dropping `role` and `deactivated_at` throws away who was an owner and who
-- was deactivated. If this migration is ever re-applied afterward (a
-- down-then-up cycle), the up migration recreates both columns from
-- scratch: `deactivated_at` defaults to NULL, so every account that was
-- deactivated comes back active — including a departed employee's, with
-- their known password regaining a live login — and `role` defaults to
-- 'member' except for the single lowest-id account, which the up migration
-- re-promotes to 'owner', so every other owner is silently demoted to
-- member. None of this is recoverable from the database alone: the
-- pre-rollback role and deactivation state is gone.
--
-- If you must roll back past this migration, re-audit every user's role and
-- deactivation status immediately afterward.
DROP INDEX IF EXISTS users_active_idx;
ALTER TABLE users DROP COLUMN IF EXISTS deactivated_at;
ALTER TABLE users DROP COLUMN IF EXISTS role;
