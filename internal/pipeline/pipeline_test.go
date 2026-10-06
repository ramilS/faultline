package pipeline

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/storage"
)

var fixedNow = time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

type recordingSink struct {
	mu      sync.Mutex
	batches []storage.Batch
}

func (r *recordingSink) PersistBatch(_ context.Context, batch storage.Batch) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.batches = append(r.batches, batch)
	return nil
}

func (r *recordingSink) eventCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	total := 0
	for _, batch := range r.batches {
		total += len(batch.Events)
	}
	return total
}

func (r *recordingSink) eventIDs() map[uuid.UUID]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	seen := make(map[uuid.UUID]int)
	for _, batch := range r.batches {
		for _, event := range batch.Events {
			seen[event.EventID]++
		}
	}
	return seen
}

func newRecordingSink() *recordingSink {
	return &recordingSink{}
}

// persistedEvents counts events across all PersistBatch calls so far.
func (r *recordingSink) persistedEvents() int {
	return r.eventCount()
}

// flakySink fails the first n PersistBatch calls, then records like
// recordingSink. It exercises the durable retry path in shard.persist.
type flakySink struct {
	mu        sync.Mutex
	failsLeft int
	batches   []storage.Batch
}

func newFlakySink(failCount int) *flakySink {
	return &flakySink{failsLeft: failCount}
}

func (f *flakySink) PersistBatch(_ context.Context, batch storage.Batch) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failsLeft > 0 {
		f.failsLeft--
		return fmt.Errorf("flaky sink: transient failure")
	}
	f.batches = append(f.batches, batch)
	return nil
}

func (f *flakySink) persistedEvents() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, batch := range f.batches {
		total += len(batch.Events)
	}
	return total
}

// alwaysFailSink rejects every PersistBatch call. It proves a durable shard's
// retry loop never succeeds on its own, so the only way out is context
// cancellation (CancelRetries).
type alwaysFailSink struct{}

func (alwaysFailSink) PersistBatch(_ context.Context, _ storage.Batch) error {
	return fmt.Errorf("always-fail sink: persist rejected")
}

// sampleItem builds a minimal Item for tests that don't care about fingerprint
// affinity, keyed by id so callers can distinguish dispatched events.
func sampleItem(t *testing.T, id string) Item {
	t.Helper()
	return Item{
		Row: storage.EventRow{
			ProjectID:   1,
			EventID:     uuid.New(),
			Timestamp:   fixedNow,
			Level:       "error",
			Environment: "production",
			Message:     "boom",
			Fingerprint: id,
		},
		Title: "boom",
	}
}

func dispatchItem(pipeline *Pipeline, projectID int64, fingerprint string) uuid.UUID {
	id := uuid.New()
	pipeline.Dispatch(fingerprint, projectID, Item{
		Row: storage.EventRow{
			ProjectID:   projectID,
			EventID:     id,
			Timestamp:   fixedNow,
			Level:       "error",
			Environment: "production",
			Message:     "boom",
			Fingerprint: fingerprint,
		},
		Title: "boom",
	})
	return id
}

func TestPipelineRoutesSameHintAndProjectToSameShard(t *testing.T) {
	pipeline := New(&recordingSink{}, 8, func() time.Time { return fixedNow }, nil)

	first := pipeline.shardIndex(42, "svc.total")
	second := pipeline.shardIndex(42, "svc.total")
	if first != second {
		t.Fatalf("same hint routed to shards %d and %d", first, second)
	}
	if first < 0 || first >= len(pipeline.shards) {
		t.Fatalf("shard index %d out of range [0,%d)", first, len(pipeline.shards))
	}
	if pipeline.shardIndex(42, "other") == first && pipeline.shardIndex(7, "svc.total") == first {
		t.Log("distinct inputs may still collide; affinity only guarantees stability")
	}
}

func TestPipelineFlushesAtEventThreshold(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &recordingSink{}
		pipeline := New(sink, 1, func() time.Time { return fixedNow }, nil)
		pipeline.interval = time.Hour // the threshold, not the timer, must drive this flush
		pipeline.Start()
		defer func() {
			_ = pipeline.Stop(t.Context())
		}()

		for range flushEventThreshold {
			dispatchItem(pipeline, 1, "fp")
		}

		synctest.Wait()

		if got := sink.eventCount(); got != flushEventThreshold {
			t.Fatalf("event count = %d, want exactly %d", got, flushEventThreshold)
		}
	})
}

func TestPipelineFlushesOnTimerTick(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := &recordingSink{}
		pipeline := New(sink, 1, func() time.Time { return fixedNow }, nil)
		ticks := make(chan time.Time, 1)
		pipeline.newTimer = func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} }
		pipeline.Start()
		defer func() {
			_ = pipeline.Stop(t.Context())
		}()

		dispatchItem(pipeline, 1, "fp")
		dispatchItem(pipeline, 1, "fp")

		// Both items are aggregated but unflushed once the shard goes idle;
		// a single tick then flushes exactly them.
		synctest.Wait()
		ticks <- fixedNow
		synctest.Wait()

		if got := sink.eventCount(); got != 2 {
			t.Fatalf("event count = %d, want 2", got)
		}
	})
}

