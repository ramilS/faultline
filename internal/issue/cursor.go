package issue

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrCursorSortMismatch reports a cursor that was produced under one sort but
// is being applied to a query running under a different one. Two sorts can
// share the same boundary-value shape (times_seen and users_seen are both
// bigints; last_seen and first_seen are both RFC3339Nano timestamps), so a
// type-based parse of the cursor's Value alone cannot catch a swapped cursor
// — the recorded Sort itself must be checked. See Cursor.RequireSort.
var ErrCursorSortMismatch = errors.New("cursor sort mismatch")

// Cursor is the opaque keyset position of the issue list. It records the sort
// field it was produced under so the server can rebuild the exact predicate.
type Cursor struct {
	Sort  Sort   `json:"s"`
	Value string `json:"v"`
	ID    int64  `json:"id"`
}

// RequireSort reports ErrCursorSortMismatch if the cursor was not produced
// under sort. A caller executing a query under sort must reject a cursor
// recorded under a different one rather than reinterpreting its boundary
// value under the wrong column, which would silently skip or repeat issues.
//
// No HTTP request can trigger this today: applyCursor
// (internal/api/issues.go) sets filters.Sort = cursor.Sort before the
// service ever runs, so a mismatch never reaches here over the API. This
// guards direct ListFilters callers instead — tests today, and any future
// non-HTTP caller — which is why it stays even though current traffic never
// exercises the mismatch branch.
func (c Cursor) RequireSort(sort Sort) error {
	if c.Sort != sort {
		return fmt.Errorf("cursor sort %q, want %q: %w", c.Sort, sort, ErrCursorSortMismatch)
	}
	return nil
}

// EncodeCursor renders a Cursor as a URL-safe, opaque pagination token.
func EncodeCursor(cursor Cursor) string {
	encoded, err := json.Marshal(cursor)
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(encoded)
}

// DecodeCursor parses a token produced by EncodeCursor.
func DecodeCursor(token string) (Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return Cursor{}, fmt.Errorf("decode cursor: %w", err)
	}
	var cursor Cursor
	if err := json.Unmarshal(raw, &cursor); err != nil {
		return Cursor{}, fmt.Errorf("parse cursor: %w", err)
	}
	if cursor.Sort == "" || cursor.Value == "" || cursor.ID <= 0 {
		return Cursor{}, fmt.Errorf("incomplete cursor")
	}
	return cursor, nil
}
