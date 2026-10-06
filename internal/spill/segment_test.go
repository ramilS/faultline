package spill

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSegmentAppendAndRead(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "segment-0000000001.log")

	w, err := openSegmentFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	recs := []Record{
		{ProjectID: 1, ReceivedAt: time.Unix(0, 1).UTC(), Payload: []byte("a")},
		{ProjectID: 2, ReceivedAt: time.Unix(0, 2).UTC(), Payload: []byte("bb")},
	}
	for _, rec := range recs {
		if err := w.append(encodeRecord(rec)); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	if err := w.sync(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	if err := w.close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	var got []Record
	seg := Segment{Seq: 1, Path: path}
	if err := seg.Read(func(rec Record) error { got = append(got, rec); return nil }); err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 || string(got[0].Payload) != "a" || string(got[1].Payload) != "bb" {
		t.Fatalf("read mismatch: %+v", got)
	}
}

func TestSegmentReadStopsAtTornTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "segment-0000000001.log")
	frame := encodeRecord(Record{ProjectID: 1, ReceivedAt: time.Unix(0, 1).UTC(), Payload: []byte("ok")})
	// One good record followed by a truncated frame.
	if err := os.WriteFile(path, append(frame, frame[:3]...), 0o644); err != nil {
		t.Fatal(err)
	}

	count := 0
	seg := Segment{Seq: 1, Path: path}
	if err := seg.Read(func(Record) error { count++; return nil }); err != nil {
		t.Fatalf("read: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 intact record before torn tail, got %d", count)
	}
}
