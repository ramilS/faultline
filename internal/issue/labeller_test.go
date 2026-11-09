package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
)

type memoryLabelStore struct {
	labelled   map[int64]Labels
	findErr    error
	refreshErr error
}

func (s *memoryLabelStore) UnlabelledIssues(_ context.Context, issueIDs []int64) ([]int64, error) {
	if s.findErr != nil {
		return nil, s.findErr
	}
	var unlabelled []int64
	for _, id := range issueIDs {
		if _, ok := s.labelled[id]; !ok {
			unlabelled = append(unlabelled, id)
		}
	}
	return unlabelled, nil
}

func (s *memoryLabelStore) RefreshDisplayLabels(_ context.Context, issueID int64, labels Labels) error {
	if s.refreshErr != nil {
		return s.refreshErr
	}
	if s.labelled == nil {
		s.labelled = map[int64]Labels{}
	}
	s.labelled[issueID] = labels
	return nil
}

type resolverFunc func(sourcemap.EventInput) (*sourcemap.Exception, error)

func (f resolverFunc) Resolve(_ context.Context, input sourcemap.EventInput) (*sourcemap.Exception, error) {
	return f(input)
}

func retracedByPayload(input sourcemap.EventInput) (*sourcemap.Exception, error) {
	return &sourcemap.Exception{Type: "SourceException", ObfuscatedType: "bu4", Value: string(input.Payload)}, nil
}

type labelledBatch struct {
	batch    storage.Batch
	issueIDs map[string]int64
}

func (b *labelledBatch) add(issueID int64, symbolicatedCulprit string) uuid.UUID {
	fingerprint := strconv.FormatInt(issueID, 10)
	if b.issueIDs == nil {
		b.issueIDs = map[string]int64{}
	}
	b.issueIDs[fingerprint] = issueID
	b.batch.Issues = append(b.batch.Issues, storage.IssueDelta{Fingerprint: fingerprint, SymbolicatedCulprit: symbolicatedCulprit})
	eventID := uuid.New()
	b.batch.Events = append(b.batch.Events, storage.EventRow{
		ProjectID: 1, EventID: eventID, Fingerprint: fingerprint, Payload: []byte(fmt.Sprintf("error %d", issueID)),
	})
	return eventID
}

func labelNow(t *testing.T, labeller *Labeller, resolver sourcemap.Resolver, batch labelledBatch) {
	t.Helper()
	labeller.Offer(batch.batch, batch.issueIDs)
	select {
	case candidates := <-labeller.jobs:
		labeller.label(t.Context(), resolver, candidates)
	default:
		t.Fatal("Offer queued nothing")
	}
}

func TestLabellerLabelsIssuesFromTheirSymbolicatedEvents(t *testing.T) {
	store := &memoryLabelStore{}
	labeller := NewLabeller(store, LabellerOptions{})
	var batch labelledBatch
	batch.add(10, "")

	labelNow(t, labeller, resolverFunc(retracedByPayload), batch)

	if got, want := store.labelled[10], (Labels{Title: "SourceException: error 10"}); got != want {
		t.Fatalf("labels = %+v, want %+v", got, want)
	}
}

func TestLabellerUsesTheCulpritIngestSymbolicatedWithoutResolving(t *testing.T) {
	store := &memoryLabelStore{}
	labeller := NewLabeller(store, LabellerOptions{})
	var batch labelledBatch
	batch.add(10, "submit_order")
	resolver := resolverFunc(func(sourcemap.EventInput) (*sourcemap.Exception, error) {
		t.Fatal("the labeller resolved an event whose culprit ingest already named")
		return nil, nil
	})

	labelNow(t, labeller, resolver, batch)

	if got := store.labelled[10]; got != (Labels{Culprit: "submit_order"}) {
		t.Fatalf("labels = %+v, want the ingest culprit", got)
	}
}

func TestLabellerSkipsIssuesThatAlreadyHaveLabels(t *testing.T) {
	store := &memoryLabelStore{labelled: map[int64]Labels{10: {Title: "kept"}}}
	labeller := NewLabeller(store, LabellerOptions{})
	var batch labelledBatch
	batch.add(10, "")
	resolved := 0
	resolver := resolverFunc(func(input sourcemap.EventInput) (*sourcemap.Exception, error) {
		resolved++
		return retracedByPayload(input)
	})

	labelNow(t, labeller, resolver, batch)

	if resolved != 0 || store.labelled[10] != (Labels{Title: "kept"}) {
		t.Fatalf("resolved %d events, labels %+v; want a labelled issue left alone", resolved, store.labelled[10])
	}
}

