//go:build integration

package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
)

type queuedDelivery struct {
	channelID   int64
	issueID     int64
	trigger     string
	environment string
}

func createChannel(t *testing.T, ctx context.Context, channels notify.ChannelRepository, projectID int64, input notify.ChannelInput) notify.Channel {
	t.Helper()
	if input.Kind == "" {
		input.Kind = notify.KindWebhook
		input.Target = "https://hooks.example/" + input.Name
	}
	if input.MinLevel == "" {
		input.MinLevel = "debug"
	}
	if input.Environments == nil {
		input.Environments = []string{}
	}
	created, err := channels.Create(ctx, projectID, input)
	if err != nil {
		t.Fatalf("create channel %s: %v", input.Name, err)
	}
	return created
}

func queuedDeliveries(t *testing.T, ctx context.Context, pool *pgxpool.Pool) []queuedDelivery {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT channel_id, issue_id, trigger, environment FROM notification_deliveries ORDER BY id`)
	if err != nil {
		t.Fatalf("query deliveries: %v", err)
	}
	defer rows.Close()
	var deliveries []queuedDelivery
	for rows.Next() {
		var delivery queuedDelivery
		if err := rows.Scan(&delivery.channelID, &delivery.issueID, &delivery.trigger, &delivery.environment); err != nil {
			t.Fatalf("scan delivery: %v", err)
		}
		deliveries = append(deliveries, delivery)
	}
	return deliveries
}

// persistEvent stores one event and returns its id.
func persistEvent(t *testing.T, ctx context.Context, sink storage.EventSink, projectID int64, fingerprint, level, environment string, at time.Time) string {
	t.Helper()
	event := newEventRow(projectID, fingerprint, at)
	event.Environment = environment
	err := sink.PersistBatch(ctx, storage.Batch{
		ProjectID: projectID,
		Issues:    []storage.IssueDelta{{Fingerprint: fingerprint, Title: "boom", Culprit: "app.js", Level: level, Count: 1, FirstSeen: at, LastSeen: at}},
		Events:    []storage.EventRow{event},
	})
	if err != nil {
		t.Fatalf("persist batch: %v", err)
	}
	return event.EventID.String()
}

func issueIDFor(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64, fingerprint string) int64 {
	t.Helper()
	return queryIssue(t, ctx, pool, projectID, fingerprint).id
}

func setIssueStatus(t *testing.T, ctx context.Context, pool *pgxpool.Pool, issueID int64, status string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `UPDATE issues SET status = $2::issue_status WHERE id = $1`, issueID, status); err != nil {
		t.Fatalf("set issue status: %v", err)
	}
}

func clearDeliveries(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM notification_deliveries`); err != nil {
		t.Fatalf("clear deliveries: %v", err)
	}
}

