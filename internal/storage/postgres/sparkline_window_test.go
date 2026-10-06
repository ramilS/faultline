package postgres

import (
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/issue"
)

// TestBucketWidthPicksSmallestBucketUnder90Points pins the {1h, 6h, 24h}
// selection rule against every trailing preset the API accepts
// (parseStatsPeriod) plus absolute ranges at the boundaries where the choice
// flips, so a future edit to the thresholds shows up as a table failure
// rather than a wrong sparkline shape discovered downstream.
func TestBucketWidthPicksSmallestBucketUnder90Points(t *testing.T) {
	cases := []struct {
		name   string
		window issue.Window
		want   time.Duration
	}{
		{"trailing 24h stays hourly (24 points)", issue.Window{Trailing: 24 * time.Hour}, time.Hour},
		{"trailing 7d needs 6h (28 points; hourly would be 168)", issue.Window{Trailing: 7 * 24 * time.Hour}, 6 * time.Hour},
		{"trailing 14d stays at 6h (56 points)", issue.Window{Trailing: 14 * 24 * time.Hour}, 6 * time.Hour},
		{"trailing 30d needs 24h (30 points; 6h would be 120)", issue.Window{Trailing: 30 * 24 * time.Hour}, 24 * time.Hour},
		{"trailing 90d stays at 24h (90 points, the cap)", issue.Window{Trailing: 90 * 24 * time.Hour}, 24 * time.Hour},

		// Boundary just under/at/over the hourly cap: 90h/1h = 90 (fits),
		// 91h/1h = 91 (does not) so it must flip to 6h.
		{"absolute 90h stays hourly at the 90-point cap", absoluteWindow(90 * time.Hour), time.Hour},
		{"absolute 91h flips to 6h once hourly exceeds 90 points", absoluteWindow(91 * time.Hour), 6 * time.Hour},

		// Same boundary one tier up: 540h (22.5d) / 6h = 90 (fits),
		// 546h/6h = 91 (does not) so it must flip to 24h.
		{"absolute 540h stays at 6h at the 90-point cap", absoluteWindow(540 * time.Hour), 6 * time.Hour},
		{"absolute 546h flips to 24h once 6h exceeds 90 points", absoluteWindow(546 * time.Hour), 24 * time.Hour},

		// Regression: bucketWidth used to floor span/candidate while
		// windowBuckets ceiled it, so a span just past a tier boundary could
		// pass this check (floor <= 90) yet still produce 91 buckets once
		// windowBuckets rounded the partial final bucket up. 90h30m/1h floors
		// to 90 but truly needs 91 hourly buckets, so it must escalate to 6h;
		// 540h30m/6h floors to 90 but truly needs 91 six-hour buckets, so it
		// must escalate to 24h.
		{"absolute 90h30m escalates past hourly despite flooring to 90", absoluteWindow(90*time.Hour + 30*time.Minute), 6 * time.Hour},
		{"absolute 540h30m escalates past 6h despite flooring to 90", absoluteWindow(540*time.Hour + 30*time.Minute), 24 * time.Hour},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := bucketWidth(testCase.window)
			if got != testCase.want {
				t.Fatalf("bucketWidth(%+v) = %s, want %s", testCase.window, got, testCase.want)
			}
			span := testCase.window.Trailing
			if testCase.window.IsAbsolute() {
				span = testCase.window.End.Sub(testCase.window.Start)
			}
			if points := bucketCount(span, got); points > 90 {
				t.Fatalf("bucketWidth(%+v) picked %s, giving %d points, want <= 90", testCase.window, got, points)
			}
		})
	}
}

func absoluteWindow(span time.Duration) issue.Window {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	return issue.Window{Start: start, End: start.Add(span)}
}

// TestWindowBucketsAnchorsAtTheWindowStart pins the bucket-boundary
// generation attachWindowedSparklines relies on: the first bucket is always
// exactly the window's lower bound (never truncated to a clock grid, and
// never earlier), so no event just inside the window's opening edge falls in
// a gap before bucket zero. That gap is exactly what the previous
// end-anchored, step-backward design left: truncating "now" (or "End") down
// to a bucket boundary before stepping back by whole buckets shifted the
// entire series forward by up to one bucket width, silently excluding the
// window's opening stretch.
func TestWindowBucketsAnchorsAtTheWindowStart(t *testing.T) {
	now := time.Date(2026, 8, 10, 15, 30, 0, 0, time.UTC)

	t.Run("trailing 24h: first bucket is exactly now-24h, not a truncated approximation", func(t *testing.T) {
		buckets := windowBuckets(issue.Window{Trailing: 24 * time.Hour}, time.Hour, now)
		if len(buckets) != 24 {
			t.Fatalf("len(buckets) = %d, want 24", len(buckets))
		}
		wantFirst := now.Add(-24 * time.Hour)
		if !buckets[0].Equal(wantFirst) {
			t.Fatalf("first bucket = %s, want %s (now - Trailing exactly)", buckets[0], wantFirst)
		}
	})

	t.Run("absolute window: first bucket is exactly Start", func(t *testing.T) {
		start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		end := start.Add(48 * time.Hour)
		buckets := windowBuckets(issue.Window{Start: start, End: end}, 6*time.Hour, now)
		if len(buckets) != 8 {
			t.Fatalf("len(buckets) = %d, want 8 (48h / 6h)", len(buckets))
		}
		if !buckets[0].Equal(start) {
			t.Fatalf("first bucket = %s, want window start %s", buckets[0], start)
		}
	})

	t.Run("a span that does not divide evenly by width still starts exactly at Start", func(t *testing.T) {
		start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
		end := start.Add(20 * time.Hour) // 20h / 6h = 3.33 -> must round up to 4 buckets
		buckets := windowBuckets(issue.Window{Start: start, End: end}, 6*time.Hour, now)
		if len(buckets) != 4 {
			t.Fatalf("len(buckets) = %d, want 4", len(buckets))
		}
		if !buckets[0].Equal(start) {
			t.Fatalf("first bucket = %s, want window start %s", buckets[0], start)
		}
		// The rounding slop from a non-exact span lands at the end (past
		// upperBound, where attachWindowedSparklines' query clamp cuts it
		// off), not at the start — so the last bucket's nominal range
		// (4*6h = 24h from Start) overruns end (20h from Start) by design.
		wantLast := start.Add(3 * 6 * time.Hour)
		if !buckets[len(buckets)-1].Equal(wantLast) {
			t.Fatalf("last bucket = %s, want %s", buckets[len(buckets)-1], wantLast)
		}
	})

	t.Run("consecutive buckets are exactly one width apart", func(t *testing.T) {
		buckets := windowBuckets(issue.Window{Trailing: 30 * 24 * time.Hour}, 24*time.Hour, now)
		for i := 1; i < len(buckets); i++ {
			if got := buckets[i].Sub(buckets[i-1]); got != 24*time.Hour {
				t.Fatalf("gap between bucket %d and %d = %s, want 24h", i-1, i, got)
			}
		}
	})
}
