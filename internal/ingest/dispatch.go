package ingest

import (
	"context"
	"fmt"
	"time"

	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/storage"
)

// RuleLookup resolves a project's cached grouping rules, ordered by position.
// *grouping.RuleCache.Get satisfies this signature; a nil RuleLookup disables
// rule evaluation entirely (deterministic fingerprinting only, e.g. in tests
// that don't care about rules).
type RuleLookup func(ctx context.Context, projectID int64) ([]grouping.Rule, error)

// SettingsLookup resolves projectID's ingestion-time settings.
// *ProjectSettingsCache.Lookup satisfies this signature. DispatchEnvelope is
// the one function both the ingest handler's non-durable path and the
// durable feeder's replay path call, so applying the settings here — rather
// than in either caller — covers both configurations by construction instead
// of by remembering to apply them twice. (An earlier version of the
// store_user_ip gate lived in ServeSentry alone and silently never ran for
// durably-spilled envelopes, which is why it now lives here instead.)
type SettingsLookup func(ctx context.Context, projectID int64) (ProjectSettings, error)

// DispatchEnvelope dedups (authoritatively), fingerprints, and dispatches each
// event, then dispatches the envelope's spans. Shared by the flag-off handler
// and the durable feeder so both build identical pipeline items. The rule and
// settings lookups each run once for the whole envelope — every event in
// an envelope belongs to the same project by construction, so a per-event
// lookup would resolve identically each time while paying its cost (and, on
// an outage, its failed-query and reportError cost) up to MaxEvents times
// over. A rule lookup failure is reported once and never blocks ingestion —
// every event still fingerprints deterministically. A settings lookup
// failure is also reported once, and fails closed (see resolveSettings).
func DispatchEnvelope(ctx context.Context, env Envelope, projectID int64, receivedAt time.Time, dedup *Deduplicator, rules RuleLookup, settings SettingsLookup, dispatch Dispatcher, dispatchSpans SpanDispatcher, reportError func(error)) {
	resolvedRules, applyRules := resolveEnvelopeRules(ctx, projectID, rules, reportError)
	resolvedSettings := resolveSettings(ctx, projectID, settings, reportError)
	gateIPIdentity(env.Events, resolvedSettings.StoreUserIP)
	for _, event := range env.Events {
		if dedup.Seen(projectID, event.EventID) {
			continue
		}
		fingerprint, project, item := fingerprintItem(projectID, event, receivedAt, resolvedRules, applyRules)
		scrubItem(&item, resolvedSettings, reportError)
		dispatch(fingerprint, project, item)
	}
	dispatchSpanRows(projectID, env.Spans, resolvedSettings.scrubber, dispatchSpans, reportError)
}

// resolveEnvelopeRules looks up projectID's rules once for the envelope. A nil
// lookup (rule evaluation disabled) reports applyRules=false without calling
// anything. A lookup error is reported once here — not once per event — and
// also yields applyRules=false, so every event falls through to deterministic
// fingerprinting.
func resolveEnvelopeRules(ctx context.Context, projectID int64, rules RuleLookup, reportError func(error)) ([]grouping.Rule, bool) {
	if rules == nil {
		return nil, false
	}
	cached, err := rules(ctx, projectID)
	if err != nil {
		reportError(fmt.Errorf("grouping rule lookup: %w", err))
		return nil, false
	}
	return cached, true
}

// resolveSettings looks up projectID's settings once for the envelope,
// mirroring resolveEnvelopeRules's per-envelope-not-per-event hoist. A nil
// lookup and a lookup error both resolve to the closed defaults — not opted
// in to store_user_ip, default scrubbing on — not fail open: a transient
// failure must never risk aggregating an address the project has not
// consented to aggregate, or storing a secret the project would have
// scrubbed. Losing an identity only costs a count; aggregating one without
// consent is the exact harm the toggle exists to prevent. Settings whose stored
// rules do not compile are treated the same as a failed lookup.
func resolveSettings(ctx context.Context, projectID int64, lookup SettingsLookup, reportError func(error)) ProjectSettings {
	if lookup == nil {
		return closedSettings()
	}
	settings, err := lookup(ctx, projectID)
	if err == nil {
		settings, err = settings.withScrubber()
	}
	if err != nil {
		reportError(fmt.Errorf("project settings lookup: %w", err))
		return closedSettings()
	}
	return settings
}

