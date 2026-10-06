package ingest

import (
	"time"

	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/storage"
)

// Dispatcher hands an accepted event to the persistence pipeline. The hint is
// the shard-affinity key; projectID scopes the event.
type Dispatcher func(fingerprintHint string, projectID int64, item pipeline.Item)

// SpanDispatcher hands an accepted envelope's spans to the persistence
// pipeline. projectID scopes the spans; the slice is non-empty.
type SpanDispatcher func(projectID int64, spans []storage.SpanRow)

// Handler admits Sentry ingestion envelopes over POST
// /api/{project_id}/envelope/ (ServeSentry, in sentry_handler.go). It
// authenticates with the project id path segment plus a Sentry public key —
// from X-Sentry-Auth or the sentry_key query parameter — no session, no
// Origin check. Order is lookup → decode → charge → deduplicate, so an
// oversize or invalid envelope is rejected before it can consume a key's rate
// budget. Accepted, non-duplicate events are fingerprinted and dispatched to
// the pipeline before the response.
type Handler struct {
	keys          *KeyLookup
	limiter       *Limiter
	dedup         *Deduplicator
	now           Clock
	reportError   func(error)
	dispatch      Dispatcher
	dispatchSpans SpanDispatcher
	spiller       Spiller
	durable       bool
	rules         RuleLookup
	settings      SettingsLookup
}

// Spiller durably appends an accepted envelope before the response. spill.Store
// implements it via a thin adapter in command wiring.
type Spiller interface {
	Append(projectID int64, receivedAt time.Time, envelope []byte) error
}

// HandlerOptions configures Handler construction. Durable opts the handler
// into the spill-before-ack path (Spiller.Append before the response); the
// flag-off default (Durable: false) preserves Phase-1 behavior byte-for-byte.
type HandlerOptions struct {
	Store         KeyStore
	Now           Clock
	ReportError   func(error)
	Dispatch      Dispatcher
	DispatchSpans SpanDispatcher
	Dedup         *Deduplicator
	Spiller       Spiller
	Durable       bool
	Rules         RuleLookup
	// Settings resolves an event's project settings: the ip-identity opt-in
	// and data scrubbing (see SettingsLookup, DispatchEnvelope).
	// Only read on the non-durable dispatch path — see the comment at this
	// field's call site in command wiring for why the durable branch omits
	// it, mirroring Rules.
	Settings SettingsLookup
}

func NewHandler(store KeyStore, now Clock, reportError func(error), dispatch Dispatcher, dispatchSpans SpanDispatcher) *Handler {
	return NewHandlerWithOptions(HandlerOptions{
		Store:         store,
		Now:           now,
		ReportError:   reportError,
		Dispatch:      dispatch,
		DispatchSpans: dispatchSpans,
	})
}

func NewHandlerWithOptions(opts HandlerOptions) *Handler {
	reportError := opts.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	dispatch := opts.Dispatch
	if dispatch == nil {
		dispatch = func(string, int64, pipeline.Item) {}
	}
	dispatchSpans := opts.DispatchSpans
	if dispatchSpans == nil {
		dispatchSpans = func(int64, []storage.SpanRow) {}
	}
	dedup := opts.Dedup
	if dedup == nil {
		dedup = NewDeduplicator()
	}
	return &Handler{
		keys:          NewKeyLookup(opts.Store, opts.Now),
		limiter:       NewLimiter(),
		dedup:         dedup,
		now:           opts.Now,
		reportError:   reportError,
		dispatch:      dispatch,
		dispatchSpans: dispatchSpans,
		spiller:       opts.Spiller,
		durable:       opts.Durable,
		rules:         opts.Rules,
		settings:      opts.Settings,
	}
}
