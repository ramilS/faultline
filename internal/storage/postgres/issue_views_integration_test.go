//go:build integration

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func seedViewer(t *testing.T, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	var id int64
	mustScanRow(t, pool, []any{&id}, `
		INSERT INTO users (email, name, password_hash) VALUES ($1, $1, 'x') RETURNING id
	`, email)
	return id
}

func unseenByTitle(t *testing.T, ctx context.Context, repository issue.Repository, filters issue.ListFilters) map[string]bool {
	t.Helper()
	filters.Limit = 50
	filters.Sort = issue.SortLastSeen
	page, err := repository.List(ctx, filters)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	unseen := make(map[string]bool, len(page.Issues))
	for _, listed := range page.Issues {
		unseen[listed.Title] = listed.Unseen
	}
	return unseen
}

func TestIssueListUnseenIsPerViewer(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)
	web, _ := seedTwoProjectsWithAnIssueEach(t, pool)
	webIssue := issueIDForProjectSlug(t, pool, "web")
	alice := seedViewer(t, pool, "alice@example.com")
	bob := seedViewer(t, pool, "bob@example.com")
	scope := access.Limited([]int64{web})

	t.Run("an issue nobody opened is unseen", func(t *testing.T) {
		got := unseenByTitle(t, ctx, repository, issue.ListFilters{Scope: scope, ViewerID: alice})
		if !got["Web issue"] {
			t.Fatalf("unseen = %v, want Web issue unseen", got)
		}
	})

	if err := repository.RecordView(ctx, scope, alice, webIssue); err != nil {
		t.Fatalf("RecordView: %v", err)
	}

	t.Run("the member who opened it sees it as seen", func(t *testing.T) {
		got := unseenByTitle(t, ctx, repository, issue.ListFilters{Scope: scope, ViewerID: alice})
		if got["Web issue"] {
			t.Fatalf("unseen = %v, want Web issue seen for alice", got)
		}
	})

	t.Run("another member's view does not count", func(t *testing.T) {
		got := unseenByTitle(t, ctx, repository, issue.ListFilters{Scope: scope, ViewerID: bob})
		if !got["Web issue"] {
			t.Fatalf("unseen = %v, want Web issue still unseen for bob", got)
		}
	})
}

func TestIssueListUnseenReturnsOnlyOnRegression(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := postgres.NewIssueRepository(pool)
	viewer := seedViewer(t, pool, "viewer@example.com")
	fingerprint := hex64("Boom")
	seedIssueAt(t, ctx, pool, projectID, fingerprint, "Boom", nil, time.Now().UTC().Add(-time.Minute))
	issueID := issueIDForFingerprint(t, ctx, pool, fingerprint)
	scope := access.Unrestricted()
	filters := issue.ListFilters{ProjectSlug: testProjectSlug, Scope: scope, ViewerID: viewer}

	if err := repository.RecordView(ctx, scope, viewer, issueID); err != nil {
		t.Fatalf("RecordView: %v", err)
	}

	t.Run("new events after the view keep it seen", func(t *testing.T) {
		later := time.Now().UTC().Add(time.Minute)
		seedIssueAt(t, ctx, pool, projectID, fingerprint, "Boom", nil, later)
		got := unseenByTitle(t, ctx, repository, filters)
		if got["Boom"] {
			t.Fatalf("unseen = %v, want Boom seen after a non-regressing event", got)
		}
	})

	t.Run("a regression after the view makes it unseen again", func(t *testing.T) {
		if err := repository.UpdateStatus(ctx, scope, issueID, issue.StatusResolved); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		later := time.Now().UTC().Add(2 * time.Minute)
		seedIssueAt(t, ctx, pool, projectID, fingerprint, "Boom", nil, later)
		got := unseenByTitle(t, ctx, repository, filters)
		if !got["Boom"] {
			t.Fatalf("unseen = %v, want Boom unseen after it regressed", got)
		}
	})

	t.Run("opening the regressed issue makes it seen again", func(t *testing.T) {
		mustExec(t, pool, `UPDATE issues SET regressed_at = now() - interval '1 second' WHERE id = $1`, issueID)
		if err := repository.RecordView(ctx, scope, viewer, issueID); err != nil {
			t.Fatalf("RecordView: %v", err)
		}
		got := unseenByTitle(t, ctx, repository, filters)
		if got["Boom"] {
			t.Fatalf("unseen = %v, want Boom seen after reopening it", got)
		}
	})
}

