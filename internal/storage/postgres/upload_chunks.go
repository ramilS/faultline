package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/sentrycli"
)

type uploadChunkStore struct {
	pool           *pgxpool.Pool
	maxStagedBytes int64
}

var _ sentrycli.ChunkStore = (*uploadChunkStore)(nil)

// NewUploadChunkStore returns a chunk staging adapter backed by pool. Chunks
// live in Postgres rather than on disk so a restart or a second replica does
// not strand a half-uploaded bundle.
func NewUploadChunkStore(pool *pgxpool.Pool) sentrycli.ChunkStore {
	return &uploadChunkStore{pool: pool, maxStagedBytes: sentrycli.MaxStagedBytesPerProject}
}

// Save stages chunks idempotently: sentry-cli retries, and one chunk can be
// referenced by several bundles, so a repeat upload must not be an error.
// It refuses a project already over its staging ceiling — see checkStagingQuota.
func (store *uploadChunkStore) Save(ctx context.Context, projectID int64, bodies map[sentrycli.Checksum][]byte) error {
	if err := store.checkStagingQuota(ctx, projectID); err != nil {
		return err
	}
	batch := &pgx.Batch{}
	for checksum, body := range bodies {
		batch.Queue(`
			INSERT INTO upload_chunks (project_id, checksum, body)
			VALUES ($1, $2, $3)
			ON CONFLICT (project_id, checksum) DO NOTHING
		`, projectID, checksum[:], body)
	}
	results := store.pool.SendBatch(ctx, batch)
	for range bodies {
		if _, err := results.Exec(); err != nil {
			// Close's error would only restate this one: it reports the same
			// terminal batch error once execution has already failed.
			_ = results.Close()
			return fmt.Errorf("save upload chunk: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("save upload chunk: %w", err)
	}
	return nil
}

// checkStagingQuota refuses a project that already holds its ceiling in
// staged bytes. It measures what is on disk now rather than what this request
// adds, because identical chunks collapse on (project_id, checksum): a retry
// of the same bundle re-writes the rows it already wrote instead of adding to
// them, which is what makes twice the largest assemblable bundle enough
// headroom. A project at or above the line is refused whether or not its
// request is a pure retry; the headroom is what keeps a retry from reaching
// the line, not this check.
//
// Only the never-assembled path reaches this ceiling — a successful assemble
// deletes the chunks it consumed — but that path needs one valid artifact
// token to fill the shared disk for every project on the instance, and
// DeleteExpired does not sweep for 24 hours.
func (store *uploadChunkStore) checkStagingQuota(ctx context.Context, projectID int64) error {
	var staged int64
	if err := store.pool.QueryRow(ctx, `
		SELECT coalesce(sum(octet_length(body)), 0) FROM upload_chunks WHERE project_id = $1
	`, projectID).Scan(&staged); err != nil {
		return fmt.Errorf("measure staged upload chunks: %w", err)
	}
	if staged >= store.maxStagedBytes {
		return fmt.Errorf("%w: project %d holds %d staged bytes against a ceiling of %d",
			sentrycli.ErrStagingQuotaExceeded, projectID, staged, store.maxStagedBytes)
	}
	return nil
}

// Load returns the subset of want this project has staged. A caller computes
// the missing set by difference: reporting it is how the assemble response
// tells sentry-cli what to re-upload.
func (store *uploadChunkStore) Load(ctx context.Context, projectID int64, want []sentrycli.Checksum) (map[sentrycli.Checksum][]byte, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT checksum, body FROM upload_chunks
		WHERE project_id = $1 AND checksum = ANY($2)
	`, projectID, checksumBytes(want))
	if err != nil {
		return nil, fmt.Errorf("load upload chunks: %w", err)
	}
	defer rows.Close()

	loaded := make(map[sentrycli.Checksum][]byte, len(want))
	for rows.Next() {
		var rawChecksum, body []byte
		if err := rows.Scan(&rawChecksum, &body); err != nil {
			return nil, fmt.Errorf("scan upload chunk: %w", err)
		}
		var checksum sentrycli.Checksum
		if len(rawChecksum) != len(checksum) {
			return nil, fmt.Errorf("scan upload chunk: stored checksum length is %d", len(rawChecksum))
		}
		copy(checksum[:], rawChecksum)
		loaded[checksum] = body
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load upload chunks: %w", err)
	}
	return loaded, nil
}

// Delete removes the chunks an assemble consumed, scoped to projectID so one
// project's assemble can never remove another project's staged chunk.
func (store *uploadChunkStore) Delete(ctx context.Context, projectID int64, used []sentrycli.Checksum) error {
	if _, err := store.pool.Exec(ctx, `
		DELETE FROM upload_chunks WHERE project_id = $1 AND checksum = ANY($2)
	`, projectID, checksumBytes(used)); err != nil {
		return fmt.Errorf("delete upload chunks: %w", err)
	}
	return nil
}

// DeleteExpired removes what an abandoned upload left behind. It is global
// rather than per project: an abandoned upload has no one left to trigger a
// scoped sweep. created_at is indexed (upload_chunks_gc_idx) so this is an
// index range scan, not a sequential scan of the table.
func (store *uploadChunkStore) DeleteExpired(ctx context.Context, before time.Time) (int64, error) {
	tag, err := store.pool.Exec(ctx, `DELETE FROM upload_chunks WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("delete expired upload chunks: %w", err)
	}
	return tag.RowsAffected(), nil
}

// checksumBytes converts sentrycli.Checksum values to the [][]byte form pgx
// needs for = ANY($n). The conversion is one-directional and lives only here:
// nothing in this file accepts a bare []byte where a Checksum is meant, so a
// source-map SHA-256 ([32]byte) cannot be passed where a chunk checksum
// ([20]byte) is expected — the two hash types never share a parameter.
func checksumBytes(checksums []sentrycli.Checksum) [][]byte {
	raw := make([][]byte, 0, len(checksums))
	for _, checksum := range checksums {
		raw = append(raw, checksum[:])
	}
	return raw
}
