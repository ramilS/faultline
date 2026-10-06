package pipeline

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/storage"
)

func item(projectID int64, fingerprint string, at time.Time, tags map[string]string, release string) Item {
	var releasePtr *string
	if release != "" {
		releasePtr = &release
	}
	return Item{
		Row: storage.EventRow{
			ProjectID:   projectID,
			EventID:     uuid.New(),
			Timestamp:   at,
			ReceivedAt:  at,
			Level:       "error",
			Environment: "production",
			Message:     "boom",
			Release:     releasePtr,
			Fingerprint: fingerprint,
		},
		Title:   "boom",
		Culprit: "svc.total",
		Tags:    tags,
		Release: release,
	}
}

func TestAggregateFoldsItemsByFingerprint(t *testing.T) {
	early := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)

	aggregate := newAggregator()
	aggregate.add(item(1, "fp-a", late, map[string]string{"service": "billing"}, "svc@1.0.0"))
	aggregate.add(item(1, "fp-a", early, map[string]string{"service": "billing"}, "svc@1.0.0"))
	aggregate.add(item(1, "fp-b", late, nil, ""))

	if aggregate.pending() != 3 {
		t.Fatalf("pending = %d, want 3", aggregate.pending())
	}

	batches := aggregate.drain()
	if len(batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(batches))
	}
	batch := batches[0]
	if batch.ProjectID != 1 {
		t.Fatalf("project id = %d, want 1", batch.ProjectID)
	}
	if len(batch.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(batch.Events))
	}
	if len(batch.Issues) != 2 {
		t.Fatalf("issues = %d, want 2", len(batch.Issues))
	}

	issueA := findIssue(t, batch.Issues, "fp-a")
	if issueA.Count != 2 {
		t.Fatalf("fp-a count = %d, want 2", issueA.Count)
	}
	if !issueA.FirstSeen.Equal(early) {
		t.Fatalf("fp-a first_seen = %v, want %v", issueA.FirstSeen, early)
	}
	if !issueA.LastSeen.Equal(late) {
		t.Fatalf("fp-a last_seen = %v, want %v", issueA.LastSeen, late)
	}
	if issueA.Title != "boom" || issueA.Culprit != "svc.total" || issueA.Level != "error" {
		t.Fatalf("fp-a identity = %+v", issueA)
	}
	// Sorted by key: environment and release are synthesized from the reserved
	// scalar columns alongside the user-supplied "service" tag.
	if len(issueA.Tags) != 3 {
		t.Fatalf("fp-a tags = %+v, want 3 (environment, release, service)", issueA.Tags)
	}
	wantTags := map[string]string{"environment": "production", "release": "svc@1.0.0", "service": "billing"}
	for _, tag := range issueA.Tags {
		if want, ok := wantTags[tag.Key]; !ok || tag.Value != want || tag.Count != 2 {
			t.Fatalf("fp-a tag %+v does not match expected %v", tag, wantTags)
		}
	}

	if batch.Releases[0] != "svc@1.0.0" || len(batch.Releases) != 1 {
		t.Fatalf("releases = %v, want single svc@1.0.0", batch.Releases)
	}
}

func TestAggregateGroupsBatchesByProject(t *testing.T) {
	at := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	aggregate := newAggregator()
	aggregate.add(item(2, "fp", at, nil, ""))
	aggregate.add(item(1, "fp", at, nil, ""))
	aggregate.add(item(1, "fp", at, nil, ""))

	batches := aggregate.drain()
	if len(batches) != 2 {
		t.Fatalf("batches = %d, want 2", len(batches))
	}
	if batches[0].ProjectID != 1 || batches[1].ProjectID != 2 {
		t.Fatalf("batches not ordered by project id: %d, %d", batches[0].ProjectID, batches[1].ProjectID)
	}
	if len(batches[0].Events) != 2 || len(batches[1].Events) != 1 {
		t.Fatalf("event counts = %d, %d", len(batches[0].Events), len(batches[1].Events))
	}
}

func TestAggregateResetsAfterDrain(t *testing.T) {
	at := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	aggregate := newAggregator()
	aggregate.add(item(1, "fp", at, nil, ""))

	if len(aggregate.drain()) != 1 {
		t.Fatal("first drain should yield one batch")
	}
	if aggregate.pending() != 0 {
		t.Fatalf("pending after drain = %d, want 0", aggregate.pending())
	}
	if batches := aggregate.drain(); batches != nil {
		t.Fatalf("second drain = %v, want nil", batches)
	}
}

func TestAggregatorDrainsSpansOnlyProject(t *testing.T) {
	baseTime := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)
	agg := newAggregator()
	agg.addSpans(9, []storage.SpanRow{
		{ProjectID: 9, RequestID: "req-1", SpanID: "s1", Name: "a", StartTime: baseTime},
		{ProjectID: 9, RequestID: "req-1", SpanID: "s2", Name: "b", StartTime: baseTime},
	})
	if agg.pending() != 2 {
		t.Fatalf("pending = %d, want 2", agg.pending())
	}
	batches := agg.drain()
	if len(batches) != 1 {
		t.Fatalf("batches = %d, want 1", len(batches))
	}
	if batches[0].ProjectID != 9 || len(batches[0].Spans) != 2 {
		t.Fatalf("batch = %+v", batches[0])
	}
	if len(batches[0].Events) != 0 || len(batches[0].Issues) != 0 {
		t.Fatalf("spans-only batch should have no events/issues: %+v", batches[0])
	}
	if agg.pending() != 0 {
		t.Fatalf("pending after drain = %d, want 0", agg.pending())
	}
}

