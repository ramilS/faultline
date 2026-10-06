package spill

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestRecordRoundTrip(t *testing.T) {
	rec := Record{ProjectID: 42, ReceivedAt: time.Unix(0, 1_700_000_000_000_000_000).UTC(), Payload: []byte(`{"events":[]}`)}

	var buf bytes.Buffer
	buf.Write(encodeRecord(rec))

	got, err := readRecord(bufio.NewReader(&buf))
	if err != nil {
		t.Fatalf("readRecord: %v", err)
	}
	if got.ProjectID != rec.ProjectID || !got.ReceivedAt.Equal(rec.ReceivedAt) || !bytes.Equal(got.Payload, rec.Payload) {
		t.Fatalf("round-trip mismatch: got %+v want %+v", got, rec)
	}
}

func TestReadRecordCleanEOF(t *testing.T) {
	_, err := readRecord(bufio.NewReader(bytes.NewReader(nil)))
	if !errors.Is(err, io.EOF) {
		t.Fatalf("empty reader: got %v want io.EOF", err)
	}
}

func TestReadRecordTornTail(t *testing.T) {
	frame := encodeRecord(Record{ProjectID: 1, ReceivedAt: time.Unix(0, 0).UTC(), Payload: []byte("abc")})
	// Truncate mid-body to simulate a crash during append.
	torn := frame[:len(frame)-2]

	_, err := readRecord(bufio.NewReader(bytes.NewReader(torn)))
	if !errors.Is(err, errTorn) {
		t.Fatalf("truncated frame: got %v want errTorn", err)
	}
}

func TestReadRecordCRCMismatch(t *testing.T) {
	frame := encodeRecord(Record{ProjectID: 1, ReceivedAt: time.Unix(0, 0).UTC(), Payload: []byte("abc")})
	frame[len(frame)-1] ^= 0xFF // corrupt the payload byte

	_, err := readRecord(bufio.NewReader(bytes.NewReader(frame)))
	if !errors.Is(err, errTorn) {
		t.Fatalf("corrupt frame: got %v want errTorn", err)
	}
}
