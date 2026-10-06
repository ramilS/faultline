package pipeline

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"runtime"
	"sync"
	"time"

	"github.com/ramilS/faultline/internal/storage"
)

const (
	// shardChannelCapacity bounds each shard's queue; a full channel applies
	// backpressure by blocking Dispatch.
	shardChannelCapacity = 4096
	// flushEventThreshold flushes a shard once this many events buffer.
	flushEventThreshold = 500
	// defaultFlushInterval flushes buffered events on this cadence.
	defaultFlushInterval = 250 * time.Millisecond
)

// Pipeline fans dispatched items across fingerprint-affinity shards. Each shard
// aggregates issue deltas and flushes them to the sink on an event threshold or
// a timer, so events for one fingerprint always aggregate on the same shard.
type Pipeline struct {
	now         func() time.Time
	reportError func(error)
	interval    time.Duration
	newTimer    func(time.Duration) (<-chan time.Time, func())
	shards      []*shard
	wait        sync.WaitGroup
	started     bool
	// lifeCtx is the pipeline's own lifetime, not a caller's request context:
	// the shard workers outlive every Dispatch call, so there is no request
	// scope to inherit. CancelRetries cancels it to unwedge durable retries.
	//nolint:containedctx // process-lifetime context, see above
	lifeCtx    context.Context
	cancelLife context.CancelFunc
}

type shard struct {
	items       chan Item
	spans       chan spanWork
	barrier     chan chan error
	aggregate   *aggregator
	sink        storage.EventSink
	reportError func(error)
	interval    time.Duration
	newTimer    func(time.Duration) (<-chan time.Time, func())
	// finalCtx carries Stop's deadline to the drain flush. It is written once
	// per shard immediately before close(items) and read only after the
	// worker observes that close, so the channel close orders the two.
	// lifeCtx is the pipeline's own lifetime; see Pipeline.lifeCtx.
	//nolint:containedctx // shutdown-deadline and lifetime contexts, see above
	finalCtx context.Context
	//nolint:containedctx // process-lifetime context, see Pipeline.lifeCtx
	lifeCtx context.Context
	durable bool
}

// Options configures a Pipeline. Durable makes flushes retry until success or
// context cancellation (used with the spill feeder) instead of dropping.
type Options struct {
	Shards      int
	Now         func() time.Time
	ReportError func(error)
	Durable     bool
}

// New builds a pipeline with the given sink and shard count. A non-positive
// shards count defaults to runtime.GOMAXPROCS(0). now defaults to time.Now and
// reportError to a no-op; reportError surfaces asynchronous persistence
// failures that cannot be returned to a caller.
func New(sink storage.EventSink, shards int, now func() time.Time, reportError func(error)) *Pipeline {
	return NewWithOptions(sink, Options{Shards: shards, Now: now, ReportError: reportError})
}

// NewWithOptions builds a pipeline per opts. See Options for field semantics.
func NewWithOptions(sink storage.EventSink, opts Options) *Pipeline {
	shards := opts.Shards
	if shards <= 0 {
		shards = runtime.GOMAXPROCS(0)
	}
	if shards <= 0 {
		shards = 1
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	reportError := opts.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}

	lifeCtx, cancelLife := context.WithCancel(context.Background())
	pipeline := &Pipeline{
		now:         now,
		reportError: reportError,
		interval:    defaultFlushInterval,
		newTimer:    systemTimer,
		shards:      make([]*shard, shards),
		lifeCtx:     lifeCtx,
		cancelLife:  cancelLife,
	}
	for index := range pipeline.shards {
		pipeline.shards[index] = &shard{
			items:       make(chan Item, shardChannelCapacity),
			spans:       make(chan spanWork, shardChannelCapacity),
			barrier:     make(chan chan error),
			aggregate:   newAggregator(),
			sink:        sink,
			reportError: reportError,
			durable:     opts.Durable,
			lifeCtx:     lifeCtx,
		}
	}
	return pipeline
}

// Start launches one worker goroutine per shard. It is idempotent.
func (p *Pipeline) Start() {
	if p.started {
		return
	}
	p.started = true
	for _, worker := range p.shards {
		worker.interval = p.interval
		worker.newTimer = p.newTimer
		p.wait.Go(worker.run)
	}
}

// Dispatch routes item to the shard owning (projectID, fingerprintHint) with a
// bounded blocking send. It stamps ReceivedAt from the clock when unset so every
// persisted row carries a receive time.
func (p *Pipeline) Dispatch(fingerprintHint string, projectID int64, item Item) {
	if item.Row.ReceivedAt.IsZero() {
		item.Row.ReceivedAt = p.now()
	}
	p.shards[p.shardIndex(projectID, fingerprintHint)].items <- item
}

// DispatchSpans routes an envelope's accepted spans to a single shard for
// batched persistence. Spans carry no fingerprint; the affinity hint is the
// first span's request id, which keeps one request's spans on one shard without
// hot-spotting a project. A nil/empty slice is a no-op.
func (p *Pipeline) DispatchSpans(projectID int64, spans []storage.SpanRow) {
	if len(spans) == 0 {
		return
	}
	hint := spans[0].RequestID
	p.shards[p.shardIndex(projectID, hint)].spans <- spanWork{projectID: projectID, rows: spans}
}