func TestPipelineStopFlushesPendingItemsExactlyOnce(t *testing.T) {
	sink := &recordingSink{}
	pipeline := New(sink, 4, func() time.Time { return fixedNow }, nil)
	pipeline.interval = time.Hour
	pipeline.Start()

	dispatched := make(map[uuid.UUID]struct{})
	for i := range 50 {
		dispatched[dispatchItem(pipeline, int64(i%3), "fp")] = struct{}{}
	}

	if err := pipeline.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	if sink.eventCount() != 50 {
		t.Fatalf("event count = %d, want 50", sink.eventCount())
	}
	seen := sink.eventIDs()
	if len(seen) != len(dispatched) {
		t.Fatalf("distinct events persisted = %d, want %d", len(seen), len(dispatched))
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("event %s persisted %d times, want exactly once", id, count)
		}
		if _, ok := dispatched[id]; !ok {
			t.Fatalf("event %s persisted but never dispatched", id)
		}
	}
}

func TestDispatchSpansPersistsThroughFlush(t *testing.T) {
	sink := &recordingSink{}
	pipeline := New(sink, 1, func() time.Time { return fixedNow }, nil)
	pipeline.interval = time.Hour
	pipeline.Start()

	pipeline.DispatchSpans(3, []storage.SpanRow{
		{
			ProjectID: 3,
			RequestID: "req-1",
			SpanID:    "s1",
			Name:      "a",
			StartTime: fixedNow,
		},
	})

	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := pipeline.Stop(ctx); err != nil {
		t.Fatalf("stop: %v", err)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.batches) != 1 || len(sink.batches[0].Spans) != 1 {
		t.Fatalf("batches = %+v", sink.batches)
	}
	if sink.batches[0].Spans[0].SpanID != "s1" {
		t.Fatalf("span = %+v", sink.batches[0].Spans[0])
	}
}

func TestPipelineStampsReceivedAtWhenUnset(t *testing.T) {
	sink := &recordingSink{}
	pipeline := New(sink, 1, func() time.Time { return fixedNow }, nil)
	pipeline.interval = time.Hour
	pipeline.Start()

	dispatchItem(pipeline, 1, "fp")
	if err := pipeline.Stop(t.Context()); err != nil {
		t.Fatalf("stop: %v", err)
	}

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.batches) == 0 || len(sink.batches[0].Events) == 0 {
		t.Fatal("no events persisted")
	}
	if !sink.batches[0].Events[0].ReceivedAt.Equal(fixedNow) {
		t.Fatalf("received_at = %v, want stamped %v", sink.batches[0].Events[0].ReceivedAt, fixedNow)
	}
}

func TestFlushBarrierPersistsBufferedItems(t *testing.T) {
	sink := newRecordingSink()
	p := NewWithOptions(sink, Options{Shards: 2, Now: func() time.Time { return time.Unix(0, 0) }, Durable: true})
	p.Start()
	defer func() {
		_ = p.Stop(t.Context())
	}()

	p.Dispatch("hintA", 1, sampleItem(t, "a"))
	p.Dispatch("hintB", 1, sampleItem(t, "b"))

	if err := p.Flush(t.Context()); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got := sink.persistedEvents(); got != 2 {
		t.Fatalf("after barrier persisted %d events, want 2", got)
	}
}

func TestDurableFlushRetriesOnTransientError(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		sink := newFlakySink(2) // fails first 2 PersistBatch calls, then succeeds
		p := NewWithOptions(sink, Options{Shards: 1, Now: func() time.Time { return time.Unix(0, 0) }, Durable: true})
		p.Start()
		defer func() {
			_ = p.Stop(t.Context())
		}()

		p.Dispatch("hint", 1, sampleItem(t, "a"))
		if err := p.Flush(t.Context()); err != nil {
			t.Fatalf("Flush should succeed after retries: %v", err)
		}
		if sink.persistedEvents() != 1 {
			t.Fatalf("event not persisted after retries")
		}
	})
}

// TestCancelRetriesUnblocksStuckDurableFlush proves the mechanism CancelRetries
// exists for: a durable shard stuck retrying PersistBatch against a sink that
// never succeeds blocks Flush indefinitely, and only cancelling the pipeline's
// lifetime context unwedges it. If CancelRetries were a no-op, this test would
// time out waiting for Flush to return.
func TestCancelRetriesUnblocksStuckDurableFlush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := NewWithOptions(alwaysFailSink{}, Options{Shards: 1, Now: func() time.Time { return fixedNow }, Durable: true})
		p.Start()
		stopCtx, cancelStop := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancelStop()
		defer func() {
			_ = p.Stop(stopCtx)
		}()

		p.Dispatch("hint", 1, sampleItem(t, "a"))

		flushErr := make(chan error, 1)
		go func() {
			flushErr <- p.Flush(t.Context())
		}()

		select {
		case err := <-flushErr:
			t.Fatalf("Flush returned early (err=%v); want it blocked retrying against the always-failing sink", err)
		case <-time.After(100 * time.Millisecond):
		}

		p.CancelRetries()

		// The timeout stays even inside the bubble: a CancelRetries that did
		// nothing would leave the retry loop rescheduling its backoff timer
		// forever, so virtual time would keep advancing and the bubble's
		// all-goroutines-blocked panic would never fire.
		select {
		case err := <-flushErr:
			if err == nil {
				t.Fatal("Flush returned nil error after CancelRetries; want the persist or ctx error to propagate")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("Flush did not unblock within 2s of CancelRetries")
		}
	})
}
