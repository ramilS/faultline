// Package usage records that an artifact was used without writing on every
// use: symbolication runs per event, retention needs only a coarse "recently".
package usage

import (
	"sync"
	"time"
)

// Interval is how stale a recorded use may get. Retention keeps the most
// recently used files out of dozens, so an hour cannot reorder anything that
// matters.
const Interval = time.Hour

// pruneAbove bounds the set of remembered keys; past it, keys whose interval
// has run out are forgotten, as they would be due again anyway.
const pruneAbove = 4096

// Throttle answers whether a use of a key is due to be recorded.
type Throttle struct {
	mu       sync.Mutex
	interval time.Duration
	now      func() time.Time
	last     map[string]time.Time
}

func NewThrottle(interval time.Duration, now func() time.Time) *Throttle {
	return &Throttle{interval: interval, now: now, last: make(map[string]time.Time)}
}

// Due reports whether key has not been recorded within the interval, and if so
// counts this call as its recording.
func (t *Throttle) Due(key string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	if last, ok := t.last[key]; ok && now.Sub(last) < t.interval {
		return false
	}
	if len(t.last) >= pruneAbove {
		for stale, at := range t.last {
			if now.Sub(at) >= t.interval {
				delete(t.last, stale)
			}
		}
	}
	t.last[key] = now
	return true
}

// Forget makes key due again, for a recording that failed.
func (t *Throttle) Forget(key string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.last, key)
}
