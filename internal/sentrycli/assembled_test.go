package sentrycli_test

import (
	"errors"
	"io"
	"testing"

	"github.com/ramilS/faultline/internal/sentrycli"
)

func assembledOf(t *testing.T, chunks ...string) *sentrycli.Assembled {
	t.Helper()
	bodies := map[sentrycli.Checksum][]byte{}
	order := make([]sentrycli.Checksum, 0, len(chunks))
	whole := []byte{}
	for _, chunk := range chunks {
		checksum := sentrycli.ComputeChecksum([]byte(chunk))
		bodies[checksum] = []byte(chunk)
		order = append(order, checksum)
		whole = append(whole, chunk...)
	}
	assembled, err := sentrycli.Assemble(order, bodies, sentrycli.ComputeChecksum(whole))
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return assembled
}

func TestAssembledReadsAcrossChunkBoundaries(t *testing.T) {
	assembled := assembledOf(t, "hello ", "", "wide ", "world")
	for _, test := range []struct {
		name    string
		offset  int64
		length  int
		want    string
		wantErr error
	}{
		{"within the first chunk", 1, 3, "ell", nil},
		{"across an empty chunk", 4, 5, "o wid", nil},
		{"spanning three chunks", 0, 16, "hello wide world", nil},
		{"short at the end", 12, 10, "orld", io.EOF},
		{"exactly at the end", 16, 1, "", io.EOF},
		{"past the end", 40, 1, "", io.EOF},
	} {
		t.Run(test.name, func(t *testing.T) {
			buffer := make([]byte, test.length)
			read, err := assembled.ReadAt(buffer, test.offset)
			if got := string(buffer[:read]); got != test.want || !errors.Is(err, test.wantErr) {
				t.Fatalf("ReadAt(%d, %d) = %q, %v; want %q, %v", test.length, test.offset, got, err, test.want, test.wantErr)
			}
		})
	}
	if assembled.Size() != 16 {
		t.Fatalf("Size() = %d, want 16", assembled.Size())
	}
	whole, err := io.ReadAll(assembled.Reader())
	if err != nil || string(whole) != "hello wide world" {
		t.Fatalf("Reader() read %q, %v", whole, err)
	}
}

func TestAssembledRejectsANegativeOffset(t *testing.T) {
	if _, err := assembledOf(t, "x").ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("ReadAt(-1) succeeded, want an error")
	}
}
