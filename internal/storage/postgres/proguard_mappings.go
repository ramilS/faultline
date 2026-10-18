package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/proguard"
)

type proguardMappingRepository struct {
	pool   *pgxpool.Pool
	limits artifactLimits
}

var _ proguard.Repository = (*proguardMappingRepository)(nil)

// NewProguardMappingRepository returns a ProGuard/R8 mapping adapter backed by pool.
func NewProguardMappingRepository(pool *pgxpool.Pool, options ...ArtifactStoreOption) proguard.Repository {
	return &proguardMappingRepository{pool: pool, limits: newArtifactLimits(options)}
}

// Put stores a mapping once per project and UUID. The UPDATE on conflict makes
// RETURNING yield the existing row's digest, so identical bytes report
// created=false and different bytes under a taken UUID are refused, keeping a
// stored mapping immutable; a repeated upload counts as a use. A new mapping
// evicts the least recently used ones beyond the project's retention. A new mapping that would take the project
// past its quota fails with storage.ErrArtifactQuotaExceeded.
func (repository *proguardMappingRepository) Put(ctx context.Context, projectID int64, mappingUUID string, upload proguard.DecodedUpload) (bool, error) {
	parsed, err := uuid.Parse(mappingUUID)
	if err != nil {
		return false, fmt.Errorf("put proguard mapping: invalid uuid: %w", err)
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin proguard mapping put: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := repository.limits.lock(ctx, tx, projectID); err != nil {
		return false, err
	}
	var (
		storedHash []byte
		created    bool
	)
	err = tx.QueryRow(ctx, `
		INSERT INTO proguard_mappings (project_id, mapping_uuid, content_sha256, compressed_mapping, uncompressed_bytes)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (project_id, mapping_uuid)
		DO UPDATE SET last_used_at = now()
		RETURNING content_sha256, (xmax = 0)
	`, projectID, parsed, upload.SHA256[:], upload.Compressed, upload.UncompressedBytes).Scan(&storedHash, &created)
	if err != nil {
		return false, fmt.Errorf("put proguard mapping: %w", err)
	}
	if !bytes.Equal(storedHash, upload.SHA256[:]) {
		return false, fmt.Errorf("put proguard mapping: %w", proguard.ErrConflict)
	}
	if created {
		if err := repository.limits.evict(ctx, tx, projectID, evictProguardMappings); err != nil {
			return false, fmt.Errorf("put proguard mapping: %w", err)
		}
		if err := repository.limits.enforce(ctx, tx, projectID); err != nil {
			return false, fmt.Errorf("put proguard mapping: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit proguard mapping put: %w", err)
	}
	return created, nil
}

// Find loads a mapping within one project. mapping_uuid is a Postgres uuid
// column and pgx rejects a malformed string before the query is sent; an SDK
// can report anything in debug_meta, so a value that is not a UUID is "no such
// mapping" rather than a driver error.
func (repository *proguardMappingRepository) Find(ctx context.Context, projectID int64, mappingUUID string) (proguard.Stored, error) {
	parsed, err := uuid.Parse(mappingUUID)
	if err != nil {
		return proguard.Stored{}, fmt.Errorf("find proguard mapping: %w", proguard.ErrNotFound)
	}
	stored := proguard.Stored{UUID: parsed.String()}
	err = repository.pool.QueryRow(ctx, `
		SELECT compressed_mapping, uncompressed_bytes
		FROM proguard_mappings
		WHERE project_id = $1 AND mapping_uuid = $2
	`, projectID, parsed).Scan(&stored.Compressed, &stored.UncompressedBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return proguard.Stored{}, fmt.Errorf("find proguard mapping: %w", proguard.ErrNotFound)
	}
	if err != nil {
		return proguard.Stored{}, fmt.Errorf("find proguard mapping: %w", err)
	}
	return stored, nil
}

// MarkUsed moves a mapping to the front of the project's retention order.
func (repository *proguardMappingRepository) MarkUsed(ctx context.Context, projectID int64, mappingUUID string) error {
	parsed, err := uuid.Parse(mappingUUID)
	if err != nil {
		return fmt.Errorf("mark proguard mapping used: invalid uuid: %w", err)
	}
	if _, err := repository.pool.Exec(ctx, `
		UPDATE proguard_mappings SET last_used_at = now()
		WHERE project_id = $1 AND mapping_uuid = $2
	`, projectID, parsed); err != nil {
		return fmt.Errorf("mark proguard mapping used: %w", err)
	}
	return nil
}
