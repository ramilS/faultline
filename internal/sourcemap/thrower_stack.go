package sourcemap

import (
	"bytes"
	"encoding/json"
)

// MaxThrowerFrames bounds the frames of a stack the resolver will render. Ingest
// rejects events over it, and the thread-stack fallback honours it too, so a
// stack ingest could not decode cannot make every issue view resolve an
// unbounded number of frames.
const MaxThrowerFrames = 200

// ThreadValues decodes an event's threads interface in either wire shape, the
// {"values": [...]} object or a bare array, and never fails: threads only
// supply a fallback stack, so a malformed interface must not reject or blank an
// event that is otherwise valid. S is the caller's stacktrace type.
type ThreadValues[S any] struct {
	Values []Thread[S]
}

type Thread[S any] struct {
	Crashed    bool `json:"crashed"`
	Stacktrace *S   `json:"stacktrace"`
}

func (t *ThreadValues[S]) UnmarshalJSON(data []byte) error {
	t.Values = decodeThreads[S](data)
	return nil
}

func decodeThreads[S any](data []byte) []Thread[S] {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) > 0 && trimmed[0] == '[' {
		var values []Thread[S]
		if err := json.Unmarshal(trimmed, &values); err != nil {
			return nil
		}
		return values
	}
	var object struct {
		Values []Thread[S] `json:"values"`
	}
	if err := json.Unmarshal(trimmed, &object); err != nil {
		return nil
	}
	return object.Values
}

// ThrowerStacktrace is the stack of the thread that raised the error, for an
// exception that carries none of its own (the Rust SDK sends it this way): the
// thread marked crashed, else the only thread that has a stack. With several
// candidates and no marker it returns nil, since a wrong stack would blame the
// wrong code. It is the one selection rule ingest and the resolvers share.
func (t *ThreadValues[S]) ThrowerStacktrace() *S {
	if t == nil {
		return nil
	}
	var stacked []*S
	for _, thread := range t.Values {
		if thread.Stacktrace == nil {
			continue
		}
		if thread.Crashed {
			return thread.Stacktrace
		}
		stacked = append(stacked, thread.Stacktrace)
	}
	if len(stacked) == 1 {
		return stacked[0]
	}
	return nil
}
