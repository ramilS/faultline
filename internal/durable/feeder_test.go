package durable

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/spill"
	"github.com/ramilS/faultline/internal/storage"
)

// sentryEventEnvelope builds a minimal newline-delimited Sentry envelope
// spilling a single event item, matching the bytes ServeSentry itself would
// have captured and appended.
func sentryEventEnvelope(eventID string) string {
	return "{}\n{\"type\":\"event\"}\n" +
		`{"event_id":"` + eventID + `","timestamp":"2026-07-21T00:00:00Z","level":"error","message":"boom"}` + "\n"
}

func TestFeederReplaysSealedSegmentsAndPrunes(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		opts := spill.Options{Dir: dir, SegmentBytes: 1 << 20, SegmentMaxAge: time.Hour, FsyncInterval: time.Hour, MaxBytes: 1 << 30}

		// Append one envelope, then Close — reopening seals the leftover segment,
		// the deterministic way to produce a sealed segment for the feeder.
		first, err := spill.Open(opts)
		if err != nil {
			t.Fatal(err)
		}
		raw := []byte(sentryEventEnvelope(uuid.NewString()))
		if err := first.Append(spill.Record{ProjectID: 9, ReceivedAt: time.Unix(0, 1).UTC(), Payload: raw}); err != nil {
			t.Fatal(err)
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := spill.Open(opts) // seals the leftover segment
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		var dispatched atomic.Int32
		feeder := New(Config{
			Store: store, Dedup: ingest.NewDeduplicator(),
			Dispatch:      func(string, int64, pipeline.Item) { dispatched.Add(1) },
			DispatchSpans: func(int64, []storage.SpanRow) {},
			Flush:         func(context.Context) error { return nil },
			ReportError:   func(error) {},
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go feeder.Run(ctx)

		synctest.Wait()

		if dispatched.Load() != 1 || len(store.Sealed()) != 0 {
			t.Fatalf("feeder did not dispatch+prune: dispatched=%d sealed=%d", dispatched.Load(), len(store.Sealed()))
		}
	})
}

func TestFeederDoesNotPruneWhenFlushFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		opts := spill.Options{Dir: dir, SegmentBytes: 1 << 20, SegmentMaxAge: time.Hour, FsyncInterval: time.Hour, MaxBytes: 1 << 30}

		first, err := spill.Open(opts)
		if err != nil {
			t.Fatal(err)
		}
		raw := []byte(sentryEventEnvelope(uuid.NewString()))
		if err := first.Append(spill.Record{ProjectID: 9, ReceivedAt: time.Unix(0, 1).UTC(), Payload: raw}); err != nil {
			t.Fatal(err)
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := spill.Open(opts) // seals the leftover segment
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		var dispatched atomic.Int32
		feeder := New(Config{
			Store: store, Dedup: ingest.NewDeduplicator(),
			Dispatch:      func(string, int64, pipeline.Item) { dispatched.Add(1) },
			DispatchSpans: func(int64, []storage.SpanRow) {},
			Flush:         func(context.Context) error { return errors.New("db down") },
			ReportError:   func(error) {},
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go feeder.Run(ctx)

		synctest.Wait()

		if dispatched.Load() < 1 {
			t.Fatalf("feeder never dispatched the record: dispatched=%d", dispatched.Load())
		}

		// Wait leaves the feeder durably blocked with no work left, so this is its
		// settled state: a failed Flush must leave the segment sealed (never
		// pruned), and Notify won't fire again to re-drain it into a false-positive
		// prune.
		if got := len(store.Sealed()); got != 1 {
			t.Fatalf("segment was pruned despite Flush failing: sealed=%d", got)
		}
	})
}

func TestFeederSkipsUndecodableRecordAndContinues(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		opts := spill.Options{Dir: dir, SegmentBytes: 1 << 20, SegmentMaxAge: time.Hour, FsyncInterval: time.Hour, MaxBytes: 1 << 30}

		first, err := spill.Open(opts)
		if err != nil {
			t.Fatal(err)
		}
		if err := first.Append(spill.Record{ProjectID: 9, ReceivedAt: time.Unix(0, 1).UTC(), Payload: []byte("not json")}); err != nil {
			t.Fatal(err)
		}
		validRaw := []byte(sentryEventEnvelope(uuid.NewString()))
		if err := first.Append(spill.Record{ProjectID: 9, ReceivedAt: time.Unix(0, 2).UTC(), Payload: validRaw}); err != nil {
			t.Fatal(err)
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := spill.Open(opts) // seals the leftover segment (both records)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		var dispatched atomic.Int32
		var reportedErrors atomic.Int32
		feeder := New(Config{
			Store: store, Dedup: ingest.NewDeduplicator(),
			Dispatch:      func(string, int64, pipeline.Item) { dispatched.Add(1) },
			DispatchSpans: func(int64, []storage.SpanRow) {},
			Flush:         func(context.Context) error { return nil },
			ReportError:   func(error) { reportedErrors.Add(1) },
		})

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go feeder.Run(ctx)

		synctest.Wait()

		if got := len(store.Sealed()); got != 0 {
			t.Fatalf("segment was never pruned: sealed=%d", got)
		}
		if got := dispatched.Load(); got != 1 {
			t.Fatalf("expected only the valid record to be dispatched, got dispatched=%d", got)
		}
		if got := reportedErrors.Load(); got < 1 {
			t.Fatalf("expected the undecodable record's error to be reported, got reportedErrors=%d", got)
		}
	})
}

