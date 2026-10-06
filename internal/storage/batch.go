package storage

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/google/uuid"
)

// EventRow is a single event ready to be COPYed into the events hypertable,
// already stamped with its computed fingerprint and byte-preserved payload.
type EventRow struct {
	ProjectID   int64
	EventID     uuid.UUID
	Timestamp   time.Time
	ReceivedAt  time.Time
	Level       string
	Environment string
	Message     string
	Release     *string
	RequestID   *string
	Fingerprint string
	Payload     json.RawMessage
}

// SpanRow is a single span ready to be COPYed into the spans hypertable. It is
// correlated to events by (ProjectID, RequestID); it carries no fingerprint and
// participates in no issue aggregation.
type SpanRow struct {
	ProjectID    int64
	RequestID    string
	SpanID       string
	ParentSpanID *string
	Name         string
	StartTime    time.Time
	DurationMS   float64
	Status       string
	Attributes   json.RawMessage
}

// TagDelta is an accumulated count of one (key, value) facet within a flush.
type TagDelta struct {
	Key       string
	Value     string
	Count     int64
	FirstSeen time.Time
	LastSeen  time.Time
}

// UserDelta is an accumulated count of one end-user identity — a
// (user_key, user_value) pair — contributed to an issue within a flush. It is
// absent entirely for an event whose identity was nil (no user, or an ip
// identity the project has not opted into aggregating); such an event still
// contributes to IssueDelta.Count and never to a UserDelta.
type UserDelta struct {
	Key       string
	Value     string
	Count     int64
	FirstSeen time.Time
	LastSeen  time.Time
}

// IssueDelta is the aggregated contribution to a single issue within a flush.
// Identity fields (fingerprint, title, culprit, level) are fixed by the
// fingerprint; Count and the seen bounds accumulate across merged events.
type IssueDelta struct {
	Fingerprint string
	Title       string
	Culprit     string
	Level       string
	Count       int64
	FirstSeen   time.Time
	LastSeen    time.Time
	Tags        []TagDelta
	Users       []UserDelta
}

// Batch is one shard's flush for a single project: releases to register,
// issue deltas to upsert, the event rows to COPY, and the span rows to COPY.
type Batch struct {
	ProjectID int64
	Releases  []string
	Issues    []IssueDelta
	Events    []EventRow
	Spans     []SpanRow
}

// MergeIssueDelta folds incoming into base and returns a new IssueDelta,
// mutating neither input. Counts add, FirstSeen keeps the earliest and
// LastSeen the latest, tag facets accumulate per (key, value), and user
// identities accumulate per (key, value) the same way.
func MergeIssueDelta(base, incoming IssueDelta) IssueDelta {
	merged := base
	merged.Count = base.Count + incoming.Count
	merged.FirstSeen = earliest(base.FirstSeen, incoming.FirstSeen)
	merged.LastSeen = latest(base.LastSeen, incoming.LastSeen)
	merged.Tags = mergeTagDeltas(base.Tags, incoming.Tags)
	merged.Users = mergeUserDeltas(base.Users, incoming.Users)
	return merged
}

// mergeTagDeltas accumulates incoming facets into a copy of base, preserving
// base's insertion order and appending previously unseen (key, value) pairs.
func mergeTagDeltas(base, incoming []TagDelta) []TagDelta {
	merged := slices.Clone(base)
	index := make(map[deltaKey]int, len(merged))
	for position, tag := range merged {
		index[deltaKey{tag.Key, tag.Value}] = position
	}
	for _, tag := range incoming {
		key := deltaKey{tag.Key, tag.Value}
		if position, ok := index[key]; ok {
			merged[position].Count += tag.Count
			merged[position].FirstSeen = earliest(merged[position].FirstSeen, tag.FirstSeen)
			merged[position].LastSeen = latest(merged[position].LastSeen, tag.LastSeen)
			continue
		}
		index[key] = len(merged)
		merged = append(merged, tag)
	}
	return merged
}

// mergeUserDeltas accumulates incoming user identities into a copy of base,
// preserving base's insertion order and appending previously unseen
// (key, value) pairs. Same shape as mergeTagDeltas, kept as a separate
// function because TagDelta and UserDelta are distinct types.
func mergeUserDeltas(base, incoming []UserDelta) []UserDelta {
	merged := slices.Clone(base)
	index := make(map[deltaKey]int, len(merged))
	for position, user := range merged {
		index[deltaKey{user.Key, user.Value}] = position
	}
	for _, user := range incoming {
		key := deltaKey{user.Key, user.Value}
		if position, ok := index[key]; ok {
			merged[position].Count += user.Count
			merged[position].FirstSeen = earliest(merged[position].FirstSeen, user.FirstSeen)
			merged[position].LastSeen = latest(merged[position].LastSeen, user.LastSeen)
			continue
		}
		index[key] = len(merged)
		merged = append(merged, user)
	}
	return merged
}

// deltaKey groups a TagDelta or UserDelta by its (key, value) pair when
// merging incoming facets/identities into an existing accumulation.
type deltaKey struct {
	key   string
	value string
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() {
		return b
	}
	if b.IsZero() || a.Before(b) {
		return a
	}
	return b
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}
