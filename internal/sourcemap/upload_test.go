package sourcemap

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDecodeUpload(t *testing.T) {
	valid := readFixture(t, "app.js.map")

	tests := []struct {
		name     string
		body     []byte
		encoding string
		err      error
	}{
		{name: "plain JSON", body: valid},
		{name: "gzip JSON", body: gzipBytes(t, valid), encoding: "gzip"},
		{name: "unsupported encoding", body: valid, encoding: "br", err: ErrInvalidMap},
		{name: "bad gzip", body: []byte("not gzip"), encoding: "gzip", err: ErrInvalidMap},
		{name: "trailing JSON", body: append(append([]byte{}, valid...), []byte(` {}`)...), err: ErrInvalidMap},
		{name: "invalid map", body: []byte(`{"version":2,"sources":[],"names":[],"mappings":""}`), err: ErrInvalidMap},
		{name: "malformed indexed map", body: []byte(`{"version":3,"sections":[{}]}`), err: ErrInvalidMap},
		{name: "compressed limit", body: bytes.Repeat([]byte("x"), MaxCompressedBytes+1), encoding: "gzip", err: ErrTooLarge},
		{name: "decoded limit", body: gzipBytes(t, bytes.Repeat([]byte(" "), MaxDecodedBytes+1)), encoding: "gzip", err: ErrTooLarge},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := DecodeUpload(bytes.NewReader(test.body), test.encoding)
			if !errors.Is(err, test.err) {
				t.Fatalf("DecodeUpload error = %v, want %v", err, test.err)
			}
			if test.err != nil {
				return
			}
			if !bytes.Equal(got.JSON, valid) {
				t.Fatalf("decoded JSON = %q, want %q", got.JSON, valid)
			}
			if got.SHA256 != sha256.Sum256(valid) {
				t.Fatal("decoded JSON digest mismatch")
			}
			reader, err := gzip.NewReader(bytes.NewReader(got.Compressed))
			if err != nil {
				t.Fatalf("open compressed map: %v", err)
			}
			decompressed, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read compressed map: %v", err)
			}
			if err := reader.Close(); err != nil {
				t.Fatalf("close compressed map: %v", err)
			}
			if !bytes.Equal(decompressed, valid) {
				t.Fatal("compressed map does not contain decoded JSON")
			}
		})
	}
}

func TestDecodeUploadPlainMapMayExceedCompressedLimit(t *testing.T) {
	padding := strings.Repeat("x", MaxCompressedBytes+1)
	body := []byte(`{"version":3,"file":"app.js","sources":["app.ts"],"sourcesContent":["` + padding + `"],"names":[],"mappings":"AAAA"}`)

	got, err := DecodeUpload(bytes.NewReader(body), "")
	if err != nil {
		t.Fatalf("DecodeUpload returned error for %d-byte plain map: %v", len(body), err)
	}
	if !bytes.Equal(got.JSON, body) {
		t.Fatal("decoded JSON does not match the plain upload")
	}
}

func TestDecodeUploadRejectsOversizedGzipWireBody(t *testing.T) {
	valid := readFixture(t, "app.js.map")
	body := append(gzipBytes(t, valid), bytes.Repeat([]byte("x"), MaxCompressedBytes)...)

	_, err := DecodeUpload(bytes.NewReader(body), "gzip")
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("DecodeUpload error = %v, want %v", err, ErrTooLarge)
	}
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	return data
}

func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return compressed.Bytes()
}
