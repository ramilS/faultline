package ingest

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/storage"
)

func TestDispatchEnvelopeDedupsAndDispatches(t *testing.T) {
	dedup := NewDeduplicator()
	id := uuid.New()
	env := Envelope{Events: []Event{
		{EventID: id, Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom", Environment: "production"},
		{EventID: id, Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom", Environment: "production"},
	}}

	var dispatched int
	DispatchEnvelope(t.Context(), env, 5, time.Unix(0, 0).UTC(), dedup, nil, nil,
		func(string, int64, pipeline.Item) { dispatched++ },
		func(int64, []storage.SpanRow) {}, func(error) {})

	if dispatched != 1 {
		t.Fatalf("dispatched %d, want 1 (the second event is a duplicate and must be dropped)", dispatched)
	}
}

func TestDispatchEnvelopeAppliesMatchingTextRule(t *testing.T) {
	dedup := NewDeduplicator()
	event := Event{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Logger: "billing", Message: "payment failed for order 42", Environment: "production"}
	env := Envelope{Events: []Event{event}}
	rule := grouping.Rule{ID: 1, ProjectID: 5, Position: 1, Matcher: `message:"payment failed*"`, Template: "payments,payment-failure"}
	lookup := func(context.Context, int64) ([]grouping.Rule, error) { return []grouping.Rule{rule}, nil }

	deterministic := grouping.Fingerprint(event.groupingInput())
	want := grouping.EvaluateRules([]grouping.Rule{rule}, event.groupingRuleInput(), deterministic)

	var hint string
	var item pipeline.Item
	DispatchEnvelope(t.Context(), env, 5, time.Unix(0, 0).UTC(), dedup, lookup, nil,
		func(fingerprintHint string, _ int64, dispatched pipeline.Item) {
			hint = fingerprintHint
			item = dispatched
		},
		func(int64, []storage.SpanRow) {}, func(error) {})

	if hint != want.Fingerprint {
		t.Fatalf("fingerprint hint = %q, want %q (rule-derived)", hint, want.Fingerprint)
	}
	if hint == deterministic.Fingerprint {
		t.Fatal("rule match must override the deterministic fingerprint")
	}
	// A rule may only steer the fingerprint; Title/Culprit must stay the
	// deterministic values even when the fingerprint itself is rule-derived.
	if item.Title != deterministic.Title {
		t.Fatalf("item.Title = %q, want deterministic %q (rules must not override Title/Culprit)", item.Title, deterministic.Title)
	}
	if item.Culprit != deterministic.Culprit {
		t.Fatalf("item.Culprit = %q, want deterministic %q (rules must not override Title/Culprit)", item.Culprit, deterministic.Culprit)
	}
}

// TestDispatchEnvelopeMatchesRawCaseNotNormalizedMessage proves fingerprintItem
// hands the matcher raw (pre-normalization) event values, not the lowercased
// text grouping.Fingerprint produces internally. Matching is case-sensitive
// (ParsedMatcher.Match), while normalizeMessage lowercases everything; a
// matcher pattern that only matches the original casing therefore only
// matches if the raw message reaches EvaluateRules.
func TestDispatchEnvelopeMatchesRawCaseNotNormalizedMessage(t *testing.T) {
	dedup := NewDeduplicator()
	event := Event{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "Payment Failed", Environment: "production"}
	env := Envelope{Events: []Event{event}}
	rule := grouping.Rule{ID: 1, ProjectID: 5, Position: 1, Matcher: `message:"Payment Failed"`, Template: "payments,payment-failure"}
	lookup := func(context.Context, int64) ([]grouping.Rule, error) { return []grouping.Rule{rule}, nil }

	deterministic := grouping.Fingerprint(event.groupingInput())
	want := grouping.EvaluateRules([]grouping.Rule{rule}, event.groupingRuleInput(), deterministic)

	var hint string
	DispatchEnvelope(t.Context(), env, 5, time.Unix(0, 0).UTC(), dedup, lookup, nil,
		func(fingerprintHint string, _ int64, _ pipeline.Item) { hint = fingerprintHint },
		func(int64, []storage.SpanRow) {}, func(error) {})

	if hint != want.Fingerprint {
		t.Fatalf("fingerprint hint = %q, want %q (rule-derived)", hint, want.Fingerprint)
	}
	if hint == deterministic.Fingerprint {
		t.Fatal("case-sensitive exact matcher must match the raw, non-lowercased message")
	}
}

// TestDispatchEnvelopeReportsRuleLookupErrorOncePerEnvelope pins the
// outage-amplification fix: a rule-store failure must be reported exactly
// once for the whole envelope, not once per event, and every event in the
// envelope must still ingest via deterministic fingerprinting.
func TestDispatchEnvelopeReportsRuleLookupErrorOncePerEnvelope(t *testing.T) {
	dedup := NewDeduplicator()
	events := []Event{
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom one", Environment: "production"},
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom two", Environment: "production"},
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom three", Environment: "production"},
	}
	env := Envelope{Events: events}
	var lookups int
	lookup := func(context.Context, int64) ([]grouping.Rule, error) {
		lookups++
		return nil, errors.New("store unavailable")
	}

	var reported, dispatched int
	hints := make([]string, 0, len(events))
	DispatchEnvelope(t.Context(), env, 5, time.Unix(0, 0).UTC(), dedup, lookup, nil,
		func(fingerprintHint string, _ int64, _ pipeline.Item) {
			hints = append(hints, fingerprintHint)
			dispatched++
		},
		func(int64, []storage.SpanRow) {},
		func(error) { reported++ })

	if dispatched != len(events) {
		t.Fatalf("dispatched = %d, want %d (deterministic fallback for every event)", dispatched, len(events))
	}
	if lookups != 1 {
		t.Fatalf("rule lookups = %d, want exactly 1 for the whole envelope", lookups)
	}
	if reported != 1 {
		t.Fatalf("reported = %d, want 1 (once per envelope, not once per event)", reported)
	}
	for index, hint := range hints {
		want := grouping.Fingerprint(events[index].groupingInput()).Fingerprint
		if hint != want {
			t.Fatalf("hint[%d] = %q, want deterministic %q when the rule lookup fails", index, hint, want)
		}
	}
}

// TestDispatchEnvelopeLooksUpRulesOncePerEnvelope pins the per-envelope-not-
// per-event hoist directly: an envelope with multiple events must trigger
// exactly one rule lookup, since every event in an envelope shares the same
// project by construction.
func TestDispatchEnvelopeLooksUpRulesOncePerEnvelope(t *testing.T) {
	dedup := NewDeduplicator()
	env := Envelope{Events: []Event{
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom one", Environment: "production"},
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom two", Environment: "production"},
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom three", Environment: "production"},
	}}

	var lookups, dispatched int
	lookup := func(context.Context, int64) ([]grouping.Rule, error) {
		lookups++
		return nil, nil
	}

	DispatchEnvelope(t.Context(), env, 5, time.Unix(0, 0).UTC(), dedup, lookup, nil,
		func(string, int64, pipeline.Item) { dispatched++ },
		func(int64, []storage.SpanRow) {}, func(error) {})

	if dispatched != 3 {
		t.Fatalf("dispatched = %d, want 3", dispatched)
	}
	if lookups != 1 {
		t.Fatalf("rule lookups = %d, want exactly 1 for a 3-event envelope", lookups)
	}
}

// TestDispatchEnvelopeGatesIPIdentityBySettingsLookup pins the gate's new
// home: DispatchEnvelope, not either caller, decides whether an ip-keyed
// identity survives, so both the non-durable handler and the durable
// feeder's replay path — the two callers of DispatchEnvelope — gate
// identically by construction.
func TestDispatchEnvelopeGatesIPIdentityBySettingsLookup(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		settings SettingsLookup
		wantUser bool
	}{
		{"opted in keeps the identity", func(context.Context, int64) (ProjectSettings, error) { return ProjectSettings{StoreUserIP: true}, nil }, true},
		{"opted out drops the identity", func(context.Context, int64) (ProjectSettings, error) { return ProjectSettings{}, nil }, false},
		{"nil lookup fails closed", nil, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			dedup := NewDeduplicator()
			env := Envelope{Events: []Event{
				{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "boom", Environment: "production", User: &EventUser{Key: userKeyIP, Value: "203.0.113.5"}},
			}}

			var item pipeline.Item
			DispatchEnvelope(t.Context(), env, 5, time.Unix(0, 0).UTC(), dedup, nil, testCase.settings,
				func(_ string, _ int64, dispatched pipeline.Item) { item = dispatched },
				func(int64, []storage.SpanRow) {}, func(error) {})

			if testCase.wantUser {
				if item.User == nil || item.User.Key != userKeyIP || item.User.Value != "203.0.113.5" {
					t.Fatalf("user = %+v, want {Key: ip, Value: 203.0.113.5}", item.User)
				}
			} else if item.User != nil {
				t.Fatalf("user = %+v, want nil", item.User)
			}
		})
	}
}

