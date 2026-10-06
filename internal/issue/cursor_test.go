package issue

import (
	"errors"
	"testing"
)

func TestEncodeDecodeCursorRoundTrip(t *testing.T) {
	cases := []Cursor{
		{Sort: SortLastSeen, Value: "2026-07-16T10:00:00.5Z", ID: 42},
		{Sort: SortTimesSeen, Value: "1000", ID: 7},
		{Sort: SortFirstSeen, Value: "2026-01-02T03:04:05Z", ID: 1},
		{Sort: SortUsersSeen, Value: "250", ID: 9},
	}
	for _, want := range cases {
		token := EncodeCursor(want)
		if token == "" {
			t.Fatalf("EncodeCursor(%+v) returned empty token", want)
		}
		got, err := DecodeCursor(token)
		if err != nil {
			t.Fatalf("DecodeCursor(%q): %v", token, err)
		}
		if got != want {
			t.Fatalf("round trip = %+v, want %+v", got, want)
		}
	}
}

func TestDecodeCursorRejectsGarbage(t *testing.T) {
	for _, token := range []string{"not-base64!!", "", "###"} {
		if _, err := DecodeCursor(token); err == nil {
			t.Fatalf("DecodeCursor(%q) = nil error, want error", token)
		}
	}
}

// TestCursorRequireSortRejectsMismatch pins the self-describing contract: a
// cursor produced under one sort must be rejected when a query executes under
// a different sort, not silently reinterpreted under the wrong column. This
// matters most for times_seen/users_seen, which share the same bigint value
// shape — a type-based parse of Value alone cannot tell them apart.
func TestCursorRequireSortRejectsMismatch(t *testing.T) {
	cursor := Cursor{Sort: SortTimesSeen, Value: "1000", ID: 7}

	if err := cursor.RequireSort(SortTimesSeen); err != nil {
		t.Fatalf("RequireSort(matching sort) = %v, want nil", err)
	}

	for _, mismatched := range []Sort{SortUsersSeen, SortLastSeen, SortFirstSeen} {
		err := cursor.RequireSort(mismatched)
		if !errors.Is(err, ErrCursorSortMismatch) {
			t.Fatalf("RequireSort(%q) = %v, want ErrCursorSortMismatch", mismatched, err)
		}
	}
}
