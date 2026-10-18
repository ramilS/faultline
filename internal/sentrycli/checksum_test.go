package sentrycli_test

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/sentrycli"
)

func TestAssembleReassemblesInDeclaredOrderNotArrivalOrder(t *testing.T) {
	first := []byte("hello ")
	second := []byte("world")
	whole := append(append([]byte{}, first...), second...)

	bodies := map[sentrycli.Checksum][]byte{
		sentrycli.ComputeChecksum(second): second,
		sentrycli.ComputeChecksum(first):  first,
	}
	order := []sentrycli.Checksum{sentrycli.ComputeChecksum(first), sentrycli.ComputeChecksum(second)}

	assembled, err := sentrycli.Assemble(order, bodies, sentrycli.ComputeChecksum(whole))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	got, err := io.ReadAll(assembled.Reader())
	if err != nil || string(got) != "hello world" {
		t.Fatalf("assembled = %q, %v, want %q", got, err, "hello world")
	}
}

// The declared total checksum is the only thing proving the client and the
// server agree on the assembled bytes. Accepting a mismatch would store a
// corrupt bundle that fails much later, as an unparseable zip.
func TestAssembleRejectsATotalChecksumMismatch(t *testing.T) {
	body := []byte("payload")
	order := []sentrycli.Checksum{sentrycli.ComputeChecksum(body)}
	bodies := map[sentrycli.Checksum][]byte{order[0]: body}

	_, err := sentrycli.Assemble(order, bodies, sentrycli.ComputeChecksum([]byte("other")))
	if !errors.Is(err, sentrycli.ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrChecksumMismatch", err)
	}
}

// The assemble endpoint reports a missing chunk back to sentry-cli as
// missingChunks so the CLI re-uploads it; silently skipping or zero-filling it
// instead would ship a corrupt bundle without either side noticing.
func TestAssembleReportsAMissingChunk(t *testing.T) {
	order := []sentrycli.Checksum{sentrycli.ComputeChecksum([]byte("x"))}

	_, err := sentrycli.Assemble(order, map[sentrycli.Checksum][]byte{}, sentrycli.Checksum{})
	if !errors.Is(err, sentrycli.ErrMissingChunk) {
		t.Fatalf("error = %v, want ErrMissingChunk", err)
	}
	if errors.Is(err, sentrycli.ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrMissingChunk not ErrChecksumMismatch", err)
	}
}

func TestParseChecksumRejectsMalformedInput(t *testing.T) {
	for _, raw := range []string{"", "zz", strings.Repeat("a", 39), strings.Repeat("a", 41), strings.Repeat("g", 40)} {
		if _, err := sentrycli.ParseChecksum(raw); err == nil {
			t.Fatalf("ParseChecksum(%q) succeeded, want error", raw)
		}
	}
	valid := strings.Repeat("ab", 20)
	parsed, err := sentrycli.ParseChecksum(valid)
	if err != nil {
		t.Fatalf("ParseChecksum(%q): %v", valid, err)
	}
	if parsed.String() != valid {
		t.Fatalf("round trip = %q, want %q", parsed.String(), valid)
	}
}
