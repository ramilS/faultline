//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestProjectRepository(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	repository := storagepostgres.NewProjectRepository(pool)
	projects := repository.Projects
	keys := repository.Keys

	t.Run("creates and lists projects in deterministic order", func(t *testing.T) {
		first, err := projects.Create(ctx, project.CreateProject{Slug: "web", Name: "Web App", Platform: project.PlatformJavaScript})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		if first.ID == 0 || first.Slug != "web" || first.Name != "Web App" || first.Platform != project.PlatformJavaScript {
			t.Fatalf("created project = %+v", first)
		}
		assertProjectUTC(t, "created project", first.CreatedAt)

		second, err := projects.Create(ctx, project.CreateProject{Slug: "api", Name: "API", Platform: project.PlatformJVMSpring})
		if err != nil {
			t.Fatalf("create second project: %v", err)
		}

		listed, err := projects.List(ctx, access.Unrestricted())
		if err != nil {
			t.Fatalf("list projects: %v", err)
		}
		if len(listed) != 2 || listed[0].ID != first.ID || listed[1].ID != second.ID {
			t.Fatalf("listed projects = %+v, want ordered by id", listed)
		}
		assertProjectUTC(t, "listed project", listed[0].CreatedAt)
	})

	t.Run("updates name and platform but keeps slug and creation time", func(t *testing.T) {
		created, err := projects.Create(ctx, project.CreateProject{Slug: "renamed", Name: "Old", Platform: project.PlatformOther})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		updated, err := projects.Update(ctx, "renamed", project.UpdateProject{Name: "New", Platform: project.PlatformGo})
		if err != nil {
			t.Fatalf("update project: %v", err)
		}
		want := project.Project{ID: created.ID, Slug: "renamed", Name: "New", Platform: project.PlatformGo, CreatedAt: created.CreatedAt}
		if updated != want {
			t.Fatalf("updated project = %+v, want %+v", updated, want)
		}
		found, err := projects.FindBySlug(ctx, access.Unrestricted(), "renamed")
		if err != nil {
			t.Fatalf("find updated project: %v", err)
		}
		if found != want {
			t.Fatalf("stored project = %+v, want %+v", found, want)
		}
		if _, err := projects.Update(ctx, "does-not-exist", project.UpdateProject{Name: "X", Platform: project.PlatformGo}); !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("missing slug update error = %v, want ErrNotFound", err)
		}
	})

	t.Run("finds project by slug and reports missing slug", func(t *testing.T) {
		created, err := projects.Create(ctx, project.CreateProject{Slug: "mobile", Name: "Mobile", Platform: project.PlatformOther})
		if err != nil {
			t.Fatalf("create project: %v", err)
		}
		found, err := projects.FindBySlug(ctx, access.Unrestricted(), "mobile")
		if err != nil {
			t.Fatalf("find by slug: %v", err)
		}
		if found != created {
			t.Fatalf("found project = %+v, want %+v", found, created)
		}
		if _, err := projects.FindBySlug(ctx, access.Unrestricted(), "does-not-exist"); !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("missing slug error = %v, want ErrNotFound", err)
		}
	})

	t.Run("translates duplicate slug as conflict", func(t *testing.T) {
		if _, err := projects.Create(ctx, project.CreateProject{Slug: "billing", Name: "Billing", Platform: project.PlatformOther}); err != nil {
			t.Fatalf("create project: %v", err)
		}
		_, err := projects.Create(ctx, project.CreateProject{Slug: "billing", Name: "Billing Duplicate", Platform: project.PlatformOther})
		if !errors.Is(err, project.ErrConflict) {
			t.Fatalf("duplicate slug error = %v, want ErrConflict", err)
		}
	})

	t.Run("creates and lists keys scoped to project slug", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "keys-list")
		other := createProject(t, ctx, projects, "keys-other")

		created, err := keys.Create(ctx, project.CreateKey{
			ProjectID:      owner.ID,
			PublicKey:      "public-key-1",
			RateLimitRPS:   200,
			RateLimitBurst: 1000,
		})
		if err != nil {
			t.Fatalf("create key: %v", err)
		}
		if created.ID == 0 || created.ProjectID != owner.ID || created.PublicKey != "public-key-1" ||
			created.RateLimitRPS != 200 || created.RateLimitBurst != 1000 || created.RevokedAt != nil {
			t.Fatalf("created key = %+v", created)
		}

		if _, err := keys.Create(ctx, project.CreateKey{
			ProjectID:      other.ID,
			PublicKey:      "public-key-2",
			RateLimitRPS:   50,
			RateLimitBurst: 100,
		}); err != nil {
			t.Fatalf("create other key: %v", err)
		}

		ownerKeys, err := keys.ListByProjectSlug(ctx, access.Unrestricted(), "keys-list")
		if err != nil {
			t.Fatalf("list keys: %v", err)
		}
		if len(ownerKeys) != 1 || ownerKeys[0] != created {
			t.Fatalf("owner keys = %+v, want only %+v", ownerKeys, created)
		}
	})

	t.Run("translates duplicate public key as conflict", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "keys-dup")
		if _, err := keys.Create(ctx, project.CreateKey{ProjectID: owner.ID, PublicKey: "shared-key", RateLimitRPS: 1, RateLimitBurst: 1}); err != nil {
			t.Fatalf("create key: %v", err)
		}
		_, err := keys.Create(ctx, project.CreateKey{ProjectID: owner.ID, PublicKey: "shared-key", RateLimitRPS: 1, RateLimitBurst: 1})
		if !errors.Is(err, project.ErrConflict) {
			t.Fatalf("duplicate public key error = %v, want ErrConflict", err)
		}
	})

	t.Run("revokes key idempotently and preserves the first timestamp", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "keys-revoke")
		created, err := keys.Create(ctx, project.CreateKey{ProjectID: owner.ID, PublicKey: "revoke-key", RateLimitRPS: 1, RateLimitBurst: 1})
		if err != nil {
			t.Fatalf("create key: %v", err)
		}

		if err := keys.Revoke(ctx, access.Unrestricted(), "keys-revoke", created.ID); err != nil {
			t.Fatalf("revoke key: %v", err)
		}
		afterFirst := listKey(t, ctx, keys, "keys-revoke", created.ID)
		if afterFirst.RevokedAt == nil {
			t.Fatalf("revoked key still active: %+v", afterFirst)
		}
		assertProjectUTC(t, "revoked_at", *afterFirst.RevokedAt)

		if err := keys.Revoke(ctx, access.Unrestricted(), "keys-revoke", created.ID); err != nil {
			t.Fatalf("repeat revoke: %v", err)
		}
		afterSecond := listKey(t, ctx, keys, "keys-revoke", created.ID)
		if afterSecond.RevokedAt == nil || !afterSecond.RevokedAt.Equal(*afterFirst.RevokedAt) {
			t.Fatalf("repeat revoke changed timestamp: first=%v second=%v", afterFirst.RevokedAt, afterSecond.RevokedAt)
		}
	})

	t.Run("reports not found revoking missing or foreign key", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "keys-missing")
		created, err := keys.Create(ctx, project.CreateKey{ProjectID: owner.ID, PublicKey: "missing-scope-key", RateLimitRPS: 1, RateLimitBurst: 1})
		if err != nil {
			t.Fatalf("create key: %v", err)
		}
		if err := keys.Revoke(ctx, access.Unrestricted(), "keys-missing", 9_999_999); !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("revoke missing key error = %v, want ErrNotFound", err)
		}
		foreign := createProject(t, ctx, projects, "keys-foreign")
		_ = foreign
		if err := keys.Revoke(ctx, access.Unrestricted(), "keys-foreign", created.ID); !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("revoke key under wrong slug error = %v, want ErrNotFound", err)
		}
	})

	t.Run("preserves context cancellation", func(t *testing.T) {
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := projects.List(canceled, access.Unrestricted()); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled list error = %v, want context.Canceled", err)
		}
	})
}

func createProject(t *testing.T, ctx context.Context, repository project.ProjectRepository, slug string) project.Project {
	t.Helper()
	created, err := repository.Create(ctx, project.CreateProject{Slug: slug, Name: slug, Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project %s: %v", slug, err)
	}
	return created
}

func listKey(t *testing.T, ctx context.Context, repository project.KeyRepository, slug string, keyID int64) project.Key {
	t.Helper()
	listed, err := repository.ListByProjectSlug(ctx, access.Unrestricted(), slug)
	if err != nil {
		t.Fatalf("list keys for %s: %v", slug, err)
	}
	for _, key := range listed {
		if key.ID == keyID {
			return key
		}
	}
	t.Fatalf("key %d not present under slug %s", keyID, slug)
	return project.Key{}
}

func assertProjectUTC(t *testing.T, name string, timestamp time.Time) {
	t.Helper()
	if timestamp.Location() != time.UTC {
		t.Fatalf("%s location = %s, want UTC", name, timestamp.Location())
	}
}
