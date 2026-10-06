package ingest_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/ingest"
)

func TestMapSentryEventChoosesTheIdentityKeyByPrecedence(t *testing.T) {
	cases := []struct {
		name      string
		payload   string
		wantKey   string
		wantValue string
	}{
		{"id wins over everything", `{"id":"u1","email":"a@example.com","username":"ann","ip_address":"203.0.113.1"}`, "id", "u1"},
		{"email wins without id", `{"email":"a@example.com","username":"ann","ip_address":"203.0.113.1"}`, "email", "a@example.com"},
		{"username wins without id or email", `{"username":"ann","ip_address":"203.0.113.1"}`, "username", "ann"},
		{"ip only when it is alone", `{"ip_address":"203.0.113.1"}`, "ip", "203.0.113.1"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","user":` + testCase.payload + `}`)
			event, err := ingest.MapSentryEvent(payload, time.Unix(0, 0).UTC())
			if err != nil {
				t.Fatalf("MapSentryEvent: %v", err)
			}
			if event.User == nil {
				t.Fatal("User = nil, want an identity")
			}
			if event.User.Key != testCase.wantKey || event.User.Value != testCase.wantValue {
				t.Fatalf("User = %+v, want {%s %s}", *event.User, testCase.wantKey, testCase.wantValue)
			}
		})
	}
}

func TestMapSentryEventRejectsAnAllEmptyUserObject(t *testing.T) {
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","user":{"id":"","email":"","username":"","ip_address":""}}`)
	if _, err := ingest.MapSentryEvent(payload, time.Unix(0, 0).UTC()); !errors.Is(err, ingest.ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
	}
}

func TestMapSentryEventTreatsAUserWithoutKeysAsNoUser(t *testing.T) {
	for _, user := range []string{`{}`, `{ }`, `null`} {
		t.Run(user, func(t *testing.T) {
			payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","user":` + user + `}`)
			event, err := ingest.MapSentryEvent(payload, time.Unix(0, 0).UTC())
			if err != nil {
				t.Fatalf("MapSentryEvent: %v — the Ruby SDK sends \"user\":{} on every event", err)
			}
			if event.User != nil {
				t.Fatalf("User = %+v, want nil", *event.User)
			}
		})
	}
}

func TestMapSentryEventWithoutAUserLeavesTheIdentityUnset(t *testing.T) {
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc"}`)
	event, err := ingest.MapSentryEvent(payload, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("MapSentryEvent: %v", err)
	}
	if event.User != nil {
		t.Fatalf("User = %+v, want nil so the event counts once and no user counts", *event.User)
	}
}

func TestMapSentryEventRejectsAnOverlongUserField(t *testing.T) {
	long := strings.Repeat("x", ingest.MaxUserFieldBytes+1)
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","user":{"id":"` + long + `"}}`)
	if _, err := ingest.MapSentryEvent(payload, time.Unix(0, 0).UTC()); !errors.Is(err, ingest.ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope — a truncated identity merges two people into one count", err)
	}
}

func TestMapSentryEventRejectsAnOverlongLowPrecedenceFieldEvenWhenIdWins(t *testing.T) {
	long := strings.Repeat("x", ingest.MaxUserFieldBytes+1)
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","user":{"id":"u1","ip_address":"` + long + `"}}`)
	if _, err := ingest.MapSentryEvent(payload, time.Unix(0, 0).UTC()); !errors.Is(err, ingest.ErrInvalidEnvelope) {
		t.Fatalf("err = %v, want ErrInvalidEnvelope — a valid id must not stop the ip_address length check", err)
	}
}
