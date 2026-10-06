package ingest

import (
	"bytes"
	"compress/gzip"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDecodeSentryEnvelopeReadsHeaderAndItems(t *testing.T) {
	body := "{\"event_id\":\"9ec79c33ec9942ab8353589fcb2e04dc\"}\n" +
		"{\"type\":\"event\",\"length\":16}\n" +
		"{\"message\":\"hi\"}\n"

	envelope, captured, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if envelope.EventID != "9ec79c33ec9942ab8353589fcb2e04dc" {
		t.Fatalf("got event id %q", envelope.EventID)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(envelope.Items))
	}
	if envelope.Items[0].Type != "event" {
		t.Fatalf("got item type %q, want event", envelope.Items[0].Type)
	}
	if string(envelope.Items[0].Payload) != `{"message":"hi"}` {
		t.Fatalf("got payload %q", envelope.Items[0].Payload)
	}
	if string(captured) != body {
		t.Fatalf("captured bytes do not round-trip the request body")
	}
}

func TestDecodeSentryEnvelopeReadsItemWithoutDeclaredLength(t *testing.T) {
	body := "{}\n{\"type\":\"event\"}\n{\"message\":\"hi\"}\n"

	envelope, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Items) != 1 || string(envelope.Items[0].Payload) != `{"message":"hi"}` {
		t.Fatalf("got items %+v", envelope.Items)
	}
}

func TestDecodeSentryEnvelopeAcceptsMissingTrailingNewline(t *testing.T) {
	body := "{}\n{\"type\":\"event\",\"length\":16}\n{\"message\":\"hi\"}"

	envelope, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Items) != 1 || string(envelope.Items[0].Payload) != `{"message":"hi"}` {
		t.Fatalf("got items %+v", envelope.Items)
	}
}

func TestDecodeSentryEnvelopeKeepsUnknownItemTypes(t *testing.T) {
	body := "{}\n" +
		"{\"type\":\"session\"}\n{\"sid\":\"x\"}\n" +
		"{\"type\":\"event\"}\n{\"message\":\"hi\"}\n"

	envelope, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if err != nil {
		t.Fatalf("an unknown item type must not fail the envelope: %v", err)
	}
	if len(envelope.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(envelope.Items))
	}
	if envelope.Items[0].Type != "session" || envelope.Items[1].Type != "event" {
		t.Fatalf("items lost their order or type: %+v", envelope.Items)
	}
}

func TestDecodeSentryEnvelopeAcceptsEmptyEnvelope(t *testing.T) {
	envelope, _, err := DecodeSentryEnvelope(strings.NewReader("{}\n"), RequestOptions{})

	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Items) != 0 {
		t.Fatalf("got %d items, want 0", len(envelope.Items))
	}
}

func TestDecodeSentryEnvelopeRejectsMalformedHeader(t *testing.T) {
	_, _, err := DecodeSentryEnvelope(strings.NewReader("not json\n"), RequestOptions{})

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope", err)
	}
}

func TestDecodeSentryEnvelopeRejectsDeclaredLengthExceedingRemainingBytes(t *testing.T) {
	body := "{}\n{\"type\":\"event\",\"length\":9000}\n{\"message\":\"hi\"}\n"

	_, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge (declared length exceeds available data)", err)
	}
}

func TestDecodeSentryEnvelopeRejectsItemHeaderWithoutType(t *testing.T) {
	_, _, err := DecodeSentryEnvelope(strings.NewReader("{}\n{\"length\":2}\nhi\n"), RequestOptions{})

	if !errors.Is(err, ErrInvalidEnvelope) {
		t.Fatalf("got %v, want ErrInvalidEnvelope", err)
	}
}

func TestDecodeSentryEnvelopeReadsGzip(t *testing.T) {
	body := "{}\n{\"type\":\"event\"}\n{\"message\":\"hi\"}\n"
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(body)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}

	envelope, captured, err := DecodeSentryEnvelope(&compressed, RequestOptions{Gzip: true})

	if err != nil {
		t.Fatalf("decode gzip: %v", err)
	}
	if len(envelope.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(envelope.Items))
	}
	if string(captured) != body {
		t.Fatalf("captured bytes must be the decompressed envelope")
	}
}

func TestDecodeSentryEnvelopeRejectsTooManyItems(t *testing.T) {
	var builder strings.Builder
	builder.WriteString("{}\n")
	for index := 0; index <= MaxEnvelopeItems; index++ {
		builder.WriteString("{\"type\":\"session\"}\n{}\n")
	}

	_, _, err := DecodeSentryEnvelope(strings.NewReader(builder.String()), RequestOptions{})

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestDecodeSentryEnvelopeRejectsOversizeBody(t *testing.T) {
	body := "{}\n{\"type\":\"event\"}\n" + strings.Repeat("x", MaxCompressedBytes+1) + "\n"

	_, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestDecodeSentryEnvelopeRejectsAnImpossiblyLargeDeclaredLength(t *testing.T) {
	// An item header declaring a length far larger than the envelope must be rejected
	// without allocating. This prevents a DoS where an attacker crashes the server
	// with an out-of-memory error on a single request.
	body := "{}\n{\"type\":\"event\",\"length\":1000000000}\n{\"message\":\"hi\"}\n"

	_, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("got %v, want ErrTooLarge", err)
	}
}

func TestDecodeSentryEnvelopeAcceptsZeroLengthItemAtBufferEnd(t *testing.T) {
	// The item header line has no trailing newline and consumes the rest of the buffer,
	// so offset already equals len(data) by the time readItemPayload runs; a declared
	// length of 0 then makes it return a bytesRead of 0. This pins the case the parser's
	// offset-advancing loop must tolerate without spinning.
	body := "{}\n{\"type\":\"event\",\"length\":0}"

	envelope, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Items) != 1 || len(envelope.Items[0].Payload) != 0 {
		t.Fatalf("got items %+v", envelope.Items)
	}
}

func TestDecodeSentryEnvelopeSkipsConsecutiveBlankLines(t *testing.T) {
	body := "{}\n\n\n{\"type\":\"event\"}\n{\"message\":\"hi\"}\n\n\n"

	envelope, _, err := DecodeSentryEnvelope(strings.NewReader(body), RequestOptions{})

	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Items) != 1 || string(envelope.Items[0].Payload) != `{"message":"hi"}` {
		t.Fatalf("got items %+v", envelope.Items)
	}
}

func TestEnvelopeFromSentryItemsUsesTheHeaderIDOnlyForASingleEvent(t *testing.T) {
	const headerID = "5d6a1c1a0a4f4b7b8f3e2d1c0b9a8f77"
	idless := SentryItem{Type: "event", Payload: []byte(`{"message":"boom"}`)}

	t.Run("one id-less event takes the header id", func(t *testing.T) {
		parsed, err := EnvelopeFromSentryItems(SentryEnvelope{EventID: headerID, Items: []SentryItem{idless}}, time.Unix(0, 0))
		if err != nil {
			t.Fatalf("EnvelopeFromSentryItems: %v", err)
		}
		if len(parsed.Events) != 1 || hexEventID(parsed.Events[0]) != headerID {
			t.Fatalf("events = %+v, want one with the header id", parsed.Events)
		}
	})

	t.Run("two id-less events are rejected rather than collapsed onto one id", func(t *testing.T) {
		_, err := EnvelopeFromSentryItems(SentryEnvelope{EventID: headerID, Items: []SentryItem{idless, idless}}, time.Unix(0, 0))
		if !errors.Is(err, ErrInvalidEnvelope) {
			t.Fatalf("err = %v, want ErrInvalidEnvelope", err)
		}
	})
}
