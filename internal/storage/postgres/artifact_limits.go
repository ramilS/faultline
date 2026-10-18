package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ramilS/faultline/internal/storage"
)

// artifactQuotaLockClassID scopes the per-project upload lock to its own slice
// of Postgres' shared advisory-lock namespace; see groupingRuleLockClassID.
const artifactQuotaLockClassID int32 = 0x6152_5100

// ArtifactStoreOption configures all project-scoped artifact stores.
type ArtifactStoreOption func(*artifactLimits)

// WithArtifactQuota caps the stored (compressed) bytes of source maps,
// ProGuard/R8 mappings, and native debug files one project may hold.
func WithArtifactQuota(limitBytes int64) ArtifactStoreOption {
	return func(limits *artifactLimits) { limits.quotaBytes = limitBytes }
}

// WithDebugFileRetention keeps only the keep most recently used ProGuard/R8
// mappings, and separately the keep most recently used native debug files, of
// each project. Zero keeps everything.
func WithDebugFileRetention(keep int) ArtifactStoreOption {
	return func(limits *artifactLimits) { limits.keepDebugFiles = keep }
}

// artifactLimits bounds one project's stored artifacts. A project's uploads
// take an advisory lock so two concurrent uploads cannot both pass the quota
// check and together overshoot it, nor race each other's eviction.
type artifactLimits struct {
	quotaBytes     int64
	keepDebugFiles int
}

func newArtifactLimits(options []ArtifactStoreOption) artifactLimits {
	var limits artifactLimits
	for _, option := range options {
		option(&limits)
	}
	return limits
}

// lock serializes the calling upload against every other upload of the project
// until tx ends. Project ids are bigint; see lockProjectForRuleMutation for why
// truncating to int32 is safe here.
func (l artifactLimits) lock(ctx context.Context, tx pgx.Tx, projectID int64) error {
	if l.quotaBytes <= 0 && l.keepDebugFiles <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, artifactQuotaLockClassID, int32(projectID)); err != nil {
		return fmt.Errorf("lock project artifact storage: %w", err)
	}
	return nil
}

// enforce fails with storage.ErrArtifactQuotaExceeded when the project's stored
// artifacts, including rows tx has just written, exceed the cap. It counts the
// bytes on disk, the gzip each artifact is kept as: the quota protects storage,
// and an R8 mapping stores at under a tenth of its size. It runs after the
// insert and only when a row was actually created, so re-uploading content the
// project already holds never fails, even for a project over a lowered cap.
func (l artifactLimits) enforce(ctx context.Context, tx pgx.Tx, projectID int64) error {
	if l.quotaBytes <= 0 {
		return nil
	}
	var usedBytes int64
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT COALESCE(SUM(octet_length(compressed_map)), 0) FROM source_map_blobs WHERE project_id = $1)
		     + (SELECT COALESCE(SUM(octet_length(compressed_mapping)), 0) FROM proguard_mappings WHERE project_id = $1)
		     + (SELECT COALESCE(SUM(octet_length(compressed_file)), 0) FROM native_debug_file_blobs WHERE project_id = $1)
	`, projectID).Scan(&usedBytes); err != nil {
		return fmt.Errorf("measure project artifact storage: %w", err)
	}
	if usedBytes > l.quotaBytes {
		return storage.ErrArtifactQuotaExceeded
	}
	return nil
}

// evictProguardMappings and evictNativeDebugFiles delete what retention no
// longer keeps. Every release build uploads a mapping under a fresh UUID,
// including local ones nobody ships, so recency is last use, not upload: the
// mapping of the version users run keeps being used by its crashes and
// outlives a run of local builds. Deleting a native blob takes its debug-id
// refs with it (ON DELETE CASCADE).
const (
	evictProguardMappings = `
		DELETE FROM proguard_mappings WHERE id IN (
			SELECT id FROM proguard_mappings WHERE project_id = $1
			ORDER BY last_used_at DESC, id DESC OFFSET $2)`
	evictNativeDebugFiles = `
		DELETE FROM native_debug_file_blobs WHERE id IN (
			SELECT id FROM native_debug_file_blobs WHERE project_id = $1
			ORDER BY last_used_at DESC, id DESC OFFSET $2)`
)

// evict runs one of the eviction statements above when retention is on. It
// runs after the insert, so the upload itself, used most recently, is kept.
func (l artifactLimits) evict(ctx context.Context, tx pgx.Tx, projectID int64, statement string) error {
	if l.keepDebugFiles <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, statement, projectID, l.keepDebugFiles); err != nil {
		return fmt.Errorf("evict least recently used debug files: %w", err)
	}
	return nil
}
