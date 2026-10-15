package postgres

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
)

// TestPredicateBuilderPinsWildcardSemanticsForLegacyTagFamilyParams locks down
// the `*`-is-a-wildcard behaviour for the three legacy tag-family list
// params (environment, release, tag), which go through predicateBuilder like
// every other list filter: `?environment=prod*` now matches every production
// issue via LIKE, where it previously matched zero issues via a
// literal-string equality check. The negative cases pin the boundary of that
// change: a value with no `*`, a value containing a literal `%` that must
// not be misread as a wildcard, and a `\*`-escaped literal asterisk must all
// keep using `= ANY` with an unescaped value.
func TestPredicateBuilderPinsWildcardSemanticsForLegacyTagFamilyParams(t *testing.T) {
	const likeClause = " AND (EXISTS (SELECT 1 FROM issue_tag_values t\n" +
		"\t\tWHERE t.issue_id = i.id AND t.key = $3 AND (t.value LIKE $2)))"
	const equalityClause = " AND (EXISTS (SELECT 1 FROM issue_tag_values t\n" +
		"\t\tWHERE t.issue_id = i.id AND t.key = $3 AND (t.value = ANY($2))))"

	cases := []struct {
		name      string
		predicate issue.Predicate
		wantSQL   string
		wantArgs  []any
	}{
		{
			name:      "environment wildcard becomes LIKE with the % pattern",
			predicate: issue.Predicate{Field: issue.FieldEnvironment, Values: []string{"prod*"}},
			wantSQL:   likeClause,
			wantArgs:  []any{"prod%", "environment"},
		},
		{
			name:      "release wildcard becomes LIKE with the % pattern",
			predicate: issue.Predicate{Field: issue.FieldRelease, Values: []string{"1.2.*"}},
			wantSQL:   likeClause,
			wantArgs:  []any{"1.2.%", "release"},
		},
		{
			name:      "tag wildcard becomes LIKE with the % pattern",
			predicate: issue.Predicate{Field: issue.FieldTag, Key: "k", Values: []string{"a*"}},
			wantSQL:   likeClause,
			wantArgs:  []any{"a%", "k"},
		},
		{
			name:      "environment without a wildcard stays equality, value unescaped",
			predicate: issue.Predicate{Field: issue.FieldEnvironment, Values: []string{"prod"}},
			wantSQL:   equalityClause,
			wantArgs:  []any{[]string{"prod"}, "environment"},
		},
		{
			name:      "a literal percent sign stays equality, not converted to a wildcard",
			predicate: issue.Predicate{Field: issue.FieldEnvironment, Values: []string{"100%"}},
			wantSQL:   equalityClause,
			wantArgs:  []any{[]string{"100%"}, "environment"},
		},
		{
			name:      "an escaped star stays equality and matches a literal asterisk",
			predicate: issue.Predicate{Field: issue.FieldEnvironment, Values: []string{`prod\*`}},
			wantSQL:   equalityClause,
			wantArgs:  []any{[]string{"prod*"}, "environment"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			builder := newPredicateBuilder()
			if err := builder.add(testCase.predicate); err != nil {
				t.Fatalf("add: %v", err)
			}
			if got := builder.sql(); got != testCase.wantSQL {
				t.Fatalf("sql = %q, want %q", got, testCase.wantSQL)
			}
			if got := builder.args(); !reflect.DeepEqual(got, testCase.wantArgs) {
				t.Fatalf("args = %#v, want %#v", got, testCase.wantArgs)
			}
		})
	}
}

