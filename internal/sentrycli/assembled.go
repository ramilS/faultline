package sentrycli

import (
	//nolint:gosec // G505: see the package comment on SHA-1 in checksum.go
	"crypto/sha1"
	"fmt"
	"io"
	"sort"
)

// Assembled is a file reassembled from staged chunks in the client's declared
// order without copying them into one buffer. Debug files reach hundreds of
// megabytes, and a concatenated copy next to the staged chunks is what once
// OOM-killed the server on a 90 MB mapping.
type Assembled struct {
	parts   [][]byte
	offsets []int64
	size    int64
}

var _ io.ReaderAt = (*Assembled)(nil)

// Assemble orders bodies as the assemble request names them and verifies the
// whole against the checksum the client declared for it.
func Assemble(order []Checksum, bodies map[Checksum][]byte, want Checksum) (*Assembled, error) {
	assembled := &Assembled{parts: make([][]byte, 0, len(order)), offsets: make([]int64, 0, len(order))}
	//nolint:gosec // G401: see the package comment on SHA-1 in checksum.go
	digest := sha1.New()
	for _, checksum := range order {
		body, ok := bodies[checksum]
		if !ok {
			return nil, fmt.Errorf("%w: %s", ErrMissingChunk, checksum)
		}
		assembled.parts = append(assembled.parts, body)
		assembled.offsets = append(assembled.offsets, assembled.size)
		assembled.size += int64(len(body))
		digest.Write(body)
	}
	var got Checksum
	digest.Sum(got[:0])
	if got != want {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrChecksumMismatch, got, want)
	}
	return assembled, nil
}

// Size is the length of the whole file.
func (a *Assembled) Size() int64 {
	return a.size
}

// Reader reads the whole file from its start.
func (a *Assembled) Reader() io.Reader {
	return io.NewSectionReader(a, 0, a.size)
}

// ReadAt reads across chunk boundaries as if the chunks were one buffer.
func (a *Assembled) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, fmt.Errorf("assembled file: negative offset %d", off)
	}
	if off >= a.size {
		return 0, io.EOF
	}
	part := sort.Search(len(a.offsets), func(i int) bool { return a.offsets[i] > off }) - 1
	read := 0
	for ; part >= 0 && part < len(a.parts) && read < len(p); part++ {
		within := off + int64(read) - a.offsets[part]
		read += copy(p[read:], a.parts[part][within:])
	}
	if read < len(p) {
		return read, io.EOF
	}
	return read, nil
}
