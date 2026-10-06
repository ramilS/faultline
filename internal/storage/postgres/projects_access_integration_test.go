//go:build integration

package postgres_test

import (
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestProjectReadsHonourScope(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repositories := postgres.NewProjectRepository(pool)

	var web, secret int64
	mustScanRow(t, pool, []any{&web}, `INSERT INTO projects (slug, name, platform)
		VALUES ('web', 'Web', 'javascript') RETURNING id`)
	mustScanRow(t, pool, []any{&secret}, `INSERT INTO projects (slug, name, platform)
		VALUES ('secret', 'Secret', 'other') RETURNING id`)
	mustExec(t, pool, `INSERT INTO project_keys (project_id, public_key) VALUES ($1, 'k-secret')`, secret)

	t.Run("List returns only scoped projects", func(t *testing.T) {
		projects, err := repositories.Projects.List(ctx, access.Limited([]int64{web}))
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(projects) != 1 || projects[0].Slug != "web" {
			t.Fatalf("projects = %+v, want just web", projects)
		}
	})

	t.Run("List under an unrestricted scope returns everything", func(t *testing.T) {
		projects, err := repositories.Projects.List(ctx, access.Unrestricted())
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(projects) != 2 {
			t.Fatalf("projects = %d, want 2", len(projects))
		}
	})

	t.Run("List under the zero scope returns nothing", func(t *testing.T) {
		projects, err := repositories.Projects.List(ctx, access.Scope{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(projects) != 0 {
			t.Fatalf("projects = %+v, want none", projects)
		}
	})

	t.Run("FindBySlug reports an out-of-scope project as missing", func(t *testing.T) {
		_, err := repositories.Projects.FindBySlug(ctx, access.Limited([]int64{web}), "secret")
		if !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("err = %v, want project.ErrNotFound", err)
		}
	})

	// The listed public_key is the live ingest credential, so this is the
	// route through which the boundary would leak a working write capability.
	t.Run("ListByProjectSlug does not leak an out-of-scope project's keys", func(t *testing.T) {
		keys, err := repositories.Keys.ListByProjectSlug(ctx, access.Limited([]int64{web}), "secret")
		if err != nil && !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("err = %v, want nil or project.ErrNotFound", err)
		}
		if len(keys) != 0 {
			t.Fatalf("keys = %+v, want none", keys)
		}
	})
}
