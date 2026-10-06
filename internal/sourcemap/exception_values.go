package sourcemap

import (
	"bytes"
	"encoding/json"
)

// ExceptionValues decodes an event's exception interface in either wire shape:
// the {"values": [...]} object, or the bare array the Go SDK sends. Values keep
// Sentry's oldest-first order.
type ExceptionValues[T any] struct {
	Values []T
}

func (e *ExceptionValues[T]) UnmarshalJSON(data []byte) error {
	if trimmed := bytes.TrimSpace(data); len(trimmed) > 0 && trimmed[0] == '[' {
		return json.Unmarshal(trimmed, &e.Values)
	}
	var object struct {
		Values []T `json:"values"`
	}
	if err := json.Unmarshal(data, &object); err != nil {
		return err
	}
	e.Values = object.Values
	return nil
}
