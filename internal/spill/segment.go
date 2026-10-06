package spill

import (
	"bufio"
	"errors"
	"io"
	"os"
)

// Segment is a sealed (immutable) log file consumed by the feeder.
type Segment struct {
	Seq  uint64
	Path string
}

// Read streams every intact record in the segment. A torn trailing record
// (crash mid-append) ends the read cleanly, so a crash-truncated segment
// replays every record written before the crash.
func (seg Segment) Read(fn func(Record) error) error {
	file, err := os.Open(seg.Path)
	if err != nil {
		return err
	}
	// Read-only handle: a Close error reports nothing the reads above did not.
	defer func() { _ = file.Close() }()

	reader := bufio.NewReader(file)
	for {
		rec, err := readRecord(reader)
		if errors.Is(err, io.EOF) || errors.Is(err, errTorn) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := fn(rec); err != nil {
			return err
		}
	}
}

// segmentFile is the append-only writer for the active segment.
type segmentFile struct {
	file *os.File
	size int64
}

func openSegmentFile(path string) (*segmentFile, error) {
	//nolint:gosec // G304: path is the store's own directory joined with an
	// internally generated segment name, never caller-supplied.
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return &segmentFile{file: file, size: info.Size()}, nil
}

// append writes one framed record, growing the segment's tracked size by
// what was actually written. It does not fsync; durability is provided by
// the store's group-commit fsync.
func (w *segmentFile) append(frame []byte) error {
	n, err := w.file.Write(frame)
	w.size += int64(n)
	return err
}

func (w *segmentFile) sync() error  { return w.file.Sync() }
func (w *segmentFile) close() error { return w.file.Close() }
