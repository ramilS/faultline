package spill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"
)

// testOptions returns Options whose fsync loop stays idle, resting on both the
// hour-long FsyncInterval and a frozen now that keeps an aged roll from firing.
// Inside a synctest bubble neither holds — virtual time jumps to the pending
// timer — so a bubbled test must override both intervals.
func testOptions(dir string, now func() time.Time) Options {
	return Options{
		Dir: dir, SegmentBytes: 1 << 20, SegmentMaxAge: time.Second,
		FsyncInterval: time.Hour, MaxBytes: 1 << 30, Now: now,
	}
}

func TestStoreSealsPreExistingSegmentsOnOpen(t *testing.T) {
	dir := t.TempDir()
	// A leftover segment from a prior (crashed) run.
	old := filepath.Join(dir, "segment-0000000001.log")
	if err := os.WriteFile(old, encodeRecord(Record{ProjectID: 7, ReceivedAt: time.Unix(0, 1).UTC(), Payload: []byte("x")}), 0o644); err != nil {
		t.Fatal(err)
	}

	now := func() time.Time { return time.Unix(100, 0) }
	store, err := Open(testOptions(dir, now))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	sealed := store.Sealed()
	if len(sealed) != 1 || sealed[0].Seq != 1 {
		t.Fatalf("expected leftover segment sealed, got %+v", sealed)
	}
}

func TestStoreRollsOnSize(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return time.Unix(100, 0) }
	opts := testOptions(dir, now)
	opts.SegmentBytes = 40 // tiny so each record rolls
	store, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for i := range 3 {
		if err := store.Append(Record{ProjectID: 1, ReceivedAt: now(), Payload: []byte("payload")}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if got := len(store.Sealed()); got < 2 {
		t.Fatalf("expected multiple sealed segments after size rolls, got %d", got)
	}
}

func TestStoreAppendRejectsAtCap(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return time.Unix(100, 0) }
	opts := testOptions(dir, now)
	opts.MaxBytes = 60 // room for ~one small record
	store, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.Append(Record{ProjectID: 1, ReceivedAt: now(), Payload: []byte("first")}); err != nil {
		t.Fatalf("first append: %v", err)
	}
	err = store.Append(Record{ProjectID: 1, ReceivedAt: now(), Payload: []byte("this one overflows the cap")})
	if !errors.Is(err, ErrSpillFull) {
		t.Fatalf("append past cap: got %v want ErrSpillFull", err)
	}
}

// TestStoreBackgroundRollSealsAgedActiveSegment proves that a non-empty active
// segment ages out and seals from the background fsyncLoop alone, with no
// second Append to trigger the roll. Without the fix, an idle store leaves
// the last Append's record stranded in the never-sealed active segment.
func TestStoreBackgroundRollSealsAgedActiveSegment(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		const segmentMaxAge = time.Second

		opts := testOptions(t.TempDir(), time.Now)
		opts.SegmentMaxAge = segmentMaxAge
		opts.SegmentBytes = 1 << 20 // large: size must never trigger the roll
		opts.FsyncInterval = segmentMaxAge / 10

		store, err := Open(opts)
		if err != nil {
			t.Fatalf("open: %v", err)
		}
		defer store.Close()

		if err := store.Append(Record{ProjectID: 1, ReceivedAt: time.Now(), Payload: []byte("payload")}); err != nil {
			t.Fatalf("append: %v", err)
		}
		if got := len(store.Sealed()); got != 0 {
			t.Fatalf("expected 0 sealed segments right after append, got %d", got)
		}

		synctest.Sleep(2 * segmentMaxAge)

		if got := len(store.Sealed()); got != 1 {
			t.Fatalf("active segment never sealed from background loop; sealed=%d", got)
		}
	})
}

