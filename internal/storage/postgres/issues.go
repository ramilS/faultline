package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/issue"
)

// maxTimelineSpans bounds the spans returned for one request timeline; legitimate
// requests have far fewer, so this only caps pathological accumulation (spans are
// not deduplicated on retry).
const maxTimelineSpans = 2000

// mergedSparklineRefreshTimeout bounds the background issue_counts_hourly
// refresh scheduleMergedSparklineRefresh launches after a merge commits. It is
// generous because nothing waits on this anymore, but it must still be finite
// so a wedged refresh_continuous_aggregate call cannot hold its goroutine (and
// a pool connection) open forever.
const mergedSparklineRefreshTimeout = 10 * time.Minute

// environmentTagKey and releaseTagKey are the two reserved keys that
// tagFacets breaks out into Detail.Environments/Detail.Releases instead of
// the generic facet list, and that TagKeys excludes from the project-level
// tag vocabulary for the same reason — the dedicated Environment/Release
// fields and filter controls are what the API response documents and the UI
// renders, so the same keys must not also appear as ordinary tags.
const (
	environmentTagKey = "environment"
	releaseTagKey     = "release"
)

type issueRepository struct {
	pool                       *pgxpool.Pool
	invalidator                grouping.CacheInvalidator
	reportError                func(error)
	afterMergeSparklineRefresh func()
}

var _ issue.Repository = (*issueRepository)(nil)

// NewIssueRepository returns the issue.Repository adapter backed by pool. An
// optional grouping.CacheInvalidator (at most one is used) is invalidated for
// the affected project synchronously after a successful merge commit — see
// MergeIssue's doc comment. Omitting it (as every non-production caller and
// every other repository method does) is safe: MergeIssue simply skips the
// call, and ordinary rule CRUD never touches this at all. Errors from
// MergeIssue's best-effort background sparkline refresh are silently
// swallowed by this constructor (use NewIssueRepositoryWithOptions to observe
// them) — acceptable here because every existing caller of this constructor
// is a test that doesn't assert on that error path.
func NewIssueRepository(pool *pgxpool.Pool, invalidators ...grouping.CacheInvalidator) issue.Repository {
	options := IssueRepositoryOptions{}
	if len(invalidators) > 0 {
		options.Invalidator = invalidators[0]
	}
	return NewIssueRepositoryWithOptions(pool, options)
}

// IssueRepositoryOptions configures optional behavior for the issue.Repository
// Postgres adapter beyond the required pool.
type IssueRepositoryOptions struct {
	// Invalidator is called synchronously right after a merge commit — see
	// MergeIssue's doc comment for why merge alone needs this.
	Invalidator grouping.CacheInvalidator
	// ReportError observes a failure (or a recovered panic) in the
	// issue_counts_hourly continuous-aggregate refresh MergeIssue schedules
	// in the background after a merge commits — see
	// scheduleMergedSparklineRefresh. Nil is a safe no-op.
	ReportError func(error)
	// AfterMergeSparklineRefresh, when set, is called once that background
	// refresh has finished, whatever the outcome. It exists purely so tests
	// can synchronize with the goroutine instead of sleeping or polling;
	// production callers should leave it nil.
	AfterMergeSparklineRefresh func()
}

// NewIssueRepositoryWithOptions is NewIssueRepository with full control over
// its optional collaborators.
func NewIssueRepositoryWithOptions(pool *pgxpool.Pool, options IssueRepositoryOptions) issue.Repository {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &issueRepository{
		pool:                       pool,
		invalidator:                options.Invalidator,
		reportError:                reportError,
		afterMergeSparklineRefresh: options.AfterMergeSparklineRefresh,
	}
}

// sortColumn maps a validated sort to its issues column. The service rejects
// any other value before the repository is reached.
func sortColumn(sort issue.Sort) string {
	switch sort {
	case issue.SortTimesSeen:
		return "i.times_seen"
	case issue.SortFirstSeen:
		return "i.first_seen"
	case issue.SortUsersSeen:
		return "i.users_seen"
	default:
		return "i.last_seen"
	}
}

// buildListFilterQuery renders the base issue-list SELECT plus its scope,
// window, predicate, and free-text WHERE clauses, and the args bound to it in
// bind order. It is split out from List so the $1-project-slug-then-window-
// then-predicates-then-text placeholder ordering can be pinned by a unit test
// without a database: List appends its own cursor/sort/limit clauses (which
// reuse len(args) the same way) directly onto this result. The predicate
// builder is created before the SELECT itself so windowClause can mint its
// placeholders first and its CTE (absolute windows only) can be prepended
// ahead of the SELECT it's referenced from.
//
// The scope predicate is computed last, after every other placeholder is
// already minted, and bound at whatever $n that leaves free — never at a
// fixed position. Slotting it in earlier would force every window/predicate/
// text placeholder computed by the shared predicateBuilder to renumber around
// it; binding it last instead means its clause can still sit textually right
// after the base WHERE (scopePredicate's own SQL fragment, not its argument
// position, is what needs to read naturally there).
func buildListFilterQuery(filters issue.ListFilters) (string, []any, error) {
	predicates := newPredicateBuilder()
	cte, windowPredicate := windowClause(filters.Window, predicates)

	for _, predicate := range filters.Predicates {
		if err := predicates.add(predicate); err != nil {
			return "", nil, fmt.Errorf("list issues: %w", err)
		}
	}

	args := []any{filters.ProjectSlug}
	args = append(args, predicates.args()...)

	textClause := ""
	if filters.Text != "" {
		args = append(args, "%"+filters.Text+"%")
		textClause = fmt.Sprintf(" AND (i.title ILIKE $%d OR i.culprit ILIKE $%d)", len(args), len(args))
	}

	scopeClause, scopeArgs := scopePredicate(filters.Scope, "p.id", len(args)+1)
	args = append(args, scopeArgs...)

	builder := &strings.Builder{}
	builder.WriteString(cte)
	builder.WriteString(`
		SELECT i.id, i.fingerprint, i.title, i.culprit, i.level, i.status,
		       i.times_seen, i.users_seen, i.first_seen, i.last_seen, u.id, u.name, u.email
		FROM issues i
		JOIN projects p ON p.id = i.project_id
		LEFT JOIN users u ON u.id = i.assignee_id
		WHERE p.slug = $1 AND i.merged_into_id IS NULL`)
	builder.WriteString(scopeClause)
	builder.WriteString(windowPredicate)
	builder.WriteString(predicates.sql())
	builder.WriteString(textClause)
	return builder.String(), args, nil
}

func (repository *issueRepository) List(ctx context.Context, filters issue.ListFilters) (issue.Page, error) {
	query, args, err := buildListFilterQuery(filters)
	if err != nil {
		return issue.Page{}, err
	}
	builder := &strings.Builder{}
	builder.WriteString(query)

	column := sortColumn(filters.Sort)
	if filters.Cursor != nil {
		value, err := cursorSortValue(filters.Sort, filters.Cursor.Value)
		if err != nil {
			return issue.Page{}, err
		}
		args = append(args, value, filters.Cursor.ID)
		fmt.Fprintf(builder, " AND (%s, i.id) < ($%d, $%d)", column, len(args)-1, len(args))
	}

	// The service normally clamps Limit to a positive value (NormalizeLimit);
	// guard here too so a stray zero/negative value can't panic the slice math
	// below instead of just returning no next cursor.
	limit := max(filters.Limit, 1)
	args = append(args, limit+1)
	fmt.Fprintf(builder, " ORDER BY %s DESC, i.id DESC LIMIT $%d", column, len(args))

	rows, err := repository.pool.Query(ctx, builder.String(), args...)
	if err != nil {
		return issue.Page{}, translateIssueError("list issues", err)
	}
	defer rows.Close()

	issues := make([]issue.Issue, 0, limit)
	for rows.Next() {
		var (
			listed                      issue.Issue
			assigneeID                  *int64
			assigneeName, assigneeEmail *string
		)
		if err := rows.Scan(&listed.ID, &listed.Fingerprint, &listed.Title, &listed.Culprit,
			&listed.Level, &listed.Status, &listed.TimesSeen, &listed.UsersSeen, &listed.FirstSeen, &listed.LastSeen,
			&assigneeID, &assigneeName, &assigneeEmail); err != nil {
			return issue.Page{}, translateIssueError("scan issue", err)
		}
		listed.FirstSeen = listed.FirstSeen.UTC()
		listed.LastSeen = listed.LastSeen.UTC()
		if assigneeID != nil {
			listed.Assignee = &issue.Assignee{ID: *assigneeID, Name: deref(assigneeName), Email: deref(assigneeEmail)}
		}
		issues = append(issues, listed)
	}
	if err := rows.Err(); err != nil {
		return issue.Page{}, translateIssueError("iterate issues", err)
	}
	if len(issues) == 0 {
		if err := repository.requireProjectInScope(ctx, filters.Scope, filters.ProjectSlug); err != nil {
			return issue.Page{}, err
		}
	}

	page := issue.Page{}
	if len(issues) > limit {
		last := issues[limit-1]
		issues = issues[:limit]
		page.NextCursor = &issue.Cursor{Sort: filters.Sort, Value: sortCursorValue(filters.Sort, last), ID: last.ID}
	}
	page.Issues = issues

	if err := repository.attachSparklines(ctx, page.Issues, filters.Window); err != nil {
		return issue.Page{}, err
	}
	return page, nil
}

