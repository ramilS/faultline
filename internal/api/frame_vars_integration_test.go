//go:build integration

package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestEventDetailShowsScrubbedFrameVariables(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()
	var projectID int64
	if err := pool.QueryRow(ctx, `INSERT INTO projects (slug,name,platform) VALUES ('shop','Shop','python') RETURNING id`).Scan(&projectID); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	now := time.Now().UTC()
	envelope := []byte(`{"event_id":"` + uuid.New().String() + `","timestamp":"` + now.Format(time.RFC3339) + `","level":"error","platform":"python","message":"KeyError",` +
		`"exception":{"values":[{"type":"KeyError","value":"'cart'","stacktrace":{"frames":[` +
		`{"filename":"shop/cart.py","abs_path":"/app/shop/cart.py","function":"load","lineno":88,"in_app":true,"vars":{"user_id":42,"password":"hunter2"}}` +
		`]}}]}}`)
	mapped, err := ingest.MapSentryEvent(envelope, now)
	if err != nil {
		t.Fatalf("map sentry event: %v", err)
	}
	scrubbed, err := scrub.NewDefault().Payload(mapped.Payload, false)
	if err != nil {
		t.Fatalf("scrub payload: %v", err)
	}
	if err := storagepostgres.NewEventSink(pool).PersistBatch(ctx, storage.Batch{
		ProjectID: projectID,
		Issues:    []storage.IssueDelta{{Fingerprint: fill64("7c"), Title: "KeyError", Level: "error", Count: 1, FirstSeen: now, LastSeen: now}},
		Events: []storage.EventRow{{ProjectID: projectID, EventID: mapped.EventID, Timestamp: mapped.Timestamp, ReceivedAt: now, Level: mapped.Level,
			Environment: mapped.Environment, Message: mapped.Message, Fingerprint: fill64("7c"), Payload: scrubbed}},
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}
	var issueID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM issues WHERE project_id = $1`, projectID).Scan(&issueID); err != nil {
		t.Fatalf("find issue: %v", err)
	}

	resolver := sourcemap.NewResolver(storagepostgres.NewSourceMapRepository(pool), sourcemap.NewParsedMapCache(1<<20))
	handler := api.NewIssueHandler(issue.NewService(storagepostgres.NewIssueRepository(pool), resolver), api.Options{})
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/issues/1/events/latest", http.NoBody)
	request.SetPathValue("id", strconv.FormatInt(issueID, 10))
	request = request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
	handler.LatestEvent(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d body %s", recorder.Code, recorder.Body.String())
	}

	var body struct {
		Event struct {
			SymbolicatedException struct {
				Frames []struct {
					Vars map[string]any `json:"vars"`
				} `json:"frames"`
			} `json:"symbolicated_exception"`
		} `json:"event"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	frames := body.Event.SymbolicatedException.Frames
	if len(frames) != 1 {
		t.Fatalf("event = %s", recorder.Body.String())
	}
	if frames[0].Vars["password"] != scrub.Filtered || frames[0].Vars["user_id"] != float64(42) {
		t.Fatalf("vars = %v", frames[0].Vars)
	}
}
