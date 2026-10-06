package proguard

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"io"
	"strings"
	"testing"
)

const mappingUUID = "cd395950-361a-563d-9df3-49aea740c6f6"

func TestDecodeUpload(t *testing.T) {
	upload, err := DecodeUpload(strings.NewReader(shopMapping))
	if err != nil {
		t.Fatalf("DecodeUpload: %v", err)
	}

	if upload.SHA256 != sha256.Sum256([]byte(shopMapping)) {
		t.Fatal("digest must be of the uncompressed mapping")
	}
	if upload.SHA1 != sha1.Sum([]byte(shopMapping)) {
		t.Fatal("SHA1 must be of the uncompressed mapping")
	}
	if upload.UncompressedBytes != int64(len(shopMapping)) {
		t.Fatalf("UncompressedBytes = %d, want %d", upload.UncompressedBytes, len(shopMapping))
	}
	reader, err := gzip.NewReader(bytes.NewReader(upload.Compressed))
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	roundTrip, _ := io.ReadAll(reader)
	if string(roundTrip) != shopMapping {
		t.Fatal("the compressed form must round-trip to the mapping")
	}
}

func TestDecodeUploadRejects(t *testing.T) {
	for _, test := range []struct {
		name string
		body io.Reader
		want error
	}{
		{"not a mapping", strings.NewReader("hello world\n"), ErrInvalidMapping},
		{"empty", strings.NewReader(""), ErrInvalidMapping},
		{"over the size cap", io.MultiReader(strings.NewReader("a.B -> c:\n"), zeroes(MaxDecodedBytes)), ErrTooLarge},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := DecodeUpload(test.body); !errors.Is(err, test.want) {
				t.Fatalf("DecodeUpload() error = %v, want %v", err, test.want)
			}
		})
	}
}

type zeroReader struct{ remaining int }

func zeroes(n int) io.Reader { return &zeroReader{remaining: n} }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.remaining == 0 {
		return 0, io.EOF
	}
	n := min(len(p), z.remaining)
	for i := range p[:n] {
		p[i] = ' '
	}
	z.remaining -= n
	return n, nil
}

func TestMappingUUIDFromName(t *testing.T) {
	for _, test := range []struct {
		name string
		want string
		ok   bool
	}{
		{"/proguard/" + mappingUUID + ".txt", mappingUUID, true},
		{"proguard/" + mappingUUID + ".txt", mappingUUID, true},
		{"/proguard/" + strings.ToUpper(mappingUUID) + ".txt", mappingUUID, true},
		{"/proguard/not-a-uuid.txt", "", false},
		{"/proguard/" + mappingUUID + ".map", "", false},
		{"/other/" + mappingUUID + ".txt", "", false},
		{"/proguard/../" + mappingUUID + ".txt", "", false},
		{"", "", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, ok := MappingUUIDFromName(test.name)
			if ok != test.ok || got != test.want {
				t.Fatalf("MappingUUIDFromName(%q) = %q, %v; want %q, %v", test.name, got, ok, test.want, test.ok)
			}
		})
	}
}

func zipOf(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, body := range entries {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buffer.Bytes()
}

func TestMappingsFromZip(t *testing.T) {
	archive := zipOf(t, map[string]string{"proguard/" + mappingUUID + ".txt": shopMapping})

	mappings, err := MappingsFromZip(archive)
	if err != nil {
		t.Fatalf("MappingsFromZip: %v", err)
	}

	if len(mappings) != 1 || mappings[0].UUID != mappingUUID {
		t.Fatalf("mappings = %+v", mappings)
	}
	if mappings[0].Upload.SHA256 != sha256.Sum256([]byte(shopMapping)) {
		t.Fatal("the entry must be decoded like a direct upload")
	}
}

// paddedMapping builds a syntactically valid mapping of roughly size bytes by
// repeating one class block; every repeat carries its own class header, so
// Validate keeps accepting it at any length.
func paddedMapping(size int) string {
	const block = "com.x.A -> a:\n    1:1:void run():3:3 -> a\n"
	return strings.Repeat(block, size/len(block)+1)
}

func TestMappingsFromZipRejectsAnArchiveWhoseEntriesTogetherExceedTheLimit(t *testing.T) {
	first := paddedMapping(MaxDecodedBytes - (2 << 20))
	second := paddedMapping(3 << 20)
	archive := zipOf(t, map[string]string{
		"proguard/" + mappingUUID + ".txt":                  first,
		"proguard/11111111-2222-5333-8444-555555555555.txt": second,
	})

	_, err := MappingsFromZip(archive)

	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("MappingsFromZip() error = %v, want ErrTooLarge for entries totalling more than %d bytes even though each is individually under it", err, MaxDecodedBytes)
	}
}

func TestMappingsFromZipRejects(t *testing.T) {
	for _, test := range []struct {
		name    string
		archive []byte
	}{
		{"not a zip", []byte("PK garbage")},
		{"no proguard entry", zipOf(t, map[string]string{"readme.txt": "hi"})},
		{"entry that is not a mapping", zipOf(t, map[string]string{"proguard/" + mappingUUID + ".txt": "nope"})},
		{"entry name that is not a uuid", zipOf(t, map[string]string{"proguard/x.txt": shopMapping})},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := MappingsFromZip(test.archive); err == nil {
				t.Fatal("MappingsFromZip() error = nil")
			}
		})
	}
}