// Stop closes every shard, drains buffered items with a final flush, and waits
// for the workers to finish. It returns ctx.Err() if the drain outlasts ctx.
func (p *Pipeline) Stop(ctx context.Context) error {
	for _, worker := range p.shards {
		worker.finalCtx = ctx
		close(worker.items)
	}

	done := make(chan struct{})
	go func() {
		p.wait.Wait()
		close(done)
	}()

	var err error
	select {
	case <-done:
		err = nil
	case <-ctx.Done():
		err = ctx.Err()
	}
	// Cancel at the end, not the start: cancelling first would let a racing
	// tick/threshold flush observe a dead context and drop a still-persistable
	// batch on an otherwise clean shutdown. The close-path final flush above
	// uses finalCtx (=ctx), never lifeCtx, so it always gets a chance to
	// persist everything regardless of this cancellation. Idempotent with an
	// earlier CancelRetries call, and always cancelled here so the flag-off
	// path (which never calls CancelRetries) doesn't leak the context.
	p.cancelLife()
	return err
}

// CancelRetries abandons in-flight durable persist retries by cancelling the
// pipeline's lifetime context. A shard blocked retrying PersistBatch against a
// failed sink unwinds and resumes draining its channels, which unblocks any
// producer (the spill feeder) blocked on a full shard channel. Shutdown calls
// this only when the feeder does not drain on its own, so the feeder can exit
// before Stop closes the channels. Idempotent.
func (p *Pipeline) CancelRetries() { p.cancelLife() }

func (p *Pipeline) shardIndex(projectID int64, fingerprintHint string) int {
	hash := fnv.New32a()
	var buffer [8]byte
	binary.LittleEndian.PutUint64(buffer[:], uint64(projectID))
	_, _ = hash.Write(buffer[:])
	_, _ = hash.Write([]byte(fingerprintHint))
	return int(hash.Sum32() % uint32(len(p.shards)))
}

// Flush drives a synchronous barrier: each shard drains its channels and
// flushes. It returns the first flush error, or ctx.Err() if cancelled.
func (p *Pipeline) Flush(ctx context.Context) error {
	dones := make([]chan error, len(p.shards))
	for i, worker := range p.shards {
		done := make(chan error, 1)
		dones[i] = done
		select {
		case worker.barrier <- done:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var firstErr error
	for _, done := range dones {
		select {
		case err := <-done:
			if err != nil && firstErr == nil {
				firstErr = err
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return firstErr
}

func (s *shard) run() {
	tick, stop := s.newTimer(s.interval)
	defer stop()

	for {
		select {
		case item, ok := <-s.items:
			if !ok {
				s.drainSpans()
				// The error is discarded on every unattended flush below:
				// persist reports each sink failure through s.reportError and
				// only returns ctx.Err() once shutdown has begun, which this
				// loop can do nothing about. Flush's barrier path (below) is
				// the one caller that has somewhere to return it.
				_ = s.flush(s.finalCtx)
				return
			}
			s.aggregate.add(item)
			if s.aggregate.pending() >= flushEventThreshold {
				_ = s.flush(s.lifeCtx)
			}
		case work := <-s.spans:
			s.aggregate.addSpans(work.projectID, work.rows)
			if s.aggregate.pending() >= flushEventThreshold {
				_ = s.flush(s.lifeCtx)
			}
		case <-tick:
			_ = s.flush(s.lifeCtx)
		case done := <-s.barrier:
			s.drainItems()
			s.drainSpans()
			done <- s.flush(s.lifeCtx)
		}
	}
}

// drainItems pulls any items still buffered in the channel into the aggregator
// without blocking, so a Flush barrier picks up everything queued so far.
func (s *shard) drainItems() {
	for {
		select {
		case item, ok := <-s.items:
			if !ok {
				return
			}
			s.aggregate.add(item)
		default:
			return
		}
	}
}

// drainSpans pulls any spans still buffered in the channel at shutdown into the
// aggregator so the final flush persists them. It is only called after items is
// closed, so no new work races in.
func (s *shard) drainSpans() {
	for {
		select {
		case work := <-s.spans:
			s.aggregate.addSpans(work.projectID, work.rows)
		default:
			return
		}
	}
}

func (s *shard) flush(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for _, batch := range s.aggregate.drain() {
		if err := s.persist(ctx, batch); err != nil {
			return err
		}
	}
	return nil
}

// persist writes batch to the sink. When the shard is not durable, a
// persistence error is reported and swallowed — this is the Phase-1
// drop-and-continue behavior and must stay unchanged when Durable is false.
// When durable, it retries with bounded exponential backoff until success or
// ctx cancellation.
func (s *shard) persist(ctx context.Context, batch storage.Batch) error {
	const baseDelay = 5 * time.Millisecond
	const maxDelay = time.Second
	delay := baseDelay
	for {
		err := s.sink.PersistBatch(ctx, batch)
		if err == nil {
			return nil
		}
		if !s.durable {
			s.reportError(fmt.Errorf("persist batch project %d: %w", batch.ProjectID, err))
			return nil
		}
		s.reportError(fmt.Errorf("persist batch project %d (retrying): %w", batch.ProjectID, err))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		if delay < maxDelay {
			delay *= 2
		}
	}
}

func systemTimer(interval time.Duration) (<-chan time.Time, func()) {
	ticker := time.NewTicker(interval)
	return ticker.C, ticker.Stop
}
