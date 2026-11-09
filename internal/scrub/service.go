package scrub

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrNoEvents = errors.New("project has no stored events")

// Repository persists a project's scrubbing configuration.
type Repository interface {
	Get(ctx context.Context, projectID int64) (Config, error)
	Update(ctx context.Context, projectID int64, config Config) error
	GlobalRules(ctx context.Context) ([]Rule, error)
	UpdateGlobalRules(ctx context.Context, rules []Rule) error
	StoresUserIP(ctx context.Context, projectID int64) (bool, error)
	HasEvents(ctx context.Context, projectID int64) (bool, error)
	LatestEventPayload(ctx context.Context, projectID int64) (json.RawMessage, error)
}

type ProjectPreviewInput struct {
	Draft       Config
	LatestEvent bool
	Sample      string
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

func (s *Service) HasEvents(ctx context.Context, projectID int64) (bool, error) {
	exists, err := s.repository.HasEvents(ctx, projectID)
	if err != nil {
		return false, fmt.Errorf("find latest event: %w", err)
	}
	return exists, nil
}

func (s *Service) PreviewProject(ctx context.Context, projectID int64, input ProjectPreviewInput) (Preview, error) {
	draft, err := input.Draft.Normalize()
	if err != nil {
		return Preview{}, err
	}
	global, err := s.repository.GlobalRules(ctx)
	if err != nil {
		return Preview{}, fmt.Errorf("preview data scrubbing: %w", err)
	}
	storesIP, err := s.repository.StoresUserIP(ctx, projectID)
	if err != nil {
		return Preview{}, fmt.Errorf("preview data scrubbing: %w", err)
	}
	scrubber, err := NewWithGlobal(draft, global)
	if err != nil {
		return Preview{}, err
	}
	if !input.LatestEvent {
		return scrubber.PreviewSample(input.Sample, !storesIP)
	}
	payload, err := s.repository.LatestEventPayload(ctx, projectID)
	if err != nil {
		return Preview{}, fmt.Errorf("preview data scrubbing: %w", err)
	}
	return scrubber.PreviewPayload(payload, !storesIP)
}

func (s *Service) PreviewGlobal(draft []Rule, sample string) (Preview, error) {
	rules, err := NormalizeRules(draft)
	if err != nil {
		return Preview{}, err
	}
	scrubber, err := NewWithGlobal(DefaultConfig(), rules)
	if err != nil {
		return Preview{}, err
	}
	return scrubber.PreviewSample(sample, false)
}
