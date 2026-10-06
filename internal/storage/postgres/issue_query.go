package postgres

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ramilS/faultline/internal/issue"
)

// likePattern converts a filter value into a Postgres LIKE pattern, reporting
// whether it contains a wildcard at all (a caller with no wildcard can use a
// plain equality test, which is both faster and index-friendly).
//
// The escaping order is load-bearing: LIKE metacharacters in the *user's* text
// (`%`, `_`, `\`) must be escaped before `*` is translated to `%`, or a value
// like "100%" silently becomes a match-anything pattern. A backslash escapes a
// following `*` back into a literal.
func likePattern(value string) (string, bool) {
	var pattern strings.Builder
	isWildcard := false
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch character {
		case '\\':
			if index+1 < len(value) && value[index+1] == '*' {
				index++
				pattern.WriteByte('*')
				continue
			}
			pattern.WriteByte('\\')
			pattern.WriteByte('\\')
		case '%', '_':
			pattern.WriteByte('\\')
			pattern.WriteByte(character)
		case '*':
			pattern.WriteByte('%')
			isWildcard = true
		default:
			pattern.WriteByte(character)
		}
	}
	return pattern.String(), isWildcard
}

// predicateBuilder accumulates AND'd WHERE fragments and their bind arguments.
// Argument numbering starts at $2: List reserves $1 for the project slug.
type predicateBuilder struct {
	clauses   strings.Builder
	arguments []any
}

func newPredicateBuilder() *predicateBuilder {
	return &predicateBuilder{}
}

func (b *predicateBuilder) sql() string { return b.clauses.String() }

func (b *predicateBuilder) args() []any { return b.arguments }

// placeholder appends value and returns its 1-based bind position, offset by
// the project-slug argument List binds as $1.
func (b *predicateBuilder) placeholder(value any) int {
	b.arguments = append(b.arguments, value)
	return len(b.arguments) + 1
}

func (b *predicateBuilder) add(predicate issue.Predicate) error {
	clause, err := b.clauseFor(predicate)
	if err != nil {
		return err
	}
	if predicate.Negate {
		clause = negate(predicate, clause)
	}
	fmt.Fprintf(&b.clauses, " AND (%s)", clause)
	return nil
}

// negate wraps a clause so it excludes rather than includes. Tag-style clauses
// are already EXISTS subqueries, so they invert to NOT EXISTS directly; column
// comparisons need NOT (...) plus an explicit NULL guard, because SQL's
// three-valued logic would otherwise drop unassigned rows from a negated
// assignee filter.
func negate(predicate issue.Predicate, clause string) string {
	switch predicate.Field {
	case issue.FieldEnvironment, issue.FieldRelease, issue.FieldTag:
		return "NOT " + clause
	default:
		return fmt.Sprintf("NOT (%s) OR (%s) IS NULL", clause, clause)
	}
}

func (b *predicateBuilder) clauseFor(predicate issue.Predicate) (string, error) {
	switch predicate.Field {
	case issue.FieldStatus:
		return b.columnMatch("i.status::text", predicate.Values), nil
	case issue.FieldLevel:
		return b.columnMatch("i.level", predicate.Values), nil
	case issue.FieldAssignee:
		return b.assigneeClause(predicate.Values)
	case issue.FieldEnvironment:
		return b.tagClause("environment", predicate.Values), nil
	case issue.FieldRelease:
		return b.tagClause("release", predicate.Values), nil
	case issue.FieldTag:
		return b.tagClause(predicate.Key, predicate.Values), nil
	case issue.FieldTimesSeen:
		return b.numericClause("i.times_seen", predicate.Op, predicate.Values)
	case issue.FieldFirstSeen:
		return b.timeClause("i.first_seen", predicate.Op, predicate.Values)
	case issue.FieldLastSeen:
		return b.timeClause("i.last_seen", predicate.Op, predicate.Values)
	default:
		return "", fmt.Errorf("unsupported filter field %d", predicate.Field)
	}
}

// columnMatch renders an OR-set over one column, using `= ANY` when no value
// carries a wildcard and falling back to per-value LIKE when one does.
func (b *predicateBuilder) columnMatch(column string, values []string) string {
	patterns := make([]string, 0, len(values))
	anyWildcard := false
	for _, value := range values {
		pattern, isWildcard := likePattern(value)
		anyWildcard = anyWildcard || isWildcard
		patterns = append(patterns, pattern)
	}
	if !anyWildcard {
		return fmt.Sprintf("%s = ANY($%d)", column, b.placeholder(literalValues(values)))
	}
	clauses := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		clauses = append(clauses, fmt.Sprintf("%s LIKE $%d", column, b.placeholder(pattern)))
	}
	return strings.Join(clauses, " OR ")
}

// literalValues resolves each value's `\*` escape hatch back to a literal `*`
// for an equality bind. It cannot reuse likePattern's output here: that output
// also backslash-escapes `%`, `_`, and `\` so the string is safe inside a LIKE
// pattern, but `=` never interprets those characters, so escaping them would
// bind a value with spurious backslashes that no equality match can ever hit.
func literalValues(values []string) []string {
	out := make([]string, len(values))
	for index, value := range values {
		out[index] = literalValue(value)
	}
	return out
}

