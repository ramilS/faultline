package postgres

import (
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/platform/config"
)

func TestPoolConfigAppliesDatabaseConfiguration(t *testing.T) {
	database := config.DatabaseConfig{
		URL:               "postgres://faultline:secret@db:5432/faultline?sslmode=disable",
		MaxConns:          32,
		MinConns:          4,
		MaxConnLifetime:   45 * time.Minute,
		MaxConnIdleTime:   5 * time.Minute,
		HealthCheckPeriod: 20 * time.Second,
	}

	got, err := poolConfig(database)
	if err != nil {
		t.Fatalf("poolConfig() error = %v", err)
	}
	if got.MaxConns != database.MaxConns || got.MinConns != database.MinConns {
		t.Fatalf("connection bounds = %d/%d", got.MinConns, got.MaxConns)
	}
	if got.MaxConnLifetime != database.MaxConnLifetime || got.MaxConnIdleTime != database.MaxConnIdleTime || got.HealthCheckPeriod != database.HealthCheckPeriod {
		t.Fatalf("pool durations were not applied: %#v", got)
	}
	if got.ConnConfig.Host != "db" || got.ConnConfig.Database != "faultline" || got.ConnConfig.User != "faultline" {
		t.Fatalf("connection URL was not parsed: %#v", got.ConnConfig)
	}
}

func TestPoolConfigRejectsMalformedURL(t *testing.T) {
	_, err := poolConfig(config.DatabaseConfig{URL: "://bad", MaxConns: 1})
	if err == nil {
		t.Fatal("poolConfig() error = nil")
	}
}

// A zero HealthCheckPeriod must fail Open synchronously rather than reach
// pgxpool's background health-check goroutine, where time.NewTicker panics —
// asynchronously, after Open would already have returned a pool. Open must
// reject the config before that goroutine is ever started, so this needs no
// real database: an invalid config is rejected before anything dials out.
func TestOpenRejectsZeroHealthCheckPeriod(t *testing.T) {
	database := config.DatabaseConfig{
		URL:      "postgres://faultline:secret@db:5432/faultline?sslmode=disable",
		MaxConns: 1,
		// HealthCheckPeriod left at its zero value.
	}
	if _, err := Open(t.Context(), database); err == nil {
		t.Fatal("Open() error = nil, want a rejection of the zero health check period")
	}
}
