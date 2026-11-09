//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// TestProjectSettingsStoreReflectsStoreUserIP pins the standalone,
// project-id-keyed resolver DispatchEnvelope's gate uses (see
// internal/ingest/dispatch.go's SettingsLookup): it must report the
// column's live value, defaulting to false for a project that has not opted
// in and flipping to true once the project does.
func TestProjectSettingsStoreReflectsStoreUserIP(t *testing.T) {
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

	repository := storagepostgres.NewProjectRepository(pool)
	created, err := repository.Projects.Create(ctx, project.CreateProject{Slug: "settings", Name: "Settings", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	store := storagepostgres.NewProjectSettingsStore(pool)

	settings, err := store.Settings(ctx, created.ID)
	if err != nil {
		t.Fatalf("lookup before opt-in: %v", err)
	}
	if settings.StoreUserIP {
		t.Fatal("StoreUserIP = true, want false: projects.store_user_ip defaults to false")
	}

	if _, err := pool.Exec(ctx, `UPDATE projects SET store_user_ip = true WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("opt project into store_user_ip: %v", err)
	}

	settings, err = store.Settings(ctx, created.ID)
	if err != nil {
		t.Fatalf("lookup after opt-in: %v", err)
	}
	if !settings.StoreUserIP {
		t.Fatal("StoreUserIP = false, want true: the lookup must reflect the project's current column")
	}
}

// TestProjectSettingsStoreScrubbingDefaultsOnAndRoundTrips pins the migration
// default (on for every project, existing ones included) and that an update is
// visible both to the ingest lookup and to the dashboard's read.
func TestProjectSettingsStoreScrubbingDefaultsOnAndRoundTrips(t *testing.T) {
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
	created, err := storagepostgres.NewProjectRepository(pool).Projects.Create(ctx, project.CreateProject{Slug: "scrubbing", Name: "Scrubbing", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	store := storagepostgres.NewProjectSettingsStore(pool)

	initial, err := store.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !initial.Enabled || len(initial.ExtraFields) != 0 || len(initial.SafeFields) != 0 {
		t.Fatalf("initial = %+v, want enabled with empty lists", initial)
	}

	want := scrub.Config{Enabled: false, ExtraFields: []string{"ssn"}, SafeFields: []string{"token"}, Rules: []scrub.Rule{}}
	if err := store.Update(ctx, created.ID, want); err != nil {
		t.Fatalf("update: %v", err)
	}
	settings, err := store.Settings(ctx, created.ID)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if !reflect.DeepEqual(settings.Scrub, want) {
		t.Fatalf("settings.Scrub = %+v, want %+v", settings.Scrub, want)
	}

	if err := store.Update(ctx, created.ID+1000, want); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("update of a missing project = %v, want project.ErrNotFound", err)
	}
}

func TestProjectSettingsStoreRoundTripsProjectAndGlobalRules(t *testing.T) {
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
	created, err := storagepostgres.NewProjectRepository(pool).Projects.Create(ctx, project.CreateProject{Slug: "scrubbing-rules", Name: "Scrubbing rules", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	store := storagepostgres.NewProjectSettingsStore(pool)
	projectRules := []scrub.Rule{{Name: "account", Pattern: `acct-\d+`, Replacement: ""}}
	globalRules := []scrub.Rule{{Name: "Telegram", Pattern: `bot\d{6,}:[A-Za-z0-9_-]{30,}`, Replacement: "bot[Filtered]"}}

	config := scrub.Config{Enabled: true, ExtraFields: []string{}, SafeFields: []string{}, Rules: projectRules}
	if err := store.Update(ctx, created.ID, config); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := store.UpdateGlobalRules(ctx, globalRules); err != nil {
		t.Fatalf("UpdateGlobalRules: %v", err)
	}

	settings, err := store.Settings(ctx, created.ID)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	if !reflect.DeepEqual(settings.Scrub.Rules, projectRules) || !reflect.DeepEqual(settings.GlobalRules, globalRules) {
		t.Fatalf("settings = %+v, want both rule lists round-tripped", settings)
	}
}

func TestProjectSettingsStoreRefusesStoredRulesThatDoNotValidate(t *testing.T) {
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
	created, err := storagepostgres.NewProjectRepository(pool).Projects.Create(ctx, project.CreateProject{Slug: "hand-edited", Name: "Hand edited", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	store := storagepostgres.NewProjectSettingsStore(pool)
	const invalidRules = `[{"name":"x","pattern":"(","replacement":""}]`

	if _, err := pool.Exec(ctx, `UPDATE instance_scrubbing SET rules = $1`, invalidRules); err != nil {
		t.Fatalf("write invalid global rules: %v", err)
	}
	if _, err := store.Settings(ctx, created.ID); !errors.Is(err, scrub.ErrInvalidConfig) {
		t.Fatalf("Settings error = %v with an invalid stored global rule, want ErrInvalidConfig", err)
	}

	if _, err := pool.Exec(ctx, `UPDATE instance_scrubbing SET rules = '[]'`); err != nil {
		t.Fatalf("reset global rules: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET scrub_rules = $2 WHERE id = $1`, created.ID, invalidRules); err != nil {
		t.Fatalf("write invalid project rules: %v", err)
	}
	if _, err := store.Settings(ctx, created.ID); !errors.Is(err, scrub.ErrInvalidConfig) {
		t.Fatalf("Settings error = %v with an invalid stored project rule, want ErrInvalidConfig", err)
	}
}

func TestProjectSettingsStoreUpdateGlobalRulesFailsWhenTheSingletonRowIsMissing(t *testing.T) {
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
	if _, err := pool.Exec(ctx, `DELETE FROM instance_scrubbing`); err != nil {
		t.Fatalf("delete singleton row: %v", err)
	}

	if err := storagepostgres.NewProjectSettingsStore(pool).UpdateGlobalRules(ctx, []scrub.Rule{}); err == nil {
		t.Fatal("UpdateGlobalRules succeeded with no instance_scrubbing row, want an error")
	}
}

func TestProjectSettingsStoreFindsTheLatestEventToPreview(t *testing.T) {
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
	projects := storagepostgres.NewProjectRepository(pool).Projects
	created, err := projects.Create(ctx, project.CreateProject{Slug: "preview", Name: "Preview", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	other, err := projects.Create(ctx, project.CreateProject{Slug: "preview-other", Name: "Other", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create other project: %v", err)
	}
	store := storagepostgres.NewProjectSettingsStore(pool)

	if has, err := store.HasEvents(ctx, created.ID); err != nil || has {
		t.Fatalf("HasEvents before any event = %v, %v; want false", has, err)
	}
	if _, err := store.LatestEventPayload(ctx, created.ID); !errors.Is(err, scrub.ErrNoEvents) {
		t.Fatalf("LatestEventPayload before any event = %v, want scrub.ErrNoEvents", err)
	}

	base := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	seedPreviewEvent(ctx, t, pool, other.ID, "other", base.Add(time.Hour), `{"message":"other project"}`)
	if has, err := store.HasEvents(ctx, created.ID); err != nil || has {
		t.Fatalf("HasEvents with only another project's event = %v, %v; want false", has, err)
	}
	seedPreviewEvent(ctx, t, pool, created.ID, "older", base, `{"message":"older issue"}`)
	seedPreviewEvent(ctx, t, pool, created.ID, "newer", base.Add(time.Minute), `{"message":"newer issue"}`)

	if has, err := store.HasEvents(ctx, created.ID); err != nil || !has {
		t.Fatalf("HasEvents = %v, %v; want true", has, err)
	}
	payload, err := store.LatestEventPayload(ctx, created.ID)
	if err != nil || string(payload) != `{"message": "newer issue"}` {
		t.Fatalf("LatestEventPayload = %s, %v; want the newer issue's event", payload, err)
	}

	if stores, err := store.StoresUserIP(ctx, created.ID); err != nil || stores {
		t.Fatalf("StoresUserIP = %v, %v; want false by default", stores, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE projects SET store_user_ip = true WHERE id = $1`, created.ID); err != nil {
		t.Fatalf("opt in: %v", err)
	}
	if stores, err := store.StoresUserIP(ctx, created.ID); err != nil || !stores {
		t.Fatalf("StoresUserIP after opt-in = %v, %v; want true", stores, err)
	}
	if _, err := store.StoresUserIP(ctx, other.ID+1000); !errors.Is(err, project.ErrNotFound) {
		t.Fatalf("StoresUserIP of a missing project = %v, want project.ErrNotFound", err)
	}
}

func seedPreviewEvent(ctx context.Context, t *testing.T, pool *pgxpool.Pool, projectID int64, fingerprint string, seen time.Time, payload string) {
	t.Helper()
	var issueID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO issues (project_id, fingerprint, title, first_seen, last_seen)
		VALUES ($1, $2, $3, $4, $4) RETURNING id
	`, projectID, fingerprint, fingerprint, seen).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO events (project_id, issue_id, event_id, received_at, timestamp, level, message, payload)
		VALUES ($1, $2, gen_random_uuid(), $3, $3, 'error', $4, $5::jsonb)
	`, projectID, issueID, seen, fingerprint, payload); err != nil {
		t.Fatalf("insert event: %v", err)
	}
}
