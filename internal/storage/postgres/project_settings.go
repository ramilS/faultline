package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
)

type ProjectSettingsStore struct {
	pool *pgxpool.Pool
}

var (
	_ ingest.ProjectSettingsStore = (*ProjectSettingsStore)(nil)
	_ scrub.Repository            = (*ProjectSettingsStore)(nil)
)

// NewProjectSettingsStore returns a project-settings resolver backed by pool.
// It is deliberately independent of NewIngestKeyStore: DispatchEnvelope needs
// a lookup keyed by project id (both the synchronous ingest path and the
// durable feeder's replay path only ever have a project id in hand at that
// point), not by the public key the ingest-key cache is keyed on. The same
// store backs the dashboard's data-scrubbing editor.
func NewProjectSettingsStore(pool *pgxpool.Pool) *ProjectSettingsStore {
	return &ProjectSettingsStore{pool: pool}
}

func (store *ProjectSettingsStore) Settings(ctx context.Context, projectID int64) (ingest.ProjectSettings, error) {
	var settings ingest.ProjectSettings
	err := store.pool.QueryRow(ctx,
		`SELECT p.store_user_ip, p.scrub_data, p.scrub_extra_fields, p.scrub_safe_fields, p.scrub_rules, g.rules
		 FROM projects p CROSS JOIN instance_scrubbing g WHERE p.id = $1`, projectID,
	).Scan(&settings.StoreUserIP, &settings.Scrub.Enabled, &settings.Scrub.ExtraFields, &settings.Scrub.SafeFields,
		&settings.Scrub.Rules, &settings.GlobalRules)
	if err != nil {
		return ingest.ProjectSettings{}, fmt.Errorf("find project settings: %w", err)
	}
	// Rules are validated on every write; a row that no longer validates was
	// edited by hand. Failing the load sends ingest to its closed defaults and
	// reports the error, rather than skipping a rule that protects a secret.
	if settings.Scrub.Rules, err = scrub.NormalizeRules(settings.Scrub.Rules); err != nil {
		return ingest.ProjectSettings{}, fmt.Errorf("project scrubbing rules: %w", err)
	}
	if settings.GlobalRules, err = scrub.NormalizeRules(settings.GlobalRules); err != nil {
		return ingest.ProjectSettings{}, fmt.Errorf("global scrubbing rules: %w", err)
	}
	return settings, nil
}

func (store *ProjectSettingsStore) Get(ctx context.Context, projectID int64) (scrub.Config, error) {
	var config scrub.Config
	err := store.pool.QueryRow(ctx,
		`SELECT scrub_data, scrub_extra_fields, scrub_safe_fields, scrub_rules FROM projects WHERE id = $1`, projectID,
	).Scan(&config.Enabled, &config.ExtraFields, &config.SafeFields, &config.Rules)
	if err != nil {
		return scrub.Config{}, fmt.Errorf("find data scrubbing: %w", err)
	}
	return config, nil
}

func (store *ProjectSettingsStore) Update(ctx context.Context, projectID int64, config scrub.Config) error {
	tag, err := store.pool.Exec(ctx,
		`UPDATE projects SET scrub_data = $2, scrub_extra_fields = $3, scrub_safe_fields = $4, scrub_rules = $5 WHERE id = $1`,
		projectID, config.Enabled, config.ExtraFields, config.SafeFields, config.Rules)
	if err != nil {
		return fmt.Errorf("update data scrubbing: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return project.ErrNotFound
	}
	return nil
}

func (store *ProjectSettingsStore) GlobalRules(ctx context.Context) ([]scrub.Rule, error) {
	var rules []scrub.Rule
	if err := store.pool.QueryRow(ctx, `SELECT rules FROM instance_scrubbing`).Scan(&rules); err != nil {
		return nil, fmt.Errorf("find global scrubbing rules: %w", err)
	}
	return rules, nil
}

func (store *ProjectSettingsStore) UpdateGlobalRules(ctx context.Context, rules []scrub.Rule) error {
	tag, err := store.pool.Exec(ctx, `UPDATE instance_scrubbing SET rules = $1`, rules)
	if err != nil {
		return fmt.Errorf("update global scrubbing rules: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return errors.New("update global scrubbing rules: the instance_scrubbing row is missing")
	}
	return nil
}
