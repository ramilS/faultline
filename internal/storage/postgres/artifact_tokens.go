package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/project"
)

type artifactTokenRepository struct {
	pool *pgxpool.Pool
}

var _ artifactauth.Repository = (*artifactTokenRepository)(nil)

// NewArtifactTokenRepository returns an artifact-token adapter backed by pool.
func NewArtifactTokenRepository(pool *pgxpool.Pool) artifactauth.Repository {
	return &artifactTokenRepository{pool: pool}
}

func (repository *artifactTokenRepository) Create(ctx context.Context, input artifactauth.Token) (artifactauth.Token, error) {
	var created artifactauth.Token
	var storedHash []byte
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO artifact_tokens (project_id, name, token_prefix, token_hash)
		VALUES ($1, $2, $3, $4)
		RETURNING id, project_id, name, token_prefix, token_hash, created_at, last_used_at, revoked_at
	`, input.ProjectID, input.Name, input.TokenPrefix, input.TokenHash[:]).Scan(
		&created.ID,
		&created.ProjectID,
		&created.Name,
		&created.TokenPrefix,
		&storedHash,
		&created.CreatedAt,
		&created.LastUsedAt,
		&created.RevokedAt,
	)
	if err != nil {
		return artifactauth.Token{}, translateArtifactTokenError("create artifact token", err)
	}
	if err := populateArtifactTokenHash(&created, storedHash); err != nil {
		return artifactauth.Token{}, err
	}
	normalizeArtifactTokenTimes(&created)
	return created, nil
}

func (repository *artifactTokenRepository) ListByProjectSlug(ctx context.Context, slug string) ([]artifactauth.Token, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT t.id, t.project_id, t.name, t.token_prefix, t.token_hash, t.created_at, t.last_used_at, t.revoked_at
		FROM artifact_tokens AS t
		JOIN projects AS p ON p.id = t.project_id
		WHERE p.slug = $1
		ORDER BY t.id
	`, slug)
	if err != nil {
		return nil, translateArtifactTokenError("list artifact tokens", err)
	}
	defer rows.Close()

	tokens := make([]artifactauth.Token, 0)
	for rows.Next() {
		listed, err := scanArtifactToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, listed)
	}
	if err := rows.Err(); err != nil {
		return nil, translateArtifactTokenError("iterate artifact tokens", err)
	}
	return tokens, nil
}

func (repository *artifactTokenRepository) Revoke(ctx context.Context, slug string, tokenID int64) error {
	var revokedID int64
	err := repository.pool.QueryRow(ctx, `
		UPDATE artifact_tokens AS t
		SET revoked_at = COALESCE(t.revoked_at, now())
		FROM projects AS p
		WHERE t.project_id = p.id AND p.slug = $1 AND t.id = $2
		RETURNING t.id
	`, slug, tokenID).Scan(&revokedID)
	if err != nil {
		return translateArtifactTokenError("revoke artifact token", err)
	}
	return nil
}

func (repository *artifactTokenRepository) FindByPrefix(ctx context.Context, prefix string) (artifactauth.Token, error) {
	token, err := scanArtifactToken(repository.pool.QueryRow(ctx, `
		SELECT id, project_id, name, token_prefix, token_hash, created_at, last_used_at, revoked_at
		FROM artifact_tokens
		WHERE token_prefix = $1
	`, prefix))
	if err != nil {
		return artifactauth.Token{}, err
	}
	return token, nil
}

func (repository *artifactTokenRepository) Touch(ctx context.Context, tokenID int64) error {
	result, err := repository.pool.Exec(ctx, `
		UPDATE artifact_tokens
		SET last_used_at = now()
		WHERE id = $1
	`, tokenID)
	if err != nil {
		return translateArtifactTokenError("touch artifact token", err)
	}
	if result.RowsAffected() == 0 {
		return fmt.Errorf("touch artifact token: %w", artifactauth.ErrNotFound)
	}
	return nil
}

type artifactTokenScanner interface {
	Scan(...any) error
}

func scanArtifactToken(row artifactTokenScanner) (artifactauth.Token, error) {
	var (
		token      artifactauth.Token
		storedHash []byte
	)
	err := row.Scan(
		&token.ID,
		&token.ProjectID,
		&token.Name,
		&token.TokenPrefix,
		&storedHash,
		&token.CreatedAt,
		&token.LastUsedAt,
		&token.RevokedAt,
	)
	if err != nil {
		return artifactauth.Token{}, translateArtifactTokenError("scan artifact token", err)
	}
	if err := populateArtifactTokenHash(&token, storedHash); err != nil {
		return artifactauth.Token{}, err
	}
	normalizeArtifactTokenTimes(&token)
	return token, nil
}

func populateArtifactTokenHash(token *artifactauth.Token, storedHash []byte) error {
	if len(storedHash) != len(token.TokenHash) {
		return fmt.Errorf("scan artifact token: stored token hash length is %d", len(storedHash))
	}
	copy(token.TokenHash[:], storedHash)
	return nil
}

func normalizeArtifactTokenTimes(token *artifactauth.Token) {
	token.CreatedAt = token.CreatedAt.UTC()
	if token.LastUsedAt != nil {
		lastUsedAt := token.LastUsedAt.UTC()
		token.LastUsedAt = &lastUsedAt
	}
	if token.RevokedAt != nil {
		revokedAt := token.RevokedAt.UTC()
		token.RevokedAt = &revokedAt
	}
}

func translateArtifactTokenError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, artifactauth.ErrNotFound)
	}
	if postgresError, ok := errors.AsType[*pgconn.PgError](err); ok && postgresError.Code == "23505" {
		return fmt.Errorf("%s: %w", operation, project.ErrConflict)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
