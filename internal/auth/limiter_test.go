package auth

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoginLimiterStoresFixedSizeNonRetainingKeys(t *testing.T) {
	limiter := NewLoginLimiter(&fakeClock{now: serviceTestNow})
	marker := strings.Repeat("attacker-controlled-marker", 600)

	if allowed, _ := limiter.Allow(marker+"@example.com", "192.0.2.1:1000"); !allowed {
		t.Fatal("Allow() unexpectedly denied")
	}
	if got := len(limiter.entries); got != 1 {
		t.Fatalf("entry count = %d, want 1", got)
	}
	for key := range limiter.entries {
		if len(key) != sha256.Size {
			t.Fatalf("stored key length = %d, want %d", len(key), sha256.Size)
		}
		if bytes.Contains(key[:], []byte("attacker-controlled-marker")) {
			t.Fatal("stored key retained attacker-controlled email input")
		}
	}
}

func TestLoginLimiterAllowsFiveAttemptBurstAndReturnsRetryDuration(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for i := range 5 {
		allowed, retry := limiter.Allow("admin@example.com", "192.0.2.10:54321")
		if !allowed || retry != 0 {
			t.Fatalf("Allow() attempt %d = (%v, %v), want (true, 0)", i+1, allowed, retry)
		}
	}
	allowed, retry := limiter.Allow("admin@example.com", "192.0.2.10:9999")
	if allowed || retry != 12*time.Second {
		t.Fatalf("Allow() exhausted = (%v, %v), want (false, 12s)", allowed, retry)
	}
	clock.now = clock.now.Add(2 * time.Second)
	allowed, retry = limiter.Allow("admin@example.com", "192.0.2.10:1")
	if allowed || retry != 10*time.Second {
		t.Fatalf("Allow() after 2s = (%v, %v), want (false, 10s)", allowed, retry)
	}
}

func TestLoginLimiterRefillsFiveTokensPerMinute(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for range 5 {
		limiter.Allow("admin@example.com", "192.0.2.10:1")
	}
	clock.now = clock.now.Add(time.Minute)
	for i := range 5 {
		allowed, retry := limiter.Allow("admin@example.com", "192.0.2.10:2")
		if !allowed || retry != 0 {
			t.Fatalf("Allow() refilled attempt %d = (%v, %v)", i+1, allowed, retry)
		}
	}
	if allowed, _ := limiter.Allow("admin@example.com", "192.0.2.10:3"); allowed {
		t.Fatal("Allow() sixth refilled attempt = true, want false")
	}
}

func TestLoginLimiterNormalizesEmailAndRemovesDirectIPPort(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for i := range 5 {
		if allowed, _ := limiter.Allow(" ADMIN@Example.COM ", "[2001:db8::1]:1000"); !allowed {
			t.Fatalf("Allow() attempt %d unexpectedly denied", i+1)
		}
	}
	if allowed, _ := limiter.Allow("admin@example.com", "[2001:db8::1]:2000"); allowed {
		t.Fatal("Allow() with normalized key and another port = true, want false")
	}
}

func TestLoginLimiterSeparatesEmailAndIPCombinations(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for range 5 {
		limiter.Allow("first@example.com", "192.0.2.1:1000")
	}
	if allowed, _ := limiter.Allow("second@example.com", "192.0.2.1:2000"); !allowed {
		t.Fatal("different email unexpectedly shared limit")
	}
	if allowed, _ := limiter.Allow("first@example.com", "192.0.2.2:1000"); !allowed {
		t.Fatal("different IP unexpectedly shared limit")
	}
}

func TestLoginLimiterAllowUserAppliesTheSameFiveAttemptBurst(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for i := range 5 {
		allowed, retry := limiter.AllowUser(9)
		if !allowed || retry != 0 {
			t.Fatalf("AllowUser() attempt %d = (%v, %v), want (true, 0)", i+1, allowed, retry)
		}
	}
	allowed, retry := limiter.AllowUser(9)
	if allowed || retry != 12*time.Second {
		t.Fatalf("AllowUser() exhausted = (%v, %v), want (false, 12s)", allowed, retry)
	}
}

func TestLoginLimiterAllowUserKeysOnlyOnTheUserID(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for range 5 {
		limiter.AllowUser(9)
	}
	if allowed, _ := limiter.AllowUser(10); !allowed {
		t.Fatal("a different user id unexpectedly shared user 9's limit")
	}
	// Unlike Allow, there is no address to vary: repeating the same call from
	// a different (simulated) caller must still hit the same bucket, since
	// AllowUser takes no remote-address parameter at all.
	if allowed, _ := limiter.AllowUser(9); allowed {
		t.Fatal("user 9's bucket unexpectedly had capacity left")
	}
}

// TestLoginLimiterKeepsUserAndLoginKeySpacesApart pins the domain separation
// between the limiter's two key kinds. Both fields are length-prefixed, so
// Allow's two fields cannot be confused with each other — but without a
// namespace tag AllowUser(42) and Allow("42", "") hash identically, and one
// actor could then exhaust the other's allowance.
func TestLoginLimiterKeepsUserAndLoginKeySpacesApart(t *testing.T) {
	limiter := NewLoginLimiter(&fakeClock{now: serviceTestNow})
	for range 5 {
		limiter.AllowUser(42)
	}
	if allowed, _ := limiter.AllowUser(42); allowed {
		t.Fatal("user 42's bucket unexpectedly had capacity left")
	}

	if allowed, _ := limiter.Allow("42", ""); !allowed {
		t.Fatal(`Allow("42", "") shares a bucket with AllowUser(42)`)
	}
	if got := len(limiter.entries); got != 2 {
		t.Fatalf("entry count = %d, want 2 — one bucket per key space", got)
	}
}

