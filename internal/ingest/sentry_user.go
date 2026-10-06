package ingest

import (
	"encoding/json"
	"fmt"
)

// userKey names the four identity fields, in the precedence order the spec
// fixes: a stable id beats an email, an email beats a display name, and an IP
// address is a last resort used only when nothing else identifies the person.
const (
	userKeyID       = "id"
	userKeyEmail    = "email"
	userKeyUsername = "username"
	userKeyIP       = "ip"
)

// sentryUser is the envelope's user object. Sentry's field for the address is
// ip_address; the shorter "ip" is our own storage key, not a wire name.
type sentryUser struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
	IP       string `json:"ip_address"`

	// hasNoKeys marks the literal object {}, which SDKs such as Ruby's send
	// when no user is set. It is not the same as an object whose keys are all
	// present but empty.
	hasNoKeys bool
}

func (raw *sentryUser) UnmarshalJSON(data []byte) error {
	type wireUser sentryUser
	var decoded wireUser
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		return err
	}
	*raw = sentryUser(decoded)
	raw.hasNoKeys = len(keys) == 0
	return nil
}

// userField pairs a storage key with the wire value it was read from, ordered
// by precedence.
type userField struct {
	key   string
	value string
}

// fields lists the four identity candidates in precedence order. Built once
// and walked twice by identity(): one shared list means a field added or
// reordered here changes both the length check and the selection together,
// rather than risking the two drifting apart.
func (raw *sentryUser) fields() []userField {
	return []userField{
		{userKeyID, raw.ID},
		{userKeyEmail, raw.Email},
		{userKeyUsername, raw.Username},
		{userKeyIP, raw.IP},
	}
}

// identity reduces the object to the one key/value pair that counts. It
// returns a nil user for an object that is absent or has no keys, and an error
// for one that is present but carries nothing usable — an SDK sending that has
// a bug, and silently dropping it would hide the bug.
//
// This deliberately walks the fields twice: once to length-check all four
// before any is chosen, and again to pick the first non-empty one. A single
// pass that returned on the first non-empty field would let a valid id skip
// the check on an oversized ip_address sitting right next to it.
func (raw *sentryUser) identity() (*EventUser, error) {
	if raw == nil || raw.hasNoKeys {
		return nil, nil
	}
	fields := raw.fields()
	for _, field := range fields {
		if len(field.value) > MaxUserFieldBytes {
			return nil, fmt.Errorf("%w: user.%s longer than %d bytes", ErrInvalidEnvelope, field.key, MaxUserFieldBytes)
		}
	}
	for _, field := range fields {
		if field.value != "" {
			return &EventUser{Key: field.key, Value: field.value}, nil
		}
	}
	return nil, fmt.Errorf("%w: user object carries no identity", ErrInvalidEnvelope)
}
