package grouping

import (
	"context"
	"errors"
	"testing"
	"time"
)

var ruleCacheBase = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

type ruleCacheTestClock struct{ now time.Time }

func (c *ruleCacheTestClock) Now() time.Time          { return c.now }
func (c *ruleCacheTestClock) advance(d time.Duration) { c.now = c.now.Add(d) }

type fakeRuleStore struct {
	rules []Rule
	err   error
	calls map[int64]int
}

func newFakeRuleStore() *fakeRuleStore { return &fakeRuleStore{calls: make(map[int64]int)} }

func (s *fakeRuleStore) ListByProjectID(_ context.Context, projectID int64) ([]Rule, error) {
	s.calls[projectID]++
	return s.rules, s.err
}

func TestRuleCacheServesHitsWithinTTL(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.rules = []Rule{{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "billing"}}
	cache := NewRuleCache(store, clock.Now)

	first, err := cache.Get(t.Context(), 5)
	if err != nil || len(first) != 1 || store.calls[5] != 1 {
		t.Fatalf("first Get = %+v, %v, calls = %d", first, err, store.calls[5])
	}

	clock.advance(29 * time.Second)
	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("cached Get: %v", err)
	}
	if store.calls[5] != 1 {
		t.Fatalf("store calls = %d, want cache hit", store.calls[5])
	}

	clock.advance(2 * time.Second)
	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("refreshed Get: %v", err)
	}
	if store.calls[5] != 2 {
		t.Fatalf("store calls = %d, want refresh after 30s", store.calls[5])
	}
}

func TestRuleCacheCachesEmptyRuleListPositively(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	cache := NewRuleCache(store, clock.Now)

	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("second Get: %v", err)
	}
	if store.calls[5] != 1 {
		t.Fatalf("store calls = %d, want a zero-rule project cached like any other", store.calls[5])
	}
}

func TestRuleCacheIsolatesProjects(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.rules = []Rule{{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "billing"}}
	cache := NewRuleCache(store, clock.Now)

	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("project 5: %v", err)
	}
	if _, err := cache.Get(t.Context(), 9); err != nil {
		t.Fatalf("project 9: %v", err)
	}
	if store.calls[5] != 1 || store.calls[9] != 1 {
		t.Fatalf("calls = %+v, want one lookup per project", store.calls)
	}
}

func TestRuleCacheGetReturnsIndependentCopy(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.rules = []Rule{{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "billing"}}
	cache := NewRuleCache(store, clock.Now)

	// Miss path: first Get populates cache.
	first, err := cache.Get(t.Context(), 5)
	if err != nil {
		t.Fatalf("first Get (miss): %v", err)
	}
	if store.calls[5] != 1 {
		t.Fatalf("first Get should trigger store lookup; calls = %d", store.calls[5])
	}

	// Hit path: second Get returns from cache.
	// Capture this hit-path return and verify later it is an independent copy.
	hitPathResult, err := cache.Get(t.Context(), 5)
	if err != nil {
		t.Fatalf("second Get (hit): %v", err)
	}
	if store.calls[5] != 1 {
		t.Fatalf("second Get should be a cache hit; calls = %d", store.calls[5])
	}

	// Mutate the hit-path result.
	hitPathResult[0].Matcher = "mutated-from-hit"

	// Third Get (another hit): verify the cache still returns the unmutated original.
	third, err := cache.Get(t.Context(), 5)
	if err != nil {
		t.Fatalf("third Get (hit): %v", err)
	}
	if third[0].Matcher != "logger:billing" {
		t.Fatalf("third Get = %+v, mutating the hit-path result corrupted the cache", third[0])
	}

	// Also verify the miss-path copy is independent:
	// mutate first and confirm third is unaffected.
	first[0].Matcher = "mutated-from-miss"
	if third[0].Matcher != "logger:billing" {
		t.Fatalf("third Get = %+v, mutating the miss-path result corrupted the cache", third[0])
	}
}

