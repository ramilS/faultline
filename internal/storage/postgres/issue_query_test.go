package postgres

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/issue"
)

func TestLikePattern(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantPattern  string
		wantWildcard bool
	}{
		{name: "plain value is unchanged", input: "production", wantPattern: "production"},
		{
			name:        "percent is escaped so it stays literal",
			input:       "100%",
			wantPattern: `100\%`,
		},
		{
			name:        "underscore is escaped so it matches one literal char",
			input:       "a_b",
			wantPattern: `a\_b`,
		},
		{
			name:        "backslash is escaped",
			input:       `a\b`,
			wantPattern: `a\\b`,
		},
		{
			name:         "star becomes a wildcard",
			input:        "1.2.*",
			wantPattern:  "1.2.%",
			wantWildcard: true,
		},
		{
			name:        "escaped star is a literal star",
			input:       `1.2.\*`,
			wantPattern: "1.2.*",
		},
		{
			name:         "wildcard and literal percent coexist",
			input:        "*100%",
			wantPattern:  `%100\%`,
			wantWildcard: true,
		},
		{
			name:         "non-ASCII with wildcard",
			input:        "café*",
			wantPattern:  "café%",
			wantWildcard: true,
		},
		{
			name:        "non-ASCII without wildcard",
			input:       "café",
			wantPattern: "café",
		},
		{
			name:        "trailing lone backslash is escaped",
			input:       "test\\",
			wantPattern: `test\\`,
		},
		{
			name:        "backslash followed by ordinary char is escaped",
			input:       `a\zb`,
			wantPattern: `a\\zb`,
		},
		{
			name:        "doubled backslash followed by asterisk",
			input:       `\\*`,
			wantPattern: `\\*`,
		},
		{
			name:         "input that is only asterisk",
			input:        "*",
			wantPattern:  "%",
			wantWildcard: true,
		},
		{
			name:        "empty input",
			input:       "",
			wantPattern: "",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			pattern, isWildcard := likePattern(testCase.input)
			if pattern != testCase.wantPattern {
				t.Fatalf("pattern = %q, want %q", pattern, testCase.wantPattern)
			}
			if isWildcard != testCase.wantWildcard {
				t.Fatalf("isWildcard = %v, want %v", isWildcard, testCase.wantWildcard)
			}
		})
	}
}

