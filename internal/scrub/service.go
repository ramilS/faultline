package scrub

import (
	"context"
	"fmt"
)

// Repository persists a project's scrubbing configuration.
type Repository interface {
	Get(ctx context.Context, projectID int64) (Config, error)
	Update(ctx context.Context, projectID int64, config Config) error
	GlobalRules(ctx context.Context) ([]Rule, error)
	UpdateGlobalRules(ctx context.Context, rules []Rule) error
}

// Service validates configuration before it is stored.
type Service struct {
	repository Repository
}

func NewService(repository Repository) *Service {
	return &Service{repository: repository}
}

func (s *Service) Get(ctx context.Context, projectID int64) (Config, error) {
	config, err := s.repository.Get(ctx, projectID)
	if err != nil {
		return Config{}, fmt.Errorf("get data scrubbing: %w", err)
	}
	return config, nil
}

// Update stores the normalized configuration and returns what was stored.
func (s *Service) Update(ctx context.Context, projectID int64, config Config) (Config, error) {
	normalized, err := config.Normalize()
	if err != nil {
		return Config{}, err
	}
	if err := s.repository.Update(ctx, projectID, normalized); err != nil {
		return Config{}, fmt.Errorf("update data scrubbing: %w", err)
	}
	return normalized, nil
}

// GetGlobal returns the stored instance-wide rules.
func (s *Service) GetGlobal(ctx context.Context) ([]Rule, error) {
	rules, err := s.repository.GlobalRules(ctx)
	if err != nil {
		return nil, fmt.Errorf("get global scrubbing rules: %w", err)
	}
	return rules, nil
}

// UpdateGlobal stores the normalized instance-wide rules and returns them.
func (s *Service) UpdateGlobal(ctx context.Context, rules []Rule) ([]Rule, error) {
	normalized, err := NormalizeRules(rules)
	if err != nil {
		return nil, err
	}
	if err := s.repository.UpdateGlobalRules(ctx, normalized); err != nil {
		return nil, fmt.Errorf("update global scrubbing rules: %w", err)
	}
	return normalized, nil
}