func TestIssueListUnseenReturnsOnEscalation(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := postgres.NewIssueRepository(pool)
	viewer := seedViewer(t, pool, "viewer@example.com")
	fingerprint := hex64("Escalating")
	seedIssueAt(t, ctx, pool, projectID, fingerprint, "Escalating", nil, time.Now().UTC())
	issueID := issueIDForFingerprint(t, ctx, pool, fingerprint)
	scope := access.Unrestricted()
	filters := issue.ListFilters{ProjectSlug: testProjectSlug, Scope: scope, ViewerID: viewer}
	if err := repository.Ignore(ctx, scope, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 10}); err != nil {
		t.Fatalf("Ignore: %v", err)
	}
	if err := repository.RecordView(ctx, scope, viewer, issueID); err != nil {
		t.Fatalf("RecordView: %v", err)
	}

	t.Run("events short of the target keep it seen", func(t *testing.T) {
		seedIssueAt(t, ctx, pool, projectID, fingerprint, "Escalating", nil, time.Now().UTC())
		if got := unseenByTitle(t, ctx, repository, filters); got["Escalating"] {
			t.Fatalf("unseen = %v, want Escalating seen while still ignored", got)
		}
	})

	t.Run("the escalation makes it unseen again", func(t *testing.T) {
		mustExec(t, pool, `UPDATE issues SET ignore_until_times_seen = times_seen + 1 WHERE id = $1`, issueID)
		seedIssueAt(t, ctx, pool, projectID, fingerprint, "Escalating", nil, time.Now().UTC())
		if got := unseenByTitle(t, ctx, repository, filters); !got["Escalating"] {
			t.Fatalf("unseen = %v, want Escalating unseen after it escalated", got)
		}
	})
}

func TestIssueListOnlyUnseenComposesWithStatus(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := postgres.NewIssueRepository(pool)
	viewer := seedViewer(t, pool, "viewer@example.com")
	scope := access.Unrestricted()
	for _, title := range []string{"Unseen open", "Seen open", "Unseen resolved"} {
		seedIssue(t, ctx, pool, projectID, title, nil)
	}
	seenOpen := issueIDForFingerprint(t, ctx, pool, hex64("Seen open"))
	if err := repository.RecordView(ctx, scope, viewer, seenOpen); err != nil {
		t.Fatalf("RecordView: %v", err)
	}
	resolved := issueIDForFingerprint(t, ctx, pool, hex64("Unseen resolved"))
	if err := repository.UpdateStatus(ctx, scope, resolved, issue.StatusResolved); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	status := issue.Predicate{Field: issue.FieldStatus, Values: []string{"unresolved"}}

	t.Run("status alone keeps seen and unseen rows", func(t *testing.T) {
		got := unseenByTitle(t, ctx, repository, issue.ListFilters{
			ProjectSlug: testProjectSlug, Scope: scope, ViewerID: viewer,
			Predicates: []issue.Predicate{status},
		})
		if len(got) != 2 || !got["Unseen open"] || got["Seen open"] {
			t.Fatalf("unseen = %v, want Unseen open (unseen) and Seen open (seen)", got)
		}
	})

	t.Run("only unseen narrows the status filter to the viewer's unseen rows", func(t *testing.T) {
		got := unseenByTitle(t, ctx, repository, issue.ListFilters{
			ProjectSlug: testProjectSlug, Scope: scope, ViewerID: viewer,
			Predicates: []issue.Predicate{status, {Field: issue.FieldUnseen}},
		})
		if len(got) != 1 || !got["Unseen open"] {
			t.Fatalf("unseen = %v, want just Unseen open", got)
		}
	})
}

func TestRecordView(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	repository := postgres.NewIssueRepository(pool)
	web, _ := seedTwoProjectsWithAnIssueEach(t, pool)
	viewer := seedViewer(t, pool, "viewer@example.com")
	scope := access.Limited([]int64{web})
	webIssue := issueIDForProjectSlug(t, pool, "web")
	viewRows := func(issueID int64) int {
		t.Helper()
		var count int
		mustScanRow(t, pool, []any{&count}, `SELECT count(*) FROM issue_views WHERE issue_id = $1`, issueID)
		return count
	}

	t.Run("an out-of-scope issue is not found and nothing is written", func(t *testing.T) {
		foreign := issueIDForProjectSlug(t, pool, "secret")
		assertNotFound(t, repository.RecordView(ctx, scope, viewer, foreign))
		if rows := viewRows(foreign); rows != 0 {
			t.Fatalf("view rows = %d, want 0", rows)
		}
	})

	t.Run("a merged source is not found and nothing is written", func(t *testing.T) {
		source := seedBareIssue(t, pool, web, strings.Repeat("d", 64), "Merged away")
		if _, err := repository.MergeIssue(ctx, scope, source, webIssue); err != nil {
			t.Fatalf("MergeIssue: %v", err)
		}
		assertNotFound(t, repository.RecordView(ctx, scope, viewer, source))
		if rows := viewRows(source); rows != 0 {
			t.Fatalf("view rows = %d, want 0", rows)
		}
	})

	t.Run("a repeat open only moves viewed_at forward", func(t *testing.T) {
		if err := repository.RecordView(ctx, scope, viewer, webIssue); err != nil {
			t.Fatalf("RecordView: %v", err)
		}
		future := time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond)
		mustExec(t, pool, `UPDATE issue_views SET viewed_at = $1 WHERE issue_id = $2`, future, webIssue)
		if err := repository.RecordView(ctx, scope, viewer, webIssue); err != nil {
			t.Fatalf("RecordView: %v", err)
		}
		var viewedAt time.Time
		mustScanRow(t, pool, []any{&viewedAt}, `SELECT viewed_at FROM issue_views WHERE issue_id = $1`, webIssue)
		if !viewedAt.Equal(future) {
			t.Fatalf("viewed_at = %v, want it kept at %v", viewedAt, future)
		}
		if rows := viewRows(webIssue); rows != 1 {
			t.Fatalf("view rows = %d, want 1", rows)
		}
	})
}
