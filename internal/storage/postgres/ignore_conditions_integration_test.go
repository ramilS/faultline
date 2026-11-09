//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
)

func persistOccurrences(t *testing.T, ctx context.Context, sink storage.EventSink, projectID int64, fingerprint string, events int, users []string) {
	t.Helper()
	at := time.Now().UTC()
	rows := make([]storage.EventRow, events)
	for index := range rows {
		rows[index] = newEventRow(projectID, fingerprint, at)
	}
	identities := make([]storage.UserDelta, len(users))
	for index, email := range users {
		identities[index] = storage.UserDelta{Key: "email", Value: email, Count: 1, FirstSeen: at, LastSeen: at}
	}
	err := sink.PersistBatch(ctx, storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "boom", Culprit: "app.js", Level: "error",
			Count: int64(events), FirstSeen: at, LastSeen: at, Users: identities,
		}},
		Events: rows,
	})
	if err != nil {
		t.Fatalf("persist batch: %v", err)
	}
}

func emails(prefix string, count int) []string {
	list := make([]string, count)
	for index := range list {
		list[index] = fmt.Sprintf("%s-%d@example.com", prefix, index)
	}
	return list
}

type reopenMarks struct {
	status      string
	escalatedAt *time.Time
	regressedAt *time.Time
}

func readReopenMarks(t *testing.T, pool *pgxpool.Pool, issueID int64) reopenMarks {
	t.Helper()
	var marks reopenMarks
	mustScanRow(t, pool, []any{&marks.status, &marks.escalatedAt, &marks.regressedAt},
		`SELECT status::text, escalated_at, regressed_at FROM issues WHERE id = $1`, issueID)
	return marks
}

func detailOf(t *testing.T, ctx context.Context, repository issue.Repository, issueID int64) issue.Detail {
	t.Helper()
	detail, err := repository.Detail(ctx, access.Unrestricted(), issueID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	return detail
}

func ignoreIssue(t *testing.T, ctx context.Context, repository issue.Repository, issueID int64, condition issue.IgnoreCondition) {
	t.Helper()
	if err := repository.Ignore(ctx, access.Unrestricted(), issueID, condition); err != nil {
		t.Fatalf("ignore: %v", err)
	}
}

type escalationEnv struct {
	ctx         context.Context
	pool        *pgxpool.Pool
	projectID   int64
	sink        storage.EventSink
	repository  issue.Repository
	regressions notify.Channel
}

func newEscalationEnv(t *testing.T) escalationEnv {
	t.Helper()
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	regressions := createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "regressions", Enabled: true, NotifyRegression: true})
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "new-only", Enabled: true, NotifyNew: true})
	return escalationEnv{
		ctx: ctx, pool: pool, projectID: projectID,
		sink:        storagepostgres.NewEventSink(pool),
		repository:  storagepostgres.NewIssueRepository(pool),
		regressions: regressions,
	}
}

func (env escalationEnv) seed(t *testing.T, name string, events int, users []string) (string, int64) {
	t.Helper()
	fingerprint := hex64(name)
	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, events, users)
	clearDeliveries(t, env.ctx, env.pool)
	return fingerprint, issueIDFor(t, env.ctx, env.pool, env.projectID, fingerprint)
}

func (env escalationEnv) wantOneEscalation(t *testing.T, issueID int64) {
	t.Helper()
	want := []queuedDelivery{{env.regressions.ID, issueID, string(notify.TriggerEscalation), "production"}}
	if got := queuedDeliveries(t, env.ctx, env.pool); !equalDeliveries(got, want) {
		t.Fatalf("deliveries = %+v, want %+v", got, want)
	}
}

func (env escalationEnv) wantNoDeliveries(t *testing.T) {
	t.Helper()
	if got := queuedDeliveries(t, env.ctx, env.pool); len(got) != 0 {
		t.Fatalf("deliveries = %+v, want none", got)
	}
}

func wantEscalated(t *testing.T, pool *pgxpool.Pool, issueID int64) {
	t.Helper()
	marks := readReopenMarks(t, pool, issueID)
	if marks.status != "unresolved" || marks.escalatedAt == nil || marks.regressedAt != nil {
		t.Fatalf("marks = status %q, escalated_at %v, regressed_at %v; want unresolved, escalated, not regressed",
			marks.status, marks.escalatedAt, marks.regressedAt)
	}
}

func wantStillIgnored(t *testing.T, pool *pgxpool.Pool, issueID int64) {
	t.Helper()
	if marks := readReopenMarks(t, pool, issueID); marks.status != "ignored" || marks.escalatedAt != nil {
		t.Fatalf("marks = status %q, escalated_at %v; want still ignored", marks.status, marks.escalatedAt)
	}
}

