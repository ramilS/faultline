// Package spill implements an append-only, CRC-framed write-ahead log of
// accepted ingestion envelopes plus a reader for replay. It is used only when
// pre-ack durability is enabled; the default ingest path never touches it.
package spill

import (
	"bufio"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"time"
)

var errTorn = errors.New("spill: torn record")

// Record is one accepted envelope. Payload holds the decompressed envelope JSON.
type Record struct {
	ProjectID  int64
	ReceivedAt time.Time
	Payload    []byte
}

const recordHeaderBytes = 8 + 8 + 4 // projectID + receivedAtUnixNano + payloadLen

// encodeRecord frames a record as len(uint32) ‖ crc32(body) ‖ body.
func encodeRecord(rec Record) []byte {
	body := make([]byte, recordHeaderBytes+len(rec.Payload))
	binary.LittleEndian.PutUint64(body[0:8], uint64(rec.ProjectID))
	binary.LittleEndian.PutUint64(body[8:16], uint64(rec.ReceivedAt.UnixNano()))
	binary.LittleEndian.PutUint32(body[16:20], uint32(len(rec.Payload)))
	copy(body[20:], rec.Payload)

	frame := make([]byte, 8+len(body))
	binary.LittleEndian.PutUint32(frame[0:4], uint32(len(body)))
	binary.LittleEndian.PutUint32(frame[4:8], crc32.ChecksumIEEE(body))
	copy(frame[8:], body)
	return frame
}

// readRecord decodes the next framed record. It returns io.EOF at a clean end
// of stream and errTorn for a short or CRC-mismatched trailing record, which a
// reader treats as the clean end of a crash-truncated segment.
func readRecord(r *bufio.Reader) (Record, error) {
	var prefix [8]byte
	if _, err := io.ReadFull(r, prefix[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return Record{}, io.EOF
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Record{}, errTorn
		}
		return Record{}, err
	}
	bodyLen := binary.LittleEndian.Uint32(prefix[0:4])
	wantCRC := binary.LittleEndian.Uint32(prefix[4:8])

	body := make([]byte, bodyLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return Record{}, errTorn
	}
	if crc32.ChecksumIEEE(body) != wantCRC || len(body) < recordHeaderBytes {
		return Record{}, errTorn
	}
	payloadLen := binary.LittleEndian.Uint32(body[16:20])
	if int(payloadLen) != len(body)-recordHeaderBytes {
		return Record{}, errTorn
	}
	return Record{
		ProjectID:  int64(binary.LittleEndian.Uint64(body[0:8])),
		ReceivedAt: time.Unix(0, int64(binary.LittleEndian.Uint64(body[8:16]))).UTC(),
		Payload:    body[20:],
	}, nil
}