// TestStoreRollSurvivesSyncCloseError proves rollLocked never wedges the
// store: even when the outgoing segment's sync/close both fail, the store
// still advances to a fresh active segment and stays usable. No seam is
// added to segmentFile for this — the test is in-package, so it reaches
// into the unexported *segmentFile.file to close the OS file out from under
// the store, which is the least invasive way to force a real sync/close
// failure.
func TestStoreRollSurvivesSyncCloseError(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return time.Unix(100, 0) }
	opts := testOptions(dir, now)
	store, err := Open(opts)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	if err := store.Append(Record{ProjectID: 1, ReceivedAt: now(), Payload: []byte("payload")}); err != nil {
		t.Fatalf("append: %v", err)
	}

	store.mu.Lock()
	brokenSeq := store.activeSeq
	// Close the underlying OS file directly so the store's own sync/close
	// calls in rollLocked both fail with a real "file already closed" error.
	if err := store.active.file.Close(); err != nil {
		store.mu.Unlock()
		t.Fatalf("pre-closing active file: %v", err)
	}
	rollErr := store.rollLocked()
	advancedSeq := store.activeSeq
	store.mu.Unlock()

	if rollErr == nil {
		t.Fatal("expected rollLocked to surface the sync/close error")
	}
	if advancedSeq == brokenSeq {
		t.Fatalf("active segment did not advance past broken segment %d", brokenSeq)
	}

	foundBroken := false
	for _, seg := range store.Sealed() {
		if seg.Seq == brokenSeq {
			foundBroken = true
		}
	}
	if !foundBroken {
		t.Fatalf("expected broken segment %d sealed, got %+v", brokenSeq, store.Sealed())
	}

	// The store must remain usable: the next Append lands in the new active
	// segment instead of failing forever against the broken one.
	if err := store.Append(Record{ProjectID: 1, ReceivedAt: now(), Payload: []byte("payload-2")}); err != nil {
		t.Fatalf("append after roll error: %v", err)
	}
}

func TestStorePruneDeletesSealedThroughSeq(t *testing.T) {
	dir := t.TempDir()
	now := func() time.Time { return time.Unix(100, 0) }
	opts := testOptions(dir, now)
	opts.SegmentBytes = 40
	store, err := Open(opts)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	for range 3 {
		if err := store.Append(Record{ProjectID: 1, ReceivedAt: now(), Payload: []byte("payload")}); err != nil {
			t.Fatal(err)
		}
	}
	sealed := store.Sealed()
	if len(sealed) < 2 {
		t.Fatalf("need >=2 sealed, got %d", len(sealed))
	}
	pruneSeq := sealed[0].Seq
	if err := store.Prune(pruneSeq); err != nil {
		t.Fatalf("prune: %v", err)
	}
	for _, seg := range store.Sealed() {
		if seg.Seq <= pruneSeq {
			t.Fatalf("segment %d survived prune", seg.Seq)
		}
	}
	if _, err := os.Stat(sealed[0].Path); !os.IsNotExist(err) {
		t.Fatalf("pruned file still on disk: %v", err)
	}
}

func TestOpenTightensPreExistingDirectory(t *testing.T) {
	// A store directory created before segment payloads were treated as
	// secrets: MkdirAll's mode argument does not apply to it, so only an
	// explicit chmod on open can bring it to 0700.
	dir := filepath.Join(t.TempDir(), "spill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	store, err := Open(testOptions(dir, func() time.Time { return time.Unix(100, 0) }))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory mode = %04o, want 0700", got)
	}
}

func TestOpenTightensPreExistingSegments(t *testing.T) {
	// Segments written before the mode change keep 0644: OpenFile's mode
	// argument applies only to files it creates, and the store never reopens
	// a sealed segment for writing anyway.
	dir := t.TempDir()
	old := filepath.Join(dir, "segment-0000000001.log")
	if err := os.WriteFile(old, encodeRecord(Record{ProjectID: 7, ReceivedAt: time.Unix(0, 1).UTC(), Payload: []byte("x")}), 0o644); err != nil {
		t.Fatal(err)
	}

	store, err := Open(testOptions(dir, func() time.Time { return time.Unix(100, 0) }))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer store.Close()

	info, err := os.Stat(old)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("segment mode = %04o, want 0600", got)
	}
}
