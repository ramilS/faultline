package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/digestlink"
)

type digestLinkRepository struct {
	pool *pgxpool.Pool
}

var _ digestlink.Repository = (*digestLinkRepository)(nil)

func NewDigestLinkRepository(pool *pgxpool.Pool) digestlink.Repository {
	return &digestLinkRepository{pool: pool}
}

func (repository *digestLinkRepository) Create(ctx context.Context, scope access.Scope, link digestlink.Link) error {
	predicate, scopeArgs := scopePredicate(scope, "i.project_id", 5)
	args := append([]any{link.IssueID, link.TokenHash[:], link.CreatedBy, link.ExpiresAt}, scopeArgs...)
	var id int64
	err := repository.pool.QueryRow(ctx, `
		INSERT INTO issue_digest_links (issue_id, token_hash, created_by, expires_at)
		SELECT i.id, $2, $3, $4
		FROM issues i
		WHERE i.id = $1 AND i.merged_into_id IS NULL`+predicate+`
		RETURNING id
	`, args...).Scan(&id)
	return translateDigestLinkError("create digest link", err)
}

func (repository *digestLinkRepository) FindActive(ctx context.Context, tokenHash [32]byte, now time.Time) (digestlink.Target, error) {
	var target digestlink.Target
	err := repository.pool.QueryRow(ctx, `
		SELECT l.issue_id, i.project_id
		FROM issue_digest_links l
		JOIN issues i ON i.id = l.issue_id
		WHERE l.token_hash = $1 AND l.revoked_at IS NULL AND l.expires_at > $2
	`, tokenHash[:], now).Scan(&target.IssueID, &target.ProjectID)
	if err != nil {
		return digestlink.Target{}, translateDigestLinkError("find digest link", err)
	}
	return target, nil
}

func (repository *digestLinkRepository) RevokeAll(ctx context.Context, scope access.Scope, issueID int64, now time.Time) error {
	predicate, scopeArgs := scopePredicate(scope, "i.project_id", 3)
	args := append([]any{issueID, now}, scopeArgs...)
	var found int64
	err := repository.pool.QueryRow(ctx, `
		WITH target AS (
			SELECT i.id FROM issues i WHERE i.id = $1`+predicate+`
		), revoked AS (
			UPDATE issue_digest_links
			SET revoked_at = $2
			WHERE issue_id IN (SELECT id FROM target) AND revoked_at IS NULL
			RETURNING id
		)
		SELECT count(*) FROM target
	`, args...).Scan(&found)
	if err != nil {
		return translateDigestLinkError("revoke digest links", err)
	}
	if found == 0 {
		return fmt.Errorf("revoke digest links: %w", digestlink.ErrNotFound)
	}
	return nil
}

func translateDigestLinkError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, digestlink.ErrNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
