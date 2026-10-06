package ingest

import (
	"testing"
	"time"
)

var limiterBase = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

func TestLimiterChargesEventCountAndRejectsWhenExhausted(t *testing.T) {
	limiter := NewLimiter()
	info := KeyInfo{PublicKey: "public-key", RateLimitRPS: 1, RateLimitBurst: 10}

	retry, ok := limiter.Allow(info, 10, limiterBase)
	if !ok || retry != 0 {
		t.Fatalf("first charge retry = %v, ok = %v, want immediate allow", retry, ok)
	}

	retry, ok = limiter.Allow(info, 1, limiterBase)
	if ok {
		t.Fatal("expected rejection once the burst is exhausted")
	}
	if retry <= 0 || retry > time.Second {
		t.Fatalf("retry = %v, want a positive delay near one second", retry)
	}
}

func TestLimiterIsolatesKeys(t *testing.T) {
	limiter := NewLimiter()
	a := KeyInfo{PublicKey: "a", RateLimitRPS: 1, RateLimitBurst: 1}
	b := KeyInfo{PublicKey: "b", RateLimitRPS: 1, RateLimitBurst: 1}

	if _, ok := limiter.Allow(a, 1, limiterBase); !ok {
		t.Fatal("first request for a must be allowed")
	}
	if _, ok := limiter.Allow(a, 1, limiterBase); ok {
		t.Fatal("second request for a must be rejected")
	}
	if _, ok := limiter.Allow(b, 1, limiterBase); !ok {
		t.Fatal("b must have its own bucket")
	}
}

func TestLimiterRebuildsWhenSettingsChange(t *testing.T) {
	limiter := NewLimiter()
	info := KeyInfo{PublicKey: "public-key", RateLimitRPS: 1, RateLimitBurst: 1}

	if _, ok := limiter.Allow(info, 1, limiterBase); !ok {
		t.Fatal("first request must be allowed")
	}
	if _, ok := limiter.Allow(info, 1, limiterBase); ok {
		t.Fatal("burst of one must reject the second request")
	}

	updated := KeyInfo{PublicKey: "public-key", RateLimitRPS: 5, RateLimitBurst: 5}
	if _, ok := limiter.Allow(updated, 1, limiterBase); !ok {
		t.Fatal("changed settings must rebuild the limiter with a fresh burst")
	}
}

func TestLimiterRejectsCostAboveBurst(t *testing.T) {
	limiter := NewLimiter()
	info := KeyInfo{PublicKey: "public-key", RateLimitRPS: 1, RateLimitBurst: 10}

	if _, ok := limiter.Allow(info, 11, limiterBase); ok {
		t.Fatal("a cost above the burst can never be satisfied")
	}
}
