package sourcemap

import (
	"bytes"
	"encoding/json"
)

// FrameVars keeps a payload frame's vars only when they are a non-empty JSON
// object, the shape Sentry SDKs send. The payload is untrusted, so any other
// shape is dropped rather than failing the frame.
func FrameVars(raw json.RawMessage) json.RawMessage {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(trimmed, &fields) != nil || len(fields) == 0 {
		return nil
	}
	return trimmed
}