// TestDispatchEnvelopeFailsClosedAndReportsOnceWhenSettingsLookupErrors
// pins the coordinator's ruling: a transient lookup failure must never risk
// aggregating an address the project may not have consented to aggregate, so it
// drops the identity rather than keeping it, and — mirroring the rule
// lookup's outage behaviour — reports the failure exactly once per envelope,
// not once per event.
func TestDispatchEnvelopeFailsClosedAndReportsOnceWhenSettingsLookupErrors(t *testing.T) {
	dedup := NewDeduplicator()
	events := []Event{
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "one", Environment: "production", User: &EventUser{Key: userKeyIP, Value: "203.0.113.5"}},
		{EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: "two", Environment: "production", User: &EventUser{Key: userKeyIP, Value: "203.0.113.6"}},
	}
	env := Envelope{Events: events}

	var lookups int
	failingLookup := func(context.Context, int64) (ProjectSettings, error) {
		lookups++
		return ProjectSettings{StoreUserIP: true}, errors.New("project settings store unavailable")
	}

	var reported int
	var items []pipeline.Item
	DispatchEnvelope(t.Context(), env, 5, time.Unix(0, 0).UTC(), dedup, nil, failingLookup,
		func(_ string, _ int64, dispatched pipeline.Item) { items = append(items, dispatched) },
		func(int64, []storage.SpanRow) {},
		func(error) { reported++ })

	if lookups != 1 {
		t.Fatalf("lookups = %d, want exactly 1 for the whole envelope", lookups)
	}
	if reported != 1 {
		t.Fatalf("reported = %d, want 1 (once per envelope, not once per event)", reported)
	}
	if len(items) != len(events) {
		t.Fatalf("dispatched = %d, want %d (the events themselves must still ingest)", len(items), len(events))
	}
	for _, item := range items {
		if item.User != nil {
			t.Fatalf("user = %+v, want nil: a lookup error must fail closed even though it returned true alongside the error", item.User)
		}
	}
}