func TestPersistBatchQueuesNotificationsForNewAndRegressedIssues(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	sink := storagepostgres.NewEventSink(pool)
	all := createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "all", Enabled: true, NotifyNew: true, NotifyRegression: true})
	production := createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "prod", Enabled: true, NotifyNew: true, NotifyRegression: true, Environments: []string{"production"}})
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "staging", Enabled: true, NotifyNew: true, NotifyRegression: true, Environments: []string{"staging"}})
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "fatal-only", Enabled: true, NotifyNew: true, NotifyRegression: true, MinLevel: "fatal"})
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "disabled", Enabled: false, NotifyNew: true, NotifyRegression: true})
	newOnly := createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "new-only", Enabled: true, NotifyNew: true})
	fingerprint := hex64("notify-lifecycle")
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)

	persistEvent(t, ctx, sink, projectID, fingerprint, "error", "production", base)
	issueID := issueIDFor(t, ctx, pool, projectID, fingerprint)

	want := []queuedDelivery{
		{all.ID, issueID, "new", "production"},
		{production.ID, issueID, "new", "production"},
		{newOnly.ID, issueID, "new", "production"},
	}
	if got := queuedDeliveries(t, ctx, pool); !equalDeliveries(got, want) {
		t.Fatalf("after a new issue, deliveries = %+v, want %+v", got, want)
	}

	clearDeliveries(t, ctx, pool)
	persistEvent(t, ctx, sink, projectID, fingerprint, "error", "production", base.Add(time.Minute))
	if got := queuedDeliveries(t, ctx, pool); len(got) != 0 {
		t.Fatalf("a repeat event on an open issue queued %+v", got)
	}

	setIssueStatus(t, ctx, pool, issueID, "ignored")
	persistEvent(t, ctx, sink, projectID, fingerprint, "error", "production", base.Add(2*time.Minute))
	if got := queuedDeliveries(t, ctx, pool); len(got) != 0 {
		t.Fatalf("an event on an ignored issue queued %+v", got)
	}

	setIssueStatus(t, ctx, pool, issueID, "resolved")
	persistEvent(t, ctx, sink, projectID, fingerprint, "error", "production", base.Add(3*time.Minute))
	want = []queuedDelivery{
		{all.ID, issueID, "regression", "production"},
		{production.ID, issueID, "regression", "production"},
	}
	if got := queuedDeliveries(t, ctx, pool); !equalDeliveries(got, want) {
		t.Fatalf("after a regression, deliveries = %+v, want %+v", got, want)
	}
	if status := queryIssue(t, ctx, pool, projectID, fingerprint).status; status != "unresolved" {
		t.Fatalf("regressed issue status = %q, want unresolved", status)
	}

	repository := storagepostgres.NewIssueRepository(pool)
	detail, err := repository.Detail(ctx, access.Unrestricted(), issueID)
	if err != nil {
		t.Fatalf("detail: %v", err)
	}
	if want := (&issue.Regression{At: base.Add(3 * time.Minute)}); detail.Regression == nil || *detail.Regression != *want {
		t.Fatalf("regression = %+v, want %+v", detail.Regression, want)
	}
	if err := repository.UpdateStatus(ctx, access.Unrestricted(), issueID, issue.StatusResolved); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if detail, err := repository.Detail(ctx, access.Unrestricted(), issueID); err != nil || detail.Regression != nil {
		t.Fatalf("after resolving, regression = %+v (%v), want none", detail.Regression, err)
	}
}

// A fingerprint merged away keeps landing on its merge target through the
// tombstone redirect. If that target was resolved, the redirect reopens it,
// and that reopening is a regression like any other.
func TestPersistBatchQueuesARegressionReopenedThroughAMergeTombstone(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	sink := storagepostgres.NewEventSink(pool)
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	source, target := hex64("notify-merge-source"), hex64("notify-merge-target")
	persistEvent(t, ctx, sink, projectID, source, "error", "production", base)
	persistEvent(t, ctx, sink, projectID, target, "error", "production", base)
	sourceID, targetID := issueIDFor(t, ctx, pool, projectID, source), issueIDFor(t, ctx, pool, projectID, target)
	if _, err := pool.Exec(ctx, `UPDATE issues SET merged_into_id = $2 WHERE id = $1`, sourceID, targetID); err != nil {
		t.Fatalf("tombstone source: %v", err)
	}
	setIssueStatus(t, ctx, pool, targetID, "resolved")
	channel := createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "all", Enabled: true, NotifyNew: true, NotifyRegression: true})

	persistEvent(t, ctx, sink, projectID, source, "error", "staging", base.Add(time.Minute))

	want := []queuedDelivery{{channel.ID, targetID, "regression", "staging"}}
	if got := queuedDeliveries(t, ctx, pool); !equalDeliveries(got, want) {
		t.Fatalf("deliveries = %+v, want %+v", got, want)
	}
}

