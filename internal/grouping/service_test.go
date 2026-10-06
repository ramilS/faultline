package grouping

import (
	"context"
	"errors"
	"testing"
)

type fakeRuleRepository struct {
	rules      []Rule
	created    Rule
	updated    Rule
	moved      Rule
	err        error
	lastCreate struct {
		projectID         int64
		matcher, template string
	}
	deletedProjectID, deletedRuleID int64
	lastListMethod                  string // "ListByProjectID" or "ListByProjectSlug", for test verification
}

func (f *fakeRuleRepository) ListByProjectID(context.Context, int64) ([]Rule, error) {
	f.lastListMethod = "ListByProjectID"
	return f.rules, f.err
}
func (f *fakeRuleRepository) ListByProjectSlug(context.Context, string) ([]Rule, error) {
	f.lastListMethod = "ListByProjectSlug"
	return f.rules, f.err
}
func (f *fakeRuleRepository) Create(_ context.Context, projectID int64, matcher, template string) (Rule, error) {
	f.lastCreate.projectID, f.lastCreate.matcher, f.lastCreate.template = projectID, matcher, template
	return f.created, f.err
}
func (f *fakeRuleRepository) Update(context.Context, int64, int64, string, string) (Rule, error) {
	return f.updated, f.err
}
func (f *fakeRuleRepository) Move(context.Context, int64, int64, MoveDirection) (Rule, error) {
	return f.moved, f.err
}
func (f *fakeRuleRepository) Delete(_ context.Context, projectID, ruleID int64) error {
	f.deletedProjectID, f.deletedRuleID = projectID, ruleID
	return f.err
}

func TestRuleServiceCreateRuleValidatesMatcherAndTemplate(t *testing.T) {
	repository := &fakeRuleRepository{}
	service := NewRuleService(repository)

	if _, err := service.CreateRule(t.Context(), 5, "not-a-matcher", "literal"); !errors.Is(err, ErrInvalidMatcher) {
		t.Fatalf("invalid matcher error = %v, want ErrInvalidMatcher", err)
	}
	if _, err := service.CreateRule(t.Context(), 5, "logger:billing", ""); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("invalid template error = %v, want ErrInvalidTemplate", err)
	}
}

func TestRuleServiceCreateRuleEnforcesCap(t *testing.T) {
	repository := &fakeRuleRepository{}
	repository.rules = make([]Rule, MaxRulesPerProject)
	service := NewRuleService(repository)

	if _, err := service.CreateRule(t.Context(), 5, "logger:billing", "literal"); !errors.Is(err, ErrTooManyRules) {
		t.Fatalf("cap error = %v, want ErrTooManyRules", err)
	}
}

// TestRuleServiceCreateRuleAllowsRuleJustUnderCap pins the boundary from the
// other side: a project one rule short of the cap must still be allowed to
// create — an off-by-one (len(existing) > MaxRulesPerProject instead of >=)
// would let a project reach 101 rules without this case catching it.
func TestRuleServiceCreateRuleAllowsRuleJustUnderCap(t *testing.T) {
	repository := &fakeRuleRepository{created: Rule{ID: 9, ProjectID: 5}}
	repository.rules = make([]Rule, MaxRulesPerProject-1)
	service := NewRuleService(repository)

	if _, err := service.CreateRule(t.Context(), 5, "logger:billing", "literal"); err != nil {
		t.Fatalf("CreateRule() error = %v, want nil", err)
	}
}

func TestRuleServiceCreateRuleDelegatesWhenValid(t *testing.T) {
	repository := &fakeRuleRepository{created: Rule{ID: 9, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "literal"}}
	service := NewRuleService(repository)

	created, err := service.CreateRule(t.Context(), 5, "logger:billing", "literal")
	if err != nil || created != repository.created {
		t.Fatalf("CreateRule = %+v, %v", created, err)
	}
	if repository.lastCreate.projectID != 5 || repository.lastCreate.matcher != "logger:billing" || repository.lastCreate.template != "literal" {
		t.Fatalf("repository received = %+v", repository.lastCreate)
	}
}

func TestRuleServiceUpdateRuleValidatesBeforeDelegating(t *testing.T) {
	repository := &fakeRuleRepository{}
	service := NewRuleService(repository)
	if _, err := service.UpdateRule(t.Context(), 5, 9, "bad", "literal"); !errors.Is(err, ErrInvalidMatcher) {
		t.Fatalf("update matcher error = %v, want ErrInvalidMatcher", err)
	}
	if _, err := service.UpdateRule(t.Context(), 5, 9, "logger:billing", ""); !errors.Is(err, ErrInvalidTemplate) {
		t.Fatalf("update template error = %v, want ErrInvalidTemplate", err)
	}
}

func TestRuleServiceDeleteAndMoveDelegate(t *testing.T) {
	repository := &fakeRuleRepository{moved: Rule{ID: 9, Position: 2}}
	service := NewRuleService(repository)

	if _, err := service.MoveRule(t.Context(), 5, 9, MoveDown); err != nil {
		t.Fatalf("MoveRule: %v", err)
	}
	if err := service.DeleteRule(t.Context(), 5, 9); err != nil {
		t.Fatalf("DeleteRule: %v", err)
	}
	if repository.deletedProjectID != 5 || repository.deletedRuleID != 9 {
		t.Fatalf("delete received = project %d rule %d", repository.deletedProjectID, repository.deletedRuleID)
	}
}

// TestRuleServiceListRulesSuccess verifies that ListRules returns rules in the
// same order as the repository, and that it calls ListByProjectSlug (not
// ListByProjectID). Order is load-bearing: EvaluateRules evaluates in slice
// order, so reordering must be caught.
func TestRuleServiceListRulesSuccess(t *testing.T) {
	rules := []Rule{
		{ID: 1, Position: 1, Matcher: "logger:foo", Template: "tpl1"},
		{ID: 2, Position: 2, Matcher: "logger:bar", Template: "tpl2"},
		{ID: 3, Position: 3, Matcher: "logger:baz", Template: "tpl3"},
	}
	repository := &fakeRuleRepository{rules: rules}
	service := NewRuleService(repository)

	result, err := service.ListRules(t.Context(), "my-project")
	if err != nil {
		t.Fatalf("ListRules() error = %v, want nil", err)
	}
	if len(result) != len(rules) {
		t.Fatalf("ListRules() returned %d rules, want %d", len(result), len(rules))
	}
	for i, rule := range result {
		if rule != rules[i] {
			t.Fatalf("ListRules()[%d] = %+v, want %+v", i, rule, rules[i])
		}
	}
	if repository.lastListMethod != "ListByProjectSlug" {
		t.Fatalf("ListRules called repository.%s, want ListByProjectSlug", repository.lastListMethod)
	}
}

// TestRuleServiceListRulesError verifies that ListRules propagates the
// repository error and wraps it with %w so errors.Is can match the underlying
// error.
func TestRuleServiceListRulesError(t *testing.T) {
	testErr := errors.New("database connection failed")
	repository := &fakeRuleRepository{err: testErr}
	service := NewRuleService(repository)

	_, err := service.ListRules(t.Context(), "my-project")
	if !errors.Is(err, testErr) {
		t.Fatalf("ListRules() error = %v, want to wrap %v (errors.Is should match)", err, testErr)
	}
}