func dispatchWithSettings(t *testing.T, event Event, settings SettingsLookup) (string, pipeline.Item, int) {
	t.Helper()
	var fingerprint string
	var item pipeline.Item
	reported := 0
	DispatchEnvelope(t.Context(), Envelope{Events: []Event{event}}, 5, time.Unix(0, 0).UTC(), NewDeduplicator(), nil, settings,
		func(dispatchedFingerprint string, _ int64, dispatched pipeline.Item) {
			fingerprint, item = dispatchedFingerprint, dispatched
		},
		func(int64, []storage.SpanRow) {}, func(error) { reported++ })
	return fingerprint, item, reported
}

func scrubbableEvent(message string) Event {
	return Event{
		EventID: uuid.New(), Timestamp: time.Unix(0, 0).UTC(), Level: "error", Message: message, Environment: "production",
		Payload: []byte(`{"request":{"data":{"password":"hunter2"}},"user":{"ip_address":"203.0.113.5"}}`),
	}
}

func fixedSettings(settings ProjectSettings) SettingsLookup {
	return func(context.Context, int64) (ProjectSettings, error) { return settings, nil }
}

// TestDispatchEnvelopeScrubsTheStoredPayloadButNotTheFingerprint pins that
// scrubbing rewrites only what is stored: the fingerprint is computed from the
// unscrubbed event, so grouping is the same with scrubbing on or off.
func TestDispatchEnvelopeScrubsTheStoredPayloadButNotTheFingerprint(t *testing.T) {
	const message = "charge 4111111111111111 failed"
	onFingerprint, on, _ := dispatchWithSettings(t, scrubbableEvent(message), fixedSettings(ProjectSettings{Scrub: scrub.DefaultConfig()}))
	offFingerprint, off, _ := dispatchWithSettings(t, scrubbableEvent(message), fixedSettings(ProjectSettings{}))

	if onFingerprint != offFingerprint {
		t.Fatalf("fingerprint changed: %q vs %q", onFingerprint, offFingerprint)
	}
	if on.Title != "charge [Filtered] failed" || off.Title != message {
		t.Fatalf("titles = %q / %q", on.Title, off.Title)
	}
	if got := string(on.Row.Payload); got != `{"request":{"data":{"password":"[Filtered]"}},"user":{"ip_address":"[Filtered]"}}` {
		t.Fatalf("scrubbed payload = %s", got)
	}
	if on.Row.Message != "charge [Filtered] failed" {
		t.Fatalf("scrubbed message = %q", on.Row.Message)
	}
	if string(off.Row.Payload) != string(scrubbableEvent("").Payload) || off.Row.Message != message {
		t.Fatalf("disabled scrubbing changed the row: %s / %q", off.Row.Payload, off.Row.Message)
	}
}

