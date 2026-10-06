// Package durable drains the spill write-ahead log into the persistence
// pipeline, providing crash-durable ingestion when spill is enabled.
package durable

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/spill"
)

// Config wires the feeder to the store, the shared dedup, and the pipeline.
type Config struct {
	Store         *spill.Store
	Dedup         *ingest.Deduplicator
	Dispatch      ingest.Dispatcher
	DispatchSpans ingest.SpanDispatcher
	Flush         func(context.Context) error
	ReportError   func(error)
	Rules         ingest.RuleLookup
	// Settings resolves a replayed record's project settings (ip-identity
	// opt-in, data scrubbing). It must be set here (unlike the ingest
	// handler's own copy, which durable mode leaves unset): a durably-spilled
	// envelope's events are only ever gated and scrubbed at replay time,
	// since ServeSentry's spill branch never calls DispatchEnvelope itself.
	// See ingest.SettingsLookup.
	Settings ingest.SettingsLookup
}

// Feeder consumes sealed spill segments, dispatches their envelopes into the
// pipeline, drives a flush barrier per segment, and prunes the segment once its
// records are durably persisted.
type Feeder struct {
	cfg Config
}

func New(cfg Config) *Feeder {
	if cfg.ReportError == nil {
		cfg.ReportError = func(error) {}
	}
	return &Feeder{cfg: cfg}
}

// Run drains available sealed segments, then blocks until a new segment seals
// (Store.Notify) or ctx is cancelled. Processing existing segments on start is
// the crash-replay path.
func (f *Feeder) Run(ctx context.Context) {
	for {
		f.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-f.cfg.Store.Notify():
		}
	}
}

// drain processes every currently-sealed segment oldest → newest, stopping on
// ctx cancellation or the first flush error (left for retry on the next wake).
func (f *Feeder) drain(ctx context.Context) {
	for _, segment := range f.cfg.Store.Sealed() {
		if ctx.Err() != nil {
			return
		}
		if err := f.processSegment(ctx, segment); err != nil {
			f.cfg.ReportError(err)
			return
		}
	}
}

func (f *Feeder) processSegment(ctx context.Context, segment spill.Segment) error {
	readErr := segment.Read(func(rec spill.Record) error {
		envelope, _, err := ingest.DecodeSentryEnvelope(bytes.NewReader(rec.Payload), ingest.RequestOptions{})
		if err != nil {
			// A record that no longer decodes cannot be retried into shape; report
			// and skip so one bad record cannot wedge replay.
			f.cfg.ReportError(fmt.Errorf("spill record undecodable, dropped: %w", err))
			return nil
		}
		parsed, err := ingest.EnvelopeFromSentryItems(envelope, rec.ReceivedAt)
		if err != nil {
			f.cfg.ReportError(fmt.Errorf("spill record unmappable, dropped: %w", err))
			return nil
		}
		ingest.DispatchEnvelope(ctx, parsed, rec.ProjectID, rec.ReceivedAt, f.cfg.Dedup, f.cfg.Rules, f.cfg.Settings, f.cfg.Dispatch, f.cfg.DispatchSpans, f.cfg.ReportError)
		return nil
	})
	if readErr != nil {
		return readErr
	}
	if err := f.cfg.Flush(ctx); err != nil {
		return err
	}
	return f.cfg.Store.Prune(segment.Seq)
}
