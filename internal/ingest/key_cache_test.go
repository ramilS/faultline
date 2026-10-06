package ingest

import (
	"context"
	"errors"
	"testing"
	"time"
)

var cacheBase = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

type testClock struct {
	now time.Time
}

func (c *testClock) Now() time.Time { return c.now }

func (c *testClock) advance(d time.Duration) { c.now = c.now.Add(d) }

type fakeKeyStore struct {
	info  KeyInfo
	err   error
	calls int
}

func (s *fakeKeyStore) FindByPublicKey(context.Context, string) (KeyInfo, error) {
	s.calls++
	return s.info, s.err
}

func validKeyInfo() KeyInfo {
	return KeyInfo{ProjectID: 1, PublicKey: "public-key", RateLimitRPS: 200, RateLimitBurst: 1000}
}

func TestCacheServesPositiveHitsWithinTTL(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeKeyStore{info: validKeyInfo()}
	lookup := NewKeyLookup(store, clock.Now)

	first, err := lookup.Lookup(t.Context(), "public-key")
	if err != nil {
		t.Fatalf("first lookup: %v", err)
	}
	if first != validKeyInfo() || store.calls != 1 {
		t.Fatalf("first = %+v, calls = %d", first, store.calls)
	}

	clock.advance(29 * time.Second)
	if _, err := lookup.Lookup(t.Context(), "public-key"); err != nil {
		t.Fatalf("cached lookup: %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want cache hit", store.calls)
	}

	clock.advance(2 * time.Second)
	if _, err := lookup.Lookup(t.Context(), "public-key"); err != nil {
		t.Fatalf("refreshed lookup: %v", err)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want refresh after 30s", store.calls)
	}
}

func TestCacheServesNegativeHitsWithinTTL(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeKeyStore{err: ErrKeyNotFound}
	lookup := NewKeyLookup(store, clock.Now)

	if _, err := lookup.Lookup(t.Context(), "missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("first lookup error = %v, want ErrKeyNotFound", err)
	}
	clock.advance(59 * time.Second)
	if _, err := lookup.Lookup(t.Context(), "missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("cached negative lookup error = %v", err)
	}
	if store.calls != 1 {
		t.Fatalf("store calls = %d, want negative cache hit", store.calls)
	}
	clock.advance(2 * time.Second)
	if _, err := lookup.Lookup(t.Context(), "missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("refreshed negative lookup error = %v", err)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want refresh after 60s", store.calls)
	}
}

func TestCacheReflectsRevocationAfterPositiveTTL(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeKeyStore{info: validKeyInfo()}
	lookup := NewKeyLookup(store, clock.Now)

	if _, err := lookup.Lookup(t.Context(), "public-key"); err != nil {
		t.Fatalf("initial lookup: %v", err)
	}

	// The key is revoked in storage; the ingest store now reports it as absent.
	store.info, store.err = KeyInfo{}, ErrKeyNotFound

	clock.advance(20 * time.Second)
	if _, err := lookup.Lookup(t.Context(), "public-key"); err != nil {
		t.Fatalf("within TTL the cached key must remain valid, got %v", err)
	}

	clock.advance(11 * time.Second)
	if _, err := lookup.Lookup(t.Context(), "public-key"); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("after TTL revocation must reject, got %v", err)
	}
}

func TestCacheDoesNotCacheTransientErrors(t *testing.T) {
	clock := &testClock{now: cacheBase}
	store := &fakeKeyStore{err: errors.New("connection reset")}
	lookup := NewKeyLookup(store, clock.Now)

	if _, err := lookup.Lookup(t.Context(), "public-key"); err == nil {
		t.Fatal("expected transient error")
	}
	store.err = nil
	store.info = validKeyInfo()
	if _, err := lookup.Lookup(t.Context(), "public-key"); err != nil {
		t.Fatalf("retry after transient error: %v", err)
	}
	if store.calls != 2 {
		t.Fatalf("store calls = %d, want no caching of transient error", store.calls)
	}
}