// scrubItem redacts secrets from the text about to be stored: the event row's
// message and payload, the issue's title and culprit, which are derived from
// the message, and the tag values that feed the tag facets. The fingerprint and
// identity were derived from the unscrubbed event by the caller, so grouping is
// unaffected. A payload that cannot be scrubbed is stored as received and the
// failure reported: dropping the event would cost more than it protects.
func scrubItem(item *pipeline.Item, settings ProjectSettings, reportError func(error)) {
	item.Row.Message = settings.scrubber.String(item.Row.Message)
	item.Title = settings.scrubber.String(item.Title)
	item.Culprit = settings.scrubber.String(item.Culprit)
	item.Tags = scrubTags(item.Tags, settings.scrubber)
	if len(item.Row.Payload) == 0 {
		return
	}
	payload, err := settings.scrubber.Payload(item.Row.Payload, !settings.StoreUserIP)
	if err != nil {
		reportError(fmt.Errorf("scrub event payload: %w", err))
		return
	}
	item.Row.Payload = payload
}

// scrubTags returns a copy of tags with each value scrubbed, and replaced
// outright when its key is sensitive. The event's own map is left alone.
func scrubTags(tags map[string]string, scrubber *scrub.Scrubber) map[string]string {
	if tags == nil {
		return nil
	}
	scrubbed := make(map[string]string, len(tags))
	for key, value := range tags {
		if scrubber.RedactsKey(key) {
			scrubbed[key] = scrub.Filtered
			continue
		}
		scrubbed[key] = scrubber.String(value)
	}
	return scrubbed
}

// gateIPIdentity drops an ip-keyed identity from every event unless the
// project has opted into projects.store_user_ip. Only ip is gated: an id,
// email, or username identity comes from the SDK's own instrumentation, not
// from the network layer, so none of the three carries the same PII decision.
// The event itself is never rejected — losing an identity must not cost the
// event count, so this only ever clears a field, never an element.
//
// This gate only keeps the address out of the issue_users aggregate; keeping
// it out of events.payload is scrubItem's job, and only while scrubbing is on.
func gateIPIdentity(events []Event, storeUserIP bool) {
	if storeUserIP {
		return
	}
	for index := range events {
		if events[index].User != nil && events[index].User.Key == userKeyIP {
			events[index].User = nil
		}
	}
}

func fingerprintItem(projectID int64, event Event, receivedAt time.Time, rules []grouping.Rule, applyRules bool) (string, int64, pipeline.Item) {
	deterministic := grouping.Fingerprint(event.groupingInput())
	result := deterministic
	if applyRules {
		result = grouping.EvaluateRules(rules, event.groupingRuleInput(), deterministic)
	}
	release := ""
	if event.Release != nil {
		release = *event.Release
	}
	return result.Fingerprint, projectID, pipeline.Item{
		Row: storage.EventRow{
			ProjectID: projectID, EventID: event.EventID, Timestamp: event.Timestamp,
			ReceivedAt: receivedAt, Level: event.Level, Environment: event.Environment,
			Message: event.Message, Release: event.Release, RequestID: event.RequestID,
			Fingerprint: result.Fingerprint, Payload: event.Payload,
		},
		Title: result.Title, Culprit: result.Culprit, Tags: event.Tags, Release: release,
		User: itemUserIdentity(event.User),
	}
}

// itemUserIdentity adapts the ingest package's identity to the pipeline
// package's own type (see pipeline.UserIdentity for why it isn't shared
// directly), preserving nil for an event that carries no identity.
func itemUserIdentity(user *EventUser) *pipeline.UserIdentity {
	if user == nil {
		return nil
	}
	return &pipeline.UserIdentity{Key: user.Key, Value: user.Value}
}

// dispatchSpanRows converts spans to storage rows stamped with the project id.
// Spans are not deduplicated; they are bounded by MaxSpans at decode time. A
// span's name and attributes carry URLs, SQL and headers, so they are scrubbed
// like an event payload; the ip address is an event-user concept and is not
// touched here.
func dispatchSpanRows(projectID int64, spans []Span, scrubber *scrub.Scrubber, dispatchSpans SpanDispatcher, reportError func(error)) {
	if len(spans) == 0 {
		return
	}
	rows := make([]storage.SpanRow, len(spans))
	for index, span := range spans {
		attributes := span.Attributes
		if len(attributes) > 0 {
			scrubbed, err := scrubber.Payload(attributes, false)
			if err != nil {
				reportError(fmt.Errorf("scrub span attributes: %w", err))
			} else {
				attributes = scrubbed
			}
		}
		rows[index] = storage.SpanRow{
			ProjectID: projectID, RequestID: span.RequestID, SpanID: span.SpanID,
			ParentSpanID: span.ParentSpanID, Name: scrubber.String(span.Name), StartTime: span.StartTime,
			DurationMS: span.DurationMS, Status: span.Status, Attributes: attributes,
		}
	}
	dispatchSpans(projectID, rows)
}