func TestIgnoreForeverNeverReopens(t *testing.T) {
	env := newEscalationEnv(t)
	fingerprint, issueID := env.seed(t, "ignore-forever", 1, nil)
	if err := env.repository.UpdateStatus(env.ctx, access.Unrestricted(), issueID, issue.StatusIgnored); err != nil {
		t.Fatalf("ignore: %v", err)
	}

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 1000, emails("forever", 50))

	wantStillIgnored(t, env.pool, issueID)
	env.wantNoDeliveries(t)
	if ignore := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; ignore != nil {
		t.Fatalf("ignore until = %+v, want none for an ignore without a condition", ignore)
	}
}

func TestIgnoreUntilMoreEventsReopensInTheBatchThatCrossesTheTarget(t *testing.T) {
	env := newEscalationEnv(t)
	fingerprint, issueID := env.seed(t, "ignore-events", 40, nil)
	ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 100})

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 99, nil)

	wantStillIgnored(t, env.pool, issueID)
	env.wantNoDeliveries(t)
	want := issue.ActiveIgnore{Kind: issue.IgnoreForEvents, Count: 100, SoFar: 99}
	if got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; got == nil || *got != want {
		t.Fatalf("at 139 events, ignore until = %+v, want %+v", got, want)
	}

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 2, nil)

	wantEscalated(t, env.pool, issueID)
	env.wantOneEscalation(t, issueID)
	if got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; got != nil {
		t.Fatalf("after reopening, ignore until = %+v, want cleared", got)
	}

	clearDeliveries(t, env.ctx, env.pool)
	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 5, nil)
	env.wantNoDeliveries(t)
}

func TestIgnoreUntilMoreEventsReopensWhenOneBatchCarriesTheWholeThreshold(t *testing.T) {
	env := newEscalationEnv(t)
	fingerprint, issueID := env.seed(t, "ignore-events-one-batch", 40, nil)
	ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 100})

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 101, nil)

	wantEscalated(t, env.pool, issueID)
	env.wantOneEscalation(t, issueID)
	page, err := env.repository.List(env.ctx, issue.ListFilters{
		ProjectSlug: testProjectSlug, Scope: access.Unrestricted(), Sort: issue.SortLastSeen, Limit: 10,
		Predicates: []issue.Predicate{{Field: issue.FieldStatus, Values: []string{"unresolved"}}},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(page.Issues) != 1 || page.Issues[0].ID != issueID || !page.Issues[0].Unseen {
		t.Fatalf("unresolved list = %+v, want the escalated issue, unseen", page.Issues)
	}
}

func TestIgnoreUntilMoreUsersReopensInTheBatchThatAddsTheLastUser(t *testing.T) {
	env := newEscalationEnv(t)
	fingerprint, issueID := env.seed(t, "ignore-users", 3, emails("before", 2))
	ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForUsers, Count: 10})

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 9, emails("after", 9))

	wantStillIgnored(t, env.pool, issueID)
	want := issue.ActiveIgnore{Kind: issue.IgnoreForUsers, Count: 10, SoFar: 9}
	if got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; got == nil || *got != want {
		t.Fatalf("after 9 new users, ignore until = %+v, want %+v", got, want)
	}

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 2, []string{"before-0@example.com", "tenth@example.com"})

	wantEscalated(t, env.pool, issueID)
	env.wantOneEscalation(t, issueID)
}

func TestIgnoreForADurationReopensOnlyOnAnEventAfterItExpires(t *testing.T) {
	env := newEscalationEnv(t)
	fingerprint, issueID := env.seed(t, "ignore-duration", 1, nil)
	ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForDuration, Duration: 24 * time.Hour})

	got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil
	if got == nil || got.Kind != issue.IgnoreForDuration || got.Until.Sub(time.Now().Add(24*time.Hour)).Abs() > time.Minute {
		t.Fatalf("ignore until = %+v, want a duration ending 24 hours from now", got)
	}

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 1, nil)
	wantStillIgnored(t, env.pool, issueID)

	mustExec(t, env.pool, `UPDATE issues SET ignored_until = now() - interval '2 days' WHERE id = $1`, issueID)
	wantStillIgnored(t, env.pool, issueID)
	env.wantNoDeliveries(t)

	persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 1, nil)

	wantEscalated(t, env.pool, issueID)
	env.wantOneEscalation(t, issueID)
}

func TestEscalationReachesTheMergeTargetThroughATombstone(t *testing.T) {
	env := newEscalationEnv(t)
	source, sourceID := env.seed(t, "ignore-merge-source", 1, nil)
	_, targetID := env.seed(t, "ignore-merge-target", 1, nil)
	mustExec(t, env.pool, `UPDATE issues SET merged_into_id = $2 WHERE id = $1`, sourceID, targetID)
	ignoreIssue(t, env.ctx, env.repository, targetID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 10})

	persistOccurrences(t, env.ctx, env.sink, env.projectID, source, 10, nil)

	wantEscalated(t, env.pool, targetID)
	env.wantOneEscalation(t, targetID)
}

