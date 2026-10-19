package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/artifactcatalog"
)

type artifactCatalogRepository struct{ pool *pgxpool.Pool }

var _ artifactcatalog.Repository = (*artifactCatalogRepository)(nil)

func NewArtifactCatalogRepository(pool *pgxpool.Pool) artifactcatalog.Repository {
	return &artifactCatalogRepository{pool: pool}
}

type artifactCursor struct {
	UploadedAt time.Time            `json:"uploaded_at"`
	Type       artifactcatalog.Type `json:"type"`
	ID         int64                `json:"id"`
}

func encodeArtifactCursor(cursor artifactCursor) string {
	encoded, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(encoded)
}

func decodeArtifactCursor(value string) (artifactCursor, error) {
	var cursor artifactCursor
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(decoded, &cursor) != nil || cursor.UploadedAt.IsZero() || !cursor.Type.Valid() || cursor.ID <= 0 {
		return artifactCursor{}, artifactcatalog.ErrInvalidCursor
	}
	return cursor, nil
}

func (repository *artifactCatalogRepository) List(ctx context.Context, projectID int64, options artifactcatalog.ListOptions) (artifactcatalog.Page, error) {
	limit := options.Limit
	if limit <= 0 {
		limit = 50
	}
	var cursor artifactCursor
	if options.Cursor != "" {
		var err error
		cursor, err = decodeArtifactCursor(options.Cursor)
		if err != nil {
			return artifactcatalog.Page{}, err
		}
	}
	rows, err := repository.pool.Query(ctx, `
		WITH inventory AS (
			SELECT 'source_map'::text AS artifact_type, b.id, encode(b.content_sha256, 'hex') AS content_sha256,
			       NULL::text AS mapping_uuid, b.uncompressed_bytes AS size_bytes, b.created_at AS uploaded_at,
			       NULL::timestamptz AS last_used_at,
			       jsonb_agg(jsonb_build_object('release', coalesce(rel.version, ''), 'generated_path', coalesce(r.generated_path, ''), 'debug_id', coalesce(r.debug_id::text, '')) ORDER BY r.id) AS refs
			FROM source_map_blobs b
			JOIN source_map_refs r ON r.project_id = b.project_id AND r.blob_id = b.id
			LEFT JOIN releases rel ON rel.id = r.release_id AND rel.project_id = b.project_id
			WHERE b.project_id = $1
			GROUP BY b.id
			UNION ALL
			SELECT 'proguard_mapping', m.id, encode(m.content_sha256, 'hex'), m.mapping_uuid::text,
			       m.uncompressed_bytes, m.uploaded_at, m.last_used_at, '[]'::jsonb
			FROM proguard_mappings m WHERE m.project_id = $1
			UNION ALL
			SELECT 'native_debug_file', b.id, encode(b.content_sha256, 'hex'), NULL::text,
			       b.uncompressed_bytes, b.created_at, b.last_used_at,
			       jsonb_agg(jsonb_build_object('debug_id', r.debug_id::text, 'cpu_name', r.cpu_name) ORDER BY r.id)
			FROM native_debug_file_blobs b
			JOIN native_debug_file_refs r ON r.project_id = b.project_id AND r.blob_id = b.id
			WHERE b.project_id = $1
			GROUP BY b.id
		)
		SELECT artifact_type, id, content_sha256, mapping_uuid, size_bytes, uploaded_at, last_used_at, refs
		FROM inventory
		WHERE ($2 = '' OR artifact_type = $2)
		  AND ($3::timestamptz IS NULL OR (uploaded_at, artifact_type, id) < ($3, $4, $5))
		ORDER BY uploaded_at DESC, artifact_type DESC, id DESC
		LIMIT $6
	`, projectID, string(options.Type), nullableCursorTime(cursor), string(cursor.Type), cursor.ID, limit+1)
	if err != nil {
		return artifactcatalog.Page{}, fmt.Errorf("list artifact catalog: %w", err)
	}
	defer rows.Close()

	artifacts := make([]artifactcatalog.Artifact, 0, limit+1)
	for rows.Next() {
		var artifact artifactcatalog.Artifact
		var mappingUUID *string
		var refs []byte
		if err := rows.Scan(&artifact.Type, &artifact.ID, &artifact.ContentSHA256, &mappingUUID, &artifact.SizeBytes, &artifact.UploadedAt, &artifact.LastUsedAt, &refs); err != nil {
			return artifactcatalog.Page{}, fmt.Errorf("scan artifact catalog: %w", err)
		}
		if mappingUUID != nil {
			artifact.MappingUUID = *mappingUUID
		}
		artifact.UploadedAt = artifact.UploadedAt.UTC()
		if artifact.LastUsedAt != nil {
			value := artifact.LastUsedAt.UTC()
			artifact.LastUsedAt = &value
		}
		switch artifact.Type {
		case artifactcatalog.SourceMap:
			if err := json.Unmarshal(refs, &artifact.SourceMapRefs); err != nil {
				return artifactcatalog.Page{}, fmt.Errorf("decode source map references: %w", err)
			}
		case artifactcatalog.NativeDebugFile:
			if err := json.Unmarshal(refs, &artifact.NativeDebugIDRefs); err != nil {
				return artifactcatalog.Page{}, fmt.Errorf("decode native references: %w", err)
			}
		}
		artifacts = append(artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return artifactcatalog.Page{}, fmt.Errorf("list artifact catalog rows: %w", err)
	}
	page := artifactcatalog.Page{Artifacts: artifacts}
	if len(artifacts) > limit {
		last := artifacts[limit-1]
		page.Artifacts = artifacts[:limit]
		page.NextCursor = encodeArtifactCursor(artifactCursor{UploadedAt: last.UploadedAt, Type: last.Type, ID: last.ID})
	}
	return page, nil
}

func nullableCursorTime(cursor artifactCursor) any {
	if cursor.UploadedAt.IsZero() {
		return nil
	}
	return cursor.UploadedAt
}
