package storage_test

import (
	"context"
	"testing"

	"github.com/ramilS/faultline/internal/storage"
)

type checker struct{}

func (checker) Ping(context.Context) error { return nil }

func TestHealthCheckerBoundary(t *testing.T) {
	var health storage.HealthChecker = checker{}
	if err := health.Ping(t.Context()); err != nil {
		t.Fatalf("Ping() error = %v", err)
	}
}

func TestRepositoriesGroupCapabilities(t *testing.T) {
	repositories := storage.Repositories{
		Metadata: struct{}{},
		Events:   struct{}{},
		Spans:    struct{}{},
	}
	if repositories.Metadata == nil || repositories.Events == nil || repositories.Spans == nil {
		t.Fatal("repository capability was not retained")
	}
}
