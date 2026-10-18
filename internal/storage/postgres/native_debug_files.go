package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/native"
)

type nativeDebugFileRepository struct {
	pool   *pgxpool.Pool
	limits artifactLimits
}

func NewNativeDebugFileRepository(pool *pgxpool.Pool, options ...ArtifactStoreOption) native.Repository {
	return &nativeDebugFileRepository{pool: pool, limits: newArtifactLimits(options)}
}

func (repository *nativeDebugFileRepository) Put(ctx context.Context, projectID int64, upload native.Upload) error {
	if len(upload.Objects) == 0 {
		return fmt.Errorf("put native debug file: %w", native.ErrInvalidDebugFile)
	}
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin native debug file put: %w", err)
	}
	defer tx.Rollback(ctx)
	if err := repository.limits.lock(ctx, tx, projectID); err != nil {
		return err
	}
	var blobID int64
	var created bool
	if err := tx.QueryRow(ctx, `
		INSERT INTO native_debug_file_blobs (project_id, content_sha256, compressed_file, uncompressed_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, content_sha256) DO UPDATE SET last_used_at = now()
		RETURNING id, (xmax = 0)
	`, projectID, upload.ContentSHA256[:], upload.Compressed, upload.UncompressedBytes).Scan(&blobID, &created); err != nil {
		return fmt.Errorf("upsert native debug file blob: %w", err)
	}
	for _, object := range upload.Objects {
		parsed, err := uuid.Parse(object.DebugID)
		if err != nil {
			return fmt.Errorf("put native debug file: %w", native.ErrInvalidDebugFile)
		}
		var existingBlobID int64
		var existingHash []byte
		err = tx.QueryRow(ctx, `
			INSERT INTO native_debug_file_refs (project_id, blob_id, debug_id, object_index, cpu_name, vmaddr)
			VALUES ($1, $2, $3, $4, $5, $6)
			ON CONFLICT (project_id, debug_id) DO NOTHING
			RETURNING blob_id
		`, projectID, blobID, parsed, object.Index, object.CPU, strconv.FormatUint(object.VMAddr, 10)).Scan(&existingBlobID)
		if errors.Is(err, pgx.ErrNoRows) {
			err = tx.QueryRow(ctx, `
				SELECT r.blob_id, b.content_sha256 FROM native_debug_file_refs r
				JOIN native_debug_file_blobs b ON b.project_id = r.project_id AND b.id = r.blob_id
				WHERE r.project_id = $1 AND r.debug_id = $2
			`, projectID, parsed).Scan(&existingBlobID, &existingHash)
			if err != nil {
				return fmt.Errorf("select native debug id: %w", err)
			}
			if !bytes.Equal(existingHash, upload.ContentSHA256[:]) {
				return fmt.Errorf("put native debug file: %w", native.ErrConflict)
			}
		} else if err != nil {
			return fmt.Errorf("insert native debug id: %w", err)
		}
	}
	if created {
		if err := repository.limits.evict(ctx, tx, projectID, evictNativeDebugFiles); err != nil {
			return fmt.Errorf("put native debug file: %w", err)
		}
		if err := repository.limits.enforce(ctx, tx, projectID); err != nil {
			return fmt.Errorf("put native debug file: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit native debug file put: %w", err)
	}
	return nil
}

func (repository *nativeDebugFileRepository) Find(ctx context.Context, projectID int64, debugID string) (native.Stored, error) {
	parsed, err := uuid.Parse(debugID)
	if err != nil {
		return native.Stored{}, fmt.Errorf("find native debug file: %w", native.ErrNotFound)
	}
	stored := native.Stored{DebugID: parsed.String()}
	var vmaddr string
	err = repository.pool.QueryRow(ctx, `
		SELECT r.blob_id, r.object_index, r.vmaddr::text, b.compressed_file, b.uncompressed_bytes
		FROM native_debug_file_refs r
		JOIN native_debug_file_blobs b ON b.project_id = r.project_id AND b.id = r.blob_id
		WHERE r.project_id = $1 AND r.debug_id = $2
	`, projectID, parsed).Scan(&stored.BlobID, &stored.ObjectIndex, &vmaddr, &stored.Compressed, &stored.UncompressedBytes)
	if errors.Is(err, pgx.ErrNoRows) {
		return native.Stored{}, fmt.Errorf("find native debug file: %w", native.ErrNotFound)
	}
	if err != nil {
		return native.Stored{}, fmt.Errorf("find native debug file: %w", err)
	}
	stored.VMAddr, err = strconv.ParseUint(vmaddr, 10, 64)
	if err != nil {
		return native.Stored{}, fmt.Errorf("find native debug file: invalid stored vmaddr: %w", err)
	}
	return stored, nil
}

// MarkUsed moves the debug file holding debugID to the front of the project's
// retention order.
func (repository *nativeDebugFileRepository) MarkUsed(ctx context.Context, projectID int64, debugID string) error {
	parsed, err := uuid.Parse(debugID)
	if err != nil {
		return fmt.Errorf("mark native debug file used: invalid debug id: %w", err)
	}
	if _, err := repository.pool.Exec(ctx, `
		UPDATE native_debug_file_blobs b SET last_used_at = now()
		FROM native_debug_file_refs r
		WHERE r.project_id = $1 AND r.debug_id = $2 AND b.project_id = r.project_id AND b.id = r.blob_id
	`, projectID, parsed); err != nil {
		return fmt.Errorf("mark native debug file used: %w", err)
	}
	return nil
}
