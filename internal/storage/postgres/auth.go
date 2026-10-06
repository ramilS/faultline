// Package postgres implements PostgreSQL-backed storage adapters.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/auth"
)

// AuthRepository groups the authentication adapters that share a PostgreSQL
// connection pool. Separate adapters are required because both domain
// interfaces define Create methods with different signatures.
type AuthRepository struct {
	Users      auth.UserRepository
	Sessions   auth.SessionRepository
	Identities auth.IdentityRepository
}

type userRepository struct {
	pool *pgxpool.Pool
}

type sessionRepository struct {
	pool *pgxpool.Pool
}

var (
	_ auth.UserRepository    = (*userRepository)(nil)
	_ auth.SessionRepository = (*sessionRepository)(nil)
)

// NewAuthRepository returns authentication adapters backed by pool.
func NewAuthRepository(pool *pgxpool.Pool) *AuthRepository {
	return &AuthRepository{
		Users:      &userRepository{pool: pool},
		Sessions:   &sessionRepository{pool: pool},
		Identities: &identityRepository{pool: pool},
	}
}

func (repository *userRepository) Create(ctx context.Context, input auth.NewUser) (auth.User, error) {
	var user auth.User
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role)
		VALUES ($1, $2, $3, $4)
		RETURNING id, email, name, role, deactivated_at, created_at
	`, input.Email, input.Name, input.PasswordHash, string(input.Role)).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.Role,
		&user.DeactivatedAt,
		&user.CreatedAt,
	)
	if err != nil {
		return auth.User{}, translateError("create user", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}

func (repository *userRepository) FindByEmail(ctx context.Context, email string) (auth.User, error) {
	var user auth.User
	err := repository.pool.QueryRow(ctx, `
		SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, role, deactivated_at, created_at
		FROM users
		WHERE email = $1
	`, auth.NormalizeEmail(email)).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.PasswordHash,
		&user.Role,
		&user.DeactivatedAt,
		&user.CreatedAt,
	)
	if err != nil {
		return auth.User{}, translateError("find user by email", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}

func (repository *userRepository) FindByID(ctx context.Context, id int64) (auth.User, error) {
	var user auth.User
	err := repository.pool.QueryRow(ctx, `
		SELECT id, email, name, COALESCE(password_hash, '') AS password_hash, role, deactivated_at, created_at
		FROM users
		WHERE id = $1
	`, id).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.PasswordHash,
		&user.Role,
		&user.DeactivatedAt,
		&user.CreatedAt,
	)
	if err != nil {
		return auth.User{}, translateError("find user by id", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}

func (repository *userRepository) List(ctx context.Context) ([]auth.User, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id, email, name, role, deactivated_at, created_at
		FROM users
		ORDER BY email
	`)
	if err != nil {
		return nil, translateError("list users", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

func (repository *userRepository) ListActive(ctx context.Context) ([]auth.User, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id, email, name, role, deactivated_at, created_at
		FROM users
		WHERE deactivated_at IS NULL
		ORDER BY email
	`)
	if err != nil {
		return nil, translateError("list active users", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

// ListAssignableForProject returns the active users who may be assigned an
// issue in the project identified by slug: every owner, plus the members who
// reach it through a group. It is keyed on the project rather than returning
// everyone because an assignee who cannot open the issue is a dead end, and
// because the full roster is more than a member of one project needs to know.
//
// slug is resolved against scope first, in its own query, before the roster
// is built. That order matters: the roster query's own scoping condition can
// only live on the group-membership branch (an owner must still see the
// roster of a project no group grants them), so if slug named a project
// outside scope and we skipped straight to the roster query, the
// `u.role = 'owner'` branch would still match and leak the full owner list.
// Resolving the project first, and refusing with ErrNotFound when it does
// not resolve, closes that hole without disturbing the roster query's shape.
//
// The predicate is written out rather than calling this package's shared
// scopePredicate helper: that helper takes the concrete access.Scope type,
// which this method cannot import (see auth.ProjectScope's doc comment for
// why), so it is reproduced here in the same form against the interface's
// own methods instead.
func (repository *userRepository) ListAssignableForProject(ctx context.Context, scope auth.ProjectScope, slug string) ([]auth.User, error) {
	predicate := " AND true"
	args := []any{slug}
	if !scope.IsUnrestricted() {
		predicate = " AND id = ANY($2)"
		args = append(args, scope.ProjectIDs())
	}
	var projectID int64
	err := repository.pool.QueryRow(ctx, `
		SELECT id FROM projects WHERE slug = $1`+predicate,
		args...,
	).Scan(&projectID)
	if err != nil {
		return nil, translateError("resolve project for assignable roster", err)
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT u.id, u.email, u.name, u.role, u.deactivated_at, u.created_at
		FROM users u
		WHERE u.deactivated_at IS NULL
		  AND (u.role = 'owner' OR EXISTS (
		      SELECT 1
		      FROM group_members gm
		      JOIN group_projects gp ON gp.group_id = gm.group_id
		      WHERE gm.user_id = u.id AND gp.project_id = $1
		  ))
		ORDER BY u.email
	`, projectID)
	if err != nil {
		return nil, translateError("list assignable users for project", err)
	}
	defer rows.Close()
	return scanUsers(rows)
}

func scanUsers(rows pgx.Rows) ([]auth.User, error) {
	users := []auth.User{}
	for rows.Next() {
		var user auth.User
		if err := rows.Scan(
			&user.ID,
			&user.Email,
			&user.Name,
			&user.Role,
			&user.DeactivatedAt,
			&user.CreatedAt,
		); err != nil {
			return nil, translateError("scan user", err)
		}
		user.CreatedAt = user.CreatedAt.UTC()
		users = append(users, user)
	}
	if err := rows.Err(); err != nil {
		return nil, translateError("list users", err)
	}
	return users, nil
}

// lastOwnerGuardLockKey is the pg_advisory_xact_lock key that serializes
// every user update which could remove the deployment's last active owner.
// Postgres advisory locks all share one flat, database-wide integer
// namespace (see groupingRuleLockClassID for the same concern with the
// two-argument form) — the value itself is arbitrary, only its stability
// and its distinctness from other lock keys in this codebase matter.
//
// Without this lock, counting active owners and then updating one of them
// is a classic check-then-act race: two concurrent requests, each changing
// a different one of the last two active owners, can both read count == 2
// before either commits, both conclude "another owner exists", and both
// succeed — leaving zero active owners with nobody able to administer the
// deployment. The lock forces the second guarded transaction to wait until
// the first has committed (and so has the count it will see) before it
// evaluates the guard. auth.UserRepository deliberately exposes no
// standalone active-owner count, so no caller can assemble that unsafe
// sequence: the count exists only inside this transaction.
const lastOwnerGuardLockKey int64 = 0x6f776e65725f6c6b // "owner_lk", ASCII

// queryRower is satisfied by both *pgxpool.Pool and pgx.Tx, letting
// applyUserUpdate run identically outside and inside a transaction.
type queryRower interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// mayRemoveActiveOwner reports whether changes could strip a user of active
// ownership: a role change away from owner, or a deactivation. Renames and
// reactivations can never remove an owner, so Update skips the serialized
// guard for them — it would only add lock contention with no correctness
// benefit.
func mayRemoveActiveOwner(changes auth.UserChanges) bool {
	if changes.Role != nil && *changes.Role != auth.RoleOwner {
		return true
	}
	if changes.Deactivated != nil && *changes.Deactivated {
		return true
	}
	return false
}

func (repository *userRepository) Update(ctx context.Context, id int64, changes auth.UserChanges) (auth.User, error) {
	if !mayRemoveActiveOwner(changes) {
		return applyUserUpdate(ctx, repository.pool, id, changes)
	}
	return repository.updateGuardingLastOwner(ctx, id, changes)
}

// updateGuardingLastOwner applies changes to id inside a transaction that
// atomically refuses any change which would leave the deployment without
// an active owner. See lastOwnerGuardLockKey for why a count read outside
// the writing transaction cannot make this guarantee.
func (repository *userRepository) updateGuardingLastOwner(ctx context.Context, id int64, changes auth.UserChanges) (auth.User, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return auth.User{}, translateError("begin guarded update", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, lastOwnerGuardLockKey); err != nil {
		return auth.User{}, translateError("acquire last-owner guard lock", err)
	}

	var subject auth.User
	err = tx.QueryRow(ctx, `
		SELECT id, email, name, role, deactivated_at, created_at
		FROM users
		WHERE id = $1
	`, id).Scan(
		&subject.ID, &subject.Email, &subject.Name,
		&subject.Role, &subject.DeactivatedAt, &subject.CreatedAt,
	)
	if err != nil {
		return auth.User{}, translateError("find user for guarded update", err)
	}

	if subject.IsOwner() && subject.IsActive() {
		var activeOwners int
		if err := tx.QueryRow(ctx, `
			SELECT count(*) FROM users WHERE role = $1 AND deactivated_at IS NULL
		`, string(auth.RoleOwner)).Scan(&activeOwners); err != nil {
			return auth.User{}, translateError("count active owners", err)
		}
		if activeOwners <= 1 {
			return auth.User{}, fmt.Errorf("update user: %w", auth.ErrLastOwner)
		}
	}

	user, err := applyUserUpdate(ctx, tx, id, changes)
	if err != nil {
		return auth.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return auth.User{}, translateError("commit guarded update", err)
	}
	return user, nil
}

// applyUserUpdate runs the actual column update against db, which may be
// the pool (the common, unguarded case) or a transaction already holding
// the last-owner guard lock.
func applyUserUpdate(ctx context.Context, db queryRower, id int64, changes auth.UserChanges) (auth.User, error) {
	// COALESCE over typed nulls rather than string-built SQL: the keyset
	// pagination footgun in issues.go is the precedent for never assembling
	// SQL from Go values here.
	var role *string
	if changes.Role != nil {
		value := string(*changes.Role)
		role = &value
	}
	var deactivatedAt *time.Time
	if changes.Deactivated != nil && *changes.Deactivated {
		now := time.Now().UTC()
		deactivatedAt = &now
	}
	var user auth.User
	err := db.QueryRow(ctx, `
		UPDATE users
		SET name           = COALESCE($2, name),
		    role           = COALESCE($3, role),
		    deactivated_at = CASE
		                       WHEN $4::boolean IS NULL THEN deactivated_at
		                       WHEN $4::boolean THEN COALESCE(deactivated_at, $5)
		                       ELSE NULL
		                     END
		WHERE id = $1
		RETURNING id, email, name, role, deactivated_at, created_at
	`, id, changes.Name, role, changes.Deactivated, deactivatedAt).Scan(
		&user.ID, &user.Email, &user.Name,
		&user.Role, &user.DeactivatedAt, &user.CreatedAt,
	)
	if err != nil {
		return auth.User{}, translateError("update user", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}

func (repository *userRepository) UpdatePasswordHash(ctx context.Context, id int64, passwordHash string) error {
	result, err := repository.pool.Exec(ctx, `
		UPDATE users
		SET password_hash = $2
		WHERE id = $1
	`, id, passwordHash)
	if err != nil {
		return translateError("update user password hash", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("update user password hash: %w", auth.ErrNotFound)
	}
	return nil
}

func (repository *sessionRepository) Create(ctx context.Context, input auth.NewSession) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO sessions (user_id, token_hash, created_at, expires_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5)
	`, input.UserID, input.TokenHash[:], input.CreatedAt, input.ExpiresAt, input.LastSeenAt)
	if err != nil {
		return translateError("create session", err)
	}
	return nil
}

func (repository *sessionRepository) FindByTokenHash(ctx context.Context, tokenHash [32]byte) (auth.Session, auth.User, error) {
	var (
		session       auth.Session
		user          auth.User
		storedHashRaw []byte
	)
	err := repository.pool.QueryRow(ctx, `
		SELECT
			s.id, s.user_id, s.token_hash, s.created_at, s.expires_at, s.last_seen_at,
			u.id, u.email, u.name, u.role, u.deactivated_at, u.created_at
		FROM sessions AS s
		JOIN users AS u ON u.id = s.user_id
		WHERE s.token_hash = $1
	`, tokenHash[:]).Scan(
		&session.ID,
		&session.UserID,
		&storedHashRaw,
		&session.CreatedAt,
		&session.ExpiresAt,
		&session.LastSeenAt,
		&user.ID,
		&user.Email,
		&user.Name,
		&user.Role,
		&user.DeactivatedAt,
		&user.CreatedAt,
	)
	if err != nil {
		return auth.Session{}, auth.User{}, translateError("find session by token hash", err)
	}
	// A session row could outlive deactivation if a future code path forgets
	// to delete it on deactivation; guard here rather than trust that.
	if user.DeactivatedAt != nil {
		return auth.Session{}, auth.User{}, fmt.Errorf("find session by token hash: %w", auth.ErrNotFound)
	}
	if len(storedHashRaw) != len(session.TokenHash) {
		return auth.Session{}, auth.User{}, fmt.Errorf("find session by token hash: stored token hash length is %d", len(storedHashRaw))
	}
	copy(session.TokenHash[:], storedHashRaw)
	session.CreatedAt = session.CreatedAt.UTC()
	session.ExpiresAt = session.ExpiresAt.UTC()
	session.LastSeenAt = session.LastSeenAt.UTC()
	user.CreatedAt = user.CreatedAt.UTC()
	return session, user, nil
}

func (repository *sessionRepository) Touch(ctx context.Context, tokenHash [32]byte, lastSeenAt time.Time) error {
	result, err := repository.pool.Exec(ctx, `
		UPDATE sessions
		SET last_seen_at = $2
		WHERE token_hash = $1
	`, tokenHash[:], lastSeenAt)
	if err != nil {
		return translateError("touch session", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("touch session: %w", auth.ErrNotFound)
	}
	return nil
}

func (repository *sessionRepository) DeleteByTokenHash(ctx context.Context, tokenHash [32]byte) error {
	result, err := repository.pool.Exec(ctx, `
		DELETE FROM sessions
		WHERE token_hash = $1
	`, tokenHash[:])
	if err != nil {
		return translateError("delete session by token hash", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("delete session by token hash: %w", auth.ErrNotFound)
	}
	return nil
}

func (repository *sessionRepository) DeleteExpired(ctx context.Context, cutoff time.Time) error {
	_, err := repository.pool.Exec(ctx, `
		DELETE FROM sessions
		WHERE expires_at <= $1
	`, cutoff)
	if err != nil {
		return translateError("delete expired sessions", err)
	}
	return nil
}

func (repository *sessionRepository) DeleteByUserID(ctx context.Context, userID int64) error {
	_, err := repository.pool.Exec(ctx, `
		DELETE FROM sessions
		WHERE user_id = $1
	`, userID)
	if err != nil {
		return translateError("delete sessions by user id", err)
	}
	return nil
}

func (repository *sessionRepository) DeleteByUserIDExcept(ctx context.Context, userID int64, keep [32]byte) error {
	_, err := repository.pool.Exec(ctx, `
		DELETE FROM sessions
		WHERE user_id = $1 AND token_hash <> $2
	`, userID, keep[:])
	if err != nil {
		return translateError("delete sessions by user id except one", err)
	}
	return nil
}

func translateError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, auth.ErrNotFound)
	}
	if postgresError, ok := errors.AsType[*pgconn.PgError](err); ok && postgresError.Code == "23505" {
		return fmt.Errorf("%s: %w", operation, auth.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