func (repository *issueRepository) Detail(ctx context.Context, scope access.Scope, id int64) (issue.Detail, error) {
	predicate, scopeArgs := scopePredicate(scope, "i.project_id", 2)
	args := append([]any{id}, scopeArgs...)
	var (
		detail                      issue.Detail
		assigneeID                  *int64
		assigneeName, assigneeEmail *string
	)
	err := repository.pool.QueryRow(ctx, `
		SELECT i.id, i.fingerprint, i.title, i.culprit, i.level, i.status,
		       i.times_seen, i.users_seen, i.first_seen, i.last_seen, u.id, u.name, u.email, i.merged_into_id
		FROM issues i
		LEFT JOIN users u ON u.id = i.assignee_id
		WHERE i.id = $1`+predicate, args...).Scan(&detail.Issue.ID, &detail.Issue.Fingerprint, &detail.Issue.Title,
		&detail.Issue.Culprit, &detail.Issue.Level, &detail.Issue.Status,
		&detail.Issue.TimesSeen, &detail.Issue.UsersSeen, &detail.Issue.FirstSeen, &detail.Issue.LastSeen,
		&assigneeID, &assigneeName, &assigneeEmail, &detail.Issue.MergedIntoID)
	if err != nil {
		return issue.Detail{}, translateIssueError("issue detail", err)
	}
	detail.Issue.FirstSeen = detail.Issue.FirstSeen.UTC()
	detail.Issue.LastSeen = detail.Issue.LastSeen.UTC()
	if assigneeID != nil {
		detail.Issue.Assignee = &issue.Assignee{ID: *assigneeID, Name: deref(assigneeName), Email: deref(assigneeEmail)}
	}
	// A tombstoned issue has no future events, so its tag facets and
	// sparklines carry nothing a caller reads; skip both queries for it.
	if detail.Issue.MergedIntoID != nil {
		return detail, nil
	}

	facets, environments, releases, err := repository.tagFacets(ctx, id)
	if err != nil {
		return issue.Detail{}, err
	}
	detail.Tags = facets
	detail.Environments = environments
	detail.Releases = releases

	sparked := []issue.Issue{detail.Issue}
	if err := repository.attachSparklines(ctx, sparked, issue.Window{}); err != nil {
		return issue.Detail{}, err
	}
	detail.Issue = sparked[0]

	return detail, nil
}

