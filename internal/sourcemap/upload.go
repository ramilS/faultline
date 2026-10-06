package sourcemap

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"

	gosourcemap "github.com/go-sourcemap/sourcemap"
)

// DecodeUpload reads, size-bounds, validates, and compresses one source-map
// JSON object. Only an absent Content-Encoding or gzip are accepted.
func DecodeUpload(body io.Reader, contentEncoding string) (DecodedUpload, error) {
	if contentEncoding != "" && contentEncoding != "gzip" {
		return DecodedUpload{}, fmt.Errorf("%w: unsupported content encoding", ErrInvalidMap)
	}

	var source = body
	var compressed *cappedReader
	if contentEncoding == "gzip" {
		compressed = &cappedReader{reader: body, remaining: MaxCompressedBytes + 1}
		reader, err := gzip.NewReader(compressed)
		if err != nil {
			_, _ = io.Copy(io.Discard, compressed)
			if compressed.exceeded {
				return DecodedUpload{}, ErrTooLarge
			}
			return DecodedUpload{}, fmt.Errorf("%w: gzip: %w", ErrInvalidMap, err)
		}
		defer reader.Close()
		source = reader
	}

	decoded := &cappedReader{reader: source, remaining: MaxDecodedBytes + 1}
	data, err := io.ReadAll(decoded)
	if compressed != nil {
		_, _ = io.Copy(io.Discard, compressed)
	}
	if (compressed != nil && compressed.exceeded) || decoded.exceeded {
		return DecodedUpload{}, ErrTooLarge
	}
	if err != nil {
		return DecodedUpload{}, fmt.Errorf("%w: read: %w", ErrInvalidMap, err)
	}
	if err := validateMap(data); err != nil {
		return DecodedUpload{}, err
	}

	var stored bytes.Buffer
	writer := gzip.NewWriter(&stored)
	if _, err := writer.Write(data); err != nil {
		return DecodedUpload{}, fmt.Errorf("%w: compress: %w", ErrInvalidMap, err)
	}
	if err := writer.Close(); err != nil {
		return DecodedUpload{}, fmt.Errorf("%w: compress: %w", ErrInvalidMap, err)
	}

	return DecodedUpload{
		JSON:       data,
		Compressed: stored.Bytes(),
		SHA256:     sha256.Sum256(data),
	}, nil
}

func validateMap(data []byte) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("%w: parse failure", ErrInvalidMap)
		}
	}()

	decoder := json.NewDecoder(bytes.NewReader(data))
	var object map[string]json.RawMessage
	if err := decoder.Decode(&object); err != nil {
		return fmt.Errorf("%w: JSON: %w", ErrInvalidMap, err)
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("%w: trailing JSON", ErrInvalidMap)
		}
		return fmt.Errorf("%w: JSON: %w", ErrInvalidMap, err)
	}
	if _, err := gosourcemap.Parse("", data); err != nil {
		return fmt.Errorf("%w: parse: %w", ErrInvalidMap, err)
	}
	return nil
}

type cappedReader struct {
	reader    io.Reader
	remaining int
	exceeded  bool
}

func (r *cappedReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		r.exceeded = true
		return 0, io.EOF
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	n, err := r.reader.Read(p)
	r.remaining -= n
	if r.remaining == 0 {
		r.exceeded = true
	}
	return n, err
}