// TestBuildListFilterQueryCombinesPredicatesAndFreeText is the highest-value
// regression test for the $1-reservation contract: it is the only place a
// tag-family predicate (which itself binds two args, in the order
// [value-or-pattern, key]), a second predicate, and the free-text query
// clause are all present together, so a bind-index slip anywhere in List
// (reordering the $1 reservation, appending predicate args before/after the
// wrong point, or reusing a stale len(args) for the text placeholder) shows
// up here as a wrong arg at a wrong position, not just a wrong clause count.
func TestBuildListFilterQueryCombinesPredicatesAndFreeText(t *testing.T) {
	filters := issue.ListFilters{
		ProjectSlug: "web",
		Scope:       access.Unrestricted(),
		Predicates: []issue.Predicate{
			{Field: issue.FieldEnvironment, Values: []string{"production"}},
			{Field: issue.FieldStatus, Values: []string{"unresolved"}},
		},
		Text: "boom",
	}

	query, args, err := buildListFilterQuery(filters)
	if err != nil {
		t.Fatalf("buildListFilterQuery: %v", err)
	}

	wantQuery := "\n" +
		"\t\tSELECT i.id, i.fingerprint, i.title, i.culprit, i.level, i.status,\n" +
		"\t\t       i.times_seen, i.users_seen, i.first_seen, i.last_seen, u.id, u.name, u.email, p.slug\n" +
		"\t\tFROM issues i\n" +
		"\t\tJOIN projects p ON p.id = i.project_id\n" +
		"\t\tLEFT JOIN users u ON u.id = i.assignee_id\n" +
		"\t\tWHERE p.slug = $1 AND i.merged_into_id IS NULL" +
		" AND true" +
		" AND (EXISTS (SELECT 1 FROM issue_tag_values t\n" +
		"\t\tWHERE t.issue_id = i.id AND t.key = $3 AND (t.value = ANY($2))))" +
		" AND (i.status::text = ANY($4))" +
		" AND (i.title ILIKE $5 OR i.culprit ILIKE $5)"
	if query != wantQuery {
		t.Fatalf("query = %q, want %q", query, wantQuery)
	}

	wantArgs := []any{
		"web",                  // $1: project slug
		[]string{"production"}, // $2: environment value (no wildcard, so = ANY)
		"environment",          // $3: tag key
		[]string{"unresolved"}, // $4: status value
		"%boom%",               // $5: free-text pattern, reused for title and culprit
	}
	if !reflect.DeepEqual(args, wantArgs) {
		t.Fatalf("args = %#v, want %#v", args, wantArgs)
	}
}

