//go:build integration

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
)

// seedProject inserts a second project into the schema issueTestEnv already
// migrated, returning its id.
func seedProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	var projectID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ($1, $1, 'javascript') RETURNING id
	`, slug).Scan(&projectID); err != nil {
		t.Fatalf("seed project %s: %v", slug, err)
	}
	return projectID
}

// TestTagKeysAndTagValuesAreScopedByProject is the isolation test this task
// exists to prove: itv_lookup_idx (key, value, issue_id) has no project_id
// column, so a query that filtered on key/value alone would silently return
// every project's tag vocabulary. Both facet queries must join through
// issue_tag_values.project_id (itv_project_idx) instead.
//
// It also seeds environment/release alongside an ordinary "browser" key to
// pin TagKeys' other exclusion in the same breath: those two reserved keys
// have dedicated Environment/Release controls and must never surface in the
// generic tag-key vocabulary, in any project, while an ordinary key like
// "browser" still does. TagValues stays reachable for the reserved keys
// directly (see the assertions below) — the dedicated controls call it with
// an explicit "environment"/"release" key, so only TagKeys excludes them.
func TestTagKeysAndTagValuesAreScopedByProject(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	otherProjectID := seedProject(t, ctx, pool, "other")
	repository := storagepostgres.NewIssueRepository(pool)

	seedIssue(t, ctx, pool, projectID, "Web Issue", map[string]string{
		"environment": "production",
		"release":     "1.0.0",
		"browser":     "chrome",
	})
	seedIssue(t, ctx, pool, otherProjectID, "Other Issue", map[string]string{
		"environment": "staging",
		"release":     "9.9.9",
		"browser":     "firefox",
	})

	keys, err := repository.TagKeys(ctx, access.Unrestricted(), testProjectSlug)
	if err != nil {
		t.Fatalf("TagKeys: %v", err)
	}
	seenKeys := make(map[string]bool)
	for _, key := range keys {
		seenKeys[key.Key] = true
	}
	if seenKeys["environment"] || seenKeys["release"] {
		t.Fatalf("keys = %+v, must not include the reserved environment/release keys", keys)
	}
	if !seenKeys["browser"] {
		t.Fatalf("keys = %+v, want the ordinary browser key present", keys)
	}

	values, err := repository.TagValues(ctx, access.Unrestricted(), testProjectSlug, "environment", "", 100)
	if err != nil {
		t.Fatalf("TagValues: %v", err)
	}
	for _, value := range values {
		if value.Value == "staging" {
			t.Fatalf("TagValues leaked other project's value: %+v", values)
		}
	}
	if len(values) != 1 || values[0].Value != "production" {
		t.Fatalf("values = %+v, want only [production]", values)
	}

	otherValues, err := repository.TagValues(ctx, access.Unrestricted(), "other", "environment", "", 100)
	if err != nil {
		t.Fatalf("TagValues for other project: %v", err)
	}
	if len(otherValues) != 1 || otherValues[0].Value != "staging" {
		t.Fatalf("other project values = %+v, want only [staging]", otherValues)
	}
}

// TestTagKeysExcludesReservedEnvironmentAndReleaseKeys is a focused pin,
// independent of the scoping test above, for the exclusion itself: a project
// whose only tags are environment/release must get back an empty tag-key
// vocabulary rather than surfacing either reserved key.
func TestTagKeysExcludesReservedEnvironmentAndReleaseKeys(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	seedIssue(t, ctx, pool, projectID, "Reserved Only", map[string]string{
		"environment": "production",
		"release":     "2.0.0",
	})

	keys, err := repository.TagKeys(ctx, access.Unrestricted(), testProjectSlug)
	if err != nil {
		t.Fatalf("TagKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Fatalf("keys = %+v, want none: environment/release are the only tags and both are reserved", keys)
	}
}

// TestTagValuesStaysReachableForReservedKeys documents the deliberate
// asymmetry with TagKeys: the dedicated Environment/Release filter controls
// call TagValues directly with an explicit "environment"/"release" key (see
// FacetFilterIfPopulated in IssueFilterBar.tsx), so TagValues must keep
// answering those keys even though TagKeys no longer lists them.
func TestTagValuesStaysReachableForReservedKeys(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	seedIssue(t, ctx, pool, projectID, "Reserved Values", map[string]string{
		"environment": "production",
		"release":     "2.0.0",
	})

	environments, err := repository.TagValues(ctx, access.Unrestricted(), testProjectSlug, "environment", "", 100)
	if err != nil {
		t.Fatalf("TagValues(environment): %v", err)
	}
	if len(environments) != 1 || environments[0].Value != "production" {
		t.Fatalf("environments = %+v, want [production]", environments)
	}

	releases, err := repository.TagValues(ctx, access.Unrestricted(), testProjectSlug, "release", "", 100)
	if err != nil {
		t.Fatalf("TagValues(release): %v", err)
	}
	if len(releases) != 1 || releases[0].Value != "2.0.0" {
		t.Fatalf("releases = %+v, want [2.0.0]", releases)
	}
}

// TestTagValuesEmptyQueryReturnsTopValues verifies a blank prefix matches
// every value (top values by occurrence), rather than matching nothing.
func TestTagValuesEmptyQueryReturnsTopValues(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	seedIssue(t, ctx, pool, projectID, "Chrome Issue", map[string]string{"browser": "chrome"})
	seedIssue(t, ctx, pool, projectID, "Firefox Issue", map[string]string{"browser": "firefox"})

	values, err := repository.TagValues(ctx, access.Unrestricted(), testProjectSlug, "browser", "", 100)
	if err != nil {
		t.Fatalf("TagValues: %v", err)
	}
	if len(values) != 2 {
		t.Fatalf("values = %+v, want 2 entries for an empty prefix", values)
	}
}

// TestTagValuesOrdersTiesByValueForStability guards autocomplete against
// jumping around between keystrokes: equal times_seen must break ties
// alphabetically rather than by an unspecified row order.
func TestTagValuesOrdersTiesByValueForStability(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)

	seedIssue(t, ctx, pool, projectID, "Zulu Issue", map[string]string{"browser": "zulu"})
	seedIssue(t, ctx, pool, projectID, "Alpha Issue", map[string]string{"browser": "alpha"})

	values, err := repository.TagValues(ctx, access.Unrestricted(), testProjectSlug, "browser", "", 100)
	if err != nil {
		t.Fatalf("TagValues: %v", err)
	}
	if len(values) != 2 || values[0].Value != "alpha" || values[1].Value != "zulu" {
		t.Fatalf("values = %+v, want [alpha, zulu] tie-broken alphabetically", values)
	}
}

// TestTagValuesExcludeMergedAwayIssueRows proves a merge does not double-count:
// MergeIssue folds the source's tag rows into the target and deletes the
// source's own rows, so the facet total reflects the sum exactly once.
func TestTagValuesExcludeMergedAwayIssueRows(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	now := time.Now().UTC()

	seedIssueAt(t, ctx, pool, projectID, hex64("merge-source"), "Merge Source",
		map[string]string{"environment": "production"}, now.Add(-time.Hour))
	seedIssueAt(t, ctx, pool, projectID, hex64("merge-target"), "Merge Target",
		map[string]string{"environment": "production"}, now)

	sourceID := issueIDForFingerprint(t, ctx, pool, hex64("merge-source"))
	targetID := issueIDForFingerprint(t, ctx, pool, hex64("merge-target"))

	if _, err := repository.MergeIssue(ctx, access.Unrestricted(), sourceID, targetID); err != nil {
		t.Fatalf("MergeIssue: %v", err)
	}

	values, err := repository.TagValues(ctx, access.Unrestricted(), testProjectSlug, "environment", "", 100)
	if err != nil {
		t.Fatalf("TagValues: %v", err)
	}
	if len(values) != 1 {
		t.Fatalf("values = %+v, want one merged production entry", values)
	}
	if values[0].TimesSeen != 2 {
		t.Fatalf("times_seen = %d, want 2 (1+1, counted once after fold)", values[0].TimesSeen)
	}
}
