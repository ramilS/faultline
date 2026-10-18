package usage

import (
	"strconv"
	"testing"
	"time"
)

type clock struct{ at time.Time }

func (c *clock) now() time.Time { return c.at }

func TestThrottleRecordsAKeyOncePerInterval(t *testing.T) {
	c := &clock{at: time.Unix(1_000, 0)}
	throttle := NewThrottle(time.Hour, c.now)

	if !throttle.Due("a") {
		t.Fatal("first use not due")
	}
	c.at = c.at.Add(59 * time.Minute)
	if throttle.Due("a") {
		t.Fatal("due again within the interval")
	}
	if !throttle.Due("b") {
		t.Fatal("another key is not due")
	}
	c.at = c.at.Add(time.Minute)
	if !throttle.Due("a") {
		t.Fatal("not due once the interval has passed")
	}
}

func TestThrottleForgetMakesAKeyDueAgain(t *testing.T) {
	throttle := NewThrottle(time.Hour, (&clock{at: time.Unix(1_000, 0)}).now)
	throttle.Due("a")
	throttle.Forget("a")
	if !throttle.Due("a") {
		t.Fatal("not due after Forget")
	}
}

func TestThrottleForgetsExpiredKeysOnceItHoldsMany(t *testing.T) {
	c := &clock{at: time.Unix(1_000, 0)}
	throttle := NewThrottle(time.Hour, c.now)
	for i := range pruneAbove {
		throttle.Due(strconv.Itoa(i))
	}
	c.at = c.at.Add(time.Hour)
	throttle.Due("fresh")
	if len(throttle.last) != 1 {
		t.Fatalf("remembers %d keys, want only the fresh one", len(throttle.last))
	}
}