func TestLabellerLabelsAtMostTheBoundPerBatch(t *testing.T) {
	store := &memoryLabelStore{}
	labeller := NewLabeller(store, LabellerOptions{LabelsPerBatch: 3})
	var batch labelledBatch
	for id := range int64(500) {
		batch.add(id+1, "")
	}

	labelNow(t, labeller, resolverFunc(retracedByPayload), batch)

	if len(store.labelled) != 3 {
		t.Fatalf("labelled %d issues, want the bound of 3", len(store.labelled))
	}
	for _, id := range []int64{1, 2, 3} {
		if store.labelled[id].IsEmpty() {
			t.Fatalf("issue %d unlabelled, want the batch's first issues labelled", id)
		}
	}
}

func TestLabellerReportsAFailedResolveAndLeavesTheIssueUnlabelled(t *testing.T) {
	store := &memoryLabelStore{}
	var reported []error
	labeller := NewLabeller(store, LabellerOptions{ReportError: func(err error) { reported = append(reported, err) }})
	var batch labelledBatch
	batch.add(10, "")
	failure := errors.New("mapping unreadable")

	labelNow(t, labeller, resolverFunc(func(sourcemap.EventInput) (*sourcemap.Exception, error) { return nil, failure }), batch)

	if len(store.labelled) != 0 {
		t.Fatalf("labels = %+v, want none after a failed resolve", store.labelled)
	}
	if len(reported) != 1 || !errors.Is(reported[0], failure) {
		t.Fatalf("reported %v, want the resolver failure", reported)
	}
}

func TestLabellerRecoversFromAResolverPanic(t *testing.T) {
	store := &memoryLabelStore{}
	var reported []error
	labeller := NewLabeller(store, LabellerOptions{ReportError: func(err error) { reported = append(reported, err) }})
	var batch labelledBatch
	batch.add(10, "")
	batch.add(11, "")
	resolver := resolverFunc(func(input sourcemap.EventInput) (*sourcemap.Exception, error) {
		if string(input.Payload) == "error 10" {
			panic("corrupt debug file")
		}
		return retracedByPayload(input)
	})

	labelNow(t, labeller, resolver, batch)

	if len(reported) != 1 || store.labelled[11].IsEmpty() {
		t.Fatalf("reported %v, labels %+v; want the panic reported and the next issue labelled", reported, store.labelled)
	}
}

func TestLabellerReportsAFailedWrite(t *testing.T) {
	failure := errors.New("database down")
	store := &memoryLabelStore{refreshErr: failure}
	var reported []error
	labeller := NewLabeller(store, LabellerOptions{ReportError: func(err error) { reported = append(reported, err) }})
	var batch labelledBatch
	batch.add(10, "")

	labelNow(t, labeller, resolverFunc(retracedByPayload), batch)

	if len(reported) != 1 || !errors.Is(reported[0], failure) {
		t.Fatalf("reported %v, want the write failure", reported)
	}
}

func TestLabellerOfferNeverBlocksOnAFullQueue(t *testing.T) {
	labeller := NewLabeller(&memoryLabelStore{}, LabellerOptions{QueueSize: 1})
	var first, second labelledBatch
	first.add(1, "")
	second.add(2, "")

	labeller.Offer(first.batch, first.issueIDs)
	labeller.Offer(second.batch, second.issueIDs)

	if queued := <-labeller.jobs; queued[0].issueID != 1 || len(labeller.jobs) != 0 {
		t.Fatalf("queued issue %d with %d more waiting, want only the first batch", queued[0].issueID, len(labeller.jobs))
	}
}

func TestCandidatesOfOneIssueReachedByTwoFingerprintsPreferTheSymbolicatedOne(t *testing.T) {
	var batch labelledBatch
	batch.add(10, "")
	symbolicatedEvent := batch.add(11, "submit_order")
	batch.issueIDs["11"] = 10

	candidates := candidatesOf(batch.batch, batch.issueIDs)

	ids := make([]int64, len(candidates))
	for index, candidate := range candidates {
		ids[index] = candidate.issueID
	}
	if !slices.Equal(ids, []int64{10}) || candidates[0].event.EventID != symbolicatedEvent || candidates[0].symbolicatedCulprit != "submit_order" {
		t.Fatalf("candidates = %+v, want issue 10 once, from the symbolicated event", candidates)
	}
}
