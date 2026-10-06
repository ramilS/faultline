package sentrycli

import (
	//nolint:gosec // G505: SHA-1 addresses chunks because sentry-cli fixes that
	// choice in its protocol; it is a content address here, not a signature.
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
)

var (
	ErrInvalidChecksum  = errors.New("invalid chunk checksum")
	ErrChecksumMismatch = errors.New("assembled checksum mismatch")
	ErrMissingChunk     = errors.New("missing chunk")
)

// Checksum is a chunk's SHA-1 content address. It is deliberately not the same
// type as a source map's SHA-256 digest: the two are different algorithms over
// different bytes, and a function taking one must not accept the other.
type Checksum [20]byte

// ComputeChecksum returns body's chunk checksum.
func ComputeChecksum(body []byte) Checksum {
	//nolint:gosec // G401: see the package comment on SHA-1
	return sha1.Sum(body)
}

// ParseChecksum decodes a hex-encoded chunk checksum, such as one named in an
// assemble request's chunks[] list.
func ParseChecksum(raw string) (Checksum, error) {
	if len(raw) != hex.EncodedLen(len(Checksum{})) {
		return Checksum{}, fmt.Errorf("%w: length %d", ErrInvalidChecksum, len(raw))
	}
	decoded, err := hex.DecodeString(raw)
	if err != nil {
		return Checksum{}, fmt.Errorf("%w: %w", ErrInvalidChecksum, err)
	}
	var checksum Checksum
	copy(checksum[:], decoded)
	return checksum, nil
}

// String returns c as lowercase hex, the form sentry-cli names chunks by.
func (c Checksum) String() string {
	return hex.EncodeToString(c[:])
}

// Concat reassembles a file from its chunks in the client's declared order and
// verifies the result against the checksum the client declared for the whole.
// Chunks arrive over several concurrent requests in no particular order; order
// is what the assemble request supplies, and bodies is what storage holds.
func Concat(order []Checksum, bodies map[Checksum][]byte, want Checksum) ([]byte, error) {
	total := 0
	for _, checksum := range order {
		body, ok := bodies[checksum]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingChunk, checksum)
		}
		total += len(body)
	}
	assembled := make([]byte, 0, total)
	for _, checksum := range order {
		assembled = append(assembled, bodies[checksum]...)
	}
	if got := ComputeChecksum(assembled); got != want {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrChecksumMismatch, got, want)
	}
	return assembled, nil
}
