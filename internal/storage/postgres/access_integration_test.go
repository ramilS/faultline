//go:build integration

package postgres_test

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestProjectIDsForUser(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewAccessRepository(pool)

	var alice, bob int64
	mustScanRow(t, pool, []any{&alice}, `INSERT INTO users (email, name, password_hash, role)
		VALUES ('alice@example.com', 'Alice', 'x', 'member') RETURNING id`)
	mustScanRow(t, pool, []any{&bob}, `INSERT INTO users (email, name, password_hash, role)
		VALUES ('bob@example.com', 'Bob', 'x', 'member') RETURNING id`)

	var web, api, secret int64
	mustScanRow(t, pool, []any{&web}, `INSERT INTO projects (slug, name, platform)
		VALUES ('web', 'Web', 'javascript') RETURNING id`)
	mustScanRow(t, pool, []any{&api}, `INSERT INTO projects (slug, name, platform)
		VALUES ('api', 'API', 'other') RETURNING id`)
	mustScanRow(t, pool, []any{&secret}, `INSERT INTO projects (slug, name, platform)
		VALUES ('secret', 'Secret', 'other') RETURNING id`)

	var frontend, backend int64
	mustScanRow(t, pool, []any{&frontend}, `INSERT INTO groups (name) VALUES ('frontend') RETURNING id`)
	mustScanRow(t, pool, []any{&backend}, `INSERT INTO groups (name) VALUES ('backend') RETURNING id`)

	mustExec(t, pool, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2), ($3, $2)`,
		frontend, alice, backend)
	mustExec(t, pool, `INSERT INTO group_projects (group_id, project_id) VALUES ($1, $2), ($3, $4)`,
		frontend, web, backend, api)

	t.Run("union across groups, excluding ungranted projects", func(t *testing.T) {
		ids, err := repository.ProjectIDsForUser(ctx, alice)
		if err != nil {
			t.Fatalf("ProjectIDsForUser: %v", err)
		}
		if len(ids) != 2 {
			t.Fatalf("ids = %v, want two projects", ids)
		}
		for _, id := range ids {
			if id == secret {
				t.Errorf("ids include the ungranted project %d", secret)
			}
		}
	})

	t.Run("a user in no group gets an empty slice and no error", func(t *testing.T) {
		ids, err := repository.ProjectIDsForUser(ctx, bob)
		if err != nil {
			t.Fatalf("ProjectIDsForUser: %v", err)
		}
		if len(ids) != 0 {
			t.Errorf("ids = %v, want empty", ids)
		}
	})

	t.Run("a project granted through two groups appears once", func(t *testing.T) {
		mustExec(t, pool, `INSERT INTO group_projects (group_id, project_id) VALUES ($1, $2)`,
			backend, web)
		ids, err := repository.ProjectIDsForUser(ctx, alice)
		if err != nil {
			t.Fatalf("ProjectIDsForUser: %v", err)
		}
		if len(ids) != 2 {
			t.Errorf("ids = %v, want two distinct projects", ids)
		}
	})

	t.Run("deleting a group revokes the access it granted", func(t *testing.T) {
		mustExec(t, pool, `DELETE FROM groups WHERE id = $1`, backend)
		ids, err := repository.ProjectIDsForUser(ctx, alice)
		if err != nil {
			t.Fatalf("ProjectIDsForUser: %v", err)
		}
		if len(ids) != 1 || ids[0] != web {
			t.Errorf("ids = %v, want just the frontend project %d", ids, web)
		}
	})
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// mustScanRow executes sql with args and scans the single resulting row into
// dest, failing the test immediately if the query or the scan errors.
// pgx.Row surfaces its error only from Scan, so unlike a bare
// pool.QueryRow(...) wrapper — which invited callers to drop that error, as
// happened repeatedly before this helper existed — a genuine "must" version
// has to perform the scan itself rather than hand back a pgx.Row for the
// caller to (potentially silently) ignore.
func mustScanRow(t *testing.T, pool *pgxpool.Pool, dest []any, sql string, args ...any) {
	t.Helper()
	if err := pool.QueryRow(t.Context(), sql, args...).Scan(dest...); err != nil {
		t.Fatalf("scan row %q: %v", sql, err)
	}
}
