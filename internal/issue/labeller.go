package issue

import (
	"context"
	"fmt"
	"time"

	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
)

const (
	defaultLabelsPerBatch  = 32
	defaultLabelQueueSize  = 16
	defaultLabelResolveCap = 10 * time.Second
)

type LabelStore interface {
	UnlabelledIssues(ctx context.Context, issueIDs []int64) ([]int64, error)
	RefreshDisplayLabels(ctx context.Context, issueID int64, labels Labels) error
}

// LabellerOptions bounds the labeller. LabelsPerBatch caps the issues one
// committed batch may label, QueueSize the committed batches waiting for the
// worker, and ResolveTimeout one event's symbolication.
type LabellerOptions struct {
	LabelsPerBatch int
	QueueSize      int
	ResolveTimeout time.Duration
	ReportError    func(error)
}

// Labeller fills the display labels of issues a committed ingest batch
// touched. Offer never blocks: a batch that finds the queue full is skipped,
// and the issue's next event or page view labels it instead.
type Labeller struct {
	store          LabelStore
	jobs           chan []labelCandidate
	labelsPerBatch int
	resolveTimeout time.Duration
	reportError    func(error)
}

type labelCandidate struct {
	issueID             int64
	event               storage.EventRow
	symbolicatedCulprit string
}

func NewLabeller(store LabelStore, options LabellerOptions) *Labeller {
	labeller := &Labeller{
		store:          store,
		labelsPerBatch: options.LabelsPerBatch,
		resolveTimeout: options.ResolveTimeout,
		reportError:    options.ReportError,
	}
	if labeller.labelsPerBatch <= 0 {
		labeller.labelsPerBatch = defaultLabelsPerBatch
	}
	if labeller.resolveTimeout <= 0 {
		labeller.resolveTimeout = defaultLabelResolveCap
	}
	if labeller.reportError == nil {
		labeller.reportError = func(error) {}
	}
	queueSize := options.QueueSize
	if queueSize <= 0 {
		queueSize = defaultLabelQueueSize
	}
	labeller.jobs = make(chan []labelCandidate, queueSize)
	return labeller
}

// Offer queues the issues of a committed batch for labelling. issueIDs maps
// each delta's fingerprint to the issue its events were stored on.
func (l *Labeller) Offer(batch storage.Batch, issueIDs map[string]int64) {
	candidates := candidatesOf(batch, issueIDs)
	if len(candidates) == 0 {
		return
	}
	select {
	case l.jobs <- candidates:
	default:
	}
}

func candidatesOf(batch storage.Batch, issueIDs map[string]int64) []labelCandidate {
	firstEvent := make(map[string]int, len(batch.Issues))
	for index, event := range batch.Events {
		if _, seen := firstEvent[event.Fingerprint]; !seen {
			firstEvent[event.Fingerprint] = index
		}
	}
	positions := make(map[int64]int, len(batch.Issues))
	candidates := make([]labelCandidate, 0, len(batch.Issues))
	for _, delta := range batch.Issues {
		issueID, routed := issueIDs[delta.Fingerprint]
		eventIndex, hasEvent := firstEvent[delta.Fingerprint]
		if !routed || !hasEvent {
			continue
		}
		candidate := labelCandidate{
			issueID:             issueID,
			event:               batch.Events[eventIndex],
			symbolicatedCulprit: delta.SymbolicatedCulprit,
		}
		position, seen := positions[issueID]
		switch {
		case !seen:
			positions[issueID] = len(candidates)
			candidates = append(candidates, candidate)
		case candidates[position].symbolicatedCulprit == "":
			candidates[position] = candidate
		}
	}
	return candidates
}

func (l *Labeller) Run(ctx context.Context, resolver sourcemap.Resolver) {
	for {
		select {
		case <-ctx.Done():
			return
		case candidates := <-l.jobs:
			l.label(ctx, resolver, candidates)
		}
	}
}

func (l *Labeller) label(ctx context.Context, resolver sourcemap.Resolver, candidates []labelCandidate) {
	issueIDs := make([]int64, len(candidates))
	for index, candidate := range candidates {
		issueIDs[index] = candidate.issueID
	}
	unlabelledIDs, err := l.store.UnlabelledIssues(ctx, issueIDs)
	if err != nil {
		l.reportError(fmt.Errorf("label issues: %w", err))
		return
	}
	unlabelled := make(map[int64]struct{}, len(unlabelledIDs))
	for _, id := range unlabelledIDs {
		unlabelled[id] = struct{}{}
	}
	labelled := 0
	for _, candidate := range candidates {
		if labelled == l.labelsPerBatch || ctx.Err() != nil {
			return
		}
		if _, ok := unlabelled[candidate.issueID]; !ok {
			continue
		}
		labelled++
		labels := Labels{Culprit: candidate.symbolicatedCulprit}
		if labels.IsEmpty() {
			labels = l.resolve(ctx, resolver, candidate.event)
		}
		if labels.IsEmpty() {
			continue
		}
		if err := l.store.RefreshDisplayLabels(ctx, candidate.issueID, labels); err != nil {
			l.reportError(fmt.Errorf("label issue %d: %w", candidate.issueID, err))
		}
	}
}

func (l *Labeller) resolve(ctx context.Context, resolver sourcemap.Resolver, event storage.EventRow) (labels Labels) {
	if resolver == nil || len(event.Payload) == 0 {
		return Labels{}
	}
	ctx, cancel := context.WithTimeout(ctx, l.resolveTimeout)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			l.reportError(fmt.Errorf("symbolicate event %s for its issue labels: panic: %v", event.EventID, recovered))
			labels = Labels{}
		}
	}()
	var release string
	if event.Release != nil {
		release = *event.Release
	}
	exception, err := resolver.Resolve(ctx, sourcemap.EventInput{ProjectID: event.ProjectID, Release: release, Payload: event.Payload})
	if err != nil {
		l.reportError(fmt.Errorf("symbolicate event %s for its issue labels: %w", event.EventID, err))
	}
	return LabelsFrom(exception, event.Message)
}
