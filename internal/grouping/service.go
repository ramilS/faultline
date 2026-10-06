package grouping

import (
	"context"
	"fmt"
)

// RuleService applies grouping-rule validation and cap-enforcement policy
// over a RuleRepository. It is the dependency the dashboard HTTP handlers use.
type RuleService struct {
	repository RuleRepository
}

func NewRuleService(repository RuleRepository) *RuleService {
	return &RuleService{repository: repository}
}

// ListRules returns projectSlug's rules in position order.
func (s *RuleService) ListRules(ctx context.Context, projectSlug string) ([]Rule, error) {
	rules, err := s.repository.ListByProjectSlug(ctx, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("list grouping rules: %w", err)
	}
	return rules, nil
}

// CreateRule validates matcher and template, enforces MaxRulesPerProject, and
// delegates to the repository. The cap check reads the current count and
// then inserts as two separate calls; it is not race-safe against concurrent
// creates for the same project (a DB-level guarantee, e.g. a trigger or a
// serialized transaction in the Postgres adapter, is required to close that
// gap).
func (s *RuleService) CreateRule(ctx context.Context, projectID int64, matcher, template string) (Rule, error) {
	if err := validateRule(matcher, template); err != nil {
		return Rule{}, err
	}
	existing, err := s.repository.ListByProjectID(ctx, projectID)
	if err != nil {
		return Rule{}, fmt.Errorf("create grouping rule: %w", err)
	}
	if len(existing) >= MaxRulesPerProject {
		return Rule{}, fmt.Errorf("create grouping rule: %w", ErrTooManyRules)
	}
	created, err := s.repository.Create(ctx, projectID, matcher, template)
	if err != nil {
		return Rule{}, fmt.Errorf("create grouping rule: %w", err)
	}
	return created, nil
}

// UpdateRule validates the replacement matcher and template before
// delegating — a rule made invalid by an edit is rejected exactly like one
// created invalid.
func (s *RuleService) UpdateRule(ctx context.Context, projectID, ruleID int64, matcher, template string) (Rule, error) {
	if err := validateRule(matcher, template); err != nil {
		return Rule{}, err
	}
	updated, err := s.repository.Update(ctx, projectID, ruleID, matcher, template)
	if err != nil {
		return Rule{}, fmt.Errorf("update grouping rule: %w", err)
	}
	return updated, nil
}

// MoveRule swaps ruleID's position with its adjacent sibling. A move past
// either end of the list is the repository's no-op to report (see
// RuleRepository.Move); the service applies no additional boundary logic.
func (s *RuleService) MoveRule(ctx context.Context, projectID, ruleID int64, direction MoveDirection) (Rule, error) {
	moved, err := s.repository.Move(ctx, projectID, ruleID, direction)
	if err != nil {
		return Rule{}, fmt.Errorf("move grouping rule: %w", err)
	}
	return moved, nil
}

func (s *RuleService) DeleteRule(ctx context.Context, projectID, ruleID int64) error {
	if err := s.repository.Delete(ctx, projectID, ruleID); err != nil {
		return fmt.Errorf("delete grouping rule: %w", err)
	}
	return nil
}

func validateRule(matcher, template string) error {
	if _, err := ParseMatcher(matcher); err != nil {
		return fmt.Errorf("validate grouping rule: %w", ErrInvalidMatcher)
	}
	if _, err := ParseTemplate(template); err != nil {
		return fmt.Errorf("validate grouping rule: %w", ErrInvalidTemplate)
	}
	return nil
}