// TestBuildListFilterQueryCombinesWindowPredicatesAndFreeText extends
// TestBuildListFilterQueryCombinesPredicatesAndFreeText to pin the bind order
// once a Window enters the mix: windowClause runs against the same
// predicateBuilder before the Predicates loop, so its placeholders are always
// minted first, pushing every predicate and the free-text placeholder up by
// its own arg count. Getting this wrong silently binds a predicate's
// value into a window interval bound (or vice versa) with no query error.
func TestBuildListFilterQueryCombinesWindowPredicatesAndFreeText(t *testing.T) {
	t.Run("trailing window", func(t *testing.T) {
		filters := issue.ListFilters{
			ProjectSlug: "web",
			Scope:       access.Unrestricted(),
			Window:      issue.Window{Trailing: 24 * time.Hour},
			Predicates: []issue.Predicate{
				{Field: issue.FieldStatus, Values: []string{"unresolved"}},
			},
			Text: "boom",
		}

		query, args, err := buildListFilterQuery(filters)
		if err != nil {
			t.Fatalf("buildListFilterQuery: %v", err)
		}

		wantQuery := "\n" +
			"\t\tSELECT i.id, i.fingerprint, i.title, i.culprit, i.level, i.status,\n" +
			"\t\t       i.times_seen, i.users_seen, i.first_seen, i.last_seen, u.id, u.name, u.email, p.slug\n" +
			"\t\tFROM issues i\n" +
			"\t\tJOIN projects p ON p.id = i.project_id\n" +
			"\t\tLEFT JOIN users u ON u.id = i.assignee_id\n" +
			"\t\tWHERE p.slug = $1 AND i.merged_into_id IS NULL" +
			" AND true" +
			" AND i.last_seen >= now() - $2::interval" +
			" AND (i.status::text = ANY($3))" +
			" AND (i.title ILIKE $4 OR i.culprit ILIKE $4)"
		if query != wantQuery {
			t.Fatalf("query = %q, want %q", query, wantQuery)
		}

		wantArgs := []any{
			"web",                  // $1: project slug
			"24h0m0s",              // $2: trailing duration, bound as an interval literal
			[]string{"unresolved"}, // $3: status value
			"%boom%",               // $4: free-text pattern
		}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args = %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("absolute window", func(t *testing.T) {
		start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		end := start.Add(24 * time.Hour)
		filters := issue.ListFilters{
			ProjectSlug: "web",
			Scope:       access.Unrestricted(),
			Window:      issue.Window{Start: start, End: end},
			Predicates: []issue.Predicate{
				{Field: issue.FieldStatus, Values: []string{"unresolved"}},
			},
			Text: "boom",
		}

		query, args, err := buildListFilterQuery(filters)
		if err != nil {
			t.Fatalf("buildListFilterQuery: %v", err)
		}

		wantQuery := "WITH windowed AS (\n" +
			"\t\tSELECT DISTINCT issue_id\n" +
			"\t\tFROM issue_counts_hourly\n" +
			"\t\tWHERE issue_id IN (\n" +
			"\t\t        SELECT id FROM issues\n" +
			"\t\t        WHERE project_id = (SELECT id FROM projects WHERE slug = $1)\n" +
			"\t\t          AND merged_into_id IS NULL)\n" +
			"\t\t  AND bucket >= $2 AND bucket < $3\n" +
			"\t)" +
			"\n" +
			"\t\tSELECT i.id, i.fingerprint, i.title, i.culprit, i.level, i.status,\n" +
			"\t\t       i.times_seen, i.users_seen, i.first_seen, i.last_seen, u.id, u.name, u.email, p.slug\n" +
			"\t\tFROM issues i\n" +
			"\t\tJOIN projects p ON p.id = i.project_id\n" +
			"\t\tLEFT JOIN users u ON u.id = i.assignee_id\n" +
			"\t\tWHERE p.slug = $1 AND i.merged_into_id IS NULL" +
			" AND true" +
			" AND i.id IN (SELECT issue_id FROM windowed)" +
			" AND (i.status::text = ANY($4))" +
			" AND (i.title ILIKE $5 OR i.culprit ILIKE $5)"
		if query != wantQuery {
			t.Fatalf("query = %q, want %q", query, wantQuery)
		}

		wantArgs := []any{
			"web",                  // $1: project slug
			start,                  // $2: window start
			end,                    // $3: window end
			[]string{"unresolved"}, // $4: status value
			"%boom%",               // $5: free-text pattern
		}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args = %#v, want %#v", args, wantArgs)
		}
	})

	// TestListAbsoluteWindowExcludesGapIssues names the correctness case this
	// pins for: the aggregate join is authoritative on its own, and a row-level
	// last_seen/first_seen prefilter here would silently reintroduce a false
	// negative under client clock skew (see windowClause's doc comment) — this
	// asserts the prefilter cannot come back without the test noticing.
	t.Run("absolute window has no row-level prefilter", func(t *testing.T) {
		start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		end := start.Add(24 * time.Hour)
		filters := issue.ListFilters{
			ProjectSlug: "web",
			Scope:       access.Unrestricted(),
			Window:      issue.Window{Start: start, End: end},
		}

		query, args, err := buildListFilterQuery(filters)
		if err != nil {
			t.Fatalf("buildListFilterQuery: %v", err)
		}
		// With no Predicates or Text, the WHERE clause is exactly the base
		// condition plus the scope and window predicates — nothing else can be
		// appended after it, so this pins the window predicate's full literal
		// content, not just a substring match that a stray added column could
		// still satisfy.
		if !strings.HasSuffix(query, "WHERE p.slug = $1 AND i.merged_into_id IS NULL AND true AND i.id IN (SELECT issue_id FROM windowed)") {
			t.Fatalf("query = %q, want the aggregate-membership predicate and nothing else in the WHERE clause", query)
		}
		if len(args) != 3 {
			t.Fatalf("args = %#v, want exactly [slug, start, end] with no row-predicate args", args)
		}
	})

	// TestBuildListFilterQueryCombinesWindowPredicatesAndFreeText's other
	// subtests use a status predicate, which mints one placeholder. A
	// tag-family predicate mints two (value slot, then key slot) — the
	// ordering most likely to break once a window is also minting
	// placeholders ahead of the Predicates loop.
	t.Run("absolute window with tag-family predicate and free text", func(t *testing.T) {
		start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		end := start.Add(24 * time.Hour)
		filters := issue.ListFilters{
			ProjectSlug: "web",
			Scope:       access.Unrestricted(),
			Window:      issue.Window{Start: start, End: end},
			Predicates: []issue.Predicate{
				{Field: issue.FieldTag, Key: "tenant", Values: []string{"acme"}},
			},
			Text: "boom",
		}

		query, args, err := buildListFilterQuery(filters)
		if err != nil {
			t.Fatalf("buildListFilterQuery: %v", err)
		}

		wantQuery := "WITH windowed AS (\n" +
			"\t\tSELECT DISTINCT issue_id\n" +
			"\t\tFROM issue_counts_hourly\n" +
			"\t\tWHERE issue_id IN (\n" +
			"\t\t        SELECT id FROM issues\n" +
			"\t\t        WHERE project_id = (SELECT id FROM projects WHERE slug = $1)\n" +
			"\t\t          AND merged_into_id IS NULL)\n" +
			"\t\t  AND bucket >= $2 AND bucket < $3\n" +
			"\t)" +
			"\n" +
			"\t\tSELECT i.id, i.fingerprint, i.title, i.culprit, i.level, i.status,\n" +
			"\t\t       i.times_seen, i.users_seen, i.first_seen, i.last_seen, u.id, u.name, u.email, p.slug\n" +
			"\t\tFROM issues i\n" +
			"\t\tJOIN projects p ON p.id = i.project_id\n" +
			"\t\tLEFT JOIN users u ON u.id = i.assignee_id\n" +
			"\t\tWHERE p.slug = $1 AND i.merged_into_id IS NULL" +
			" AND true" +
			" AND i.id IN (SELECT issue_id FROM windowed)" +
			" AND (EXISTS (SELECT 1 FROM issue_tag_values t\n" +
			"\t\tWHERE t.issue_id = i.id AND t.key = $5 AND (t.value = ANY($4))))" +
			" AND (i.title ILIKE $6 OR i.culprit ILIKE $6)"
		if query != wantQuery {
			t.Fatalf("query = %q, want %q", query, wantQuery)
		}

		wantArgs := []any{
			"web",            // $1: project slug
			start,            // $2: window start
			end,              // $3: window end
			[]string{"acme"}, // $4: tag value (no wildcard, so = ANY)
			"tenant",         // $5: tag key
			"%boom%",         // $6: free-text pattern
		}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args = %#v, want %#v", args, wantArgs)
		}
	})
}

