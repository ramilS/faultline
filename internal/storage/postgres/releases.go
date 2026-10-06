package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/sentrycli"
)

type releaseStore struct {
	pool *pgxpool.Pool
}

var _ sentrycli.ReleaseStore = (*releaseStore)(nil)

// NewReleaseStore returns the release adapter the sentry-cli release endpoints
// write through.
func NewReleaseStore(pool *pgxpool.Pool) sentrycli.ReleaseStore {
	return &releaseStore{pool: pool}
}

// Upsert is idempotent: the bundler plugin creates a release on every build,
// and a second build of the same version is normal, not a conflict. It
// delegates to upsertRelease (source_maps.go) rather than duplicating the
// releases upsert SQL a second time in this package.
func (store *releaseStore) Upsert(ctx context.Context, projectID int64, version string) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin upsert release: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := upsertRelease(ctx, tx, projectID, version); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit upsert release: %w", err)
	}
	return nil
}

// Finalize records when a release was declared complete. It composes
// upsertRelease with a plain by-id UPDATE rather than a second releases
// upsert statement: the plugin's finalize call can reach a Faultline that
// never saw the create, and failing there would break a default-configured
// build, so the release row must exist (idempotently) before released_at is
// set on it.
func (store *releaseStore) Finalize(ctx context.Context, projectID int64, version string, at time.Time) error {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin finalize release: %w", err)
	}
	defer tx.Rollback(ctx)

	releaseID, err := upsertRelease(ctx, tx, projectID, version)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE releases SET released_at = $1 WHERE id = $2`, at, releaseID); err != nil {
		return fmt.Errorf("finalize release: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit finalize release: %w", err)
	}
	return nil
}