func TestDispatchEnvelopeKeepsTheIPWhenTheProjectStoresUserIPs(t *testing.T) {
	_, item, _ := dispatchWithSettings(t, scrubbableEvent("boom"), fixedSettings(ProjectSettings{StoreUserIP: true, Scrub: scrub.DefaultConfig()}))
	if got := string(item.Row.Payload); got != `{"request":{"data":{"password":"[Filtered]"}},"user":{"ip_address":"203.0.113.5"}}` {
		t.Fatalf("payload = %s", got)
	}
}

// TestDispatchEnvelopeScrubsWhenTheSettingsCannotBeRead pins the fail-closed
// default: no lookup, or a failing one, must not turn scrubbing off.
func TestDispatchEnvelopeScrubsWhenTheSettingsCannotBeRead(t *testing.T) {
	failing := func(context.Context, int64) (ProjectSettings, error) {
		return ProjectSettings{}, errors.New("settings store unavailable")
	}
	for name, lookup := range map[string]SettingsLookup{"nil lookup": nil, "failing lookup": failing} {
		t.Run(name, func(t *testing.T) {
			_, item, _ := dispatchWithSettings(t, scrubbableEvent("boom"), lookup)
			if got := string(item.Row.Payload); got != `{"request":{"data":{"password":"[Filtered]"}},"user":{"ip_address":"[Filtered]"}}` {
				t.Fatalf("payload = %s", got)
			}
		})
	}
}

// A stored rule that no longer validates must not be skipped silently: the
// whole snapshot falls back to the closed defaults and the failure is reported.
func TestDispatchEnvelopeFallsBackToTheBuiltInsWhenAStoredRuleIsInvalid(t *testing.T) {
	bad := []scrub.Rule{{Name: "bad", Pattern: `(`}}
	off := scrub.Config{Enabled: false, ExtraFields: []string{}, SafeFields: []string{}, Rules: []scrub.Rule{}}
	withBadProjectRule := off
	withBadProjectRule.Rules = bad
	for name, settings := range map[string]ProjectSettings{
		"project rule": {StoreUserIP: true, Scrub: withBadProjectRule},
		"global rule":  {StoreUserIP: true, Scrub: off, GlobalRules: bad},
	} {
		t.Run(name, func(t *testing.T) {
			_, item, reported := dispatchWithSettings(t, scrubbableEvent("boom"), fixedSettings(settings))
			if got := string(item.Row.Payload); got != `{"request":{"data":{"password":"[Filtered]"}},"user":{"ip_address":"[Filtered]"}}` {
				t.Fatalf("payload = %s, want the closed defaults", got)
			}
			if reported != 1 {
				t.Fatalf("reported = %d, want 1", reported)
			}
		})
	}
}

func TestDispatchEnvelopeStoresAnUnscrubbablePayloadAndReportsIt(t *testing.T) {
	event := scrubbableEvent("boom")
	event.Payload = []byte(`not json`)
	_, item, reported := dispatchWithSettings(t, event, fixedSettings(ProjectSettings{Scrub: scrub.DefaultConfig()}))
	if string(item.Row.Payload) != "not json" || reported != 1 {
		t.Fatalf("payload = %s, reported = %d, want the original stored and one report", item.Row.Payload, reported)
	}
}

func TestDispatchEnvelopeScrubsTheCulprit(t *testing.T) {
	event := scrubbableEvent("boom")
	event.Exception = &grouping.Exception{
		Type: "PaymentError", Value: "declined",
		Frames: []grouping.Frame{{Module: "billing", Function: "charge 4111111111111111", InApp: true}},
	}
	_, on, _ := dispatchWithSettings(t, event, fixedSettings(ProjectSettings{Scrub: scrub.DefaultConfig()}))
	_, off, _ := dispatchWithSettings(t, event, fixedSettings(ProjectSettings{}))

	if !strings.Contains(off.Culprit, "4111111111111111") {
		t.Fatalf("fixture culprit = %q, want it to carry the card number", off.Culprit)
	}
	if strings.Contains(on.Culprit, "4111") || !strings.Contains(on.Culprit, scrub.Filtered) {
		t.Fatalf("culprit = %q, want the card number redacted", on.Culprit)
	}
}