func literalValue(value string) string {
	var out strings.Builder
	for index := 0; index < len(value); index++ {
		if value[index] == '\\' && index+1 < len(value) && value[index+1] == '*' {
			out.WriteByte('*')
			index++
			continue
		}
		out.WriteByte(value[index])
	}
	return out.String()
}

func (b *predicateBuilder) tagClause(key string, values []string) string {
	inner := b.columnMatch("t.value", values)
	return fmt.Sprintf(`EXISTS (SELECT 1 FROM issue_tag_values t
		WHERE t.issue_id = i.id AND t.key = $%d AND (%s))`, b.placeholder(key), inner)
}

func (b *predicateBuilder) assigneeClause(values []string) (string, error) {
	clauses := make([]string, 0, len(values))
	for _, value := range values {
		switch value {
		case "none":
			clauses = append(clauses, "i.assignee_id IS NULL")
		default:
			clauses = append(clauses, fmt.Sprintf(
				"i.assignee_id IN (SELECT id FROM users WHERE email = $%d)", b.placeholder(value)))
		}
	}
	if len(clauses) == 0 {
		return "", fmt.Errorf("assignee filter requires at least one value")
	}
	return strings.Join(clauses, " OR "), nil
}

func (b *predicateBuilder) numericClause(column string, op issue.Operator, values []string) (string, error) {
	if len(values) != 1 {
		return "", fmt.Errorf("%s accepts exactly one value", column)
	}
	parsed, err := strconv.ParseInt(values[0], 10, 64)
	if err != nil {
		return "", fmt.Errorf("%s: %q is not a number", column, values[0])
	}
	return fmt.Sprintf("%s %s $%d", column, comparison(op), b.placeholder(parsed)), nil
}

func (b *predicateBuilder) timeClause(column string, op issue.Operator, values []string) (string, error) {
	if len(values) != 1 {
		return "", fmt.Errorf("%s accepts exactly one value", column)
	}
	parsed, err := time.Parse(time.RFC3339, values[0])
	if err != nil {
		return "", fmt.Errorf("%s: %q is not an RFC3339 timestamp", column, values[0])
	}
	return fmt.Sprintf("%s %s $%d", column, comparison(op), b.placeholder(parsed)), nil
}

// windowClause renders the time-range restriction. A trailing window needs
// only the row predicate: for a window ending at now, `last_seen >= start` is
// equivalent to "had an event in the window", because last_seen is the max
// event time. An absolute window is not — an issue with events either side of
// the range but none inside it would still pass — so it instead joins the
// hourly aggregate, which is authoritative on membership, with no row-level
// prefilter alongside it: first_seen/last_seen are derived from the
// client-supplied event timestamp, while the aggregate's bucket is derived
// from the server-stamped received_at (the same clock-skew distinction
// documented at the merge refresh-window test in
// issues_integration_test.go). A client clock running slow can therefore
// produce an event whose bucket falls inside the window while last_seen sits
// just outside it; a `last_seen >= start` prefilter would drop that issue even
// though the aggregate — the authority here — says it fired in the window.
// That is a false negative the aggregate alone does not have.
func windowClause(window issue.Window, builder *predicateBuilder) (string, string) {
	if window.IsZero() {
		return "", ""
	}
	if !window.IsAbsolute() {
		return "", fmt.Sprintf(" AND i.last_seen >= now() - $%d::interval",
			builder.placeholder(window.Trailing.String()))
	}
	startPlaceholder := builder.placeholder(window.Start)
	endPlaceholder := builder.placeholder(window.End)
	// issue_counts_hourly carries no project_id — adding one would mean
	// recreating the aggregate, which destroys 30-90 day history that events'
	// 30-day retention can no longer rebuild. Scoping by
	// subquery costs a pushed-down semi-join and no data.
	// windowed only needs to answer issue-id membership: attachWindowedSparklines
	// runs the actual period_count sum in its own query, so aggregating it here
	// too would be dead weight.
	cte := fmt.Sprintf(`WITH windowed AS (
		SELECT DISTINCT issue_id
		FROM issue_counts_hourly
		WHERE issue_id IN (
		        SELECT id FROM issues
		        WHERE project_id = (SELECT id FROM projects WHERE slug = $1)
		          AND merged_into_id IS NULL)
		  AND bucket >= $%d AND bucket < $%d
	)`, startPlaceholder, endPlaceholder)
	predicate := " AND i.id IN (SELECT issue_id FROM windowed)"
	return cte, predicate
}

func comparison(op issue.Operator) string {
	switch op {
	case issue.OpLT:
		return "<"
	case issue.OpLTE:
		return "<="
	case issue.OpGT:
		return ">"
	case issue.OpGTE:
		return ">="
	default:
		return "="
	}
}