// sentryEventEnvelopeWithIPIdentity builds the same minimal envelope as
// sentryEventEnvelope, but with an ip-only user identity, for
// TestFeederGatesIPIdentityBySettingsLookup.
func sentryEventEnvelopeWithIPIdentity(eventID, ip string) string {
	return "{}\n{\"type\":\"event\"}\n" +
		`{"event_id":"` + eventID + `","timestamp":"2026-07-21T00:00:00Z","level":"error","message":"boom","user":{"ip_address":"` + ip + `"}}` + "\n"
}

// TestFeederGatesIPIdentityBySettingsLookup is the regression test for the
// defect this fix closes: internal/command/run.go wires the server with
// Durable: true, so ServeSentry's spill branch never calls
// ingest.DispatchEnvelope — a durably-spilled envelope's events are gated
// only here, at replay time. Before this fix, an ip identity always reached
// the pipeline through this path regardless of the project's
// store_user_ip setting, because nothing in the feeder resolved it.
func TestFeederGatesIPIdentityBySettingsLookup(t *testing.T) {
	for _, testCase := range []struct {
		name        string
		storeUserIP bool
		wantUser    bool
	}{
		{"project opted in keeps the identity", true, true},
		{"project has not opted in drops the identity", false, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				dir := t.TempDir()
				opts := spill.Options{Dir: dir, SegmentBytes: 1 << 20, SegmentMaxAge: time.Hour, FsyncInterval: time.Hour, MaxBytes: 1 << 30}

				first, err := spill.Open(opts)
				if err != nil {
					t.Fatal(err)
				}
				raw := []byte(sentryEventEnvelopeWithIPIdentity(uuid.NewString(), "203.0.113.5"))
				if err := first.Append(spill.Record{ProjectID: 9, ReceivedAt: time.Unix(0, 1).UTC(), Payload: raw}); err != nil {
					t.Fatal(err)
				}
				if err := first.Close(); err != nil {
					t.Fatal(err)
				}
				store, err := spill.Open(opts) // seals the leftover segment
				if err != nil {
					t.Fatal(err)
				}
				defer store.Close()

				var item pipeline.Item
				var dispatched atomic.Int32
				feeder := New(Config{
					Store: store, Dedup: ingest.NewDeduplicator(),
					Dispatch: func(_ string, _ int64, dispatchedItem pipeline.Item) {
						item = dispatchedItem
						dispatched.Add(1)
					},
					DispatchSpans: func(int64, []storage.SpanRow) {},
					Flush:         func(context.Context) error { return nil },
					ReportError:   func(error) {},
					Settings: func(context.Context, int64) (ingest.ProjectSettings, error) {
						return ingest.ProjectSettings{StoreUserIP: testCase.storeUserIP}, nil
					},
				})

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				go feeder.Run(ctx)

				synctest.Wait()

				if dispatched.Load() != 1 {
					t.Fatal("feeder did not dispatch the replayed event")
				}

				if testCase.wantUser {
					if item.User == nil || item.User.Key != "ip" || item.User.Value != "203.0.113.5" {
						t.Fatalf("user = %+v, want {Key: ip, Value: 203.0.113.5}", item.User)
					}
				} else if item.User != nil {
					t.Fatalf("user = %+v, want nil: the project has not opted into store_user_ip", item.User)
				}
			})
		})
	}
}

// TestFeederScrubsReplayedEventsBeforeTheyReachThePipeline pins that durable
// mode scrubs too: ServeSentry's spill branch stores the raw envelope in the
// WAL, so replay is the only place the payload can be redacted.
func TestFeederScrubsReplayedEventsBeforeTheyReachThePipeline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		opts := spill.Options{Dir: dir, SegmentBytes: 1 << 20, SegmentMaxAge: time.Hour, FsyncInterval: time.Hour, MaxBytes: 1 << 30}
		first, err := spill.Open(opts)
		if err != nil {
			t.Fatal(err)
		}
		raw := "{}\n{\"type\":\"event\"}\n" +
			`{"event_id":"` + uuid.NewString() + `","timestamp":"2026-07-21T00:00:00Z","level":"error","message":"boom","extra":{"password":"hunter2"}}` + "\n"
		if err := first.Append(spill.Record{ProjectID: 9, ReceivedAt: time.Unix(0, 1).UTC(), Payload: []byte(raw)}); err != nil {
			t.Fatal(err)
		}
		if err := first.Close(); err != nil {
			t.Fatal(err)
		}
		store, err := spill.Open(opts)
		if err != nil {
			t.Fatal(err)
		}
		defer store.Close()

		var item pipeline.Item
		feeder := New(Config{
			Store: store, Dedup: ingest.NewDeduplicator(),
			Dispatch:      func(_ string, _ int64, dispatched pipeline.Item) { item = dispatched },
			DispatchSpans: func(int64, []storage.SpanRow) {},
			Flush:         func(context.Context) error { return nil },
			ReportError:   func(error) {},
			Settings: func(context.Context, int64) (ingest.ProjectSettings, error) {
				return ingest.ProjectSettings{StoreUserIP: true, Scrub: scrub.DefaultConfig()}, nil
			},
		})
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go feeder.Run(ctx)
		synctest.Wait()

		if !strings.Contains(string(item.Row.Payload), `"password":"[Filtered]"`) || strings.Contains(string(item.Row.Payload), "hunter2") {
			t.Fatalf("payload = %s, want the password redacted", item.Row.Payload)
		}
	})
}
