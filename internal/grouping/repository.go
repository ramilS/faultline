package grouping

import (
	"context"
	"errors"
)

// ErrRuleNotFound reports that a grouping rule does not exist under the given
// project (or was deleted/moved concurrently).
var ErrRuleNotFound = errors.New("grouping rule not found")

// ErrTooManyRules reports that a project already holds MaxRulesPerProject
// rules; the API layer maps this to 400.
var ErrTooManyRules = errors.New("too many grouping rules")

// MoveDirection is the direction a rule moves relative to its adjacent
// sibling. Reordering is move-up/move-down only — never free-form position
// writes, which would collide under the (project_id, position) unique index.
type MoveDirection string

const (
	MoveUp   MoveDirection = "up"
	MoveDown MoveDirection = "down"
)

// RuleRepository is the full CRUD contract for grouping rules, backing both
// the dashboard editor and (via the embedded RuleStore) the ingest RuleCache.
//
// Every method that returns a slice of rules (ListByProjectID, inherited from
// RuleStore, and ListByProjectSlug) must return them ordered by Position.
// EvaluateRules evaluates rules in the order it is given and never consults
// Position itself, so an unordered result silently breaks rule precedence
// without any error — implementers must ORDER BY position in the query.
type RuleRepository interface {
	RuleStore
	// ListByProjectSlug lists a project's rules in position order, resolving
	// the project by its dashboard-facing slug.
	ListByProjectSlug(ctx context.Context, slug string) ([]Rule, error)
	// Create appends a new rule at the project's next position. Callers must
	// enforce MaxRulesPerProject before calling Create; this method does not
	// re-check the cap itself. The service's cap check is check-then-act across
	// two calls, so it cannot be relied on under concurrent creates for the same
	// project. Implementers should additionally enforce the cap at the database
	// level — for example by recounting inside the insert transaction under a
	// row lock, or with a check constraint — to close that race.
	Create(ctx context.Context, projectID int64, matcher, template string) (Rule, error)
	// Update replaces an existing rule's matcher and template; position is
	// never edited here. Returns ErrRuleNotFound if no such rule exists under
	// projectID.
	Update(ctx context.Context, projectID, ruleID int64, matcher, template string) (Rule, error)
	// Move swaps a rule's position with its adjacent sibling (the next
	// position for MoveDown, the previous for MoveUp) in one transaction. A
	// rule already at the corresponding end of the list is a no-op: it
	// returns the rule unchanged, not an error. Returns ErrRuleNotFound if no
	// such rule exists under projectID.
	Move(ctx context.Context, projectID, ruleID int64, direction MoveDirection) (Rule, error)
	// Delete removes a rule. Returns ErrRuleNotFound if no such rule exists
	// under projectID.
	Delete(ctx context.Context, projectID, ruleID int64) error
}
