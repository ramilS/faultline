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

// ArtifactStoreOption configures the source-map and ProGuard/R8 mapping stores.
type ArtifactStoreOption func(*artifactQuota)

// WithArtifactQuota caps the uncompressed bytes of source maps and ProGuard/R8
// mappings one project may store. A limit of zero or less means no cap.
func WithArtifactQuota(limitBytes int64) ArtifactStoreOption {
	return func(quota *artifactQuota) { quota.limitBytes = limitBytes }
}

// artifactQuota bounds one project's stored artifacts across both tables. A
// project's uploads take an advisory lock so two concurrent uploads cannot both
// pass the check and together overshoot the cap.
type artifactQuota struct {
	limitBytes int64
}

func newArtifactQuota(options []ArtifactStoreOption) artifactQuota {
	var quota artifactQuota
	for _, option := range options {
		option(&quota)
	}
	return quota
}

// lock serializes the calling upload against every other upload of the project
// until tx ends. Project ids are bigint; see lockProjectForRuleMutation for why
// truncating to int32 is safe here.
func (q artifactQuota) lock(ctx context.Context, tx pgx.Tx, projectID int64) error {
	if q.limitBytes <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, artifactQuotaLockClassID, int32(projectID)); err != nil {
		return fmt.Errorf("lock project artifact storage: %w", err)
	}
	return nil
}

// enforce fails with storage.ErrArtifactQuotaExceeded when the project's stored
// artifacts, including rows tx has just written, exceed the cap. It runs after
// the insert and only when a row was actually created, so re-uploading content
// the project already holds never fails, even for a project over a lowered cap.
func (q artifactQuota) enforce(ctx context.Context, tx pgx.Tx, projectID int64) error {
	if q.limitBytes <= 0 {
		return nil
	}
	var usedBytes int64
	if err := tx.QueryRow(ctx, `
		SELECT (SELECT COALESCE(SUM(uncompressed_bytes), 0) FROM source_map_blobs WHERE project_id = $1)
		     + (SELECT COALESCE(SUM(uncompressed_bytes), 0) FROM proguard_mappings WHERE project_id = $1)
	`, projectID).Scan(&usedBytes); err != nil {
		return fmt.Errorf("measure project artifact storage: %w", err)
	}
	if usedBytes > q.limitBytes {
		return storage.ErrArtifactQuotaExceeded
	}
	return nil
}
