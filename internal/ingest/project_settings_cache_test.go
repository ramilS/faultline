package ingest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ramilS/faultline/internal/scrub"
)

type fakeProjectSettingsStore struct {
	storeUserIP bool
	err         error
	calls       int
}

func (s *fakeProjectSettingsStore) Settings(context.Context, int64) (ProjectSettings, error) {
	s.calls++
	return ProjectSettings{StoreUserIP: s.storeUserIP}, s.err
}

func lookupStoreUserIP(t *testing.T, cache *ProjectSettingsCache, projectID int64) (bool, error) {
	t.Helper()
	settings, err := cache.Lookup(t.Context(), projectID)
	return settings.StoreUserIP, err
}

func TestProjectSettingsCacheServesPositiveHitsWithinTTL(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeProjectSettingsStore{storeUserIP: true}
	cache := NewProjectSettingsCache(store, clock.Now)

	first, err := lookupStoreUserIP(t, cache, 1)
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if !first || store.calls != 1 {
		t.Fatalf("first = %v, calls = %d", first, store.calls)
	}

	clock.advance(29 * positiveTTL / 30)
	if _, err := lookupStoreUserIP(t, cache, 1); err != nil {
		t.Fatalf("cached lookup: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want cache hit", store.calls)
	}

	clock.advance(2 * positiveTTL / 30)
	if _, err := lookupStoreUserIP(t, cache, 1); err != nil {
		t.Fatalf("refreshed lookup: %v", err)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want refresh after the TTL elapses", store.calls)
	}
}

func TestProjectSettingsCacheReflectsAFlippedSettingAfterTTL(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeProjectSettingsStore{storeUserIP: false}
	cache := NewProjectSettingsCache(store, clock.Now)

	if value, err := lookupStoreUserIP(t, cache, 1); err != nil || value {
		t.Fatalf("initial lookup = %v, %v, want false, nil", value, err)
	}

	// The setting flips in storage; within TTL the cache still serves the
	// stale value — this is the documented tradeoff, not a bug.
	store.storeUserIP = true

	if value, err := lookupStoreUserIP(t, cache, 1); err != nil || value {
		t.Fatalf("within-TTL lookup = %v, %v, want stale false, nil", value, err)
	}

	clock.advance(positiveTTL)
	if value, err := lookupStoreUserIP(t, cache, 1); err != nil || !value {
		t.Fatalf("post-TTL lookup = %v, %v, want true, nil", value, err)
	}
}

func TestProjectSettingsCacheDoesNotCacheTransientErrors(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeProjectSettingsStore{err: errors.New("connection reset")}
	cache := NewProjectSettingsCache(store, clock.Now)

	if _, err := lookupStoreUserIP(t, cache, 1); err == nil {
		t.Fatal("expected transient error")
	}
	store.err = nil
	store.storeUserIP = true
	value, err := lookupStoreUserIP(t, cache, 1)
	if err != nil {
		t.Fatalf("retry after transient error: %v", err)
	}
	if !value {
		t.Fatalf("value = %v, want true", value)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want no caching of transient error", store.calls)
	}
}

func TestProjectSettingsCacheKeysByProjectIndependently(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeProjectSettingsStore{storeUserIP: true}
	cache := NewProjectSettingsCache(store, clock.Now)

	if value, err := lookupStoreUserIP(t, cache, 1); err != nil || !value {
		t.Fatalf("project 1 = %v, %v", value, err)
	}
	store.storeUserIP = false
	if value, err := lookupStoreUserIP(t, cache, 2); err != nil || value {
		t.Fatalf("project 2 = %v, %v, want false (its own cache entry, not project 1's)", value, err)
	}
	// project 1's cached true value must be unaffected by project 2's lookup.
	if value, err := lookupStoreUserIP(t, cache, 1); err != nil || !value {
		t.Fatalf("project 1 after project 2's lookup = %v, %v, want still true", value, err)
	}
}

func TestProjectSettingsCacheReturnsTheScrubConfig(t *testing.T) {
	clock := &testClock{now: cacheBase}
	want := scrub.Config{Enabled: true, ExtraFields: []string{"ssn"}, SafeFields: []string{"token"}}
	cache := NewProjectSettingsCache(&scrubStore{config: want}, clock.Now)

	settings, err := cache.Lookup(t.Context(), 1)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if !reflect.DeepEqual(settings.Scrub, want) {
		t.Fatalf("scrub = %+v, want %+v", settings.Scrub, want)
	}
}

func TestProjectSettingsCacheDoesNotCacheInvalidStoredRules(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &scrubStore{config: scrub.Config{Enabled: true, Rules: []scrub.Rule{{Name: "bad", Pattern: `(`}}}}
	cache := NewProjectSettingsCache(store, clock.Now)

	for range 2 {
		if _, err := cache.Lookup(t.Context(), 1); !errors.Is(err, scrub.ErrInvalidConfig) {
			t.Fatalf("lookup error = %v, want ErrInvalidConfig", err)
		}
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want the failure left uncached", store.calls)
	}
}

type scrubStore struct {
	config scrub.Config
	calls  int
}

func (s *scrubStore) Settings(context.Context, int64) (ProjectSettings, error) {
	s.calls++
	return ProjectSettings{Scrub: s.config}, nil
}
