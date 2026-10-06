package ingest

import (
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// Limiter enforces per-key token-bucket admission. Each public key gets one
// rate.Limiter, rebuilt when the stored rate settings change so revised limits
// take effect without a restart.
type Limiter struct {
	mu       sync.Mutex
	limiters map[string]*keyedLimiter
}

type keyedLimiter struct {
	limiter *rate.Limiter
	rps     int
	burst   int
}

func NewLimiter() *Limiter {
	return &Limiter{limiters: make(map[string]*keyedLimiter)}
}

// Allow charges the bucket for one envelope of the given event count. It returns
// (0, true) when admitted immediately, or (retryAfter, false) when rejected,
// where retryAfter is the wait until the request could succeed. A cost that
// exceeds the burst can never succeed and is rejected with a zero retryAfter.
func (l *Limiter) Allow(info KeyInfo, events int, now time.Time) (time.Duration, bool) {
	limiter := l.limiterFor(info)
	reservation := limiter.ReserveN(now, events)
	if !reservation.OK() {
		return 0, false
	}
	delay := reservation.DelayFrom(now)
	if delay > 0 {
		reservation.CancelAt(now)
		return delay, false
	}
	return 0, true
}

func (l *Limiter) limiterFor(info KeyInfo) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	existing, ok := l.limiters[info.PublicKey]
	if ok && existing.rps == info.RateLimitRPS && existing.burst == info.RateLimitBurst {
		return existing.limiter
	}
	rebuilt := &keyedLimiter{
		limiter: rate.NewLimiter(rate.Limit(info.RateLimitRPS), info.RateLimitBurst),
		rps:     info.RateLimitRPS,
		burst:   info.RateLimitBurst,
	}
	l.limiters[info.PublicKey] = rebuilt
	return rebuilt.limiter
}