// TestLiteralValue pins literalValue directly: it is a separate loop from
// likePattern (an equality bind must not carry likePattern's %/_/\ escaping,
// which no `=` comparison ever interprets), and the two must stay consistent
// on how a doubled backslash and a trailing lone backslash resolve, or a
// value binds as something no row can match.
func TestLiteralValue(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "plain value is unchanged", input: "production", want: "production"},
		{name: "escaped star unescapes to a literal star", input: `prod\*uction`, want: "prod*uction"},
		{
			name:  "a doubled backslash then star: only the second backslash pairs with the star",
			input: `\\*`,
			want:  `\*`,
		},
		{name: "a trailing lone backslash is left untouched, not read past the string", input: `test\`, want: `test\`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := literalValue(testCase.input); got != testCase.want {
				t.Fatalf("literalValue(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

func TestPredicateBuilderLevel(t *testing.T) {
	builder := newPredicateBuilder()
	if err := builder.add(issue.Predicate{
		Field:  issue.FieldLevel,
		Values: []string{"error", "fatal"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got, want := builder.sql(), " AND (i.level = ANY($2))"; got != want {
		t.Fatalf("sql = %q, want %q", got, want)
	}
	// Exact args, not just a count: len(args) == 1 alone can't tell an
	// unescaped ["error", "fatal"] bind from a mangled or wrongly-ordered one.
	wantArgs := []any{[]string{"error", "fatal"}}
	if got := builder.args(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("args = %#v, want %#v", got, wantArgs)
	}
}

func TestPredicateBuilderNegatedTag(t *testing.T) {
	builder := newPredicateBuilder()
	if err := builder.add(issue.Predicate{
		Field:  issue.FieldTag,
		Key:    "browser",
		Negate: true,
		Values: []string{"Chrome"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(builder.sql(), "NOT EXISTS") {
		t.Fatalf("sql = %q, want a NOT EXISTS clause", builder.sql())
	}
}

func TestPredicateBuilderWildcardUsesLike(t *testing.T) {
	builder := newPredicateBuilder()
	if err := builder.add(issue.Predicate{
		Field:  issue.FieldRelease,
		Values: []string{"1.2.*"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if !strings.Contains(builder.sql(), "LIKE") {
		t.Fatalf("sql = %q, want a LIKE clause", builder.sql())
	}
	if !slices.Contains(builder.args(), any("1.2.%")) {
		t.Fatalf("args = %v, want the translated wildcard pattern", builder.args())
	}
}

// TestPredicateBuilderMixedWildcardEnvironmentUsesLikeForEveryValue covers a
// mixed wildcard/non-wildcard set — reachable today via
// ?environment=prod&environment=stag* — where columnMatch's anyWildcard flip
// forces even the literal "prod" value onto the per-value LIKE path instead
// of the `= ANY` path it would take alone, so its escaping decisions change
// depending on its siblings.
func TestPredicateBuilderMixedWildcardEnvironmentUsesLikeForEveryValue(t *testing.T) {
	builder := newPredicateBuilder()
	if err := builder.add(issue.Predicate{
		Field:  issue.FieldEnvironment,
		Values: []string{"prod", "stag*"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	want := " AND (EXISTS (SELECT 1 FROM issue_tag_values t\n\t\tWHERE t.issue_id = i.id AND t.key = $4 AND (t.value LIKE $2 OR t.value LIKE $3)))"
	if got := builder.sql(); got != want {
		t.Fatalf("sql = %q, want %q", got, want)
	}
	wantArgs := []any{"prod", "stag%", "environment"}
	if got := builder.args(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("args = %#v, want %#v", got, wantArgs)
	}
}

// TestPredicateBuilderMixedWildcardNegatedLevelUsesDefaultNullGuard covers the
// same mixed-wildcard flip through negate's default arm (the NOT (...) OR
// (...) IS NULL form used by every field other than environment/release/tag),
// which TestPredicateBuilderMixedWildcardEnvironmentUsesLikeForEveryValue does
// not exercise.
func TestPredicateBuilderMixedWildcardNegatedLevelUsesDefaultNullGuard(t *testing.T) {
	builder := newPredicateBuilder()
	if err := builder.add(issue.Predicate{
		Field:  issue.FieldLevel,
		Negate: true,
		Values: []string{"error", "fatal*"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	want := " AND (NOT (i.level LIKE $2 OR i.level LIKE $3) OR (i.level LIKE $2 OR i.level LIKE $3) IS NULL)"
	if got := builder.sql(); got != want {
		t.Fatalf("sql = %q, want %q", got, want)
	}
	wantArgs := []any{"error", "fatal%"}
	if got := builder.args(); !reflect.DeepEqual(got, wantArgs) {
		t.Fatalf("args = %#v, want %#v", got, wantArgs)
	}
}

func TestPredicateBuilderTimesSeenComparison(t *testing.T) {
	builder := newPredicateBuilder()
	if err := builder.add(issue.Predicate{
		Field:  issue.FieldTimesSeen,
		Op:     issue.OpGT,
		Values: []string{"100"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if got, want := builder.sql(), " AND (i.times_seen > $2)"; got != want {
		t.Fatalf("sql = %q, want %q", got, want)
	}
	if !slices.Contains(builder.args(), any(int64(100))) {
		t.Fatalf("args = %v, want the parsed int64", builder.args())
	}
}

func TestPredicateBuilderRejectsUnparsableTimesSeen(t *testing.T) {
	builder := newPredicateBuilder()
	err := builder.add(issue.Predicate{
		Field:  issue.FieldTimesSeen,
		Op:     issue.OpGT,
		Values: []string{"many"},
	})
	if err == nil {
		t.Fatal("add() = nil, want an error for a non-numeric timesSeen value")
	}
}

func TestPredicateBuilderNegatedAssigneeGuardsNull(t *testing.T) {
	builder := newPredicateBuilder()
	if err := builder.add(issue.Predicate{
		Field:  issue.FieldAssignee,
		Negate: true,
		Values: []string{"alice@example.com"},
	}); err != nil {
		t.Fatalf("add: %v", err)
	}
	want := " AND (NOT (i.assignee_id IN (SELECT id FROM users WHERE email = $2)) OR (i.assignee_id IN (SELECT id FROM users WHERE email = $2)) IS NULL)"
	if got := builder.sql(); got != want {
		t.Fatalf("sql = %q, want %q", got, want)
	}
}

func TestComparisonOperators(t *testing.T) {
	cases := []struct {
		name string
		op   issue.Operator
		want string
	}{
		{name: "less than", op: issue.OpLT, want: "<"},
		{name: "less than or equal", op: issue.OpLTE, want: "<="},
		{name: "greater than", op: issue.OpGT, want: ">"},
		{name: "greater than or equal", op: issue.OpGTE, want: ">="},
		{name: "equal falls back to the default arm", op: issue.OpEq, want: "="},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := comparison(testCase.op); got != testCase.want {
				t.Fatalf("comparison(%v) = %q, want %q", testCase.op, got, testCase.want)
			}
		})
	}
}