func TestRuleCachePreservesStoreOrder(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.rules = []Rule{
		{ID: 3, ProjectID: 5, Position: 3, Matcher: "logger:c", Template: "c"},
		{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:a", Template: "a"},
		{ID: 2, ProjectID: 5, Position: 2, Matcher: "logger:b", Template: "b"},
	}
	cache := NewRuleCache(store, clock.Now)

	rules, err := cache.Get(t.Context(), 5)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(rules) != 3 || rules[0].ID != 3 || rules[1].ID != 1 || rules[2].ID != 2 {
		t.Fatalf("rules = %+v, want store order preserved verbatim (not sorted by Position/ID)", rules)
	}
}

func TestRuleCacheInvalidateForcesReReadOnNextGet(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.rules = []Rule{{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "billing"}}
	cache := NewRuleCache(store, clock.Now)

	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("first Get: %v", err)
	}
	if store.calls[5] != 1 {
		t.Fatalf("calls after first Get = %d, want 1", store.calls[5])
	}

	// Well within the 30s TTL — without Invalidate this would still be a hit.
	clock.advance(1 * time.Second)
	cache.Invalidate(5)

	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("Get after Invalidate: %v", err)
	}
	if store.calls[5] != 2 {
		t.Fatalf("calls after Invalidate = %d, want 2 (re-read, not served from the still-live TTL entry)", store.calls[5])
	}
}

func TestRuleCacheInvalidateOnlyAffectsGivenProject(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.rules = []Rule{{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:billing", Template: "billing"}}
	cache := NewRuleCache(store, clock.Now)

	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("project 5 Get: %v", err)
	}
	if _, err := cache.Get(t.Context(), 9); err != nil {
		t.Fatalf("project 9 Get: %v", err)
	}

	cache.Invalidate(5)

	if _, err := cache.Get(t.Context(), 9); err != nil {
		t.Fatalf("project 9 Get after invalidating 5: %v", err)
	}
	if store.calls[9] != 1 {
		t.Fatalf("project 9 calls = %d, want 1 (unaffected by invalidating a different project)", store.calls[9])
	}
	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("project 5 Get after invalidate: %v", err)
	}
	if store.calls[5] != 2 {
		t.Fatalf("project 5 calls = %d, want 2 (its own invalidation forced a re-read)", store.calls[5])
	}
}

// TestRuleCacheOrdinaryOperationsDoNotInvalidate pins the other half of the
// merge-only invalidation exception: only Invalidate evicts an entry early, so a rule
// CRUD write that never calls it (Create/Update/Move/Delete never do) leaves
// a populated cache entry serving stale data until the TTL naturally expires
// — the "accept up to 30s staleness" contract this cache exists for.
func TestRuleCacheOrdinaryOperationsDoNotInvalidate(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.rules = []Rule{{ID: 1, ProjectID: 5, Position: 1, Matcher: "logger:old", Template: "old"}}
	cache := NewRuleCache(store, clock.Now)

	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("first Get: %v", err)
	}

	// Simulate a rule CRUD write landing directly in the store, the way
	// Create/Update/Move/Delete do, without ever calling Invalidate.
	store.rules = []Rule{{ID: 2, ProjectID: 5, Position: 1, Matcher: "logger:new", Template: "new"}}

	rules, err := cache.Get(t.Context(), 5)
	if err != nil {
		t.Fatalf("Get after uninvalidated store write: %v", err)
	}
	if len(rules) != 1 || rules[0].Matcher != "logger:old" {
		t.Fatalf("Get after uninvalidated store write = %+v, want the stale cached [logger:old] (ordinary CRUD must not invalidate)", rules)
	}
	if store.calls[5] != 1 {
		t.Fatalf("store calls = %d, want 1 (still served from cache, no re-read)", store.calls[5])
	}
}

func TestRuleCacheDoesNotCacheTransientErrors(t *testing.T) {
	clock := &ruleCacheTestClock{now: ruleCacheBase}
	store := newFakeRuleStore()
	store.err = errors.New("connection reset")
	cache := NewRuleCache(store, clock.Now)

	if _, err := cache.Get(t.Context(), 5); err == nil {
		t.Fatal("expected transient error")
	}
	store.err = nil
	if _, err := cache.Get(t.Context(), 5); err != nil {
		t.Fatalf("retry after transient error: %v", err)
	}
	if store.calls[5] != 2 {
		t.Fatalf("store calls = %d, want no caching of the transient error", store.calls[5])
	}
}