func TestNotificationQueueLifecycle(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	queue := storagepostgres.NewNotificationQueue(pool)
	sink := storagepostgres.NewEventSink(pool)
	secret := "signing-key"
	channel := createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "hook", Kind: notify.KindWebhook, Target: "https://hooks.example/x", Secret: &secret, Enabled: true, NotifyNew: true})
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	eventID := persistEvent(t, ctx, sink, projectID, hex64("notify-queue"), "fatal", "production", base)
	if _, err := pool.Exec(ctx, `UPDATE projects SET icon_emoji = '🚀' WHERE id = $1`, projectID); err != nil {
		t.Fatal(err)
	}
	now := time.Now()

	claimed, err := queue.Claim(ctx, now, 10, time.Minute)
	if err != nil || len(claimed) != 1 {
		t.Fatalf("Claim = %+v, %v; want one delivery", claimed, err)
	}
	delivery := claimed[0]
	wantNotification := notify.Notification{Trigger: notify.TriggerNew, ProjectSlug: "web", ProjectName: "Web", ProjectEmoji: "🚀", IssueID: delivery.Notification.IssueID, Title: "boom", Culprit: "app.js", Level: "fatal", Environment: "production", EventID: eventID}
	if delivery.Attempts != 1 || delivery.ChannelID != channel.ID || delivery.Notification != wantNotification ||
		delivery.Destination != (notify.Destination{Kind: notify.KindWebhook, Target: "https://hooks.example/x", Secret: "signing-key"}) {
		t.Fatalf("claimed %+v", delivery)
	}

	if again, err := queue.Claim(ctx, now, 10, time.Minute); err != nil || len(again) != 0 {
		t.Fatalf("a leased delivery was claimed again: %+v, %v", again, err)
	}
	if expired, err := queue.Claim(ctx, now.Add(2*time.Minute), 10, time.Minute); err != nil || len(expired) != 1 || expired[0].Attempts != 2 {
		t.Fatalf("an expired lease was not reclaimed with attempt 2: %+v, %v", expired, err)
	}

	if err := queue.Reschedule(ctx, delivery, now.Add(time.Hour), "webhook: HTTP 503", now); err != nil {
		t.Fatalf("Reschedule: %v", err)
	}
	stored := listChannel(t, ctx, channels, projectID)
	if stored.LastError != "webhook: HTTP 503" || stored.LastErrorAt == nil {
		t.Fatalf("after Reschedule channel = %+v, want the error recorded", stored)
	}
	if early, _ := queue.Claim(ctx, now.Add(59*time.Minute), 10, time.Minute); len(early) != 0 {
		t.Fatalf("a rescheduled delivery was due early: %+v", early)
	}

	if err := queue.Complete(ctx, delivery, now); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if left := queuedDeliveries(t, ctx, pool); len(left) != 0 {
		t.Fatalf("Complete left %+v", left)
	}
	if stored := listChannel(t, ctx, channels, projectID); stored.LastSuccessAt == nil {
		t.Fatalf("after Complete channel = %+v, want last_success_at", stored)
	}
}

func TestNotificationQueueDropsDeliveriesOfADisabledChannel(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	channel := createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "hook", Enabled: true, NotifyNew: true})
	persistEvent(t, ctx, storagepostgres.NewEventSink(pool), projectID, hex64("notify-disabled"), "error", "", time.Now())
	disabled := notify.ChannelInput{Name: "hook", Target: channel.Target, Enabled: false, NotifyNew: true, Environments: []string{}, MinLevel: "debug"}
	if _, err := channels.Update(ctx, projectID, channel.ID, disabled); err != nil {
		t.Fatalf("disable channel: %v", err)
	}

	claimed, err := storagepostgres.NewNotificationQueue(pool).Claim(ctx, time.Now(), 10, time.Minute)

	if err != nil || len(claimed) != 0 {
		t.Fatalf("Claim = %+v, %v; want nothing for a disabled channel", claimed, err)
	}
	if left := queuedDeliveries(t, ctx, pool); len(left) != 0 {
		t.Fatalf("the disabled channel's delivery was kept: %+v", left)
	}
}

