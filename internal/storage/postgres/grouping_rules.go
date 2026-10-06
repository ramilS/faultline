package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/grouping"
)

// moveSentinelPosition is never a legal position (positions start at 1); it
// parks the moving row there mid-swap so the (project_id, position) unique
// index never sees a duplicate while the two rows exchange places.
const moveSentinelPosition = -1

// groupingRuleLockClassID is the classid half of the two-argument
// pg_advisory_xact_lock(classid, objid) key used to serialize every
// position-mutating operation (Create, Move, Delete) per project. Postgres
// advisory locks all share one flat, database-wide integer namespace — a bare
// pg_advisory_xact_lock(projectID) would sit in that same namespace as any
// unrelated future feature that happens to lock on a raw project id, and the
// two would silently co-serialize. Reserving a fixed classid scopes this
// package's locks to their own slice of the namespace. The value itself is
// arbitrary; only its stability matters.
const groupingRuleLockClassID int32 = 0x6752_5500

// lockProjectForRuleMutation takes the advisory lock described above,
// scoped to projectID, for the lifetime of tx.
//
// pg_advisory_xact_lock's two-argument form takes two int4 (32-bit) keys, but
// project_id is bigserial (int8). Go's int64->int32 conversion keeps the
// low-order 32 bits and reinterprets them as two's complement (defined by the
// Go spec, not implementation-dependent), so the same projectID always
// truncates to the same int32 — which is all correctness requires here: every
// mutation for a given project must map to the same lock key as every other
// mutation for that same project. A collision between two *different*
// project IDs that happen to truncate to the same int32 would only make
// those two projects' mutations serialize against each other unnecessarily;
// every query is still scoped by `WHERE project_id = $1`, so it cannot cause
// cross-project data corruption. Project ids are bigserial starting at 1, so
// in practice this wrap-around never happens.
func lockProjectForRuleMutation(ctx context.Context, tx pgx.Tx, projectID int64) error {
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1, $2)`, groupingRuleLockClassID, int32(projectID))
	return err
}

type groupingRuleRepository struct {
	pool *pgxpool.Pool
}

var _ grouping.RuleRepository = (*groupingRuleRepository)(nil)

// NewGroupingRuleRepository returns the grouping.RuleRepository adapter backed
// by pool. It also satisfies grouping.RuleStore, so the same adapter serves
// both the dashboard CRUD API and the ingest RuleCache.
func NewGroupingRuleRepository(pool *pgxpool.Pool) grouping.RuleRepository {
	return &groupingRuleRepository{pool: pool}
}

func (repository *groupingRuleRepository) ListByProjectID(ctx context.Context, projectID int64) ([]grouping.Rule, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT id, project_id, position, matcher, template
		FROM grouping_rules WHERE project_id = $1 ORDER BY position
	`, projectID)
	if err != nil {
		return nil, translateGroupingRuleError("list grouping rules by project", err)
	}
	defer rows.Close()
	return scanGroupingRules(rows)
}