func TestDispatchEnvelopeScrubsSpanNamesAndAttributes(t *testing.T) {
	span := Span{
		RequestID: "r", SpanID: "s", Name: "charge 4111111111111111", StartTime: time.Unix(0, 0).UTC(),
		Attributes: []byte(`{"db.password":"hunter2","db.system":"postgres"}`),
	}
	for name, testCase := range map[string]struct {
		settings       ProjectSettings
		wantName       string
		wantAttributes string
	}{
		"scrubbing on":  {ProjectSettings{Scrub: scrub.DefaultConfig()}, "charge [Filtered]", `{"db.password":"[Filtered]","db.system":"postgres"}`},
		"scrubbing off": {ProjectSettings{}, "charge 4111111111111111", `{"db.password":"hunter2","db.system":"postgres"}`},
	} {
		t.Run(name, func(t *testing.T) {
			var rows []storage.SpanRow
			DispatchEnvelope(t.Context(), Envelope{Spans: []Span{span}}, 5, time.Unix(0, 0).UTC(), NewDeduplicator(), nil, fixedSettings(testCase.settings),
				func(string, int64, pipeline.Item) {},
				func(_ int64, dispatched []storage.SpanRow) { rows = dispatched }, func(error) {})

			if len(rows) != 1 || rows[0].Name != testCase.wantName || string(rows[0].Attributes) != testCase.wantAttributes {
				t.Fatalf("rows = %+v, want name %q attributes %s", rows, testCase.wantName, testCase.wantAttributes)
			}
		})
	}
}

func TestDispatchEnvelopeAppliesGlobalAndProjectScrubbingRules(t *testing.T) {
	const token = "123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g"
	message := "POST https://api.telegram.org/bot" + token + "/sendMessage failed for acct-42"
	settings := ProjectSettings{
		Scrub: scrub.Config{
			Enabled: true, ExtraFields: []string{}, SafeFields: []string{},
			Rules: []scrub.Rule{{Name: "account", Pattern: `acct-\d+`}},
		},
		GlobalRules: []scrub.Rule{{Name: "Telegram", Pattern: `bot\d{6,}:[A-Za-z0-9_-]{30,}`, Replacement: "bot[Filtered]"}},
	}

	_, item, _ := dispatchWithSettings(t, scrubbableEvent(message), fixedSettings(settings))

	want := "POST https://api.telegram.org/bot[Filtered]/sendMessage failed for [Filtered]"
	if item.Row.Message != want {
		t.Fatalf("stored message = %q, want %q", item.Row.Message, want)
	}
}

func taggedEvent(tags map[string]string) Event {
	event := scrubbableEvent("boom")
	event.Tags = tags
	return event
}

const telegramTagURL = "https://api.telegram.org/bot123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g/sendMessage"

var telegramTagRule = scrub.Rule{Name: "Telegram", Pattern: `bot\d{6,}:[A-Za-z0-9_-]{30,}`, Replacement: "bot[Filtered]"}

func TestDispatchEnvelopeScrubsTagValues(t *testing.T) {
	config := scrub.DefaultConfig()
	config.Rules = []scrub.Rule{telegramTagRule}
	config.SafeFields = []string{"session_token"}
	tags := map[string]string{
		"endpoint":      telegramTagURL,
		"token":         "abc",
		"session_token": "keep-me",
		"service":       "billing",
	}

	_, item, _ := dispatchWithSettings(t, taggedEvent(tags), fixedSettings(ProjectSettings{Scrub: config}))

	want := map[string]string{
		"endpoint":      "https://api.telegram.org/bot[Filtered]/sendMessage",
		"token":         scrub.Filtered,
		"session_token": "keep-me",
		"service":       "billing",
	}
	if !maps.Equal(item.Tags, want) {
		t.Fatalf("tags = %v, want %v", item.Tags, want)
	}
	if tags["token"] != "abc" || tags["endpoint"] != telegramTagURL {
		t.Fatalf("the event's own tag map was mutated: %v", tags)
	}
}

func TestDispatchEnvelopeLeavesTagsAloneWhenScrubbingIsOff(t *testing.T) {
	tags := map[string]string{"token": "abc", "endpoint": telegramTagURL}

	_, item, _ := dispatchWithSettings(t, taggedEvent(tags), fixedSettings(ProjectSettings{}))

	if !maps.Equal(item.Tags, tags) {
		t.Fatalf("tags = %v, want %v", item.Tags, tags)
	}
}

func TestDispatchEnvelopeAppliesGlobalRulesToTagsWhenProjectScrubbingIsOff(t *testing.T) {
	tags := map[string]string{"token": "abc", "endpoint": telegramTagURL}
	settings := ProjectSettings{GlobalRules: []scrub.Rule{telegramTagRule}}

	_, item, _ := dispatchWithSettings(t, taggedEvent(tags), fixedSettings(settings))

	want := map[string]string{"token": "abc", "endpoint": "https://api.telegram.org/bot[Filtered]/sendMessage"}
	if !maps.Equal(item.Tags, want) {
		t.Fatalf("tags = %v, want %v", item.Tags, want)
	}
}
