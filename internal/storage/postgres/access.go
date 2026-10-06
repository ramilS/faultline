package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
)

type accessRepository struct {
	pool *pgxpool.Pool
}

var _ access.Repository = (*accessRepository)(nil)

// NewAccessRepository returns the Postgres-backed resolver of a user's
// project access.
func NewAccessRepository(pool *pgxpool.Pool) access.Repository {
	return &accessRepository{pool: pool}
}

// scopePredicate returns a SQL fragment and its argument that restrict a query
// to the projects a scope allows, given the position the argument will take.
// An unrestricted scope yields a constant-true fragment so callers need no
// branch of their own.
//
// The restricted form is `column = ANY($n)`, and an empty scope therefore
// yields `= ANY('{}')` — false for every row. That is the fail-closed
// direction: a scope nobody set denies everything.
func scopePredicate(scope access.Scope, column string, position int) (string, []any) {
	if scope.IsUnrestricted() {
		return " AND true", nil
	}
	return fmt.Sprintf(" AND %s = ANY($%d)", column, position), []any{scope.ProjectIDs()}
}

// ProjectIDsForUser reads the union of every project granted to any group the
// user belongs to. DISTINCT matters: the same project may be granted through
// two groups, and a duplicate would inflate the ANY() array on every
// subsequent query in the request.
func (repository *accessRepository) ProjectIDsForUser(ctx context.Context, userID int64) ([]int64, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT DISTINCT gp.project_id
		FROM group_members gm
		JOIN group_projects gp ON gp.group_id = gm.group_id
		WHERE gm.user_id = $1
		ORDER BY gp.project_id
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("project ids for user: %w", err)
	}
	defer rows.Close()

	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, fmt.Errorf("collect project ids for user: %w", err)
	}
	return ids, nil
}
