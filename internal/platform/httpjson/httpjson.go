// Package httpjson writes JSON HTTP responses in the one order that works.
package httpjson

import (
	"encoding/json"
	"net/http"
)

// Write sends payload as a JSON body with the given status.
//
// The ordering is the reason this exists. Header().Set after WriteHeader is
// silently ignored: the client receives the body with no Content-Type, no
// error surfaces anywhere, and the handler looks correct. Spelling the three
// steps out by hand at each call site makes that mistake expressible; routing
// through one function does not.
//
// Encode's error is discarded because there is nowhere to report it -- the
// status line and headers are already on the wire by the time encoding can
// fail, so the only honest alternatives are a truncated body or a panic.
// Callers pass concrete structs, whose encoding cannot fail; the signature
// cannot enforce that, so this discard is an accepted risk rather than a
// proof.
func Write(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