func (repository *issueRepository) tagFacets(ctx context.Context, id int64) ([]issue.TagFacet, []string, []string, error) {
	rows, err := repository.pool.Query(ctx, `
		SELECT key, value, times_seen, first_seen, last_seen
		FROM issue_tag_values
		WHERE issue_id = $1
		ORDER BY key, times_seen DESC, value
	`, id)
	if err != nil {
		return nil, nil, nil, translateIssueError("tag facets", err)
	}
	defer rows.Close()

	facets := make([]issue.TagFacet, 0)
	byKey := make(map[string]int)
	var environments, releases []string
	for rows.Next() {
		var (
			key, value string
			tag        issue.TagValue
		)
		if err := rows.Scan(&key, &value, &tag.TimesSeen, &tag.FirstSeen, &tag.LastSeen); err != nil {
			return nil, nil, nil, translateIssueError("scan tag facet", err)
		}
		tag.Value = value
		tag.FirstSeen = tag.FirstSeen.UTC()
		tag.LastSeen = tag.LastSeen.UTC()
		switch key {
		case environmentTagKey:
			environments = append(environments, value)
			continue
		case releaseTagKey:
			releases = append(releases, value)
			continue
		}
		position, ok := byKey[key]
		if !ok {
			position = len(facets)
			byKey[key] = position
			facets = append(facets, issue.TagFacet{Key: key})
		}
		facets[position].Values = append(facets[position].Values, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, nil, translateIssueError("iterate tag facets", err)
	}
	return facets, environments, releases, nil
}

func (repository *issueRepository) UpdateStatus(ctx context.Context, scope access.Scope, id int64, status issue.Status) error {
	predicate, scopeArgs := scopePredicate(scope, "project_id", 3)
	args := append([]any{id, string(status)}, scopeArgs...)
	var updated int64
	err := repository.pool.QueryRow(ctx, `
		UPDATE issues SET status = $2 WHERE id = $1`+predicate+` RETURNING id
	`, args...).Scan(&updated)
	if err != nil {
		return translateIssueError("update issue status", err)
	}
	return nil
}

func (repository *issueRepository) Assign(ctx context.Context, scope access.Scope, id int64, assigneeID *int64) error {
	predicate, scopeArgs := scopePredicate(scope, "project_id", 3)
	args := append([]any{id, assigneeID}, scopeArgs...)
	var updated int64
	err := repository.pool.QueryRow(ctx, `
		UPDATE issues SET assignee_id = $2 WHERE id = $1`+predicate+` RETURNING id
	`, args...).Scan(&updated)
	if err != nil {
		if pgError, ok := errors.AsType[*pgconn.PgError](err); ok && pgError.Code == "23503" {
			return fmt.Errorf("assign issue: %w", issue.ErrInvalidAssignee)
		}
		return translateIssueError("assign issue", err)
	}
	return nil
}

// CanBeAssigned reports whether userID may be assigned issueID: the account
// exists, is not deactivated, and reaches the issue's project.
//
// The foreign key on issues.assignee_id already proves existence. Activity and
// access are the halves no constraint can express — access least of all, since
// it is a property of the pair, not of either row.
func (repository *issueRepository) CanBeAssigned(ctx context.Context, issueID, userID int64) (bool, error) {
	var allowed bool
	err := repository.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM issues i
			JOIN users u ON u.id = $2
			WHERE i.id = $1
			  AND u.deactivated_at IS NULL
			  AND (u.role = 'owner' OR EXISTS (
			      SELECT 1
			      FROM group_members gm
			      JOIN group_projects gp ON gp.group_id = gm.group_id
			      WHERE gm.user_id = u.id AND gp.project_id = i.project_id
			  ))
		)
	`, issueID, userID).Scan(&allowed)
	if err != nil {
		return false, translateIssueError("assignee eligibility", err)
	}
	return allowed, nil
}

// mergeIssueRow is the row shape locked and read for either side of a merge.
type mergeIssueRow struct {
	id           int64
	projectID    int64
	fingerprint  string
	timesSeen    int64
	firstSeen    time.Time
	lastSeen     time.Time
	mergedIntoID *int64
}

// lockMergeParticipants locks, in one statement and in ascending id order,
// every issues row this merge transaction will read or write: sourceID and
// targetID themselves, plus any row already tombstoned onto either of them
// (merged_into_id IN (sourceID, targetID)) — exactly the set the cascade
// flatten UPDATE below touches.
//
// Locking that whole set here — via ORDER BY id FOR UPDATE, which Postgres
// executes as a blocking Sort feeding a LockRows node, so rows are locked one
// at a time in the sorted order, not scan order — is what makes ascending-id
// locking an actual deadlock-free invariant for this transaction. Locking
// only sourceID and targetID up front (as an earlier version of this method
// did) is not enough on its own: the flatten UPDATE two statements later
// would then request a lock on an arbitrary prior-tombstone row whose id is
// unordered relative to the two already held, and two concurrent merges that
// each hold one of those and want the other can deadlock (Postgres aborts
// one with 40P01). Pre-locking the full affected set closes that gap by
// never taking a second, unordered lock later in the transaction.
func (repository *issueRepository) lockMergeParticipants(ctx context.Context, tx pgx.Tx, scope access.Scope, sourceID, targetID int64) (map[int64]mergeIssueRow, error) {
	ids := []int64{sourceID, targetID}
	predicate, scopeArgs := scopePredicate(scope, "project_id", 2)
	args := append([]any{ids}, scopeArgs...)
	rows, err := tx.Query(ctx, `
		SELECT id, project_id, fingerprint, times_seen, first_seen, last_seen, merged_into_id
		FROM issues
		WHERE (id = ANY($1) OR merged_into_id = ANY($1))`+predicate+`
		ORDER BY id
		FOR UPDATE
	`, args...)
	if err != nil {
		return nil, translateIssueError("merge issue: lock participants", err)
	}
	defer rows.Close()

	participants := make(map[int64]mergeIssueRow)
	for rows.Next() {
		var row mergeIssueRow
		if err := rows.Scan(&row.id, &row.projectID, &row.fingerprint, &row.timesSeen,
			&row.firstSeen, &row.lastSeen, &row.mergedIntoID); err != nil {
			return nil, translateIssueError("merge issue: scan locked participant", err)
		}
		participants[row.id] = row
	}
	if err := rows.Err(); err != nil {
		return nil, translateIssueError("merge issue: iterate locked participants", err)
	}
	return participants, nil
}

// MergeIssue merges source into target in one transaction, in these steps:
// flatten prior tombstones and
// repoint prior alias-rule templates (cascade, for chained merges), mint a
// new fingerprint for target, insert alias rules resolving old fingerprints
// to it (skipping any that would be provably unreachable — see the alias
// insertion step below), reassign events, fold tag facets and counters,
// tombstone source, and invalidate the target project's rule cache entry.
func (repository *issueRepository) MergeIssue(ctx context.Context, scope access.Scope, sourceID, targetID int64) (issue.Detail, error) {
	// Service.MergeIssue already rejects this, but guard it here too: with
	// source == target the steps below would fold an issue's tag facets and
	// counters into itself (doubling both) and then tombstone it onto its own
	// id, permanently hiding a live issue behind a self-referential
	// merged_into_id with no reachable target. That is silent, unrecoverable
	// data corruption, so this repository must not trust callers to have
	// checked first.
	if sourceID == targetID {
		return issue.Detail{}, fmt.Errorf("merge issue: %w", issue.ErrSameIssue)
	}

	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return issue.Detail{}, fmt.Errorf("begin merge issue: %w", err)
	}
	defer tx.Rollback(ctx)

	participants, err := repository.lockMergeParticipants(ctx, tx, scope, sourceID, targetID)
	if err != nil {
		return issue.Detail{}, err
	}
	source, sourceFound := participants[sourceID]
	target, targetFound := participants[targetID]
	if !sourceFound || !targetFound {
		return issue.Detail{}, fmt.Errorf("merge issue: %w", issue.ErrNotFound)
	}

	if source.projectID != target.projectID {
		// Cross-project: reported identically to "missing" so a caller cannot
		// learn that a given id exists in a project they can't see into.
		return issue.Detail{}, fmt.Errorf("merge issue: %w", issue.ErrNotFound)
	}
	if source.mergedIntoID != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: source: %w", issue.ErrAlreadyMerged)
	}
	if target.mergedIntoID != nil {
		// Target is itself a tombstone from an earlier merge — the caller must
		// merge into the live head of that chain, not a link partway along it.
		return issue.Detail{}, fmt.Errorf("merge issue: target: %w", issue.ErrAlreadyMerged)
	}

	// Merge inserts new grouping_rules rows below, so it takes the same
	// per-project advisory lock Create/Move/Delete use to serialize
	// position-mutating writes (see lockProjectForRuleMutation in
	// grouping_rules.go). Without it, a concurrent Create could compute its
	// "next position" from the same MAX(position) this merge is about to
	// insert past, and the two INSERTs could collide on the table's
	// UNIQUE (project_id, position) constraint.
	if err := lockProjectForRuleMutation(ctx, tx, target.projectID); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: lock project for grouping rule mutation: %w", err)
	}

	// Cascade repoint: source may itself be an earlier merge's target, so
	// other issues' tombstones may point at it and existing alias rules may
	// resolve to its fingerprint-derived template. Flatten both directly onto
	// target before minting target's new fingerprint below.
	if _, err := tx.Exec(ctx, `UPDATE issues SET merged_into_id = $2 WHERE merged_into_id = $1`, sourceID, targetID); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: flatten prior tombstones: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE grouping_rules SET template = $2 WHERE project_id = $3 AND template = $1
	`, grouping.MergeTemplate(sourceID), grouping.MergeTemplate(targetID), target.projectID); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: repoint prior alias rules: %w", err)
	}

	newFingerprint := grouping.MergeFingerprint(targetID)
	if _, err := tx.Exec(ctx, `UPDATE issues SET fingerprint = $2 WHERE id = $1`, targetID, newFingerprint); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: mint target fingerprint: %w", err)
	}

	if err := repository.insertMergeAliasRules(ctx, tx, target.projectID, targetID, source, target); err != nil {
		return issue.Detail{}, err
	}

	// sparklineRefreshWindowStart must be read from source's own events
	// before they are reassigned below, and must use received_at (the
	// server-stamped clock issue_counts_hourly buckets on), not
	// source.firstSeen — first_seen comes straight from the client-supplied
	// event timestamp (pipeline/aggregate.go's issueDeltaFor), so a client
	// clock running fast makes first_seen later than the earliest moved
	// received_at bucket, and a refresh window derived from first_seen alone
	// would never reach that oldest bucket. LEAST of the two is a defensive
	// floor for the (expected empty) case where source has no events at all.
	var sparklineRefreshWindowStart time.Time
	if err := tx.QueryRow(ctx, `
		SELECT LEAST($2::timestamptz, COALESCE(MIN(received_at), $2::timestamptz)) FROM events WHERE issue_id = $1
	`, sourceID, source.firstSeen).Scan(&sparklineRefreshWindowStart); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: compute sparkline refresh window: %w", err)
	}

	// events.issue_id is part of the columnstore segmentby key (see
	// migrations/000004_policies.up.sql), so once a source issue has more
	// than 24h of history its older events sit in compressed segments this
	// UPDATE must rewrite. TimescaleDB caps how many compressed tuples a
	// single DML statement may decompress
	// (timescaledb.max_tuples_decompressed_per_dml_transaction, default
	// 100,000) precisely to stop a runaway UPDATE from blowing up — but here
	// the UPDATE is a deliberate, bounded, admin-triggered rewrite of exactly
	// one issue's events, and hitting that cap would abort the whole merge
	// with "tuple decompression limit exceeded", not degrade gracefully.
	// SET LOCAL scopes the override to this transaction only. Merging a
	// multi-hundred-thousand-event issue takes on the order of one second per
	// 100–150k events (observed on timescale/timescaledb:latest-pg18), during which this transaction still
	// holds the issues row locks taken by lockMergeParticipants above; very
	// large historical merges should be expected to hold those locks for
	// several seconds, not milliseconds.
	if _, err := tx.Exec(ctx, `SET LOCAL timescaledb.max_tuples_decompressed_per_dml_transaction = 0`); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: lift decompression limit: %w", err)
	}
	if _, err := tx.Exec(ctx, `UPDATE events SET issue_id = $2 WHERE issue_id = $1`, sourceID, targetID); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: reassign events: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO issue_tag_values (project_id, issue_id, key, value, times_seen, first_seen, last_seen)
		SELECT project_id, $2, key, value, times_seen, first_seen, last_seen
		FROM issue_tag_values WHERE issue_id = $1
		ON CONFLICT (issue_id, key, value) DO UPDATE SET
			times_seen = issue_tag_values.times_seen + EXCLUDED.times_seen,
			first_seen = LEAST(issue_tag_values.first_seen, EXCLUDED.first_seen),
			last_seen  = GREATEST(issue_tag_values.last_seen, EXCLUDED.last_seen)
	`, sourceID, targetID); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: fold tag facets: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM issue_tag_values WHERE issue_id = $1`, sourceID); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: delete source tag facets: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE issues SET times_seen = times_seen + $2, first_seen = LEAST(first_seen, $3), last_seen = GREATEST(last_seen, $4)
		WHERE id = $1
	`, targetID, source.timesSeen, source.firstSeen, source.lastSeen); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: fold counters: %w", err)
	}

	if _, err := tx.Exec(ctx, `UPDATE issues SET merged_into_id = $2 WHERE id = $1`, sourceID, targetID); err != nil {
		return issue.Detail{}, fmt.Errorf("merge issue: tombstone source: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return issue.Detail{}, fmt.Errorf("commit merge issue: %w", err)
	}

	// Merge-only cache invalidation (the one exception to the rule cache's
	// accept-up-to-30s-staleness contract): a
	// stale rule read after merge would fingerprint a future event from the
	// source's signal onto the just-tombstoned source issue rather than
	// target, permanently losing it from every list. Unconditional and
	// synchronous whenever an invalidator was wired in — see
	// NewIssueRepository — so this cannot be silently skipped by a caller
	// that remembered to wire the cache but forgot to invalidate after
	// merging. Any envelope that still lands on the tombstone despite this
	// (a rule lookup that already completed before this commit, a rule edit
	// that raced it, or an alias rule since deleted) is caught structurally
	// by the batch writer's issue upsert (upsertIssues), which resolves a tombstoned
	// fingerprint to its live target directly.
	if repository.invalidator != nil {
		repository.invalidator.Invalidate(target.projectID)
	}
	repository.scheduleMergedSparklineRefresh(ctx, sparklineRefreshWindowStart, targetID)
	return repository.Detail(ctx, scope, targetID)
}

// scheduleMergedSparklineRefresh runs refreshMergedSparklines in a detached
// background goroutine instead of on MergeIssue's caller-facing path.
// refresh_continuous_aggregate takes a time *window*, not an issue filter, so
// it re-materializes every issue's buckets across that window — for an old
// issue on a project with real ingest volume that can mean re-aggregating
// close to the full events retention window, not a cost proportional to the
// merged issue alone. Running that inline (as an earlier version of this
// method did) blocked the merge HTTP response on it while still holding a
// pool connection; worse, if the caller gave up, the request context
// cancelling aborted the refresh mid-way *and* made the very next
// repository.Detail(ctx, targetID) call fail on that same cancelled context —
// so the endpoint returned an error for a merge that had already committed,
// and a retry then failed with ErrAlreadyMerged.
//
// context.WithoutCancel detaches the background work from ctx's
// cancellation: the merge already committed by the time this is called, so a
// disconnecting client (or the request's own deadline) is no reason to abort
// the refresh. mergedSparklineRefreshTimeout still bounds it so a genuinely
// stuck refresh_continuous_aggregate call cannot hold the goroutine (and a
// pool connection) open forever.
//
// An unrecovered panic in a goroutine is fatal to the whole process
// regardless of any recover() elsewhere, so this goroutine must recover its
// own panics; a caught panic is reported exactly like an ordinary refresh
// error rather than crashing the server. afterMergeSparklineRefresh is nil in
// production; tests use it to synchronize with the goroutine's completion
// without sleeping or polling.
func (repository *issueRepository) scheduleMergedSparklineRefresh(ctx context.Context, windowStart time.Time, targetID int64) {
	detached, cancel := context.WithTimeout(context.WithoutCancel(ctx), mergedSparklineRefreshTimeout)
	go func() {
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				repository.reportError(fmt.Errorf("merge issue: sparkline refresh panicked for target %d: %v", targetID, recovered))
			}
			if repository.afterMergeSparklineRefresh != nil {
				repository.afterMergeSparklineRefresh()
			}
		}()
		repository.refreshMergedSparklines(detached, windowStart, targetID)
	}()
}

// refreshMergedSparklines re-materializes issue_counts_hourly buckets from
// the moved source history's earliest point through the present, after
// MergeIssue's transaction has already committed events.issue_id being
// reassigned from source to target. Without this, only the continuous
// aggregate's own incremental refresh policy window ([now-3h, now-5min] on a
// 5-minute schedule) would pick up the change:
// anything merged with more than ~3 hours of history would leave target's
// times_seen (read straight off the issues row) correct immediately while
// its 24h/14d/90d sparklines and Stats graphs kept showing only its own
// pre-merge counts, with the moved history sitting invisibly in the
// now-tombstoned source's old buckets until it aged out of the graph window
// on its own.
//
// This runs against the pool, not tx: refresh_continuous_aggregate is a
// procedure that manages its own transactions internally and cannot be
// called from inside an explicit transaction block, so it must run only
// after MergeIssue's transaction has committed. It is always called from
// scheduleMergedSparklineRefresh's background goroutine now, never inline —
// see that method's doc comment for why. A failure here is reported, not
// fatal — the merge itself already committed, and routing/times_seen/tag
// facets are already correct; this only affects how quickly historical
// graphs catch up.
//
// window_start is truncated to its hour bucket (time_bucket) before being
// passed, not left as the raw instant: verified empirically
// (timescale/timescaledb:latest-pg18) that refresh_continuous_aggregate
// silently treats a bucket as already up to date — logging "continuous
// aggregate is already up-to-date" and refreshing nothing — when window_start
// falls strictly after that bucket's own start, exactly what an untruncated
// timestamp almost always does. Without the truncation, this call would be a
// structural no-op for precisely the oldest bucket a merged issue's history
// starts in, silently leaving that one bucket stale forever.
func (repository *issueRepository) refreshMergedSparklines(ctx context.Context, from time.Time, targetID int64) {
	if _, err := repository.pool.Exec(ctx, `
		CALL refresh_continuous_aggregate('issue_counts_hourly', time_bucket('1 hour', $1::timestamptz), NULL)
	`, from); err != nil {
		repository.reportError(fmt.Errorf("merge issue: refresh issue_counts_hourly sparklines for target %d: %w", targetID, err))
	}
}

// insertMergeAliasRules inserts the fingerprint-kind alias rules that route
// future events from source's and target's pre-merge fingerprints onto
// target's newly-minted one, subject to two dedup checks that keep merge from
// growing the project's rule count with rows that can never affect routing:
//
//  1. Self-referential skip: if source's (or target's) fingerprint already
//     equals grouping.MergeFingerprint(that same issue's id), it was minted by
//     an earlier merge on that exact row (a re-merge into an already-merged
//     target, or a chained merge whose source is itself a prior target). A
//     rule matching that fingerprint is provably unreachable — no ingested
//     event's deterministic fingerprint can equal a hash of "merged,issue-N"
//     without a preimage — so inserting it would be permanent dead weight
//     evaluated on every future event for nothing.
//  2. Existing-matcher skip: if a rule with that exact matcher already exists
//     for the project (from an earlier merge or hand-written), inserting a
//     duplicate adds evaluation cost without adding any new routing.
//
// The survivors are then checked against MaxRulesPerProject the same way
// Create enforces it, and the merge fails outright with ErrTooManyRules
// rather than silently exceeding the cap — see MergeIssue's caller-facing
// doc for why merge does not get a silent exemption from the cap that
// bounds every event's rule-evaluation cost.
func (repository *issueRepository) insertMergeAliasRules(ctx context.Context, tx pgx.Tx, projectID, targetID int64, source, target mergeIssueRow) error {
	rows, err := tx.Query(ctx, `SELECT position, matcher FROM grouping_rules WHERE project_id = $1`, projectID)
	if err != nil {
		return fmt.Errorf("merge issue: list existing grouping rules: %w", err)
	}
	defer rows.Close()

	basePosition := 0
	existingMatchers := make(map[string]struct{})
	existingCount := 0
	for rows.Next() {
		var position int
		var matcher string
		if err := rows.Scan(&position, &matcher); err != nil {
			return fmt.Errorf("merge issue: scan existing grouping rule: %w", err)
		}
		if position > basePosition {
			basePosition = position
		}
		existingMatchers[matcher] = struct{}{}
		existingCount++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("merge issue: iterate existing grouping rules: %w", err)
	}

	aliasTemplate := grouping.MergeTemplate(targetID)
	candidateMatchers := make([]string, 0, 2)
	if source.fingerprint != grouping.MergeFingerprint(source.id) {
		candidateMatchers = append(candidateMatchers, "fingerprint:"+source.fingerprint)
	}
	if target.fingerprint != grouping.MergeFingerprint(target.id) {
		candidateMatchers = append(candidateMatchers, "fingerprint:"+target.fingerprint)
	}

	toInsert := make([]string, 0, len(candidateMatchers))
	for _, matcher := range candidateMatchers {
		if _, exists := existingMatchers[matcher]; exists {
			continue
		}
		toInsert = append(toInsert, matcher)
	}

	if existingCount+len(toInsert) > grouping.MaxRulesPerProject {
		return fmt.Errorf("merge issue: %w", grouping.ErrTooManyRules)
	}

	for offset, matcher := range toInsert {
		if _, err := tx.Exec(ctx, `
			INSERT INTO grouping_rules (project_id, position, matcher, template) VALUES ($1, $2, $3, $4)
		`, projectID, basePosition+1+offset, matcher, aliasTemplate); err != nil {
			return fmt.Errorf("merge issue: insert alias rule: %w", err)
		}
	}
	return nil
}

// cursorSortValue parses the string form back into the type the sort column
// compares against, so the keyset predicate binds a correctly-typed argument.
func cursorSortValue(sort issue.Sort, raw string) (any, error) {
	if sort == issue.SortTimesSeen || sort == issue.SortUsersSeen {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("cursor bigint value: %w", err)
		}
		return value, nil
	}
	value, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return nil, fmt.Errorf("cursor time value: %w", err)
	}
	return value, nil
}

func sortCursorValue(sort issue.Sort, listed issue.Issue) string {
	switch sort {
	case issue.SortTimesSeen:
		return strconv.FormatInt(listed.TimesSeen, 10)
	case issue.SortUsersSeen:
		return strconv.FormatInt(listed.UsersSeen, 10)
	case issue.SortFirstSeen:
		return listed.FirstSeen.Format(time.RFC3339Nano)
	default:
		return listed.LastSeen.Format(time.RFC3339Nano)
	}
}

func deref(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func translateIssueError(operation string, err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%s: %w", operation, issue.ErrNotFound)
	}
	return fmt.Errorf("%s: %w", operation, err)
}

// requireProjectInScope reports issue.ErrNotFound unless slug names a project
// this caller may read.
//
// A collection read cannot answer "no such project" through its rows: an
// unreachable project, a nonexistent one, and one that simply holds nothing
// all come back empty. The slug-keyed reads therefore ask the question
// separately — and only when their result set is empty, so the common,
// non-empty case pays nothing — which is what lets a project outside the
// caller's scope answer 404 exactly like one that was never created.
func (repository *issueRepository) requireProjectInScope(ctx context.Context, scope access.Scope, slug string) error {
	predicate, scopeArgs := scopePredicate(scope, "id", 2)
	args := append([]any{slug}, scopeArgs...)
	var found int
	if err := repository.pool.QueryRow(ctx,
		`SELECT 1 FROM projects WHERE slug = $1`+predicate, args...).Scan(&found); err != nil {
		return translateIssueError("resolve project", err)
	}
	return nil
}

// requireIssueInScope is requireProjectInScope's issue-keyed counterpart: it
// reports issue.ErrNotFound unless id names an issue in a project this caller
// may read. The event list needs it for the same reason — an empty page and a
// foreign issue are otherwise the same response.
func (repository *issueRepository) requireIssueInScope(ctx context.Context, scope access.Scope, id int64) error {
	predicate, scopeArgs := scopePredicate(scope, "project_id", 2)
	args := append([]any{id}, scopeArgs...)
	var found int
	if err := repository.pool.QueryRow(ctx,
		`SELECT 1 FROM issues WHERE id = $1`+predicate, args...).Scan(&found); err != nil {
		return translateIssueError("resolve issue", err)
	}
	return nil
}

// attachSparklines fills a page's occurrence data with the shape the caller's
// Window selects: a zero Window keeps the original fixed 24h/14d pair
// (attachLegacySparklines) so every existing caller stays byte-identical,
// while any other Window replaces both with a single window-scoped query
// (attachWindowedSparklines) whose bucket width follows the window's span.
func (repository *issueRepository) attachSparklines(ctx context.Context, issues []issue.Issue, window issue.Window) error {
	if len(issues) == 0 {
		return nil
	}
	if window.IsZero() {
		return repository.attachLegacySparklines(ctx, issues)
	}
	return repository.attachWindowedSparklines(ctx, issues, window)
}

// attachLegacySparklines fills Counts24h and Counts14d for the whole page
// with two occurrence queries — one hourly window, one daily window — never
// per issue.
func (repository *issueRepository) attachLegacySparklines(ctx context.Context, issues []issue.Issue) error {
	ids, index := issueIDIndex(issues)

	hourly, err := repository.occurrenceMatrix(ctx, ids, `
		WITH buckets AS (
			SELECT generate_series(date_trunc('hour', now()) - interval '23 hours',
			                       date_trunc('hour', now()), interval '1 hour') AS bucket
		), ids AS (SELECT unnest($1::bigint[]) AS issue_id)
		SELECT ids.issue_id, COALESCE(sum(c.n), 0)::bigint
		FROM ids CROSS JOIN buckets b
		LEFT JOIN issue_counts_hourly c ON c.issue_id = ids.issue_id AND c.bucket = b.bucket
		GROUP BY ids.issue_id, b.bucket
		ORDER BY ids.issue_id, b.bucket`)
	if err != nil {
		return err
	}
	for id, counts := range hourly {
		copySpark(issues[index[id]].Counts24h[:], counts)
	}

	daily, err := repository.occurrenceMatrix(ctx, ids, `
		WITH buckets AS (
			SELECT generate_series(date_trunc('day', now()) - interval '13 days',
			                       date_trunc('day', now()), interval '1 day') AS bucket
		), ids AS (SELECT unnest($1::bigint[]) AS issue_id)
		SELECT ids.issue_id, COALESCE(sum(c.n), 0)::bigint
		FROM ids CROSS JOIN buckets b
		LEFT JOIN issue_counts_hourly c ON c.issue_id = ids.issue_id AND date_trunc('day', c.bucket) = b.bucket
		GROUP BY ids.issue_id, b.bucket
		ORDER BY ids.issue_id, b.bucket`)
	if err != nil {
		return err
	}
	for id, counts := range daily {
		copySpark(issues[index[id]].Counts14d[:], counts)
	}
	return nil
}

// attachWindowedSparklines fills Counts and PeriodCount for the whole page
// with a single occurrence query bucketed at bucketWidth(window): one query
// replaces attachLegacySparklines' two, and PeriodCount falls out of the same
// result as the per-bucket sum, so no extra round trip computes it.
func (repository *issueRepository) attachWindowedSparklines(ctx context.Context, issues []issue.Issue, window issue.Window) error {
	ids, index := issueIDIndex(issues)

	now := time.Now().UTC()
	width := bucketWidth(window)
	buckets := windowBuckets(window, width, now)
	_, upperBound := windowBounds(window, now)

	// The upper join bound is LEAST(bucket+width, upperBound), not a plain
	// bucket+width: when span isn't an exact multiple of width, the last
	// bucket's nominal width overruns upperBound (see windowBuckets), and
	// without the clamp that overrun would silently sum events the caller's
	// window never asked for.
	matrix, err := repository.occurrenceMatrix(ctx, ids, `
		WITH buckets AS (SELECT unnest($2::timestamptz[]) AS bucket),
		     ids AS (SELECT unnest($1::bigint[]) AS issue_id)
		SELECT ids.issue_id, COALESCE(sum(c.n), 0)::bigint
		FROM ids CROSS JOIN buckets b
		LEFT JOIN issue_counts_hourly c ON c.issue_id = ids.issue_id
			AND c.bucket >= b.bucket AND c.bucket < LEAST(b.bucket + $3::interval, $4::timestamptz)
		GROUP BY ids.issue_id, b.bucket
		ORDER BY ids.issue_id, b.bucket`, buckets, width.String(), upperBound)
	if err != nil {
		return err
	}
	for id, counts := range matrix {
		listed := &issues[index[id]]
		listed.Counts = counts
		var total int64
		for _, count := range counts {
			total += count
		}
		listed.PeriodCount = total
	}
	return nil
}

// bucketCount is the number of width-sized buckets needed to cover span,
// rounding up so a partial final bucket still counts as a point. bucketWidth
// and windowBuckets both call this rather than each rounding independently:
// they used to disagree (bucketWidth floored, windowBuckets ceiled), which
// let a span just past a tier boundary pass bucketWidth's 90-point cap check
// while windowBuckets went on to generate 91 buckets for it.
func bucketCount(span, width time.Duration) int {
	count := int(span / width)
	if span%width != 0 {
		count++
	}
	return count
}

// bucketWidth picks the smallest bucket that keeps a window under 90 points,
// floored at the aggregate's own one-hour granularity.
func bucketWidth(window issue.Window) time.Duration {
	span := window.Trailing
	if window.IsAbsolute() {
		span = window.End.Sub(window.Start)
	}
	for _, candidate := range []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour} {
		if bucketCount(span, candidate) <= 90 {
			return candidate
		}
	}
	return 24 * time.Hour
}

// windowBounds resolves a Window to its concrete [start, end) instant pair:
// now-Trailing/now for a trailing window, Start/End for an absolute one.
func windowBounds(window issue.Window, now time.Time) (time.Time, time.Time) {
	if window.IsAbsolute() {
		return window.Start, window.End
	}
	return now.Add(-window.Trailing), now
}

// windowBuckets returns the ascending bucket start times covering window,
// anchored at the window's own lower bound rather than truncated to any
// clock grid: the first bucket is always exactly windowBounds' start, so no
// event just inside the window's opening edge falls in a gap before bucket
// zero (trailing windows used to anchor at the *upper* bound and step
// backward by whole buckets, which — since "now" rarely lands on a bucket
// boundary — left up to one full bucket width uncovered at the start; an
// issue whose only event fell there reported PeriodCount 0 despite being
// listed as a window match). Anchoring at the start instead of the end also
// means a non-exact-multiple span's rounding slop (see bucketCount) now
// lands at the *end* of the range, past upperBound, where
// attachWindowedSparklines' query clamp cuts it off — not at the start,
// where it would count events the caller's window explicitly excludes. now
// is a parameter so this stays a pure function under test rather than
// reaching for the wall clock itself.
func windowBuckets(window issue.Window, width time.Duration, now time.Time) []time.Time {
	start, end := windowBounds(window, now)
	count := bucketCount(end.Sub(start), width)
	buckets := make([]time.Time, count)
	for offset := range buckets {
		buckets[offset] = start.Add(time.Duration(offset) * width)
	}
	return buckets
}

// issueIDIndex extracts the page's issue ids and their position in issues, so
// an occurrence query result (keyed by id) can be written back to the right
// slice slot.
func issueIDIndex(issues []issue.Issue) ([]int64, map[int64]int) {
	ids := make([]int64, len(issues))
	index := make(map[int64]int, len(issues))
	for position, listed := range issues {
		ids[position] = listed.ID
		index[listed.ID] = position
	}
	return ids, index
}

func (repository *issueRepository) occurrenceMatrix(ctx context.Context, ids []int64, query string, extraArgs ...any) (map[int64][]int64, error) {
	args := append([]any{ids}, extraArgs...)
	rows, err := repository.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, translateIssueError("occurrence matrix", err)
	}
	defer rows.Close()
	matrix := make(map[int64][]int64, len(ids))
	for rows.Next() {
		var (
			id    int64
			count int64
		)
		if err := rows.Scan(&id, &count); err != nil {
			return nil, translateIssueError("scan occurrence", err)
		}
		matrix[id] = append(matrix[id], count)
	}
	if err := rows.Err(); err != nil {
		return nil, translateIssueError("iterate occurrence", err)
	}
	return matrix, nil
}

func copySpark(dst, src []int64) {
	for position := 0; position < len(dst) && position < len(src); position++ {
		dst[position] = src[position]
	}
}

// eventKeyset is the two halves of one paging decision: the sort direction and
// the keyset comparison that resumes it. They must always be chosen together —
// an ascending ORDER BY resumed with the descending "<" comparison returns an
// empty second page, and the reverse walks forever. Both look like working
// pagination until a caller pages past the first response.
type eventKeyset struct {
	direction  string
	comparison string
}

func keysetForOrder(order issue.EventOrder) eventKeyset {
	if order == issue.EventOrderOldest {
		return eventKeyset{direction: "ASC", comparison: ">"}
	}
	return eventKeyset{direction: "DESC", comparison: "<"}
}

// eventWindowBounds resolves an issue.Window to concrete bounds for a plain
// `received_at BETWEEN` predicate on the events table. It is distinct from
// windowBounds (above, in this file) only in reporting whether the window is
// set at all — windowBounds is always called on a window already known
// non-zero (from windowBuckets), while Events must tell "no window supplied"
// apart from "window resolves to some instant pair" before adding a
// predicate. Deliberately not windowClause (issue_query.go) either: that
// helper drives the issue list's hourly-bucket sparkline CTE against
// issue_counts_hourly, a different query shape than this flat per-event row
// scan.
func eventWindowBounds(window issue.Window, now time.Time) (start, end time.Time, bounded bool) {
	if window.IsZero() {
		return time.Time{}, time.Time{}, false
	}
	start, end = windowBounds(window, now)
	return start, end, true
}

func (repository *issueRepository) Events(ctx context.Context, scope access.Scope, query issue.EventQuery) (issue.EventPage, error) {
	keyset := keysetForOrder(query.Order)
	// The service normally clamps Limit to a positive value (NormalizeLimit);
	// guard here too, exactly as List does, so a stray zero/negative value
	// cannot panic the events[limit-1] cursor math below.
	limit := max(query.Limit, 1)
	args := []any{query.IssueID}
	predicate := ""
	if query.Environment != "" {
		args = append(args, query.Environment)
		predicate += fmt.Sprintf(" AND environment = $%d", len(args))
	}
	if start, end, bounded := eventWindowBounds(query.Window, time.Now().UTC()); bounded {
		args = append(args, start, end)
		predicate += fmt.Sprintf(" AND received_at BETWEEN $%d AND $%d", len(args)-1, len(args))
	}
	if query.Cursor != nil {
		args = append(args, query.Cursor.ReceivedAt, pgtype.UUID{Bytes: [16]byte(query.Cursor.EventID), Valid: true})
		predicate += fmt.Sprintf(" AND (received_at, event_id) %s ($%d, $%d)", keyset.comparison, len(args)-1, len(args))
	}
	scopeClause, scopeArgs := scopePredicate(scope, "project_id", len(args)+1)
	args = append(args, scopeArgs...)
	args = append(args, limit+1)
	statement := `
		SELECT event_id, timestamp, received_at, level, environment, release, message
		FROM events
		WHERE issue_id = $1` + predicate + scopeClause + `
		ORDER BY received_at ` + keyset.direction + `, event_id ` + keyset.direction + `
		LIMIT $` + strconv.Itoa(len(args))

	rows, err := repository.pool.Query(ctx, statement, args...)
	if err != nil {
		return issue.EventPage{}, translateIssueError("issue events", err)
	}
	defer rows.Close()

	events := make([]issue.EventSummary, 0, limit)
	for rows.Next() {
		summary, err := scanEventSummary(rows)
		if err != nil {
			return issue.EventPage{}, err
		}
		events = append(events, summary)
	}
	if err := rows.Err(); err != nil {
		return issue.EventPage{}, translateIssueError("iterate issue events", err)
	}
	if len(events) == 0 {
		if err := repository.requireIssueInScope(ctx, scope, query.IssueID); err != nil {
			return issue.EventPage{}, err
		}
	}

	page := issue.EventPage{}
	if len(events) > limit {
		last := events[limit-1]
		events = events[:limit]
		page.NextCursor = &issue.EventCursor{ReceivedAt: last.ReceivedAt, EventID: last.EventID}
	}
	page.Events = events
	return page, nil
}

func scanEventSummary(rows pgx.Rows) (issue.EventSummary, error) {
	var (
		summary issue.EventSummary
		eventID pgtype.UUID
	)
	if err := rows.Scan(&eventID, &summary.Timestamp, &summary.ReceivedAt, &summary.Level,
		&summary.Environment, &summary.Release, &summary.Message); err != nil {
		return issue.EventSummary{}, translateIssueError("scan event", err)
	}
	summary.EventID = eventID.Bytes
	summary.Timestamp = summary.Timestamp.UTC()
	summary.ReceivedAt = summary.ReceivedAt.UTC()
	return summary, nil
}

func (repository *issueRepository) LatestEvent(ctx context.Context, scope access.Scope, id int64) (issue.EventDetail, error) {
	predicate, scopeArgs := scopePredicate(scope, "project_id", 2)
	args := append([]any{id}, scopeArgs...)
	return repository.scanEventDetail(ctx, `
		SELECT event_id, timestamp, received_at, level, environment, release, message, request_id, payload, project_id
		FROM events WHERE issue_id = $1`+predicate+` ORDER BY received_at DESC, event_id DESC LIMIT 1
	`, args...)
}

func (repository *issueRepository) Event(ctx context.Context, scope access.Scope, eventID uuid.UUID) (issue.EventDetail, error) {
	predicate, scopeArgs := scopePredicate(scope, "project_id", 2)
	args := append([]any{pgtype.UUID{Bytes: [16]byte(eventID), Valid: true}}, scopeArgs...)
	return repository.scanEventDetail(ctx, `
		SELECT event_id, timestamp, received_at, level, environment, release, message, request_id, payload, project_id
		FROM events WHERE event_id = $1`+predicate+` ORDER BY received_at DESC LIMIT 1
	`, args...)
}

func (repository *issueRepository) scanEventDetail(ctx context.Context, query string, args ...any) (issue.EventDetail, error) {
	var (
		detail  issue.EventDetail
		eventID pgtype.UUID
		payload []byte
	)
	err := repository.pool.QueryRow(ctx, query, args...).Scan(&eventID, &detail.Timestamp, &detail.ReceivedAt,
		&detail.Level, &detail.Environment, &detail.Release, &detail.Message, &detail.RequestID, &payload, &detail.ProjectID)
	if err != nil {
		return issue.EventDetail{}, translateIssueError("event detail", err)
	}
	detail.EventID = eventID.Bytes
	detail.Timestamp = detail.Timestamp.UTC()
	detail.ReceivedAt = detail.ReceivedAt.UTC()
	detail.Payload = json.RawMessage(payload)
	return detail, nil
}

// Timeline resolves the event's (project_id, request_id) first, then queries
// spans in a second statement — this avoids duplicate spans if two event rows
// ever share an event_id, and lets a NULL request_id return cleanly empty
// rather than joining against no rows.
func (repository *issueRepository) Timeline(ctx context.Context, scope access.Scope, eventID uuid.UUID) ([]issue.Span, error) {
	var (
		projectID int64
		requestID *string
	)
	predicate, scopeArgs := scopePredicate(scope, "project_id", 2)
	args := append([]any{pgtype.UUID{Bytes: [16]byte(eventID), Valid: true}}, scopeArgs...)
	err := repository.pool.QueryRow(ctx, `
		SELECT project_id, request_id FROM events
		WHERE event_id = $1`+predicate+` ORDER BY received_at DESC LIMIT 1
	`, args...).Scan(&projectID, &requestID)
	if err != nil {
		return nil, translateIssueError("timeline event lookup", err)
	}
	if requestID == nil || *requestID == "" {
		return []issue.Span{}, nil
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT span_id, parent_span_id, name, start_time, duration_ms, status, attributes
		FROM spans
		WHERE project_id = $1 AND request_id = $2
		ORDER BY start_time, span_id
		LIMIT $3
	`, projectID, *requestID, maxTimelineSpans)
	if err != nil {
		return nil, fmt.Errorf("timeline spans: %w", err)
	}
	defer rows.Close()

	spans := make([]issue.Span, 0)
	for rows.Next() {
		var (
			span       issue.Span
			attributes []byte
		)
		if err := rows.Scan(&span.SpanID, &span.ParentSpanID, &span.Name,
			&span.StartTime, &span.DurationMS, &span.Status, &attributes); err != nil {
			return nil, fmt.Errorf("scan span: %w", err)
		}
		span.StartTime = span.StartTime.UTC()
		span.Attributes = json.RawMessage(attributes)
		spans = append(spans, span)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate spans: %w", err)
	}
	return spans, nil
}