func TestNotificationChannelRepository(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	token := "123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g"
	created := createChannel(t, ctx, channels, projectID, notify.ChannelInput{
		Name: "Ops", Kind: notify.KindTelegram, Target: "-100", Secret: &token, Enabled: true,
		NotifyNew: true, Environments: []string{"production"}, MinLevel: "error",
	})

	if !created.HasSecret || created.Environments[0] != "production" || created.MinLevel != "error" {
		t.Fatalf("created = %+v", created)
	}

	t.Run("an update without a secret keeps the stored one and clears the last error", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `UPDATE notification_channels SET last_error = 'old', last_error_at = now() WHERE id = $1`, created.ID); err != nil {
			t.Fatal(err)
		}
		update := notify.ChannelInput{Name: "Ops 2", Target: "-200", Enabled: true, NotifyRegression: true, Environments: []string{}, MinLevel: "warning"}

		updated, err := channels.Update(ctx, projectID, created.ID, update)

		if err != nil || updated.Name != "Ops 2" || updated.Target != "-200" || updated.NotifyNew || !updated.NotifyRegression || updated.LastError != "" || updated.LastErrorAt != nil {
			t.Fatalf("Update = %+v, %v", updated, err)
		}
		if _, destination, err := channels.Get(ctx, projectID, created.ID); err != nil || destination.Secret != token {
			t.Fatalf("stored secret = %q, %v; want it kept", destination.Secret, err)
		}
	})

	t.Run("a channel of another project is not found", func(t *testing.T) {
		otherProject := projectID + 1000
		if _, _, err := channels.Get(ctx, otherProject, created.ID); !errors.Is(err, notify.ErrChannelNotFound) {
			t.Fatalf("Get = %v, want ErrChannelNotFound", err)
		}
		if err := channels.Delete(ctx, otherProject, created.ID); !errors.Is(err, notify.ErrChannelNotFound) {
			t.Fatalf("Delete = %v, want ErrChannelNotFound", err)
		}
	})

	t.Run("creating for a missing project is project not found", func(t *testing.T) {
		_, err := channels.Create(ctx, projectID+1000, notify.ChannelInput{Name: "x", Kind: notify.KindWebhook, Target: "https://x.example", Environments: []string{}, MinLevel: "debug", NotifyNew: true})
		if !errors.Is(err, project.ErrNotFound) {
			t.Fatalf("Create = %v, want project.ErrNotFound", err)
		}
	})

	t.Run("the per-project cap holds", func(t *testing.T) {
		for index := 1; index < notify.MaxChannelsPerProject; index++ {
			createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "filler", Enabled: true, NotifyNew: true})
		}
		_, err := channels.Create(ctx, projectID, notify.ChannelInput{Name: "one too many", Kind: notify.KindWebhook, Target: "https://x.example", Environments: []string{}, MinLevel: "debug", NotifyNew: true})
		if !errors.Is(err, notify.ErrTooManyChannels) {
			t.Fatalf("Create past the cap = %v, want ErrTooManyChannels", err)
		}
	})

	t.Run("delete removes the channel", func(t *testing.T) {
		if err := channels.Delete(ctx, projectID, created.ID); err != nil {
			t.Fatalf("Delete = %v", err)
		}
		if _, _, err := channels.Get(ctx, projectID, created.ID); !errors.Is(err, notify.ErrChannelNotFound) {
			t.Fatalf("Get after delete = %v", err)
		}
	})
}

type webhookReceiver struct {
	mu       sync.Mutex
	status   int
	requests []map[string]any
	headers  []http.Header
}

func (r *webhookReceiver) ServeHTTP(w http.ResponseWriter, request *http.Request) {
	body, _ := io.ReadAll(request.Body)
	var payload map[string]any
	_ = json.Unmarshal(body, &payload)
	r.mu.Lock()
	r.requests = append(r.requests, payload)
	r.headers = append(r.headers, request.Header.Clone())
	status := r.status
	r.mu.Unlock()
	w.WriteHeader(status)
}

// The whole path with nothing faked but the receiving end: an ingested batch
// queues a delivery, the worker claims it from Postgres, the real sender
// POSTs it, and the outcome lands back on the channel.
func TestNotificationsReachAWebhookEndToEnd(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	receiver := &webhookReceiver{status: http.StatusNoContent}
	server := httptest.NewServer(receiver)
	t.Cleanup(server.Close)
	secret := "signing-key"
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "hook", Kind: notify.KindWebhook, Target: server.URL, Secret: &secret, Enabled: true, NotifyNew: true})
	worker := notify.NewWorker(storagepostgres.NewNotificationQueue(pool), loopbackSender(), notify.WorkerOptions{PublicURL: "https://faultline.example"})
	fingerprint := hex64("notify-e2e")

	eventID := persistEvent(t, ctx, storagepostgres.NewEventSink(pool), projectID, fingerprint, "error", "production", time.Now())
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(receiver.requests) != 1 {
		t.Fatalf("webhook received %d requests, want 1", len(receiver.requests))
	}
	payload := receiver.requests[0]
	issueField := payload["issue"].(map[string]any)
	issueID := issueIDFor(t, ctx, pool, projectID, fingerprint)
	if payload["event"] != "issue.new" || payload["environment"] != "production" || payload["event_id"] != eventID ||
		issueField["title"] != "boom" ||
		issueField["url"] != "https://faultline.example/web/issues/"+jsonNumber(issueID)+"?environment=production&event="+eventID {
		t.Fatalf("payload = %v", payload)
	}
	if !strings.HasPrefix(receiver.headers[0].Get("X-Faultline-Signature"), "sha256=") {
		t.Fatalf("headers = %v, want a signature", receiver.headers[0])
	}
	if left := queuedDeliveries(t, ctx, pool); len(left) != 0 {
		t.Fatalf("delivered notification still queued: %+v", left)
	}
	if channel := listChannel(t, ctx, channels, projectID); channel.LastSuccessAt == nil || channel.LastError != "" {
		t.Fatalf("channel = %+v, want a recorded success", channel)
	}
}

