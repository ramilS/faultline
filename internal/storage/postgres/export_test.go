//go:build integration

package postgres

import (
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/sentrycli"
)

// LastOwnerGuardLockKeyForTest exposes lastOwnerGuardLockKey to the
// black-box postgres_test package, so a test can take the same advisory
// lock directly and prove UpdateGuardingLastOwner genuinely contends on it
// — deterministically, rather than by racing two independent writers and
// hoping their statements overlap within a microsecond-scale window.
const LastOwnerGuardLockKeyForTest = lastOwnerGuardLockKey

// NewUploadChunkStoreWithQuota is NewUploadChunkStore with the per-project
// staging ceiling overridden, so a test can cross it without writing
// MaxStagedBytesPerProject (400 MiB) into Postgres to do it.
func NewUploadChunkStoreWithQuota(pool *pgxpool.Pool, maxStagedBytes int64) sentrycli.ChunkStore {
	return &uploadChunkStore{pool: pool, maxStagedBytes: maxStagedBytes}
}
