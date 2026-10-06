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
	"github.com/ramilS/faultline/internal/ingest"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestIngestKeyStoreRevocationTakesEffectAfterCacheExpiry(t *testing.T) {
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
	created, err := repository.Projects.Create(ctx, project.CreateProject{Slug: "ingest", Name: "Ingest", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	key, err := repository.Keys.Create(ctx, project.CreateKey{
		ProjectID:      created.ID,
		PublicKey:      "known-public-key",
		RateLimitRPS:   200,
		RateLimitBurst: 1000,
	})
	if err != nil {
		t.Fatalf("create key: %v", err)
	}

	store := storagepostgres.NewIngestKeyStore(pool)

	if _, err := store.FindByPublicKey(ctx, "never-existed"); !errors.Is(err, ingest.ErrKeyNotFound) {
		t.Fatalf("unknown key error = %v, want ErrKeyNotFound", err)
	}

	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	lookup := ingest.NewKeyLookup(store, func() time.Time { return now })

	info, err := lookup.Lookup(ctx, "known-public-key")
	if err != nil {
		t.Fatalf("initial lookup: %v", err)
	}
	if info.ProjectID != created.ID || info.RateLimitRPS != 200 || info.RateLimitBurst != 1000 {
		t.Fatalf("key info = %+v", info)
	}

	if err := repository.Keys.Revoke(ctx, access.Unrestricted(), "ingest", key.ID); err != nil {
		t.Fatalf("revoke key: %v", err)
	}

	if _, err := lookup.Lookup(ctx, "known-public-key"); err != nil {
		t.Fatalf("within cache TTL the key must remain valid, got %v", err)
	}

	now = now.Add(31 * time.Second)
	if _, err := lookup.Lookup(ctx, "known-public-key"); !errors.Is(err, ingest.ErrKeyNotFound) {
		t.Fatalf("after cache expiry the revoked key must be rejected, got %v", err)
	}
}