func TestNotificationsRecordAFailingWebhookOnTheChannel(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	receiver := &webhookReceiver{status: http.StatusInternalServerError}
	server := httptest.NewServer(receiver)
	t.Cleanup(server.Close)
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "hook", Kind: notify.KindWebhook, Target: server.URL, Enabled: true, NotifyNew: true})
	worker := notify.NewWorker(storagepostgres.NewNotificationQueue(pool), loopbackSender(), notify.WorkerOptions{})

	persistEvent(t, ctx, storagepostgres.NewEventSink(pool), projectID, hex64("notify-e2e-fail"), "error", "production", time.Now())
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if channel := listChannel(t, ctx, channels, projectID); channel.LastError != "webhook: HTTP 500" || channel.LastSuccessAt != nil {
		t.Fatalf("channel = %+v, want last_error webhook: HTTP 500", channel)
	}
	var attempts int
	var nextAttempt time.Time
	if err := pool.QueryRow(ctx, `SELECT attempts, next_attempt_at FROM notification_deliveries`).Scan(&attempts, &nextAttempt); err != nil {
		t.Fatalf("the failed delivery was not kept for retry: %v", err)
	}
	if attempts != 1 || time.Until(nextAttempt) < 20*time.Second {
		t.Fatalf("attempts = %d, next attempt in %v; want 1 and about 30s", attempts, time.Until(nextAttempt))
	}
}

// loopbackSender reaches the httptest receiver, which listens on loopback —
// the address the production webhook client refuses (see the test below).
func loopbackSender() *notify.Sender {
	return notify.NewSender(notify.SenderOptions{Client: &http.Client{Timeout: 5 * time.Second}})
}

// The production sender, private networks allowed, still refuses loopback:
// the receiver never sees a request, the delivery is dropped rather than
// retried, and the reason is left on the channel for the owner.
func TestNotificationsRefuseAWebhookOnLoopbackWithTheProductionSender(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	channels := storagepostgres.NewNotificationChannelRepository(pool)
	receiver := &webhookReceiver{status: http.StatusNoContent}
	server := httptest.NewServer(receiver)
	t.Cleanup(server.Close)
	createChannel(t, ctx, channels, projectID, notify.ChannelInput{Name: "hook", Kind: notify.KindWebhook, Target: server.URL, Enabled: true, NotifyNew: true})
	sender := notify.NewSender(notify.SenderOptions{AllowPrivateNetworks: true})
	worker := notify.NewWorker(storagepostgres.NewNotificationQueue(pool), sender, notify.WorkerOptions{})

	persistEvent(t, ctx, storagepostgres.NewEventSink(pool), projectID, hex64("notify-ssrf"), "error", "production", time.Now())
	if _, err := worker.RunOnce(ctx); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}

	if len(receiver.requests) != 0 {
		t.Fatalf("loopback receiver got %d requests, want none", len(receiver.requests))
	}
	if channel := listChannel(t, ctx, channels, projectID); channel.LastError != "webhook: destination address is not allowed: 127.0.0.1" {
		t.Fatalf("last_error = %q", channel.LastError)
	}
	if left := queuedDeliveries(t, ctx, pool); len(left) != 0 {
		t.Fatalf("a refused delivery was kept for retry: %+v", left)
	}
}

func listChannel(t *testing.T, ctx context.Context, channels notify.ChannelRepository, projectID int64) notify.Channel {
	t.Helper()
	listed, err := channels.List(ctx, projectID)
	if err != nil || len(listed) == 0 {
		t.Fatalf("List = %+v, %v", listed, err)
	}
	return listed[0]
}

func equalDeliveries(got, want []queuedDelivery) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
