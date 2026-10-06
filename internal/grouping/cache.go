package grouping

import (
	"context"
	"sync"
	"time"
)

// ruleCacheTTL mirrors ingest.KeyLookup's positive TTL exactly: rule reads
// accept up to this much staleness, so a rule edit becomes visible to new
// events within this window.
const ruleCacheTTL = 30 * time.Second

// Clock reports the current time; injectable for deterministic tests.
type Clock func() time.Time

// RuleStore resolves a project's grouping rules from durable storage, ordered
// by position. An empty slice (no rules) is a normal, cacheable result — there
// is no "not found" case, unlike ingest.KeyStore.
type RuleStore interface {
	ListByProjectID(ctx context.Context, projectID int64) ([]Rule, error)
}

// CacheInvalidator is the merge-only eviction hook a RuleCache exposes;
// *RuleCache satisfies it. Kept as a narrow interface so callers outside this
// package (the issue repository's merge transaction) can depend on just this
// one method rather than the whole cache type.
type CacheInvalidator interface {
	Invalidate(projectID int64)
}

// RuleCache resolves grouping rules through a lazy per-project TTL cache
// backed by a RuleStore. This mirrors internal/ingest/key_cache.go's
// KeyLookup shape exactly: 30s TTL, populated on miss, no background ticker.
// The one deliberate exception is Invalidate (see its doc comment) — ordinary
// rule CRUD still never calls it and keeps the lazy-TTL-only behavior.
// Transient store errors are never cached.
type RuleCache struct {
	store RuleStore
	now   Clock

	mu      sync.Mutex
	entries map[int64]ruleCacheEntry
}

type ruleCacheEntry struct {
	rules     []Rule
	expiresAt time.Time
}

func NewRuleCache(store RuleStore, now Clock) *RuleCache {
	return &RuleCache{store: store, now: now, entries: make(map[int64]ruleCacheEntry)}
}

// Get returns projectID's cached rules, loading and caching them on a miss.
// The returned slice is an independent copy: the cache never hands out its
// stored backing array, so a caller mutating its result cannot corrupt what
// other concurrent or later callers read back within the TTL.
func (c *RuleCache) Get(ctx context.Context, projectID int64) ([]Rule, error) {
	if entry, ok := c.cached(projectID); ok {
		return cloneRules(entry.rules), nil
	}
	rules, err := c.store.ListByProjectID(ctx, projectID)
	if err != nil {
		return nil, err
	}
	c.put(projectID, ruleCacheEntry{rules: rules, expiresAt: c.now().Add(ruleCacheTTL)})
	return cloneRules(rules), nil
}

func cloneRules(rules []Rule) []Rule {
	clone := make([]Rule, len(rules))
	copy(clone, rules)
	return clone
}

func (c *RuleCache) cached(projectID int64) (ruleCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[projectID]
	if !ok || !entry.expiresAt.After(c.now()) {
		return ruleCacheEntry{}, false
	}
	return entry, true
}

func (c *RuleCache) put(projectID int64, entry ruleCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[projectID] = entry
}

// Invalidate drops projectID's cached entry so the next Get re-reads from the
// store, instead of waiting out the TTL. This is a merge-only exception to
// the cache's "accept up to 30s staleness" contract (ruleCacheTTL): a stale
// rule CRUD edit just leaves an event grouped the old way, still fully
// visible, but a stale merge leaves an event landing on the tombstoned
// source issue and permanently excluded from every list — bounded-and
// -recoverable versus unbounded-and-unrecoverable. Callers performing
// ordinary rule Create/Update/Move/Delete must NOT call this; only merge
// does, synchronously after its transaction commits.
func (c *RuleCache) Invalidate(projectID int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.entries, projectID)
}