func TestLoginLimiterCleansEntriesAfterFifteenMinutesWithoutUse(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	limiter.Allow("stale@example.com", "192.0.2.1:1000")
	clock.now = clock.now.Add(15 * time.Minute)
	limiter.Allow("current@example.com", "192.0.2.2:1000")
	if got := len(limiter.entries); got != 1 {
		t.Fatalf("entry count after cleanup = %d, want 1", got)
	}
}

func TestLoginLimiterHandlesClockRollbackWithoutGrantingTokensOrExtendingRetry(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for range 5 {
		limiter.Allow("admin@example.com", "192.0.2.1:1000")
	}
	clock.now = clock.now.Add(-time.Hour)
	allowed, retry := limiter.Allow("admin@example.com", "192.0.2.1:1000")
	if allowed || retry != 12*time.Second {
		t.Fatalf("Allow() after rollback = (%v, %v), want (false, 12s)", allowed, retry)
	}
	clock.now = serviceTestNow.Add(12 * time.Second)
	allowed, retry = limiter.Allow("admin@example.com", "192.0.2.1:1000")
	if !allowed || retry != 0 {
		t.Fatalf("Allow() after recovery = (%v, %v), want (true, 0)", allowed, retry)
	}
}

func TestLoginLimiterIsConcurrencySafe(t *testing.T) {
	limiter := NewLoginLimiter(&fakeClock{now: serviceTestNow})
	var allowed atomic.Int64
	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			if ok, _ := limiter.Allow("admin@example.com", "192.0.2.1:1000"); ok {
				allowed.Add(1)
			}
		})
	}
	wg.Wait()
	if got := allowed.Load(); got != 5 {
		t.Fatalf("concurrent allowed count = %d, want 5", got)
	}
}

func TestLoginLimiterBoundsEntriesAndDeniesUnseenKeysAtCapacity(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for i := range maxLoginLimiterEntries {
		email := fmt.Sprintf("user-%d@example.com", i)
		if allowed, _ := limiter.Allow(email, "192.0.2.1:1000"); !allowed {
			t.Fatalf("Allow() entry %d denied before capacity", i)
		}
	}
	allowed, retry := limiter.Allow("overflow@example.com", "192.0.2.1:1000")
	if allowed || retry <= 0 || retry > loginEntryLifetime {
		t.Fatalf("Allow() unseen at capacity = (%v, %v), want denied with retry in (0, %v]", allowed, retry, loginEntryLifetime)
	}
	if got := len(limiter.entries); got != maxLoginLimiterEntries {
		t.Fatalf("entry count = %d, want %d", got, maxLoginLimiterEntries)
	}
	if allowed, _ := limiter.Allow("user-0@example.com", "192.0.2.1:2000"); !allowed {
		t.Fatal("existing key stopped working at capacity")
	}
}

func TestLoginLimiterStaleEntriesMakeRoomAfterCapacity(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	for i := range maxLoginLimiterEntries {
		limiter.Allow(fmt.Sprintf("user-%d@example.com", i), "192.0.2.1:1000")
	}
	clock.now = clock.now.Add(15 * time.Minute)
	allowed, retry := limiter.Allow("replacement@example.com", "192.0.2.1:1000")
	if !allowed || retry != 0 {
		t.Fatalf("Allow() after stale expiry = (%v, %v), want allowed", allowed, retry)
	}
	if got := len(limiter.entries); got != 1 {
		t.Fatalf("entry count after stale cleanup = %d, want 1", got)
	}
}

func TestLoginLimiterFullCleanupRunsAtMostOncePerMinute(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	limiter := NewLoginLimiter(clock)
	limiter.Allow("first@example.com", "192.0.2.1:1000")
	wantRuns := limiter.cleanupRuns
	for i := range 100 {
		limiter.Allow(fmt.Sprintf("user-%d@example.com", i), "192.0.2.1:1000")
	}
	if limiter.cleanupRuns != wantRuns {
		t.Fatalf("cleanup runs within same minute = %d, want %d", limiter.cleanupRuns, wantRuns)
	}
	clock.now = clock.now.Add(time.Minute)
	limiter.Allow("after-minute@example.com", "192.0.2.1:1000")
	if limiter.cleanupRuns != wantRuns+1 {
		t.Fatalf("cleanup runs after one minute = %d, want %d", limiter.cleanupRuns, wantRuns+1)
	}
}

func TestLoginLimiterZeroClockStillAmortizesCleanup(t *testing.T) {
	limiter := NewLoginLimiter(&fakeClock{})
	for i := range 10 {
		limiter.Allow(fmt.Sprintf("user-%d@example.com", i), "192.0.2.1:1000")
	}
	if limiter.cleanupRuns != 1 {
		t.Fatalf("cleanup runs at zero clock = %d, want 1", limiter.cleanupRuns)
	}
}