// statsScopeCTE bounds the occurrence query by project. issue_counts_hourly
// carries only issue_id, bucket and n — no project column a predicate could
// name — so the boundary is a one-row CTE resolving the issue within scope,
// CROSS JOINed onto the bucket series. Out of scope it resolves nothing and
// the join yields no rows at all, which Stats reports as issue.ErrNotFound:
// an issue the caller cannot reach must read like one that does not exist,
// not like a chart of zeroes.
//
// $2 is the scope's project id array, passed as SQL NULL for an unrestricted
// scope: the CTE then short-circuits to true and owners see every bucket.
// This is the only place in the codebase where NULL means "unrestricted",
// and it is safe only because Stats passes nil exactly when
// scope.IsUnrestricted() is true — an empty restricted scope passes an empty
// array, which makes `= ANY($2)` false and the CTE empty.
const statsScopeCTE = `, scoped AS (
				SELECT 1 FROM issues WHERE id = $1 AND ($2::bigint[] IS NULL OR project_id = ANY($2))
			)`

func (repository *issueRepository) Stats(ctx context.Context, scope access.Scope, id int64, period issue.Period) ([]issue.Bucket, error) {
	var projectIDs any
	if !scope.IsUnrestricted() {
		projectIDs = scope.ProjectIDs()
	}
	query := statsQuery(period)
	rows, err := repository.pool.Query(ctx, query, id, projectIDs)
	if err != nil {
		return nil, translateIssueError("issue stats", err)
	}
	defer rows.Close()
	buckets := make([]issue.Bucket, 0)
	for rows.Next() {
		var bucket issue.Bucket
		if err := rows.Scan(&bucket.Bucket, &bucket.Count); err != nil {
			return nil, translateIssueError("scan stats bucket", err)
		}
		bucket.Bucket = bucket.Bucket.UTC()
		buckets = append(buckets, bucket)
	}
	if err := rows.Err(); err != nil {
		return nil, translateIssueError("iterate stats buckets", err)
	}
	// The bucket series is generated, so a non-empty result is guaranteed for
	// any issue statsScopeCTE resolved. No rows therefore means exactly one
	// thing: the issue does not exist, or does not exist for this caller.
	if len(buckets) == 0 {
		return nil, fmt.Errorf("issue stats: %w", issue.ErrNotFound)
	}
	return buckets, nil
}

