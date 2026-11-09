package ingest

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/storage"
)

func cocoaFixtureEvent(t *testing.T, build string) Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "cocoa", build+".envelope"))
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := parseSentryEnvelope(data)
	if err != nil {
		t.Fatal(err)
	}
	event, err := mapSentryEventInEnvelope(envelope.Items[0].Payload, envelope.EventID, mapReceivedAt)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func fixtureSymbols(calls *int) NativeSymbolLookup {
	return func(_ context.Context, _ int64, images []native.Image, addresses []uint64) ([]string, error) {
		*calls++
		if len(images) == 0 {
			return nil, errors.New("no images")
		}
		names := []string{"submit_order", "checkout", "main"}
		return names[:len(addresses)], nil
	}
}

func dispatchOne(t *testing.T, event Event, rules RuleLookup, symbols NativeSymbolLookup, reportError func(error)) pipeline.Item {
	t.Helper()
	var item pipeline.Item
	DispatchEnvelope(t.Context(), Envelope{Events: []Event{event}}, 5, time.Unix(0, 0).UTC(), NewDeduplicator(), rules, nil, symbols,
		func(_ string, _ int64, dispatched pipeline.Item) { item = dispatched },
		func(int64, []storage.SpanRow) {}, reportError)
	return item
}

func TestDispatchGroupsANativeCrashFromTwoBuildsByFunction(t *testing.T) {
	var calls int
	buildA := dispatchOne(t, cocoaFixtureEvent(t, "build-a"), nil, fixtureSymbols(&calls), func(err error) { t.Errorf("reported %v", err) })
	buildB := dispatchOne(t, cocoaFixtureEvent(t, "build-b"), nil, fixtureSymbols(&calls), func(err error) { t.Errorf("reported %v", err) })
	unnamedA := dispatchOne(t, cocoaFixtureEvent(t, "build-a"), nil, nil, func(err error) { t.Errorf("reported %v", err) })

	if buildA.Row.Fingerprint != buildB.Row.Fingerprint {
		t.Fatal("one crash in two builds must share a fingerprint once its frames are named")
	}
	if buildA.LegacyFingerprint != unnamedA.Row.Fingerprint || buildA.LegacyFingerprint == buildB.LegacyFingerprint {
		t.Fatalf("legacy fingerprints = %s / %s, want each build's offset fingerprint", buildA.LegacyFingerprint, buildB.LegacyFingerprint)
	}
	if buildA.Culprit != "submit_order" {
		t.Fatalf("culprit = %q, want the crashing function", buildA.Culprit)
	}
	if buildA.SymbolicatedCulprit != "submit_order" || unnamedA.SymbolicatedCulprit != "" {
		t.Fatalf("symbolicated culprits = %q / %q, want the function only once it is named", buildA.SymbolicatedCulprit, unnamedA.SymbolicatedCulprit)
	}
	if calls != 2 {
		t.Fatalf("symbol lookups = %d, want one per event", calls)
	}
}

func TestDispatchKeepsTheOffsetFingerprintWhenTheLookupFails(t *testing.T) {
	event := cocoaFixtureEvent(t, "build-a")
	offset := dispatchOne(t, event, nil, nil, func(error) {})
	for name, lookup := range map[string]NativeSymbolLookup{
		"error": func(context.Context, int64, []native.Image, []uint64) ([]string, error) {
			return nil, errors.New("read native debug file: unexpected EOF")
		},
		"panic": func(context.Context, int64, []native.Image, []uint64) ([]string, error) {
			panic("dwarf: malformed abbrev")
		},
	} {
		t.Run(name, func(t *testing.T) {
			var reported []error
			item := dispatchOne(t, event, nil, lookup, func(err error) { reported = append(reported, err) })

			if item.Row.EventID != event.EventID || item.Row.Fingerprint != offset.Row.Fingerprint || item.LegacyFingerprint != "" {
				t.Fatalf("item = %s / legacy %q, want the event dispatched on its offset fingerprint", item.Row.Fingerprint, item.LegacyFingerprint)
			}
			if len(reported) != 1 || !strings.Contains(reported[0].Error(), event.EventID.String()) {
				t.Fatalf("reported %v, want one error naming the event", reported)
			}
		})
	}
}

func TestDispatchRunsGroupingRulesOverTheLegacyFingerprint(t *testing.T) {
	event := cocoaFixtureEvent(t, "build-a")
	var calls int
	legacy := dispatchOne(t, event, nil, nil, func(error) {}).Row.Fingerprint
	rule := grouping.Rule{ID: 1, ProjectID: 5, Position: 1, Matcher: "fingerprint:" + legacy, Template: "merged-target"}
	lookup := func(context.Context, int64) ([]grouping.Rule, error) { return []grouping.Rule{rule}, nil }

	item := dispatchOne(t, event, lookup, fixtureSymbols(&calls), func(err error) { t.Errorf("reported %v", err) })

	want := grouping.EvaluateRules([]grouping.Rule{rule}, event.groupingRuleInput(), grouping.Result{Fingerprint: legacy}).Fingerprint
	if want == legacy || item.LegacyFingerprint != want {
		t.Fatalf("legacy fingerprint = %s, want the rule's %s, where a merge alias sends the offset issue's events", item.LegacyFingerprint, want)
	}
}
