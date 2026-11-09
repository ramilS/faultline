//go:build integration

package postgres_test

import (
	"slices"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
)

func TestRefreshDisplayLabelsStoresOnlySymbolicatedLabels(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repository := storagepostgres.NewIssueRepository(pool)
	issueID := seedBareIssue(t, pool, projectID, hex64("labels"), "bu4: Source error")
	if _, err := pool.Exec(ctx, `UPDATE issues SET culprit = 'a.b.b' WHERE id = $1`, issueID); err != nil {
		t.Fatal(err)
	}
	display := func() (string, string) {
		t.Helper()
		detail, err := repository.Detail(ctx, access.Unrestricted(), issueID)
		if err != nil {
			t.Fatal(err)
		}
		return detail.Issue.DisplayTitle, detail.Issue.DisplayCulprit
	}

	steps := []struct {
		name                   string
		labels                 issue.Labels
		wantTitle, wantCulprit string
	}{
		{"labels equal to the raw ones are not stored", issue.Labels{Title: "bu4: Source error", Culprit: "a.b.b"}, "", ""},
		{"symbolicated labels are stored", issue.Labels{Title: "SourceException: Source error", Culprit: "Source.open (Source.kt)"}, "SourceException: Source error", "Source.open (Source.kt)"},
		{"an empty label keeps the stored one", issue.Labels{Culprit: "Source.read (Source.kt)"}, "SourceException: Source error", "Source.read (Source.kt)"},
	}
	for _, step := range steps {
		if err := repository.RefreshDisplayLabels(ctx, issueID, step.labels); err != nil {
			t.Fatalf("%s: %v", step.name, err)
		}
		if title, culprit := display(); title != step.wantTitle || culprit != step.wantCulprit {
			t.Fatalf("%s: display = %q / %q, want %q / %q", step.name, title, culprit, step.wantTitle, step.wantCulprit)
		}
	}
	row := queryIssue(t, ctx, pool, projectID, hex64("labels"))
	if row.id != issueID {
		t.Fatalf("issue %d no longer has its fingerprint", issueID)
	}
}

func TestUnlabelledIssuesListsIssuesWithoutDisplayLabels(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	store := storagepostgres.NewIssueLabelStore(pool)
	labelled := seedBareIssue(t, pool, projectID, hex64("labelled"), "bu4: boom")
	unlabelled := seedBareIssue(t, pool, projectID, hex64("unlabelled"), "TypeError: boom")
	if err := store.RefreshDisplayLabels(ctx, labelled, issue.Labels{Title: "SourceException: boom"}); err != nil {
		t.Fatal(err)
	}

	got, err := store.UnlabelledIssues(ctx, []int64{labelled, unlabelled, 999_999})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []int64{unlabelled}) {
		t.Fatalf("UnlabelledIssues = %v, want only %d", got, unlabelled)
	}
}

func TestPersistBatchHandsTheCommittedIssuesToAfterCommit(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	var committed []map[string]int64
	sink := storagepostgres.NewEventSinkWithOptions(pool, storagepostgres.EventSinkOptions{
		AfterCommit: func(_ storage.Batch, issueIDs map[string]int64) { committed = append(committed, issueIDs) },
	})
	at := time.Date(2026, 11, 9, 9, 0, 0, 0, time.UTC)

	persistEvent(t, ctx, sink, projectID, hex64("after-commit"), "error", "production", at)

	if len(committed) != 1 || committed[0][hex64("after-commit")] != issueIDFor(t, ctx, pool, projectID, hex64("after-commit")) {
		t.Fatalf("AfterCommit got %v, want the batch's issue once", committed)
	}

	failing := storage.Batch{ProjectID: projectID, Events: []storage.EventRow{newEventRow(projectID, hex64("orphan"), at)}}
	if err := sink.PersistBatch(ctx, failing); err == nil {
		t.Fatal("an event without its issue delta must fail the batch")
	}
	if len(committed) != 1 {
		t.Fatalf("AfterCommit ran %d times, want no call for a rolled-back batch", len(committed))
	}
}

func TestNotificationsCarryTheDisplayLabels(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "hook", Enabled: true, NotifyNew: true})
	persistEvent(t, ctx, storagepostgres.NewEventSink(pool), projectID, hex64("notify-labels"), "error", "production", time.Now())
	issueID := issueIDFor(t, ctx, pool, projectID, hex64("notify-labels"))
	if err := storagepostgres.NewIssueLabelStore(pool).RefreshDisplayLabels(ctx, issueID, issue.Labels{Title: "SourceException: boom", Culprit: "Source.open (Source.kt)"}); err != nil {
		t.Fatal(err)
	}

	claimed, err := storagepostgres.NewNotificationQueue(pool).Claim(ctx, time.Now(), 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %+v, %v; want one delivery", claimed, err)
	}
	n := claimed[0].Notification
	if n.Title != "boom" || n.Culprit != "app.js" || n.DisplayTitle != "SourceException: boom" || n.DisplayCulprit != "Source.open (Source.kt)" {
		t.Fatalf("notification = %+v, want raw and display labels", n)
	}
}
