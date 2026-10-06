package ingest

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
)

// MaxEnvelopeItems bounds the item loop. A real envelope carries one to three
// items; the cap exists so a malformed stream cannot spin allocating headers.
const MaxEnvelopeItems = 200

// SentryItem is one envelope item: its declared type and its payload bytes,
// preserved verbatim so an event can be stored exactly as the SDK sent it.
type SentryItem struct {
	Type    string
	Payload []byte
}

// SentryEnvelope is a parsed Sentry envelope. EventID comes from the envelope
// header and is echoed in the ingest response.
type SentryEnvelope struct {
	EventID string
	Items   []SentryItem
}

// DecodeSentryEnvelope reads a newline-delimited Sentry envelope, enforcing the
// compressed and decompressed size caps and the item-count cap. It returns the
// parsed envelope alongside the decompressed bytes, which the durable path
// spills so the feeder can re-parse them on replay.
func DecodeSentryEnvelope(body io.Reader, options RequestOptions) (SentryEnvelope, []byte, error) {
	compressed := &cappedReader{reader: body, remaining: MaxCompressedBytes + 1}
	var source io.Reader = compressed
	if options.Gzip {
		reader, err := gzip.NewReader(compressed)
		if err != nil {
			if compressed.exceeded {
				return SentryEnvelope{}, nil, ErrTooLarge
			}
			return SentryEnvelope{}, nil, fmt.Errorf("%w: gzip: %w", ErrInvalidEnvelope, err)
		}
		defer reader.Close()
		source = reader
	}
	decompressed := &cappedReader{reader: source, remaining: MaxDecompressedBytes + 1}

	buffer, err := io.ReadAll(decompressed)
	if compressed.exceeded || decompressed.exceeded {
		return SentryEnvelope{}, nil, ErrTooLarge
	}
	if err != nil {
		return SentryEnvelope{}, nil, wrapDecodeError(err)
	}

	envelope, err := parseSentryEnvelope(buffer)
	if err != nil {
		return SentryEnvelope{}, nil, err
	}
	return envelope, buffer, nil
}

type sentryItemHeader struct {
	Type   string `json:"type"`
	Length *int   `json:"length"`
}

func parseSentryEnvelope(data []byte) (SentryEnvelope, error) {
	offset := 0

	headerLine, newOffset, err := readLine(data, offset)
	if err != nil {
		return SentryEnvelope{}, fmt.Errorf("%w: missing envelope header", ErrInvalidEnvelope)
	}
	offset = newOffset

	var header struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal(headerLine, &header); err != nil {
		return SentryEnvelope{}, fmt.Errorf("%w: envelope header: %w", ErrInvalidEnvelope, err)
	}

	envelope := SentryEnvelope{EventID: header.EventID, Items: []SentryItem{}}
	for offset < len(data) {
		itemHeaderLine, newOffset, err := readLine(data, offset)
		if err != nil {
			return SentryEnvelope{}, wrapDecodeError(err)
		}
		offset = newOffset

		if len(bytes.TrimSpace(itemHeaderLine)) == 0 {
			continue
		}
		if len(envelope.Items) >= MaxEnvelopeItems {
			return SentryEnvelope{}, fmt.Errorf("%w: more than %d envelope items", ErrTooLarge, MaxEnvelopeItems)
		}

		var itemHeader sentryItemHeader
		if err := json.Unmarshal(itemHeaderLine, &itemHeader); err != nil {
			return SentryEnvelope{}, fmt.Errorf("%w: item header: %w", ErrInvalidEnvelope, err)
		}
		if itemHeader.Type == "" {
			return SentryEnvelope{}, fmt.Errorf("%w: item header without type", ErrInvalidEnvelope)
		}

		remaining := len(data) - offset
		payload, bytesRead, err := readItemPayload(data, offset, itemHeader.Length, remaining)
		if err != nil {
			return SentryEnvelope{}, err
		}
		offset += bytesRead
		envelope.Items = append(envelope.Items, SentryItem{Type: itemHeader.Type, Payload: payload})
	}

	return envelope, nil
}

// readLine returns the next line without its terminator, and the new offset after the line.
// A final line with no trailing newline is returned as content. An error occurs only if
// no data remains at the offset.
func readLine(data []byte, offset int) ([]byte, int, error) {
	if offset >= len(data) {
		return nil, offset, io.EOF
	}
	idx := bytes.IndexByte(data[offset:], '\n')
	if idx == -1 {
		return data[offset:], len(data), nil
	}
	line := data[offset : offset+idx]
	return line, offset + idx + 1, nil
}

// readItemPayload reads one item body from data starting at offset. A declared length is
// authoritative and the newline after it is optional; without a length the payload runs to
// the next newline. Both forms occur in the wild, so supporting only the declared form
// would break SDKs that omit it. The remaining parameter bounds the declared length against
// what is actually left in the buffer, so a crafted header cannot trigger an allocation
// larger than the request itself (the prior implementation's make([]byte, *length) could be
// driven straight from client JSON, and out of memory is unrecoverable).
//
// The returned payload aliases data rather than copying it: readItemPayload alone never
// retains more than DecodeSentryEnvelope's caller already holds for the request's duration.
// But callers that keep an item's payload past the request (MapSentryEvent's Event.Payload,
// stored on pipeline.Item and queued on a per-shard channel) must copy it themselves — otherwise
// a payload a few hundred bytes long keeps the entire decompressed buffer (up to 20 MiB) alive
// for as long as that payload is reachable, not just for the request.
func readItemPayload(data []byte, offset int, length *int, remaining int) ([]byte, int, error) {
	if length == nil {
		idx := bytes.IndexByte(data[offset:], '\n')
		if idx == -1 {
			return data[offset:], remaining, nil
		}
		return data[offset : offset+idx], idx + 1, nil
	}

	if *length < 0 {
		return nil, 0, fmt.Errorf("%w: negative item length", ErrInvalidEnvelope)
	}
	if *length > remaining {
		return nil, 0, fmt.Errorf("%w: item declared length %d exceeds remaining %d bytes", ErrTooLarge, *length, remaining)
	}

	payload := data[offset : offset+*length]
	bytesRead := *length
	if offset+*length < len(data) && data[offset+*length] == '\n' {
		bytesRead++
	}

	return payload, bytesRead, nil
}
