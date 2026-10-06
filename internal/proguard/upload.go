package proguard

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	//nolint:gosec // G505: sentry-cli names files by SHA-1 and echoes it back in its
	// legacy upload reply; it is a content address here, not a signature.
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

const (
	// MaxDecodedBytes bounds one mapping's uncompressed size. Retracing
	// decompresses and scans the whole mapping whenever a read needs a class
	// the cache has not parsed yet, so this is also the ceiling on that cost.
	MaxDecodedBytes = 100 << 20

	// maxZipEntries bounds how many entries a legacy upload archive may hold;
	// sentry-cli puts one per mapping file on its command line.
	maxZipEntries = 8

	mappingNamePrefix = "proguard/"
	mappingNameSuffix = ".txt"
	canonicalUUIDLen  = 36
)

var (
	ErrTooLarge = errors.New("proguard mapping too large")
	ErrNotFound = errors.New("proguard mapping not found")
	ErrConflict = errors.New("proguard mapping conflict")
)

// DecodedUpload is a validated mapping ready to store: its gzip form, the
// digests of the text, and the text's size. SHA256 is how Faultline addresses
// content; SHA1 exists only because sentry-cli names files by it and echoes it
// back in the legacy upload reply.
type DecodedUpload struct {
	Compressed        []byte
	SHA256            [32]byte
	SHA1              [20]byte
	UncompressedBytes int64
}

// UploadedMapping is a decoded mapping and the UUID its upload named it by.
type UploadedMapping struct {
	UUID   string
	Upload DecodedUpload
}

// Stored is a mapping read back from the repository.
type Stored struct {
	UUID              string
	Compressed        []byte
	UncompressedBytes int64
}

// Repository stores mappings per project, addressed by the mapping UUID the
// SDK reports in the event's debug_meta.
type Repository interface {
	// Put stores a mapping and reports whether it was new. Re-putting the same
	// bytes under the same UUID is not an error; different bytes under an
	// existing UUID are ErrConflict.
	Put(ctx context.Context, projectID int64, mappingUUID string, upload DecodedUpload) (created bool, err error)
	// Find returns ErrNotFound when the project holds no such mapping.
	Find(ctx context.Context, projectID int64, mappingUUID string) (Stored, error)
}

// DecodeUpload reads one mapping, bounds its size, validates its structure and
// compresses it for storage.
func DecodeUpload(reader io.Reader) (DecodedUpload, error) {
	data, err := io.ReadAll(io.LimitReader(reader, MaxDecodedBytes+1))
	if err != nil {
		return DecodedUpload{}, fmt.Errorf("%w: read: %w", ErrInvalidMapping, err)
	}
	if len(data) > MaxDecodedBytes {
		return DecodedUpload{}, ErrTooLarge
	}
	if err := Validate(bytes.NewReader(data)); err != nil {
		return DecodedUpload{}, err
	}

	var stored bytes.Buffer
	writer := gzip.NewWriter(&stored)
	if _, err := writer.Write(data); err != nil {
		return DecodedUpload{}, fmt.Errorf("compress proguard mapping: %w", err)
	}
	if err := writer.Close(); err != nil {
		return DecodedUpload{}, fmt.Errorf("compress proguard mapping: %w", err)
	}
	return DecodedUpload{
		Compressed: stored.Bytes(),
		SHA256:     sha256.Sum256(data),
		//nolint:gosec // G401: see the sha1 import
		SHA1:              sha1.Sum(data),
		UncompressedBytes: int64(len(data)),
	}, nil
}

// MappingUUIDFromName extracts the canonical lowercase mapping UUID from the
// name an upload gives a mapping, "/proguard/<uuid>.txt" or the same without the
// leading slash, and reports false for anything else.
func MappingUUIDFromName(name string) (string, bool) {
	name = strings.TrimPrefix(name, "/")
	middle, found := strings.CutPrefix(name, mappingNamePrefix)
	if !found {
		return "", false
	}
	middle, found = strings.CutSuffix(middle, mappingNameSuffix)
	if !found || len(middle) != canonicalUUIDLen {
		return "", false
	}
	parsed, err := uuid.Parse(middle)
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

// MappingsFromZip reads a legacy sentry-cli upload: a zip whose
// "proguard/<uuid>.txt" entries are the mappings. Entries with any other name
// are ignored; an archive with no mapping entry is an error.
func MappingsFromZip(archive []byte) ([]UploadedMapping, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf("%w: zip: %w", ErrInvalidMapping, err)
	}
	if len(reader.File) > maxZipEntries {
		return nil, fmt.Errorf("%w: more than %d zip entries", ErrInvalidMapping, maxZipEntries)
	}

	var mappings []UploadedMapping
	var totalUncompressed int64
	for _, file := range reader.File {
		mappingUUID, ok := MappingUUIDFromName(file.Name)
		if !ok {
			continue
		}
		entry, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("%w: zip entry: %w", ErrInvalidMapping, err)
		}
		upload, err := DecodeUpload(entry)
		_ = entry.Close()
		if err != nil {
			return nil, err
		}
		// Bounds the whole archive's decompressed work, not just each entry: a
		// small compressed body can still expand to maxZipEntries *
		// MaxDecodedBytes if each entry is checked only against its own limit.
		totalUncompressed += upload.UncompressedBytes
		if totalUncompressed > MaxDecodedBytes {
			return nil, ErrTooLarge
		}
		mappings = append(mappings, UploadedMapping{UUID: mappingUUID, Upload: upload})
	}
	if len(mappings) == 0 {
		return nil, fmt.Errorf("%w: no proguard/<uuid>.txt entry", ErrInvalidMapping)
	}
	return mappings, nil
}