// statsQuery builds a gap-filled, ordered occurrence series for the period. The
// service validates period before this is reached, so the default is the 24h case.
func statsQuery(period issue.Period) string {
	switch period {
	case issue.Period14d:
		return dailyStatsQuery(13)
	case issue.Period90d:
		return dailyStatsQuery(89)
	default:
		return `
			WITH buckets AS (
				SELECT generate_series(date_trunc('hour', now()) - interval '23 hours',
				                       date_trunc('hour', now()), interval '1 hour') AS bucket
			)` + statsScopeCTE + `
			SELECT b.bucket, COALESCE(sum(c.n), 0)::bigint
			FROM buckets b CROSS JOIN scoped
			LEFT JOIN issue_counts_hourly c ON c.issue_id = $1 AND c.bucket = b.bucket
			GROUP BY b.bucket ORDER BY b.bucket`
	}
}

func dailyStatsQuery(daysBack int) string {
	return fmt.Sprintf(`
		WITH buckets AS (
			SELECT generate_series(date_trunc('day', now()) - interval '%d days',
			                       date_trunc('day', now()), interval '1 day') AS bucket
		)%s
		SELECT b.bucket, COALESCE(sum(c.n), 0)::bigint
		FROM buckets b CROSS JOIN scoped
		LEFT JOIN issue_counts_hourly c ON c.issue_id = $1 AND date_trunc('day', c.bucket) = b.bucket
		GROUP BY b.bucket ORDER BY b.bucket`, daysBack, statsScopeCTE)
}