// TestBuildListFilterQueryAcrossEveryProject pins the slug-less shape behind
// GET /api/v1/issues: $1 stays reserved (bound to NULL) so every predicate
// keeps the placeholder numbering the per-project shape uses, while the scope
// predicate is the only thing left restricting which projects are read.
func TestBuildListFilterQueryAcrossEveryProject(t *testing.T) {
	t.Run("limited scope with a predicate", func(t *testing.T) {
		filters := issue.ListFilters{
			Scope:      access.Limited([]int64{7, 9}),
			Predicates: []issue.Predicate{{Field: issue.FieldStatus, Values: []string{"unresolved"}}},
		}

		query, args, err := buildListFilterQuery(filters)
		if err != nil {
			t.Fatalf("buildListFilterQuery: %v", err)
		}

		if !strings.HasSuffix(query, "WHERE $1::text IS NULL AND i.merged_into_id IS NULL"+
			" AND p.id = ANY($3)"+
			" AND (i.status::text = ANY($2))") {
			t.Fatalf("query = %q, want the slug restriction replaced by a NULL-bound $1", query)
		}
		wantArgs := []any{
			nil,                    // $1: no project slug
			[]string{"unresolved"}, // $2: status value
			[]int64{7, 9},          // $3: projects in scope
		}
		if !reflect.DeepEqual(args, wantArgs) {
			t.Fatalf("args = %#v, want %#v", args, wantArgs)
		}
	})

	t.Run("absolute window does not restrict the aggregate to one project", func(t *testing.T) {
		start := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		filters := issue.ListFilters{
			Scope:  access.Unrestricted(),
			Window: issue.Window{Start: start, End: start.Add(24 * time.Hour)},
		}

		query, _, err := buildListFilterQuery(filters)
		if err != nil {
			t.Fatalf("buildListFilterQuery: %v", err)
		}

		if strings.Contains(query, "slug = $1") {
			t.Fatalf("query = %q, want no slug lookup in the every-project shape", query)
		}
		if !strings.Contains(query, "SELECT id FROM issues\n\t\t        WHERE merged_into_id IS NULL)") {
			t.Fatalf("query = %q, want the windowed CTE to read every live issue", query)
		}
	})
}