func TestAManualStatusChangeReplacesTheIgnoreCondition(t *testing.T) {
	env := newEscalationEnv(t)
	scope := access.Unrestricted()

	t.Run("resolving clears it and the next event is a regression", func(t *testing.T) {
		fingerprint, issueID := env.seed(t, "ignore-then-resolve", 1, nil)
		ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 10})
		if err := env.repository.UpdateStatus(env.ctx, scope, issueID, issue.StatusResolved); err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; got != nil {
			t.Fatalf("after resolving, ignore until = %+v, want cleared", got)
		}

		persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 1, nil)

		marks := readReopenMarks(t, env.pool, issueID)
		if marks.status != "unresolved" || marks.regressedAt == nil || marks.escalatedAt != nil {
			t.Fatalf("marks = %+v, want a regression, not an escalation", marks)
		}
		want := []queuedDelivery{{env.regressions.ID, issueID, string(notify.TriggerRegression), "production"}}
		if got := queuedDeliveries(t, env.ctx, env.pool); !equalDeliveries(got, want) {
			t.Fatalf("deliveries = %+v, want %+v", got, want)
		}
	})

	t.Run("ignoring again replaces the condition", func(t *testing.T) {
		_, issueID := env.seed(t, "ignore-twice", 5, nil)
		ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 10})
		ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForUsers, Count: 100})

		want := issue.ActiveIgnore{Kind: issue.IgnoreForUsers, Count: 100}
		if got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; got == nil || *got != want {
			t.Fatalf("ignore until = %+v, want %+v", got, want)
		}
	})

	t.Run("ignoring without a condition makes it forever", func(t *testing.T) {
		fingerprint, issueID := env.seed(t, "ignore-then-forever", 1, nil)
		ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 10})
		if err := env.repository.UpdateStatus(env.ctx, scope, issueID, issue.StatusIgnored); err != nil {
			t.Fatalf("ignore forever: %v", err)
		}

		persistOccurrences(t, env.ctx, env.sink, env.projectID, fingerprint, 20, nil)

		wantStillIgnored(t, env.pool, issueID)
		env.wantNoDeliveries(t)
	})

	t.Run("unignoring clears it", func(t *testing.T) {
		_, issueID := env.seed(t, "ignore-then-unignore", 1, nil)
		ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForDuration, Duration: time.Hour})
		if err := env.repository.UpdateStatus(env.ctx, scope, issueID, issue.StatusUnresolved); err != nil {
			t.Fatalf("unignore: %v", err)
		}
		if got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; got != nil {
			t.Fatalf("after unignoring, ignore until = %+v, want cleared", got)
		}
	})
}

func TestIgnoreRefusesAUsersTargetPastTheIdentityCap(t *testing.T) {
	env := newEscalationEnv(t)
	_, issueID := env.seed(t, "ignore-users-cap", 1, nil)

	mustExec(t, env.pool, `UPDATE issues SET users_seen = $2 WHERE id = $1`, issueID, ingest.MaxUsersPerIssue-9)
	err := env.repository.Ignore(env.ctx, access.Unrestricted(), issueID, issue.IgnoreCondition{Kind: issue.IgnoreForUsers, Count: 10})
	if conditionError, ok := errors.AsType[*issue.IgnoreConditionError](err); !ok || conditionError.Field != "users" {
		t.Fatalf("ignore past the cap err = %v, want an IgnoreConditionError naming users", err)
	}
	if marks := readReopenMarks(t, env.pool, issueID); marks.status != "unresolved" {
		t.Fatalf("status after a refused ignore = %q, want unresolved", marks.status)
	}
	if got := detailOf(t, env.ctx, env.repository, issueID).Issue.IgnoreUntil; got != nil {
		t.Fatalf("ignore until after a refused ignore = %+v, want none", got)
	}

	mustExec(t, env.pool, `UPDATE issues SET users_seen = $2 WHERE id = $1`, issueID, ingest.MaxUsersPerIssue-10)
	ignoreIssue(t, env.ctx, env.repository, issueID, issue.IgnoreCondition{Kind: issue.IgnoreForUsers, Count: 10})
}

func TestIgnoreOutsideTheScopeIsNotFound(t *testing.T) {
	env := newEscalationEnv(t)
	_, issueID := env.seed(t, "ignore-out-of-scope", 1, nil)

	err := env.repository.Ignore(env.ctx, access.Limited([]int64{env.projectID + 1000}), issueID, issue.IgnoreCondition{Kind: issue.IgnoreForEvents, Count: 10})

	if !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	wantNotIgnored := readReopenMarks(t, env.pool, issueID)
	if wantNotIgnored.status != "unresolved" {
		t.Fatalf("status = %q, want unresolved", wantNotIgnored.status)
	}
}
