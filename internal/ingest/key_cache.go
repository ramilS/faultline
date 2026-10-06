// Package ingest implements the public envelope ingestion edge: key admission,
// rate limiting, bounded decoding, and deduplication. It deliberately stops
// before queues, workers, and persistence.
package ingest

import (
	"context"
	"errors"
	"sync"
	"time"
)

const (
	positiveTTL = 30 * time.Second
	negativeTTL = 60 * time.Second
)

// ErrKeyNotFound reports that a public ingest key is unknown or revoked. The
// two cases are deliberately indistinguishable so admission does not leak
// whether a key ever existed.
var ErrKeyNotFound = errors.New("ingest key not found")

// KeyInfo is an immutable snapshot of an ingest key's admission settings.
type KeyInfo struct {
	ProjectID      int64
	PublicKey      string
	RateLimitRPS   int
	RateLimitBurst int
}

// KeyStore resolves ingest keys from durable storage. It returns ErrKeyNotFound
// for keys that are unknown or revoked.
type KeyStore interface {
	FindByPublicKey(context.Context, string) (KeyInfo, error)
}

// Clock reports the current time; it is injectable for deterministic tests.
type Clock func() time.Time

// KeyLookup resolves ingest keys through a TTL cache backed by a KeyStore.
// Positive results are cached briefly so revocation still takes effect after
// the TTL elapses; negative results are cached slightly longer to blunt probing
// of unknown keys. Transient store errors are never cached.
type KeyLookup struct {
	store KeyStore
	now   Clock

	mu      sync.Mutex
	entries map[string]cacheEntry
}

type cacheEntry struct {
	info      KeyInfo
	found     bool
	expiresAt time.Time
}

func NewKeyLookup(store KeyStore, now Clock) *KeyLookup {
	return &KeyLookup{store: store, now: now, entries: make(map[string]cacheEntry)}
}

func (l *KeyLookup) Lookup(ctx context.Context, publicKey string) (KeyInfo, error) {
	if entry, ok := l.cached(publicKey); ok {
		if !entry.found {
			return KeyInfo{}, ErrKeyNotFound
		}
		return entry.info, nil
	}

	info, err := l.store.FindByPublicKey(ctx, publicKey)
	if errors.Is(err, ErrKeyNotFound) {
		l.put(publicKey, cacheEntry{found: false, expiresAt: l.now().Add(negativeTTL)})
		return KeyInfo{}, ErrKeyNotFound
	}
	if err != nil {
		return KeyInfo{}, err
	}
	l.put(publicKey, cacheEntry{info: info, found: true, expiresAt: l.now().Add(positiveTTL)})
	return info, nil
}

func (l *KeyLookup) cached(publicKey string) (cacheEntry, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry, ok := l.entries[publicKey]
	if !ok || !entry.expiresAt.After(l.now()) {
		return cacheEntry{}, false
	}
	return entry, true
}

func (l *KeyLookup) put(publicKey string, entry cacheEntry) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries[publicKey] = entry
}
