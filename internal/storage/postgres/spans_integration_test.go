//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestPersistBatchCopiesSpansOnly(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	t.Cleanup(cancel)
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)

	sink := storagepostgres.NewEventSink(pool)

	parent := "0011223344556677"
	batch := storage.Batch{
		ProjectID: 1,
		Spans: []storage.SpanRow{{
			ProjectID:    1,
			RequestID:    "req-42",
			SpanID:       "a1b2c3d4e5f60718",
			ParentSpanID: &parent,
			Name:         "GET /invoice",
			StartTime:    time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
			DurationMS:   12.5,
			Status:       "ok",
			Attributes:   []byte(`{"http.method":"GET"}`),
		}},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("persist spans: %v", err)
	}

	var (
		count      int
		name       string
		attributes string
		parentOut  *string
	)
	err = pool.QueryRow(ctx, `
		SELECT count(*), max(name), max(attributes::text), max(parent_span_id)
		FROM spans WHERE project_id = 1 AND request_id = 'req-42'
	`).Scan(&count, &name, &attributes, &parentOut)
	if err != nil {
		t.Fatalf("query spans: %v", err)
	}
	if count != 1 || name != "GET /invoice" {
		t.Fatalf("count=%d name=%q", count, name)
	}
	if attributes != `{"http.method": "GET"}` {
		t.Fatalf("attributes=%q", attributes)
	}
	if parentOut == nil || *parentOut != parent {
		t.Fatalf("parent=%v", parentOut)
	}
}

// seedIssueWithEvent persists one issue and one event through the production
// batch writer, with the event stamped with the given eventID and requestID
// (nil when empty) so Timeline tests can resolve a known event to its spans.
func seedIssueWithEvent(t *testing.T, pool *pgxpool.Pool, ctx context.Context, projectID int64, eventID uuid.UUID, requestID string) {
	t.Helper()
	sink := storagepostgres.NewEventSink(pool)
	fingerprint := hex64("timeline")
	seen := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	var requestIDPtr *string
	if requestID != "" {
		requestIDPtr = &requestID
	}

	batch := storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: "Timeline issue", Culprit: "", Level: "error",
			Count: 1, FirstSeen: seen, LastSeen: seen,
		}},
		Events: []storage.EventRow{{
			ProjectID: projectID, EventID: eventID, Timestamp: seen, ReceivedAt: seen,
			Level: "error", Environment: "production", Message: "Timeline issue",
			RequestID: requestIDPtr, Fingerprint: fingerprint,
			Payload: json.RawMessage(`{"message":"Timeline issue"}`),
		}},
	}
	if err := sink.PersistBatch(ctx, batch); err != nil {
		t.Fatalf("seed issue with event: %v", err)
	}
}

func TestTimelineReturnsSpansForEventRequest(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	sink := storagepostgres.NewEventSink(pool)
	repo := storagepostgres.NewIssueRepository(pool)

	eventID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	seedIssueWithEvent(t, pool, ctx, projectID, eventID, "req-77")

	base := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	if err := sink.PersistBatch(ctx, storage.Batch{ProjectID: projectID, Spans: []storage.SpanRow{
		{ProjectID: projectID, RequestID: "req-77", SpanID: "s2", Name: "child", StartTime: base.Add(time.Millisecond), DurationMS: 3},
		{ProjectID: projectID, RequestID: "req-77", SpanID: "s1", Name: "root", StartTime: base, DurationMS: 10},
		{ProjectID: projectID, RequestID: "other", SpanID: "x", Name: "unrelated", StartTime: base, DurationMS: 1},
	}}); err != nil {
		t.Fatalf("persist spans: %v", err)
	}

	spans, err := repo.Timeline(ctx, access.Unrestricted(), eventID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("spans = %d, want 2 (request-scoped)", len(spans))
	}
	if spans[0].SpanID != "s1" || spans[1].SpanID != "s2" {
		t.Fatalf("spans not ordered by start_time: %+v", spans)
	}
}

func TestTimelineEmptyWhenNoRequestID(t *testing.T) {
	ctx, pool, projectID := issueTestEnv(t)
	repo := storagepostgres.NewIssueRepository(pool)
	eventID := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	seedIssueWithEvent(t, pool, ctx, projectID, eventID, "") // request_id NULL

	spans, err := repo.Timeline(ctx, access.Unrestricted(), eventID)
	if err != nil {
		t.Fatalf("timeline: %v", err)
	}
	if len(spans) != 0 {
		t.Fatalf("spans = %d, want 0", len(spans))
	}
}

func TestTimelineReturnsErrNotFoundForMissingEvent(t *testing.T) {
	ctx, pool, _ := issueTestEnv(t)
	repo := storagepostgres.NewIssueRepository(pool)

	if _, err := repo.Timeline(ctx, access.Unrestricted(), uuid.New()); !errors.Is(err, issue.ErrNotFound) {
		t.Fatalf("timeline of missing event err = %v, want ErrNotFound", err)
	}
}
