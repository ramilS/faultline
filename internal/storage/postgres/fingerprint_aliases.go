package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/ramilS/faultline/internal/storage"
)

// resolveFingerprintAliases returns fingerprint -> live issue id for the
// deltas it routes: to the issue the fingerprint was aliased to, else, when no
// issue has the fingerprint, to the first issue a legacy fingerprint has,
// recording that alias. One merged_into_id hop suffices because
// issueRepository.MergeIssue keeps tombstone chains flat.
func resolveFingerprintAliases(ctx context.Context, tx pgx.Tx, projectID int64, deltas []storage.IssueDelta) (map[string]int64, error) {
	var candidates []storage.IssueDelta
	for _, delta := range deltas {
		if len(delta.LegacyFingerprints) > 0 {
			candidates = append(candidates, delta)
		}
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	fingerprints := make([]string, len(candidates))
	for index, delta := range candidates {
		fingerprints[index] = delta.Fingerprint
	}
	targets, err := liveIssueIDs(ctx, tx, `
		SELECT a.fingerprint, COALESCE(i.merged_into_id, i.id)
		FROM issue_fingerprint_aliases a JOIN issues i ON i.id = a.issue_id
		WHERE a.project_id = $1 AND a.fingerprint = ANY($2)
	`, projectID, fingerprints)
	if err != nil {
		return nil, fmt.Errorf("resolve fingerprint aliases: %w", err)
	}

	var lookups []string
	for _, delta := range candidates {
		if _, ok := targets[delta.Fingerprint]; !ok {
			lookups = append(lookups, delta.Fingerprint)
			lookups = append(lookups, delta.LegacyFingerprints...)
		}
	}
	if len(lookups) == 0 {
		return targets, nil
	}
	existing, err := liveIssueIDs(ctx, tx, `
		SELECT fingerprint, COALESCE(merged_into_id, id) FROM issues
		WHERE project_id = $1 AND fingerprint = ANY($2)
	`, projectID, lookups)
	if err != nil {
		return nil, fmt.Errorf("resolve legacy fingerprints: %w", err)
	}

	var aliasFingerprints []string
	var aliasIssueIDs []int64
	for _, delta := range candidates {
		if _, ok := targets[delta.Fingerprint]; ok {
			continue
		}
		if _, ok := existing[delta.Fingerprint]; ok {
			continue
		}
		for _, legacy := range delta.LegacyFingerprints {
			if issueID, ok := existing[legacy]; ok {
				targets[delta.Fingerprint] = issueID
				aliasFingerprints = append(aliasFingerprints, delta.Fingerprint)
				aliasIssueIDs = append(aliasIssueIDs, issueID)
				break
			}
		}
	}
	if len(aliasFingerprints) == 0 {
		return targets, nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO issue_fingerprint_aliases (project_id, fingerprint, issue_id)
		SELECT $1, fingerprint, issue_id FROM unnest($2::char(64)[], $3::bigint[]) AS a(fingerprint, issue_id)
		ON CONFLICT (project_id, fingerprint) DO NOTHING
	`, projectID, aliasFingerprints, aliasIssueIDs); err != nil {
		return nil, fmt.Errorf("record fingerprint aliases: %w", err)
	}
	return targets, nil
}

func liveIssueIDs(ctx context.Context, tx pgx.Tx, query string, projectID int64, fingerprints []string) (map[string]int64, error) {
	rows, err := tx.Query(ctx, query, projectID, fingerprints)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make(map[string]int64)
	for rows.Next() {
		var (
			fingerprint string
			id          int64
		)
		if err := rows.Scan(&fingerprint, &id); err != nil {
			return nil, err
		}
		ids[fingerprint] = id
	}
	return ids, rows.Err()
}
