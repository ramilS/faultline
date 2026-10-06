package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/ingest"
)

type ingestKeyStore struct {
	pool *pgxpool.Pool
}

var _ ingest.KeyStore = (*ingestKeyStore)(nil)

// NewIngestKeyStore returns an ingest key resolver backed by pool. It reports
// unknown and revoked keys identically as ingest.ErrKeyNotFound.
func NewIngestKeyStore(pool *pgxpool.Pool) ingest.KeyStore {
	return &ingestKeyStore{pool: pool}
}

func (store *ingestKeyStore) FindByPublicKey(ctx context.Context, publicKey string) (ingest.KeyInfo, error) {
	var info ingest.KeyInfo
	err := store.pool.QueryRow(ctx, `
		SELECT project_id, public_key, rate_limit_rps, rate_limit_burst
		FROM project_keys
		WHERE public_key = $1 AND revoked_at IS NULL
	`, publicKey).Scan(&info.ProjectID, &info.PublicKey, &info.RateLimitRPS, &info.RateLimitBurst)
	if errors.Is(err, pgx.ErrNoRows) {
		return ingest.KeyInfo{}, ingest.ErrKeyNotFound
	}
	if err != nil {
		return ingest.KeyInfo{}, fmt.Errorf("find ingest key: %w", err)
	}
	return info, nil
}
