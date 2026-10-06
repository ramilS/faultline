//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/artifactauth"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestArtifactTokenRepository(t *testing.T) {
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

	projects := storagepostgres.NewProjectRepository(pool).Projects
	repository := storagepostgres.NewArtifactTokenRepository(pool)

	t.Run("creates, lists, finds, and touches tokens", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "artifact-tokens")
		firstHash := sha256.Sum256([]byte("first raw token"))
		first, err := repository.Create(ctx, artifactauth.Token{
			ProjectID: owner.ID, Name: "CI", TokenPrefix: "first-prefix", TokenHash: firstHash,
		})
		if err != nil {
			t.Fatalf("create first token: %v", err)
		}
		if first.ID == 0 || first.ProjectID != owner.ID || first.Name != "CI" || first.TokenPrefix != "first-prefix" || first.TokenHash != firstHash || first.LastUsedAt != nil || first.RevokedAt != nil {
			t.Fatalf("created first token = %+v", first)
		}
		assertArtifactTokenUTC(t, "created_at", first.CreatedAt)

		secondHash := sha256.Sum256([]byte("second raw token"))
		second, err := repository.Create(ctx, artifactauth.Token{
			ProjectID: owner.ID, Name: "Release", TokenPrefix: "second-prefix", TokenHash: secondHash,
		})
		if err != nil {
			t.Fatalf("create second token: %v", err)
		}

		listed, err := repository.ListByProjectSlug(ctx, "artifact-tokens")
		if err != nil {
			t.Fatalf("list tokens: %v", err)
		}
		if len(listed) != 2 || listed[0] != first || listed[1] != second {
			t.Fatalf("listed tokens = %+v, want [%+v %+v]", listed, first, second)
		}

		found, err := repository.FindByPrefix(ctx, "first-prefix")
		if err != nil {
			t.Fatalf("find by prefix: %v", err)
		}
		if found != first {
			t.Fatalf("found token = %+v, want %+v", found, first)
		}

		if err := repository.Touch(ctx, first.ID); err != nil {
			t.Fatalf("touch token: %v", err)
		}
		touched, err := repository.FindByPrefix(ctx, "first-prefix")
		if err != nil {
			t.Fatalf("find touched token: %v", err)
		}
		if touched.LastUsedAt == nil {
			t.Fatalf("touched token has nil last_used_at: %+v", touched)
		}
		assertArtifactTokenUTC(t, "last_used_at", *touched.LastUsedAt)
	})

	t.Run("revokes idempotently and rejects another project's token", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "artifact-token-owner")
		createProject(t, ctx, projects, "artifact-token-foreign")
		hash := sha256.Sum256([]byte("revocable raw token"))
		created, err := repository.Create(ctx, artifactauth.Token{
			ProjectID: owner.ID, Name: "CI", TokenPrefix: "revoke-prefix", TokenHash: hash,
		})
		if err != nil {
			t.Fatalf("create token: %v", err)
		}

		if err := repository.Revoke(ctx, "artifact-token-owner", created.ID); err != nil {
			t.Fatalf("revoke token: %v", err)
		}
		afterFirst, err := repository.FindByPrefix(ctx, "revoke-prefix")
		if err != nil {
			t.Fatalf("find revoked token: %v", err)
		}
		if afterFirst.RevokedAt == nil {
			t.Fatalf("revoked token has nil revoked_at: %+v", afterFirst)
		}
		assertArtifactTokenUTC(t, "revoked_at", *afterFirst.RevokedAt)

		if err := repository.Revoke(ctx, "artifact-token-owner", created.ID); err != nil {
			t.Fatalf("repeat revoke token: %v", err)
		}
		afterSecond, err := repository.FindByPrefix(ctx, "revoke-prefix")
		if err != nil {
			t.Fatalf("find repeatedly revoked token: %v", err)
		}
		if afterSecond.RevokedAt == nil || !afterSecond.RevokedAt.Equal(*afterFirst.RevokedAt) {
			t.Fatalf("repeat revoke changed timestamp: first=%v second=%v", afterFirst.RevokedAt, afterSecond.RevokedAt)
		}

		if err := repository.Revoke(ctx, "artifact-token-foreign", created.ID); !errors.Is(err, artifactauth.ErrNotFound) {
			t.Fatalf("revoke token under foreign project error = %v, want ErrNotFound", err)
		}
	})

	t.Run("translates duplicate tokens and missing token operations", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "artifact-token-errors")
		hash := sha256.Sum256([]byte("duplicate raw token"))
		if _, err := repository.Create(ctx, artifactauth.Token{
			ProjectID: owner.ID, Name: "CI", TokenPrefix: "duplicate-prefix", TokenHash: hash,
		}); err != nil {
			t.Fatalf("create token: %v", err)
		}
		if _, err := repository.Create(ctx, artifactauth.Token{
			ProjectID: owner.ID, Name: "Duplicate", TokenPrefix: "duplicate-prefix", TokenHash: sha256.Sum256([]byte("other raw token")),
		}); !errors.Is(err, project.ErrConflict) {
			t.Fatalf("duplicate token error = %v, want ErrConflict", err)
		}
		if _, err := repository.FindByPrefix(ctx, "missing-prefix"); !errors.Is(err, artifactauth.ErrNotFound) {
			t.Fatalf("find missing token error = %v, want ErrNotFound", err)
		}
		if err := repository.Touch(ctx, 9_999_999); !errors.Is(err, artifactauth.ErrNotFound) {
			t.Fatalf("touch missing token error = %v, want ErrNotFound", err)
		}
	})
}

func assertArtifactTokenUTC(t *testing.T, name string, timestamp time.Time) {
	t.Helper()
	if timestamp.Location() != time.UTC {
		t.Fatalf("%s location = %s, want UTC", name, timestamp.Location())
	}
}