// ListByProjectSlug lists a project's rules in position order. An unknown
// slug matches no project and therefore no rows, so it returns an empty
// slice rather than an error — the same "unknown project" shape callers get
// from an empty-but-existing project.
func (repository *groupingRuleRepository) ListByProjectSlug(ctx context.Context, slug string) ([]grouping.Rule, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT gr.id, gr.project_id, gr.position, gr.matcher, gr.template
		FROM grouping_rules gr
		JOIN projects p ON p.id = gr.project_id
		WHERE p.slug = $1 ORDER BY gr.position
	`, slug)
	if err != nil {
		return nil, translateGroupingRuleError("list grouping rules by slug", err)
	}
	defer rows.Close()
	return scanGroupingRules(rows)
}

func scanGroupingRules(rows pgx.Rows) ([]grouping.Rule, error) {
	rules := make([]grouping.Rule, 0)
	for rows.Next() {
		var rule grouping.Rule
		if err := rows.Scan(&rule.ID, &rule.ProjectID, &rule.Position, &rule.Matcher, &rule.Template); err != nil {
			return nil, translateGroupingRuleError("scan grouping rule", err)
		}
		rules = append(rules, rule)
	}
	if err := rows.Err(); err != nil {
		return nil, translateGroupingRuleError("iterate grouping rules", err)
	}
	return rules, nil
}

// Create appends a new rule at the project's next position, enforcing
// MaxRulesPerProject at the database level so concurrent creates for the same
// project cannot both observe room for one more and both insert.
//
// lockProjectForRuleMutation takes a transaction-scoped exclusive advisory
// lock keyed on the project ID before counting existing rows. Unlike locking
// the grouping_rules rows themselves (SELECT ... FOR UPDATE), this works even
// when zero rows exist yet — the very case a first-ever concurrent create
// race needs to serialize against. Any other transaction calling Create,
// Move, or Delete (all three take it — see their doc comments) for the same
// project blocks on the same key until this transaction commits or rolls
// back, at which point Postgres releases the lock automatically. That makes
// the count-then-insert atomic in effect, without a second migration or a
// constraint.
func (repository *groupingRuleRepository) Create(ctx context.Context, projectID int64, matcher, template string) (grouping.Rule, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return grouping.Rule{}, fmt.Errorf("begin create grouping rule: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockProjectForRuleMutation(ctx, tx, projectID); err != nil {
		return grouping.Rule{}, fmt.Errorf("lock project for create grouping rule: %w", err)
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM grouping_rules WHERE project_id = $1`, projectID).Scan(&count); err != nil {
		return grouping.Rule{}, fmt.Errorf("count grouping rules: %w", err)
	}
	if count >= grouping.MaxRulesPerProject {
		return grouping.Rule{}, fmt.Errorf("create grouping rule: %w", grouping.ErrTooManyRules)
	}

	var created grouping.Rule
	err = tx.QueryRow(ctx, `
		INSERT INTO grouping_rules (project_id, position, matcher, template)
		VALUES ($1, COALESCE((SELECT MAX(position) FROM grouping_rules WHERE project_id = $1), 0) + 1, $2, $3)
		RETURNING id, project_id, position, matcher, template
	`, projectID, matcher, template).Scan(&created.ID, &created.ProjectID, &created.Position, &created.Matcher, &created.Template)
	if err != nil {
		return grouping.Rule{}, translateGroupingRuleError("create grouping rule", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return grouping.Rule{}, fmt.Errorf("commit create grouping rule: %w", err)
	}
	return created, nil
}

// Update changes only matcher and template; position is untouched, so it does
// not need lockProjectForRuleMutation. That lock exists to serialize
// operations that read a row's position and then depend on that position
// still being valid when they write (the swap in Move) or that read the
// current max position and then insert one past it (Create). Update's WHERE
// clause targets a row by id, and a concurrent Move or Delete changing that
// row's position or removing it entirely is either irrelevant to matcher and
// template, or turns this UPDATE into a correct no-match (ErrRuleNotFound) —
// there is no position-consistency window for Update to race against.
func (repository *groupingRuleRepository) Update(ctx context.Context, projectID, ruleID int64, matcher, template string) (grouping.Rule, error) {
	var updated grouping.Rule
	err := repository.pool.QueryRow(ctx, `
		UPDATE grouping_rules SET matcher = $3, template = $4
		WHERE project_id = $1 AND id = $2
		RETURNING id, project_id, position, matcher, template
	`, projectID, ruleID, matcher, template).Scan(&updated.ID, &updated.ProjectID, &updated.Position, &updated.Matcher, &updated.Template)
	if err != nil {
		return grouping.Rule{}, translateGroupingRuleError("update grouping rule", err)
	}
	return updated, nil
}

