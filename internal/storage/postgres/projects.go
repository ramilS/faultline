package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/project"
)

// ProjectRepository groups the project and ingest-key adapters that share a
// PostgreSQL connection pool. Separate adapters are required because both
// domain interfaces define Create methods with different signatures.
type ProjectRepository struct {
	Projects project.ProjectRepository
	Keys     project.KeyRepository
}

type projectRepository struct {
	pool *pgxpool.Pool
}

type keyRepository struct {
	pool *pgxpool.Pool
}

var (
	_ project.ProjectRepository = (*projectRepository)(nil)
	_ project.KeyRepository     = (*keyRepository)(nil)
)

// NewProjectRepository returns project and ingest-key adapters backed by pool.
func NewProjectRepository(pool *pgxpool.Pool) *ProjectRepository {
	return &ProjectRepository{
		Projects: &projectRepository{pool: pool},
		Keys:     &keyRepository{pool: pool},
	}
}

func (repository *projectRepository) Create(ctx context.Context, input project.CreateProject) (project.Project, error) {
	var created project.Project
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform)
		VALUES ($1, $2, $3)
		RETURNING id, slug, name, platform, created_at
	`, input.Slug, input.Name, input.Platform).Scan(
		&created.ID,
		&created.Slug,
		&created.Name,
		&created.Platform,
		&created.CreatedAt,
	)
	if err != nil {
		return project.Project{}, translateProjectError("create project", err)
	}
	created.CreatedAt = created.CreatedAt.UTC()
	return created, nil
}

func (repository *projectRepository) List(ctx context.Context, scope access.Scope) ([]project.Project, error) {
	predicate, args := scopePredicate(scope, "id", 1)
	rows, err := repository.pool.Query(ctx, `
		SELECT id, slug, name, platform, created_at
		FROM projects
		WHERE true`+predicate+`
		ORDER BY id
	`, args...)
	if err != nil {
		return nil, translateProjectError("list projects", err)
	}
	defer rows.Close()

	projects := make([]project.Project, 0)
	for rows.Next() {
		var listed project.Project
		if err := rows.Scan(&listed.ID, &listed.Slug, &listed.Name, &listed.Platform, &listed.CreatedAt); err != nil {
			return nil, translateProjectError("scan project", err)
		}
		listed.CreatedAt = listed.CreatedAt.UTC()
		projects = append(projects, listed)
	}
	if err := rows.Err(); err != nil {
		return nil, translateProjectError("iterate projects", err)
	}
	return projects, nil
}

func (repository *projectRepository) FindBySlug(ctx context.Context, scope access.Scope, slug string) (project.Project, error) {
	predicate, predicateArgs := scopePredicate(scope, "id", 2)
	args := append([]any{slug}, predicateArgs...)
	var found project.Project
	err := repository.pool.QueryRow(ctx, `
		SELECT id, slug, name, platform, created_at
		FROM projects
		WHERE slug = $1`+predicate+`
	`, args...).Scan(
		&found.ID,
		&found.Slug,
		&found.Name,
		&found.Platform,
		&found.CreatedAt,
	)
	if err != nil {
		return project.Project{}, translateProjectError("find project by slug", err)
	}
	found.CreatedAt = found.CreatedAt.UTC()
	return found, nil
}

func (repository *keyRepository) Create(ctx context.Context, input project.CreateKey) (project.Key, error) {
	var created project.Key
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO project_keys (project_id, public_key, rate_limit_rps, rate_limit_burst)
		VALUES ($1, $2, $3, $4)
		RETURNING id, project_id, public_key, rate_limit_rps, rate_limit_burst, revoked_at
	`, input.ProjectID, input.PublicKey, input.RateLimitRPS, input.RateLimitBurst).Scan(
		&created.ID,
		&created.ProjectID,
		&created.PublicKey,
		&created.RateLimitRPS,
		&created.RateLimitBurst,
		&created.RevokedAt,
	)
	if err != nil {
		return project.Key{}, translateProjectError("create ingest key", err)
	}
	normalizeRevokedAt(&created)
	return created, nil
}

func (repository *keyRepository) ListByProjectSlug(ctx context.Context, scope access.Scope, slug string) ([]project.Key, error) {
	predicate, predicateArgs := scopePredicate(scope, "p.id", 2)
	args := append([]any{slug}, predicateArgs...)
	rows, err := repository.pool.Query(ctx, `
		SELECT k.id, k.project_id, k.public_key, k.rate_limit_rps, k.rate_limit_burst, k.revoked_at
		FROM project_keys AS k
		JOIN projects AS p ON p.id = k.project_id
		WHERE p.slug = $1`+predicate+`
		ORDER BY k.id
	`, args...)
	if err != nil {
		return nil, translateProjectError("list ingest keys", err)
	}
	defer rows.Close()

	keys := make([]project.Key, 0)
	for rows.Next() {
		var listed project.Key
		if err := rows.Scan(
			&listed.ID,
			&listed.ProjectID,
			&listed.PublicKey,
			&listed.RateLimitRPS,
			&listed.RateLimitBurst,
			&listed.RevokedAt,
		); err != nil {
			return nil, translateProjectError("scan ingest key", err)
		}
		normalizeRevokedAt(&listed)
		keys = append(keys, listed)
	}
	if err := rows.Err(); err != nil {
		return nil, translateProjectError("iterate ingest keys", err)
	}
	return keys, nil
}

// Revoke marks a key revoked idempotently. COALESCE preserves the first
// revocation timestamp on repeat calls; a missing RETURNING row means the key
// does not exist under the given project slug.
func (repository *keyRepository) Revoke(ctx context.Context, scope access.Scope, slug string, keyID int64) error {
	predicate, predicateArgs := scopePredicate(scope, "p.id", 3)
	args := append([]any{slug, keyID}, predicateArgs...)
	var revokedID int64
	err := repository.pool.QueryRow(ctx, `
		UPDATE project_keys AS k
		SET revoked_at = COALESCE(k.revoked_at, now())
		FROM projects AS p
		WHERE k.project_id = p.id AND p.slug = $1 AND k.id = $2`+predicate+`
		RETURNING k.id
	`, args...).Scan(&revokedID)
	if err != nil {
		return translateProjectError("revoke ingest key", err)
	}
	return nil
}

func normalizeRevokedAt(key *project.Key) {
	if key.RevokedAt != nil {
		utc := key.RevokedAt.UTC()
		key.RevokedAt = &utc
	}
}

func translateProjectError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, project.ErrNotFound)
	}
	if postgresError, ok := errors.AsType[*pgconn.PgError](err); ok && postgresError.Code == "23505" {
		return fmt.Errorf("%s: %w", operation, project.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