func TestAggregateAccumulatesUserDeltasByIdentity(t *testing.T) {
	early := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)

	first := item(1, "fp-a", early, nil, "")
	first.User = &UserIdentity{Key: "email", Value: "a@example.com"}
	second := item(1, "fp-a", late, nil, "")
	second.User = &UserIdentity{Key: "email", Value: "a@example.com"}
	third := item(1, "fp-a", late, nil, "")
	third.User = &UserIdentity{Key: "email", Value: "b@example.com"}
	noUser := item(1, "fp-a", late, nil, "")

	aggregate := newAggregator()
	aggregate.add(first)
	aggregate.add(second)
	aggregate.add(third)
	aggregate.add(noUser)

	issueA := findIssue(t, aggregate.drain()[0].Issues, "fp-a")
	if issueA.Count != 4 {
		t.Fatalf("count = %d, want 4 (the nil-identity event still counts once)", issueA.Count)
	}
	if len(issueA.Users) != 2 {
		t.Fatalf("users = %+v, want 2 distinct identities", issueA.Users)
	}
	a := findUserDelta(t, issueA.Users, "email", "a@example.com")
	if a.Count != 2 {
		t.Fatalf("a@example.com times_seen = %d, want 2", a.Count)
	}
	if !a.FirstSeen.Equal(early) || !a.LastSeen.Equal(late) {
		t.Fatalf("a@example.com seen bounds = %v/%v, want %v/%v", a.FirstSeen, a.LastSeen, early, late)
	}
	b := findUserDelta(t, issueA.Users, "email", "b@example.com")
	if b.Count != 1 {
		t.Fatalf("b@example.com times_seen = %d, want 1", b.Count)
	}
}

func TestAggregateNilUserContributesNoUserDelta(t *testing.T) {
	at := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	aggregate := newAggregator()
	aggregate.add(item(1, "fp-a", at, nil, ""))

	issueA := findIssue(t, aggregate.drain()[0].Issues, "fp-a")
	if issueA.Count != 1 {
		t.Fatalf("count = %d, want 1", issueA.Count)
	}
	if len(issueA.Users) != 0 {
		t.Fatalf("users = %+v, want none — a nil identity must never fabricate a row", issueA.Users)
	}
}

func TestAggregateTreatsSameValueUnderDifferentKeysAsDistinctIdentities(t *testing.T) {
	at := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	byID := item(1, "fp-a", at, nil, "")
	byID.User = &UserIdentity{Key: "id", Value: "shared"}
	byEmail := item(1, "fp-a", at, nil, "")
	byEmail.User = &UserIdentity{Key: "email", Value: "shared"}

	aggregate := newAggregator()
	aggregate.add(byID)
	aggregate.add(byEmail)

	issueA := findIssue(t, aggregate.drain()[0].Issues, "fp-a")
	if len(issueA.Users) != 2 {
		t.Fatalf("users = %+v, want 2 (the same value under a different key is a different identity)", issueA.Users)
	}
}

func findUserDelta(t *testing.T, users []storage.UserDelta, key, value string) storage.UserDelta {
	t.Helper()
	for _, user := range users {
		if user.Key == key && user.Value == value {
			return user
		}
	}
	t.Fatalf("user %s=%s not found in %+v", key, value, users)
	return storage.UserDelta{}
}

func TestTagDeltasForSkipsNilAndEmptyRelease(t *testing.T) {
	when := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)

	deltas := tagDeltasFor(nil, "production", nil, when)
	assertNoTagKey(t, deltas, "release")

	empty := ""
	deltas = tagDeltasFor(nil, "production", &empty, when)
	assertNoTagKey(t, deltas, "release")
}

func TestTagDeltasForSkipsEmptyEnvironment(t *testing.T) {
	when := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)

	deltas := tagDeltasFor(nil, "", nil, when)
	assertNoTagKey(t, deltas, "environment")
}

func TestTagDeltasForReservedColumnsWinOverSameNamedUserTag(t *testing.T) {
	when := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	release := "svc@2.0.0"

	tags := map[string]string{"environment": "staging", "release": "svc@1.0.0", "service": "billing"}
	deltas := tagDeltasFor(tags, "production", &release, when)

	if len(deltas) != 3 {
		t.Fatalf("deltas = %+v, want 3 (environment, release, service)", deltas)
	}
	want := map[string]string{"environment": "production", "release": "svc@2.0.0", "service": "billing"}
	for _, delta := range deltas {
		if delta.Value != want[delta.Key] {
			t.Fatalf("tag %s = %q, want %q (reserved column must win over the same-named user tag)", delta.Key, delta.Value, want[delta.Key])
		}
	}
}

func assertNoTagKey(t *testing.T, deltas []storage.TagDelta, key string) {
	t.Helper()
	for _, delta := range deltas {
		if delta.Key == key {
			t.Fatalf("deltas = %+v, want no %q key", deltas, key)
		}
	}
}

func findIssue(t *testing.T, issues []storage.IssueDelta, fingerprint string) storage.IssueDelta {
	t.Helper()
	for _, issue := range issues {
		if issue.Fingerprint == fingerprint {
			return issue
		}
	}
	t.Fatalf("issue %s not found", fingerprint)
	return storage.IssueDelta{}
}
