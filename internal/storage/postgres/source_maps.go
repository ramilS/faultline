package postgres

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/sourcemap"
)

type sourceMapRepository struct {
	pool   *pgxpool.Pool
	limits artifactLimits
}

var _ sourcemap.Repository = (*sourceMapRepository)(nil)

// NewSourceMapRepository returns a source-map adapter backed by pool.
func NewSourceMapRepository(pool *pgxpool.Pool, options ...ArtifactStoreOption) sourcemap.Repository {
	return &sourceMapRepository{pool: pool, limits: newArtifactLimits(options)}
}

// Put registers the release, stores the content once per project, and points a
// release+path ref at it, in one transaction. Repeated uploads of the same
// digest return the original ref; a different digest under the same release and
// path is rejected, preserving artifact immutability. Storing content the
// project does not hold yet fails with storage.ErrArtifactQuotaExceeded when it
// would take the project past its quota.
func (repository *sourceMapRepository) Put(ctx context.Context, projectID int64, version, generatedPath string, upload sourcemap.DecodedUpload) (sourcemap.Map, bool, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return sourcemap.Map{}, false, fmt.Errorf("begin source map put: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := repository.limits.lock(ctx, tx, projectID); err != nil {
		return sourcemap.Map{}, false, err
	}
	releaseID, err := upsertRelease(ctx, tx, projectID, version)
	if err != nil {
		return sourcemap.Map{}, false, err
	}
	blobID, blobCreated, err := upsertBlob(ctx, tx, projectID, upload)
	if err != nil {
		return sourcemap.Map{}, false, err
	}
	if blobCreated {
		if err := repository.limits.enforce(ctx, tx, projectID); err != nil {
			return sourcemap.Map{}, false, fmt.Errorf("put source map: %w", err)
		}
	}

	var existingHash []byte
	var refID int64
	var uploadedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO source_map_refs (blob_id, project_id, release_id, generated_path)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (release_id, generated_path) WHERE debug_id IS NULL DO NOTHING
		RETURNING id, uploaded_at
	`, blobID, projectID, releaseID, generatedPath).Scan(&refID, &uploadedAt)
	inserted := true
	if errors.Is(err, pgx.ErrNoRows) {
		inserted = false
		err = tx.QueryRow(ctx, `
			SELECT f.id, f.uploaded_at, b.content_sha256
			FROM source_map_refs AS f
			JOIN source_map_blobs AS b ON b.id = f.blob_id
			WHERE f.release_id = $1 AND f.generated_path = $2 AND f.debug_id IS NULL
		`, releaseID, generatedPath).Scan(&refID, &uploadedAt, &existingHash)
		if err != nil {
			return sourcemap.Map{}, false, translateSourceMapError("select existing source map ref", err)
		}
		if !bytes.Equal(existingHash, upload.SHA256[:]) {
			return sourcemap.Map{}, false, fmt.Errorf("put source map: %w", sourcemap.ErrConflict)
		}
	} else if err != nil {
		return sourcemap.Map{}, false, fmt.Errorf("insert source map ref: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return sourcemap.Map{}, false, fmt.Errorf("commit source map put: %w", err)
	}
	return sourcemap.Map{
		ID: refID, BlobID: blobID, ReleaseID: releaseID, GeneratedPath: generatedPath,
		ContentSHA256: upload.SHA256, CompressedMap: upload.Compressed,
		UncompressedBytes: int64(len(upload.JSON)), UploadedAt: uploadedAt.UTC(),
	}, inserted, nil
}

// upsertRelease inserts the release if it does not exist yet and returns its
// id either way, so a source map upload never needs a caller to have created
// the release first.
func upsertRelease(ctx context.Context, tx pgx.Tx, projectID int64, version string) (int64, error) {
	var releaseID int64
	if err := tx.QueryRow(ctx, `
		INSERT INTO releases (project_id, version) VALUES ($1, $2)
		ON CONFLICT (project_id, version) DO UPDATE SET version = EXCLUDED.version
		RETURNING id
	`, projectID, version).Scan(&releaseID); err != nil {
		return 0, fmt.Errorf("upsert release: %w", err)
	}
	return releaseID, nil
}

// upsertBlob stores content once per project and reports whether this call
// created it. The no-op UPDATE is what makes RETURNING yield a row on conflict;
// DO NOTHING would return none and force a second round trip. xmax is zero
// only on a freshly inserted row.
func upsertBlob(ctx context.Context, tx pgx.Tx, projectID int64, upload sourcemap.DecodedUpload) (int64, bool, error) {
	var (
		blobID  int64
		created bool
	)
	if err := tx.QueryRow(ctx, `
		INSERT INTO source_map_blobs (project_id, content_sha256, compressed_map, uncompressed_bytes)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (project_id, content_sha256)
		DO UPDATE SET content_sha256 = EXCLUDED.content_sha256
		RETURNING id, (xmax = 0)
	`, projectID, upload.SHA256[:], upload.Compressed, len(upload.JSON)).Scan(&blobID, &created); err != nil {
		return 0, false, fmt.Errorf("upsert source map blob: %w", err)
	}
	return blobID, created, nil
}

// FindByDebugID loads a debug-id-addressed map within one project. debug_id is
// a Postgres uuid column, and pgx rejects a malformed string before the query
// ever reaches the server — so a caller-supplied id that isn't a UUID (an SDK
// or attacker can send anything in debug_meta.images[].debug_id) is treated as
// "no such map" rather than surfacing a driver parse error, keeping this
// method's only "not found" signal ErrNotFound.
func (repository *sourceMapRepository) FindByDebugID(ctx context.Context, projectID int64, debugID string) (sourcemap.Map, error) {
	if _, err := uuid.Parse(debugID); err != nil {
		return sourcemap.Map{}, fmt.Errorf("find source map by debug id: %w", sourcemap.ErrNotFound)
	}
	return scanSourceMap(repository.pool.QueryRow(ctx, `
		SELECT f.id, f.blob_id, coalesce(f.release_id, 0), coalesce(f.generated_path, ''),
		       b.content_sha256, b.compressed_map, b.uncompressed_bytes, f.uploaded_at
		FROM source_map_refs AS f
		JOIN source_map_blobs AS b ON b.id = f.blob_id
		WHERE f.project_id = $1 AND f.debug_id = $2
	`, projectID, debugID))
}

// PutBundle stores every entry of one assembled artifact bundle. An entry
// yields a debug-id ref when it carries one, plus a release+path ref when the
// bundle carried a version and the entry carries a url — the schema CHECK
// forbids one row holding both addresses, so two rows share one blob. An
// entry may supply either address alone (a legacy url-only file, or a
// debug-id-only one with no version); one that supplies neither fails the
// whole bundle rather than silently writing nothing. Re-uploading an
// identical bundle is a no-op.
//
// The release+path ref overwrites on conflict rather than returning
// ErrConflict the way Put does. That is deliberate: Put's immutability promise
// is made to a caller who addressed a map by hand, while a bundler plugin
// legitimately re-uploads a changed build under an unchanged release name, and
// failing that upload would break the plugin's default path for a case Sentry
// itself allows.
func (repository *sourceMapRepository) PutBundle(ctx context.Context, projectID int64, version string, entries []sourcemap.BundleEntry) error {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin put bundle: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := repository.limits.lock(ctx, tx, projectID); err != nil {
		return err
	}
	var releaseID *int64
	if version != "" {
		id, err := upsertRelease(ctx, tx, projectID, version)
		if err != nil {
			return err
		}
		releaseID = &id
	}

	var blobCreated bool
	for _, entry := range entries {
		hasDebugID := entry.DebugID != ""
		hasReleasePath := releaseID != nil && entry.GeneratedPath != ""
		if !hasDebugID && !hasReleasePath {
			return fmt.Errorf("put bundle: entry for project %d supplies neither a debug id nor a release path", projectID)
		}

		blobID, created, err := upsertBlob(ctx, tx, projectID, entry.Upload)
		if err != nil {
			return err
		}
		blobCreated = blobCreated || created
		if hasDebugID {
			if _, err := tx.Exec(ctx, `
				INSERT INTO source_map_refs (blob_id, project_id, debug_id)
				VALUES ($1, $2, $3)
				ON CONFLICT (project_id, debug_id) WHERE debug_id IS NOT NULL
				DO UPDATE SET blob_id = EXCLUDED.blob_id, uploaded_at = now()
			`, blobID, projectID, entry.DebugID); err != nil {
				return fmt.Errorf("insert debug id ref: %w", err)
			}
		}
		if hasReleasePath {
			if _, err := tx.Exec(ctx, `
				INSERT INTO source_map_refs (blob_id, project_id, release_id, generated_path)
				VALUES ($1, $2, $3, $4)
				ON CONFLICT (release_id, generated_path) WHERE debug_id IS NULL
				DO UPDATE SET blob_id = EXCLUDED.blob_id, uploaded_at = now()
			`, blobID, projectID, *releaseID, entry.GeneratedPath); err != nil {
				return fmt.Errorf("insert release path ref: %w", err)
			}
		}
	}

	if blobCreated {
		if err := repository.limits.enforce(ctx, tx, projectID); err != nil {
			return fmt.Errorf("put bundle: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit put bundle: %w", err)
	}
	return nil
}

// Find loads a release+path-addressed map only when its release belongs to projectID.
func (repository *sourceMapRepository) Find(ctx context.Context, projectID int64, version, generatedPath string) (sourcemap.Map, error) {
	return scanSourceMap(repository.pool.QueryRow(ctx, `
		SELECT f.id, f.blob_id, f.release_id, f.generated_path, b.content_sha256,
		       b.compressed_map, b.uncompressed_bytes, f.uploaded_at
		FROM source_map_refs AS f
		JOIN source_map_blobs AS b ON b.id = f.blob_id
		JOIN releases AS r ON r.id = f.release_id
		WHERE f.project_id = $1 AND r.project_id = $1 AND r.version = $2
		  AND f.generated_path = $3 AND f.debug_id IS NULL
	`, projectID, version, generatedPath))
}

type sourceMapScanner interface {
	Scan(...any) error
}

func scanSourceMap(row sourceMapScanner) (sourcemap.Map, error) {
	var (
		stored     sourcemap.Map
		storedHash []byte
	)
	err := row.Scan(
		&stored.ID,
		&stored.BlobID,
		&stored.ReleaseID,
		&stored.GeneratedPath,
		&storedHash,
		&stored.CompressedMap,
		&stored.UncompressedBytes,
		&stored.UploadedAt,
	)
	if err != nil {
		return sourcemap.Map{}, translateSourceMapError("scan source map", err)
	}
	if len(storedHash) != len(stored.ContentSHA256) {
		return sourcemap.Map{}, fmt.Errorf("scan source map: stored content hash length is %d", len(storedHash))
	}
	copy(stored.ContentSHA256[:], storedHash)
	stored.UploadedAt = stored.UploadedAt.UTC()
	return stored, nil
}

func translateSourceMapError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, sourcemap.ErrNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