// TestSortColumnMapsUsersSeenToItsOwnColumn pins users_seen alongside the
// three existing sorts, following SortTimesSeen's shape exactly.
func TestSortColumnMapsUsersSeenToItsOwnColumn(t *testing.T) {
	cases := map[issue.Sort]string{
		issue.SortLastSeen:  "i.last_seen",
		issue.SortTimesSeen: "i.times_seen",
		issue.SortFirstSeen: "i.first_seen",
		issue.SortUsersSeen: "i.users_seen",
	}
	for sort, want := range cases {
		if got := sortColumn(sort); got != want {
			t.Fatalf("sortColumn(%q) = %q, want %q", sort, got, want)
		}
	}
}

// TestCursorSortValueParsesUsersSeenAsBigint pins that users_seen shares
// times_seen's bigint parsing, and — the subtle part — that a value produced
// under one bigint-shaped sort is NOT rejected by cursorSortValue when
// presented under the other: both parse cleanly as int64, so this parser
// alone cannot catch a swapped cursor. That's why issue.Cursor.RequireSort
// exists as a separate, explicit check upstream of this function.
func TestCursorSortValueParsesUsersSeenAsBigint(t *testing.T) {
	value, err := cursorSortValue(issue.SortUsersSeen, "250")
	if err != nil {
		t.Fatalf("cursorSortValue(SortUsersSeen, \"250\"): %v", err)
	}
	if value != int64(250) {
		t.Fatalf("cursorSortValue(SortUsersSeen, \"250\") = %v, want int64(250)", value)
	}

	// A times_seen-shaped value still parses fine under users_seen: proving
	// this function does not itself distinguish the two sorts.
	if _, err := cursorSortValue(issue.SortUsersSeen, "1000"); err != nil {
		t.Fatalf("cursorSortValue(SortUsersSeen, \"1000\"): %v", err)
	}

	if _, err := cursorSortValue(issue.SortUsersSeen, "not-a-number"); err == nil {
		t.Fatalf("cursorSortValue(SortUsersSeen, \"not-a-number\") = nil error, want error")
	}
}

// TestSortCursorValueFormatsUsersSeenAsDecimal pins the encode side that
// pairs with TestCursorSortValueParsesUsersSeenAsBigint.
func TestSortCursorValueFormatsUsersSeenAsDecimal(t *testing.T) {
	listed := issue.Issue{UsersSeen: 250, TimesSeen: 1000}
	if got := sortCursorValue(issue.SortUsersSeen, listed); got != "250" {
		t.Fatalf("sortCursorValue(SortUsersSeen, ...) = %q, want %q", got, "250")
	}
}
