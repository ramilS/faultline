package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/issue"
)

type issueLabelStore struct {
	pool *pgxpool.Pool
}

func NewIssueLabelStore(pool *pgxpool.Pool) issue.LabelStore {
	return &issueLabelStore{pool: pool}
}

func (store *issueLabelStore) UnlabelledIssues(ctx context.Context, issueIDs []int64) ([]int64, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT id FROM issues
		WHERE id = ANY($1) AND display_title = '' AND display_culprit = ''
	`, issueIDs)
	if err != nil {
		return nil, fmt.Errorf("find unlabelled issues: %w", err)
	}
	defer rows.Close()
	var unlabelled []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan unlabelled issue: %w", err)
		}
		unlabelled = append(unlabelled, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate unlabelled issues: %w", err)
	}
	return unlabelled, nil
}

func (store *issueLabelStore) RefreshDisplayLabels(ctx context.Context, issueID int64, labels issue.Labels) error {
	return refreshDisplayLabels(ctx, store.pool, issueID, labels)
}

func (repository *issueRepository) RefreshDisplayLabels(ctx context.Context, issueID int64, labels issue.Labels) error {
	return refreshDisplayLabels(ctx, repository.pool, issueID, labels)
}

func refreshDisplayLabels(ctx context.Context, pool *pgxpool.Pool, issueID int64, labels issue.Labels) error {
	_, err := pool.Exec(ctx, `
		WITH wanted AS (
			SELECT id,
			       CASE WHEN $2 <> '' AND $2 <> title THEN $2 ELSE display_title END AS display_title,
			       CASE WHEN $3 <> '' AND $3 <> culprit THEN $3 ELSE display_culprit END AS display_culprit
			FROM issues WHERE id = $1
		)
		UPDATE issues i
		SET display_title = wanted.display_title, display_culprit = wanted.display_culprit
		FROM wanted
		WHERE i.id = wanted.id
		  AND (i.display_title, i.display_culprit) IS DISTINCT FROM (wanted.display_title, wanted.display_culprit)
	`, issueID, labels.Title, labels.Culprit)
	if err != nil {
		return fmt.Errorf("refresh display labels: %w", err)
	}
	return nil
}
