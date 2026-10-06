package auth

import (
	"crypto/sha256"
	"encoding/binary"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	loginTokenInterval     = 12 * time.Second
	loginBucketCapacity    = 5 * loginTokenInterval
	loginEntryLifetime     = 15 * time.Minute
	loginCleanupInterval   = time.Minute
	maxLoginLimiterEntries = 10_000
	minimumCapacityRetry   = time.Millisecond
)

type loginLimitEntry struct {
	credit     time.Duration
	lastRefill time.Time
	lastUsed   time.Time
}

type LoginLimiter struct {
	mu          sync.Mutex
	clock       Clock
	entries     map[[sha256.Size]byte]*loginLimitEntry
	lastCleanup time.Time
	cleanedOnce bool
	nextStaleAt time.Time
	cleanupRuns uint64
}

func NewLoginLimiter(clock Clock) *LoginLimiter {
	if clock == nil {
		clock = systemClock{}
	}
	return &LoginLimiter{clock: clock, entries: make(map[[sha256.Size]byte]*loginLimitEntry)}
}

// loginNamespace and userNamespace domain-separate the limiter's two key
// kinds. Without them AllowUser(42) and Allow("42", "") hash to the same
// bucket, so a user id and an email that happens to read as that id would
// share one allowance and either could exhaust the other's.
// addressNamespace separates the address-only buckets AllowAddress hands
// out from the other two, for the same reason: without it an email that
// reads as an empty string and a bare address would share one allowance.
const (
	loginNamespace   = "login"
	userNamespace    = "user"
	addressNamespace = "address"
)

// Allow applies the leaky-bucket limit for an unauthenticated login attempt,
// keyed on the email and IP together: neither one alone identifies an actor
// before authentication succeeds.
func (l *LoginLimiter) Allow(email, remoteAddr string) (bool, time.Duration) {
	return l.allow(loginLimitKey(loginNamespace, NormalizeEmail(email), directIP(remoteAddr)))
}

// AllowUser applies the same leaky-bucket limit to an action already
// performed by an authenticated user — e.g. self-service password change —
// keyed on the user id alone. There is no separate address signal to add:
// the caller already proved who they are by holding a valid session, unlike
// Allow's unauthenticated caller, who is identified only by email+IP.
func (l *LoginLimiter) AllowUser(userID int64) (bool, time.Duration) {
	return l.allow(loginLimitKey(userNamespace, strconv.FormatInt(userID, 10), ""))
}

// AllowAddress applies the same leaky-bucket limit to an unauthenticated
// request whose only actor signal is the client address — the OIDC callback,
// which carries no email and no session, and whose work is an outbound
// token exchange with the identity provider.
func (l *LoginLimiter) AllowAddress(remoteAddr string) (bool, time.Duration) {
	return l.allow(loginLimitKey(addressNamespace, "", directIP(remoteAddr)))
}

func (l *LoginLimiter) allow(key [sha256.Size]byte) (bool, time.Duration) {
	now := l.clock.Now()

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.cleanupDue(now) {
		l.cleanup(now)
	}

	entry, ok := l.entries[key]
	if !ok {
		if len(l.entries) >= maxLoginLimiterEntries {
			return false, l.capacityRetry(now)
		}
		entry = &loginLimitEntry{credit: loginBucketCapacity, lastRefill: now, lastUsed: now}
		l.entries[key] = entry
		expiresAt := now.Add(loginEntryLifetime)
		if l.nextStaleAt.IsZero() || expiresAt.Before(l.nextStaleAt) {
			l.nextStaleAt = expiresAt
		}
	}
	if now.After(entry.lastRefill) {
		entry.credit += now.Sub(entry.lastRefill)
		if entry.credit > loginBucketCapacity {
			entry.credit = loginBucketCapacity
		}
		entry.lastRefill = now
	}
	if now.After(entry.lastUsed) {
		entry.lastUsed = now
	}
	if entry.credit < loginTokenInterval {
		return false, loginTokenInterval - entry.credit
	}
	entry.credit -= loginTokenInterval
	return true, 0
}

// loginLimitKey hashes a namespace and two identifying fields into a bucket
// key. Every field is length-prefixed so no combination of values can be
// rearranged into another combination's key.
func loginLimitKey(namespace, identity, ip string) [sha256.Size]byte {
	hash := sha256.New()
	var length [8]byte
	for _, field := range []string{namespace, identity, ip} {
		binary.BigEndian.PutUint64(length[:], uint64(len(field)))
		_, _ = hash.Write(length[:])
		_, _ = hash.Write([]byte(field))
	}
	var key [sha256.Size]byte
	copy(key[:], hash.Sum(nil))
	return key
}

func (l *LoginLimiter) cleanupDue(now time.Time) bool {
	return !l.cleanedOnce || !now.Before(l.lastCleanup.Add(loginCleanupInterval))
}

func (l *LoginLimiter) cleanup(now time.Time) {
	l.cleanupRuns++
	l.lastCleanup = now
	l.cleanedOnce = true
	l.nextStaleAt = time.Time{}
	for key, entry := range l.entries {
		if !now.Before(entry.lastUsed) && now.Sub(entry.lastUsed) >= loginEntryLifetime {
			delete(l.entries, key)
			continue
		}
		expiresAt := entry.lastUsed.Add(loginEntryLifetime)
		if l.nextStaleAt.IsZero() || expiresAt.Before(l.nextStaleAt) {
			l.nextStaleAt = expiresAt
		}
	}
}

func (l *LoginLimiter) capacityRetry(now time.Time) time.Duration {
	retryAt := l.nextStaleAt
	cleanupAt := l.lastCleanup.Add(loginCleanupInterval)
	if retryAt.IsZero() || cleanupAt.After(retryAt) {
		retryAt = cleanupAt
	}
	return min(max(retryAt.Sub(now), minimumCapacityRetry), loginEntryLifetime)
}

func directIP(remoteAddr string) string {
	remoteAddr = strings.TrimSpace(remoteAddr)
	if host, _, err := net.SplitHostPort(remoteAddr); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			return ip.String()
		}
		return host
	}
	if ip := net.ParseIP(strings.Trim(remoteAddr, "[]")); ip != nil {
		return ip.String()
	}
	return remoteAddr
}
