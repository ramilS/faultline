package storage

import (
	"encoding/json"
	"testing"
	"time"
)

func TestBatchMergeIssueDeltaAccumulatesCountsAndSeenBounds(t *testing.T) {
	early := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	middle := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	late := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)

	base := IssueDelta{
		Fingerprint: "fp",
		Title:       "boom",
		Culprit:     "svc.total",
		Level:       "error",
		Count:       2,
		FirstSeen:   middle,
		LastSeen:    middle,
		Tags: []TagDelta{
			{Key: "service", Value: "billing", Count: 2, FirstSeen: middle, LastSeen: middle},
		},
	}
	incoming := IssueDelta{
		Fingerprint: "fp",
		Count:       3,
		FirstSeen:   early,
		LastSeen:    late,
		Tags: []TagDelta{
			{Key: "service", Value: "billing", Count: 1, FirstSeen: early, LastSeen: late},
			{Key: "tenant", Value: "acme", Count: 3, FirstSeen: early, LastSeen: late},
		},
	}

	merged := MergeIssueDelta(base, incoming)

	if merged.Count != 5 {
		t.Fatalf("count = %d, want 5", merged.Count)
	}
	if !merged.FirstSeen.Equal(early) {
		t.Fatalf("first_seen = %v, want %v", merged.FirstSeen, early)
	}
	if !merged.LastSeen.Equal(late) {
		t.Fatalf("last_seen = %v, want %v", merged.LastSeen, late)
	}
	if merged.Title != "boom" || merged.Culprit != "svc.total" || merged.Level != "error" {
		t.Fatalf("identity fields drifted: %+v", merged)
	}
	if len(merged.Tags) != 2 {
		t.Fatalf("tags = %d, want 2", len(merged.Tags))
	}
	service := findTag(t, merged.Tags, "service", "billing")
	if service.Count != 3 {
		t.Fatalf("service tag count = %d, want 3", service.Count)
	}
	if !service.FirstSeen.Equal(early) || !service.LastSeen.Equal(late) {
		t.Fatalf("service tag seen bounds = %v/%v", service.FirstSeen, service.LastSeen)
	}
	tenant := findTag(t, merged.Tags, "tenant", "acme")
	if tenant.Count != 3 {
		t.Fatalf("tenant tag count = %d, want 3", tenant.Count)
	}
}

func TestBatchMergeIssueDeltaAccumulatesUserIdentities(t *testing.T) {
	early := time.Date(2026, 7, 15, 9, 0, 0, 0, time.UTC)
	late := time.Date(2026, 7, 15, 11, 0, 0, 0, time.UTC)

	base := IssueDelta{
		Fingerprint: "fp",
		Count:       1,
		FirstSeen:   early,
		LastSeen:    early,
		Users: []UserDelta{
			{Key: "email", Value: "a@example.com", Count: 1, FirstSeen: early, LastSeen: early},
		},
	}
	incoming := IssueDelta{
		Fingerprint: "fp",
		Count:       2,
		FirstSeen:   early,
		LastSeen:    late,
		Users: []UserDelta{
			{Key: "email", Value: "a@example.com", Count: 1, FirstSeen: late, LastSeen: late},
			{Key: "id", Value: "a@example.com", Count: 1, FirstSeen: late, LastSeen: late},
		},
	}

	merged := MergeIssueDelta(base, incoming)

	if len(merged.Users) != 2 {
		t.Fatalf("users = %+v, want 2 (same value under a different key is a distinct identity)", merged.Users)
	}
	byEmail := findUser(t, merged.Users, "email", "a@example.com")
	if byEmail.Count != 2 {
		t.Fatalf("email identity times_seen = %d, want 2", byEmail.Count)
	}
	if !byEmail.FirstSeen.Equal(early) || !byEmail.LastSeen.Equal(late) {
		t.Fatalf("email identity seen bounds = %v/%v, want %v/%v", byEmail.FirstSeen, byEmail.LastSeen, early, late)
	}
	byID := findUser(t, merged.Users, "id", "a@example.com")
	if byID.Count != 1 {
		t.Fatalf("id identity times_seen = %d, want 1", byID.Count)
	}
}

func TestBatchMergeIssueDeltaDoesNotMutateInputs(t *testing.T) {
	seen := time.Date(2026, 7, 15, 10, 0, 0, 0, time.UTC)
	base := IssueDelta{
		Fingerprint: "fp",
		Count:       1,
		FirstSeen:   seen,
		LastSeen:    seen,
		Tags:        []TagDelta{{Key: "service", Value: "billing", Count: 1, FirstSeen: seen, LastSeen: seen}},
	}
	incoming := IssueDelta{
		Fingerprint: "fp",
		Count:       1,
		FirstSeen:   seen,
		LastSeen:    seen,
		Tags:        []TagDelta{{Key: "service", Value: "billing", Count: 1, FirstSeen: seen, LastSeen: seen}},
	}

	MergeIssueDelta(base, incoming)

	if base.Count != 1 || base.Tags[0].Count != 1 {
		t.Fatalf("base mutated: %+v", base)
	}
	if incoming.Count != 1 || incoming.Tags[0].Count != 1 {
		t.Fatalf("incoming mutated: %+v", incoming)
	}
}

func findTag(t *testing.T, tags []TagDelta, key, value string) TagDelta {
	t.Helper()
	for _, tag := range tags {
		if tag.Key == key && tag.Value == value {
			return tag
		}
	}
	t.Fatalf("tag %s=%s not found in %+v", key, value, tags)
	return TagDelta{}
}

func findUser(t *testing.T, users []UserDelta, key, value string) UserDelta {
	t.Helper()
	for _, user := range users {
		if user.Key == key && user.Value == value {
			return user
		}
	}
	t.Fatalf("user %s=%s not found in %+v", key, value, users)
	return UserDelta{}
}

func TestBatchCarriesSpans(t *testing.T) {
	parent := "0011223344556677"
	batch := Batch{
		ProjectID: 7,
		Spans: []SpanRow{{
			ProjectID:    7,
			RequestID:    "req-42",
			SpanID:       "a1b2c3d4e5f60718",
			ParentSpanID: &parent,
			Name:         "GET /invoice",
			StartTime:    time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC),
			DurationMS:   12.5,
			Status:       "ok",
			Attributes:   json.RawMessage(`{"http.method":"GET"}`),
		}},
	}
	if len(batch.Spans) != 1 || batch.Spans[0].SpanID != "a1b2c3d4e5f60718" {
		t.Fatalf("span not carried: %+v", batch.Spans)
	}
	if batch.Spans[0].ParentSpanID == nil || *batch.Spans[0].ParentSpanID != parent {
		t.Fatalf("parent span id = %v", batch.Spans[0].ParentSpanID)
	}
}
