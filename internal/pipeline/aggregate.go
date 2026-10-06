package pipeline

import (
	"maps"
	"slices"
	"sort"
	"time"

	"github.com/ramilS/faultline/internal/storage"
)

// aggregator accumulates dispatched items into per-project batches. It is not
// safe for concurrent use; each shard owns exactly one aggregator.
type aggregator struct {
	projects map[int64]*projectAggregate
	pendings int
}

// projectAggregate holds one project's in-flight deltas. issueOrder and the
// release slice preserve first-seen insertion order so drained batches are
// deterministic regardless of map iteration order.
type projectAggregate struct {
	issues       map[string]storage.IssueDelta
	issueOrder   []string
	events       []storage.EventRow
	releases     []string
	seenReleases map[string]struct{}
	spans        []storage.SpanRow
}

func newAggregator() *aggregator {
	return &aggregator{projects: make(map[int64]*projectAggregate)}
}

func (a *aggregator) pending() int {
	return a.pendings
}

func (a *aggregator) project(projectID int64) *projectAggregate {
	project := a.projects[projectID]
	if project == nil {
		project = &projectAggregate{
			issues:       make(map[string]storage.IssueDelta),
			seenReleases: make(map[string]struct{}),
		}
		a.projects[projectID] = project
	}
	return project
}

func (a *aggregator) add(entry Item) {
	project := a.project(entry.Row.ProjectID)

	project.events = append(project.events, entry.Row)
	a.pendings++

	if entry.Release != "" {
		if _, seen := project.seenReleases[entry.Release]; !seen {
			project.seenReleases[entry.Release] = struct{}{}
			project.releases = append(project.releases, entry.Release)
		}
	}

	incoming := issueDeltaFor(entry)
	fingerprint := entry.Row.Fingerprint
	if existing, ok := project.issues[fingerprint]; ok {
		project.issues[fingerprint] = storage.MergeIssueDelta(existing, incoming)
		return
	}
	project.issues[fingerprint] = incoming
	project.issueOrder = append(project.issueOrder, fingerprint)
}

// addSpans buffers span rows for a project. Spans participate in no issue or
// release aggregation; they only bound the pending count so a span-heavy shard
// still flushes on the size threshold.
func (a *aggregator) addSpans(projectID int64, rows []storage.SpanRow) {
	if len(rows) == 0 {
		return
	}
	project := a.project(projectID)
	project.spans = append(project.spans, rows...)
	a.pendings += len(rows)
}

// drain returns one batch per project in ascending project-id order and resets
// the aggregator. It returns nil when nothing is buffered.
func (a *aggregator) drain() []storage.Batch {
	if len(a.projects) == 0 {
		return nil
	}

	projectIDs := make([]int64, 0, len(a.projects))
	for projectID := range a.projects {
		projectIDs = append(projectIDs, projectID)
	}
	slices.Sort(projectIDs)

	batches := make([]storage.Batch, 0, len(projectIDs))
	for _, projectID := range projectIDs {
		project := a.projects[projectID]
		issues := make([]storage.IssueDelta, len(project.issueOrder))
		for index, fingerprint := range project.issueOrder {
			issues[index] = project.issues[fingerprint]
		}
		batches = append(batches, storage.Batch{
			ProjectID: projectID,
			Releases:  project.releases,
			Issues:    issues,
			Events:    project.events,
			Spans:     project.spans,
		})
	}

	a.projects = make(map[int64]*projectAggregate)
	a.pendings = 0
	return batches
}

// environmentTagKey and releaseTagKey are reserved tag keys backed by scalar
// columns (EventRow.Environment, EventRow.Release) rather than the event's
// user-supplied tag map. They must reach issue_tag_values like any other tag
// so the environment/release issue filters and facet endpoints — which only
// ever query issue_tag_values — have something to match against.
const (
	environmentTagKey = "environment"
	releaseTagKey     = "release"
)

func issueDeltaFor(entry Item) storage.IssueDelta {
	when := entry.Row.Timestamp
	return storage.IssueDelta{
		Fingerprint: entry.Row.Fingerprint,
		Title:       entry.Title,
		Culprit:     entry.Culprit,
		Level:       entry.Row.Level,
		Count:       1,
		FirstSeen:   when,
		LastSeen:    when,
		Tags:        tagDeltasFor(entry.Tags, entry.Row.Environment, entry.Row.Release, when),
		Users:       userDeltaFor(entry.User, when),
	}
}

// userDeltaFor turns the event's identity into its single-element
// contribution to the issue's distinct-user aggregate. It returns nil for an
// event that carried no identity — no user object, or an ip identity the
// project has not opted into aggregating (internal/ingest.DispatchEnvelope
// already dropped it before this stage) — so a nil identity never fabricates
// an issue_users row; the event still contributes to IssueDelta.Count above,
// just not here.
func userDeltaFor(identity *UserIdentity, when time.Time) []storage.UserDelta {
	if identity == nil {
		return nil
	}
	return []storage.UserDelta{{
		Key:       identity.Key,
		Value:     identity.Value,
		Count:     1,
		FirstSeen: when,
		LastSeen:  when,
	}}
}

// tagDeltasFor combines the event's user-supplied tags with its reserved
// environment/release columns into one deterministically ordered delta list.
//
// The reserved columns always win over a user tag of the same literal name
// instead of the two coexisting: EventRow.Environment/Release are what the
// events table stores and what the issue-detail page already reads back as
// authoritative, so a user-supplied "environment" or "release" tag with a
// different value would otherwise silently disagree with those columns, and
// emitting both would double-count the key in the batch writer's per-(issue,
// key, value) upsert.
//
// Both environment and release are skipped entirely when blank rather than
// accumulating a blank-value facet, and any user tag literally named
// "environment" or "release" is dropped along with them for the same
// authoritative-column reasoning. Environment is not known to arrive blank
// today (the Sentry event mapper defaults it to "production" and the column
// is NOT NULL DEFAULT), but a future producer must not be able to write one
// silently — so it is guarded the same way release already is.
func tagDeltasFor(tags map[string]string, environment string, release *string, when time.Time) []storage.TagDelta {
	merged := make(map[string]string, len(tags)+2)
	maps.Copy(merged, tags)
	if environment != "" {
		merged[environmentTagKey] = environment
	} else {
		delete(merged, environmentTagKey)
	}
	if release != nil && *release != "" {
		merged[releaseTagKey] = *release
	} else {
		delete(merged, releaseTagKey)
	}

	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	deltas := make([]storage.TagDelta, len(keys))
	for index, key := range keys {
		deltas[index] = storage.TagDelta{
			Key:       key,
			Value:     merged[key],
			Count:     1,
			FirstSeen: when,
			LastSeen:  when,
		}
	}
	return deltas
}