// Move swaps ruleID's position with its adjacent sibling (direction-dependent)
// inside one transaction. The swap stages through moveSentinelPosition (never
// a legal position) so the (project_id, position) unique index never sees a
// duplicate mid-swap: current moves to the sentinel first (freeing its slot),
// the adjacent row moves into that freed slot, and only then does current
// move into the slot the adjacent row just vacated. A direct two-statement
// swap (set A to B's position, set B to A's position) would instead hit the
// unique index on the first UPDATE, since Postgres checks non-deferrable
// unique constraints per statement, not just at commit — and this table's
// constraint is not deferrable.
//
// lockProjectForRuleMutation also gates Move: without it, a concurrent Create
// could compute its "next position" (MAX(position)+1) while a Move is
// mid-swap and briefly holds a row at the sentinel, two Moves on overlapping
// pairs could interleave their read-then-write, or a concurrent Delete could
// remove current or adjacent between Move's reads and its swap UPDATEs.
// Serializing every position-mutating operation (Create, Move, Delete) for a
// project behind the same lock key removes that class of interleaving
// entirely.
//
// As defence in depth against a future caller that bypasses the lock (or a
// bug in it), each swap UPDATE below also checks RowsAffected and fails with
// ErrRuleNotFound instead of silently committing a swap that touched fewer
// rows than it assumed — e.g. because current or adjacent was deleted
// concurrently.
//
// A missing adjacent sibling (current is already at the corresponding end) is
// a no-op that returns the rule unchanged, not an error.
func (repository *groupingRuleRepository) Move(ctx context.Context, projectID, ruleID int64, direction grouping.MoveDirection) (grouping.Rule, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return grouping.Rule{}, fmt.Errorf("begin move grouping rule: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockProjectForRuleMutation(ctx, tx, projectID); err != nil {
		return grouping.Rule{}, fmt.Errorf("lock project for move grouping rule: %w", err)
	}

	var current grouping.Rule
	err = tx.QueryRow(ctx, `
		SELECT id, project_id, position, matcher, template FROM grouping_rules
		WHERE project_id = $1 AND id = $2
	`, projectID, ruleID).Scan(&current.ID, &current.ProjectID, &current.Position, &current.Matcher, &current.Template)
	if err != nil {
		return grouping.Rule{}, translateGroupingRuleError("move grouping rule", err)
	}

	adjacentPosition := current.Position - 1
	if direction == grouping.MoveDown {
		adjacentPosition = current.Position + 1
	}

	var adjacentID int64
	err = tx.QueryRow(ctx, `
		SELECT id FROM grouping_rules WHERE project_id = $1 AND position = $2
	`, projectID, adjacentPosition).Scan(&adjacentID)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return grouping.Rule{}, fmt.Errorf("commit no-op move grouping rule: %w", err)
		}
		return current, nil
	}
	if err != nil {
		return grouping.Rule{}, translateGroupingRuleError("move grouping rule: find adjacent", err)
	}

	sentinelTag, err := tx.Exec(ctx, `UPDATE grouping_rules SET position = $2 WHERE id = $1`, current.ID, moveSentinelPosition)
	if err != nil {
		return grouping.Rule{}, fmt.Errorf("stage move grouping rule: %w", err)
	}
	if sentinelTag.RowsAffected() == 0 {
		return grouping.Rule{}, fmt.Errorf("stage move grouping rule: %w", grouping.ErrRuleNotFound)
	}
	adjacentTag, err := tx.Exec(ctx, `UPDATE grouping_rules SET position = $2 WHERE id = $1`, adjacentID, current.Position)
	if err != nil {
		return grouping.Rule{}, fmt.Errorf("swap adjacent grouping rule: %w", err)
	}
	if adjacentTag.RowsAffected() == 0 {
		return grouping.Rule{}, fmt.Errorf("swap adjacent grouping rule: %w", grouping.ErrRuleNotFound)
	}
	currentTag, err := tx.Exec(ctx, `UPDATE grouping_rules SET position = $2 WHERE id = $1`, current.ID, adjacentPosition)
	if err != nil {
		return grouping.Rule{}, fmt.Errorf("swap current grouping rule: %w", err)
	}
	if currentTag.RowsAffected() == 0 {
		return grouping.Rule{}, fmt.Errorf("swap current grouping rule: %w", grouping.ErrRuleNotFound)
	}
	current.Position = adjacentPosition

	if err := tx.Commit(ctx); err != nil {
		return grouping.Rule{}, fmt.Errorf("commit move grouping rule: %w", err)
	}
	return current, nil
}

// Delete removes a rule inside a transaction gated by
// lockProjectForRuleMutation. Delete vacates the deleted row's position, so
// it is itself position-mutating: without the lock, a concurrent Move could
// read a row this Delete is about to remove and swap it into a new slot after
// Delete has already committed, or vice versa. Taking the same lock key as
// Create and Move serializes Delete against both.
func (repository *groupingRuleRepository) Delete(ctx context.Context, projectID, ruleID int64) error {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin delete grouping rule: %w", err)
	}
	defer tx.Rollback(ctx)

	if err := lockProjectForRuleMutation(ctx, tx, projectID); err != nil {
		return fmt.Errorf("lock project for delete grouping rule: %w", err)
	}

	var deletedID int64
	err = tx.QueryRow(ctx, `
		DELETE FROM grouping_rules WHERE project_id = $1 AND id = $2 RETURNING id
	`, projectID, ruleID).Scan(&deletedID)
	if err != nil {
		return translateGroupingRuleError("delete grouping rule", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit delete grouping rule: %w", err)
	}
	return nil
}

func translateGroupingRuleError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, grouping.ErrRuleNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