// TagKeys and TagValues are scoped by issue_tag_values.project_id (itv_project_idx),
// not by joining back through issues, so a merged-away issue's rows — already
// deleted by MergeIssue's fold into the target (see the DELETE in that method)
// — never need a separate merged_into_id filter here.

// TagKeys excludes environment/release: they already have dedicated
// Environment/Release filter controls and query parameters, so listing them
// here too would let the generic tag picker build a `tag=environment:x`
// filter that silently shadows `environment=x` — two paths to the same
// filter. TagValues is deliberately left unrestricted: the dedicated
// Environment/Release controls call it directly with an explicit reserved
// key to populate their own value lists (see FacetFilterIfPopulated in
// IssueFilterBar.tsx), so excluding those keys there would break the very
// controls this exclusion exists to keep as the sole path to them.
func (repository *issueRepository) TagKeys(ctx context.Context, scope access.Scope, slug string) ([]issue.TagKey, error) {
	predicate, predicateArgs := scopePredicate(scope, "p.id", 4)
	args := append([]any{slug, environmentTagKey, releaseTagKey}, predicateArgs...)
	rows, err := repository.pool.Query(ctx, `
		SELECT t.key, count(DISTINCT t.value)::bigint, sum(t.times_seen)::bigint
		FROM issue_tag_values t
		JOIN projects p ON p.id = t.project_id
		WHERE p.slug = $1 AND t.key NOT IN ($2, $3)`+predicate+`
		GROUP BY t.key
		ORDER BY sum(t.times_seen) DESC, t.key
	`, args...)
	if err != nil {
		return nil, translateIssueError("tag keys", err)
	}
	defer rows.Close()

	keys := make([]issue.TagKey, 0)
	for rows.Next() {
		var key issue.TagKey
		if err := rows.Scan(&key.Key, &key.ValueCount, &key.TimesSeen); err != nil {
			return nil, translateIssueError("scan tag key", err)
		}
		keys = append(keys, key)
	}
	if err := rows.Err(); err != nil {
		return nil, translateIssueError("iterate tag keys", err)
	}
	if len(keys) == 0 {
		if err := repository.requireProjectInScope(ctx, scope, slug); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func (repository *issueRepository) TagValues(ctx context.Context, scope access.Scope, slug, key, prefix string, limit int) ([]issue.TagValue, error) {
	// The prefix is user text, so it goes through the same escaping as any
	// filter value before a `%` is appended to make it a prefix match. An
	// empty prefix escapes to "", so the pattern becomes bare "%" — every
	// value matches, which is what lets a blank query surface the top values
	// instead of nothing.
	escaped, _ := likePattern(prefix)
	predicate, predicateArgs := scopePredicate(scope, "p.id", 5)
	args := append([]any{slug, key, escaped + "%", limit}, predicateArgs...)
	rows, err := repository.pool.Query(ctx, `
		SELECT t.value, sum(t.times_seen)::bigint, min(t.first_seen), max(t.last_seen)
		FROM issue_tag_values t
		JOIN projects p ON p.id = t.project_id
		WHERE p.slug = $1 AND t.key = $2 AND t.value LIKE $3`+predicate+`
		GROUP BY t.value
		ORDER BY sum(t.times_seen) DESC, t.value
		LIMIT $4
	`, args...)
	if err != nil {
		return nil, translateIssueError("tag values", err)
	}
	defer rows.Close()

	values := make([]issue.TagValue, 0, limit)
	for rows.Next() {
		var value issue.TagValue
		if err := rows.Scan(&value.Value, &value.TimesSeen, &value.FirstSeen, &value.LastSeen); err != nil {
			return nil, translateIssueError("scan tag value", err)
		}
		value.FirstSeen = value.FirstSeen.UTC()
		value.LastSeen = value.LastSeen.UTC()
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, translateIssueError("iterate tag values", err)
	}
	if len(values) == 0 {
		if err := repository.requireProjectInScope(ctx, scope, slug); err != nil {
			return nil, err
		}
	}
	return values, nil
}

// IssueUsers returns the affected-users facet for id. Total comes from a
// dedicated `issues` lookup rather than count(*) over issue_users, both
// because issues.users_seen is already the authoritative aggregate (see
// upsertUserIdentities) and because that lookup is what proves id exists: an
// unknown issue has no issues row at all, so it — not an empty issue_users
// page, which a real issue with no identities also produces — is what maps
// to issue.ErrNotFound.
//
// Ties in the times_seen ordering are broken on (user_key, user_value): that
// pair is the table's own primary key suffix (with issue_id), so it can never
// itself tie, which is what makes the order fully deterministic across
// requests and pages.
func (repository *issueRepository) IssueUsers(ctx context.Context, scope access.Scope, id int64, limit int) (issue.UserFacet, error) {
	predicate, scopeArgs := scopePredicate(scope, "project_id", 2)
	args := append([]any{id}, scopeArgs...)
	var total int64
	err := repository.pool.QueryRow(ctx, `SELECT users_seen FROM issues WHERE id = $1`+predicate, args...).Scan(&total)
	if err != nil {
		return issue.UserFacet{}, translateIssueError("issue users total", err)
	}

	rows, err := repository.pool.Query(ctx, `
		SELECT user_key, user_value, times_seen, last_seen
		FROM issue_users
		WHERE issue_id = $1
		ORDER BY times_seen DESC, user_key, user_value
		LIMIT $2
	`, id, limit)
	if err != nil {
		return issue.UserFacet{}, translateIssueError("issue users", err)
	}
	defer rows.Close()

	users := make([]issue.UserIdentity, 0, limit)
	for rows.Next() {
		var identity issue.UserIdentity
		if err := rows.Scan(&identity.Key, &identity.Value, &identity.TimesSeen, &identity.LastSeen); err != nil {
			return issue.UserFacet{}, translateIssueError("scan issue user", err)
		}
		identity.LastSeen = identity.LastSeen.UTC()
		users = append(users, identity)
	}
	if err := rows.Err(); err != nil {
		return issue.UserFacet{}, translateIssueError("iterate issue users", err)
	}
	return issue.UserFacet{Users: users, Total: total}, nil
}
