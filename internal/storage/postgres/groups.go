package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/group"
)

type groupRepository struct {
	pool *pgxpool.Pool
}

var _ group.Repository = (*groupRepository)(nil)

// NewGroupRepository returns the group.Repository adapter backed by pool.
func NewGroupRepository(pool *pgxpool.Pool) group.Repository {
	return &groupRepository{pool: pool}
}

// List returns every group with its member and project counts. LEFT JOIN
// LATERAL keeps a group with no members and no projects in the result, unlike
// an inner join or a plain aggregate join, which would silently drop it.
func (r *groupRepository) List(ctx context.Context) ([]group.Group, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT g.id, g.name, g.created_at, members.count, projects.count
		FROM groups AS g
		LEFT JOIN LATERAL (
			SELECT count(*) AS count FROM group_members WHERE group_id = g.id
		) AS members ON true
		LEFT JOIN LATERAL (
			SELECT count(*) AS count FROM group_projects WHERE group_id = g.id
		) AS projects ON true
		ORDER BY g.name
	`)
	if err != nil {
		return nil, translateGroupError("list groups", err)
	}
	defer rows.Close()

	var groups []group.Group
	for rows.Next() {
		var current group.Group
		if err := rows.Scan(&current.ID, &current.Name, &current.CreatedAt, &current.MemberCount, &current.ProjectCount); err != nil {
			return nil, translateGroupError("scan group", err)
		}
		groups = append(groups, current)
	}
	if err := rows.Err(); err != nil {
		return nil, translateGroupError("list groups", err)
	}
	return groups, nil
}

// Find returns a group's metadata alongside its full member and project id
// sets. A missing group maps to group.ErrNotFound rather than a bare
// pgx.ErrNoRows so callers can translate it to a 404 without depending on the
// driver.
func (r *groupRepository) Find(ctx context.Context, id int64) (group.Detail, error) {
	var detail group.Detail
	detail.Group.ID = id
	err := r.pool.QueryRow(ctx, `
		SELECT name, created_at FROM groups WHERE id = $1
	`, id).Scan(&detail.Group.Name, &detail.Group.CreatedAt)
	if err != nil {
		return group.Detail{}, translateGroupError("find group", err)
	}

	userIDs, err := r.memberIDs(ctx, id)
	if err != nil {
		return group.Detail{}, err
	}
	projectIDs, err := r.projectIDs(ctx, id)
	if err != nil {
		return group.Detail{}, err
	}
	detail.UserIDs = userIDs
	detail.ProjectIDs = projectIDs
	detail.Group.MemberCount = len(userIDs)
	detail.Group.ProjectCount = len(projectIDs)
	return detail, nil
}

func (r *groupRepository) memberIDs(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT user_id FROM group_members WHERE group_id = $1 ORDER BY user_id
	`, id)
	if err != nil {
		return nil, translateGroupError("list group members", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, translateGroupError("scan group member", err)
		}
		ids = append(ids, userID)
	}
	if err := rows.Err(); err != nil {
		return nil, translateGroupError("list group members", err)
	}
	return ids, nil
}

func (r *groupRepository) projectIDs(ctx context.Context, id int64) ([]int64, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT project_id FROM group_projects WHERE group_id = $1 ORDER BY project_id
	`, id)
	if err != nil {
		return nil, translateGroupError("list group projects", err)
	}
	defer rows.Close()

	var ids []int64
	for rows.Next() {
		var projectID int64
		if err := rows.Scan(&projectID); err != nil {
			return nil, translateGroupError("scan group project", err)
		}
		ids = append(ids, projectID)
	}
	if err := rows.Err(); err != nil {
		return nil, translateGroupError("list group projects", err)
	}
	return ids, nil
}

func (r *groupRepository) Create(ctx context.Context, input group.NewGroup) (group.Group, error) {
	var created group.Group
	err := r.pool.QueryRow(ctx, `
		INSERT INTO groups (name) VALUES ($1)
		RETURNING id, name, created_at
	`, input.Name).Scan(&created.ID, &created.Name, &created.CreatedAt)
	if err != nil {
		return group.Group{}, translateGroupError("create group", err)
	}
	return created, nil
}

func (r *groupRepository) Rename(ctx context.Context, id int64, name string) (group.Group, error) {
	var renamed group.Group
	err := r.pool.QueryRow(ctx, `
		UPDATE groups SET name = $2 WHERE id = $1
		RETURNING id, name, created_at
	`, id, name).Scan(&renamed.ID, &renamed.Name, &renamed.CreatedAt)
	if err != nil {
		return group.Group{}, translateGroupError("rename group", err)
	}
	return renamed, nil
}

// Delete removes a group. group_members and group_projects rows referencing
// it are removed by the foreign keys' ON DELETE CASCADE (migration
// 000013_groups), so no explicit cleanup is needed here.
func (r *groupRepository) Delete(ctx context.Context, id int64) error {
	tag, err := r.pool.Exec(ctx, `DELETE FROM groups WHERE id = $1`, id)
	if err != nil {
		return translateGroupError("delete group", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("delete group: %w", group.ErrNotFound)
	}
	return nil
}

// ReplaceMembers replaces a group's entire membership set with userIDs. The
// delete and insert run inside one transaction: a half-applied replacement
// would silently grant or revoke access, so the two statements must not be
// separately observable.
func (r *groupRepository) ReplaceMembers(ctx context.Context, id int64, userIDs []int64) error {
	return r.replace(ctx, "group_members", "user_id", id, userIDs)
}

// ReplaceProjects replaces a group's entire project grant set with
// projectIDs, under the same single-transaction guarantee as ReplaceMembers.
func (r *groupRepository) ReplaceProjects(ctx context.Context, id int64, projectIDs []int64) error {
	return r.replace(ctx, "group_projects", "project_id", id, projectIDs)
}

func (r *groupRepository) replace(ctx context.Context, table, column string, id int64, ids []int64) error {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin replace %s: %w", table, err)
	}
	defer tx.Rollback(ctx)

	// FOR SHARE inside this transaction: a concurrent DELETE of the group
	// cannot slip in between this check and the writes below, so the
	// not-found answer stays accurate for the whole replace.
	var exists int
	if err := tx.QueryRow(ctx, `SELECT 1 FROM groups WHERE id = $1 FOR SHARE`, id).Scan(&exists); err != nil {
		return translateGroupError("find group for replace "+table, err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE group_id = $1`, id); err != nil {
		return translateGroupError("clear "+table, err)
	}
	for _, value := range ids {
		if _, err := tx.Exec(ctx,
			`INSERT INTO `+table+` (group_id, `+column+`) VALUES ($1, $2)`, id, value,
		); err != nil {
			return translateGroupError("insert "+table, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit replace %s: %w", table, err)
	}
	return nil
}

func translateGroupError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, group.ErrNotFound)
	}
	if postgresError, ok := errors.AsType[*pgconn.PgError](err); ok {
		switch postgresError.Code {
		case "23505":
			return fmt.Errorf("%s: %w", operation, group.ErrConflict)
		case "23503":
			return fmt.Errorf("%s: %w", operation, group.ErrUnknownReference)
		}
	}
	return fmt.Errorf("%s: %w", operation, err)
}
