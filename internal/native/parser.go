package native

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"debug/macho"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
)

const lcUUID = 0x1b

// DecodeUpload validates every architecture and prepares a single compressed
// blob. Filenames are deliberately not involved in debug-id extraction.
func DecodeUpload(body []byte) (Upload, error) {
	if len(body) == 0 {
		return Upload{}, ErrInvalidDebugFile
	}
	if len(body) > MaxDecodedBytes {
		return Upload{}, ErrTooLarge
	}
	objects, err := inspect(body)
	if err != nil {
		return Upload{}, fmt.Errorf("%w: %w", ErrInvalidDebugFile, err)
	}
	var compressed bytes.Buffer
	zw := gzip.NewWriter(&compressed)
	if _, err := zw.Write(body); err != nil {
		return Upload{}, fmt.Errorf("compress native debug file: %w", err)
	}
	if err := zw.Close(); err != nil {
		return Upload{}, fmt.Errorf("compress native debug file: %w", err)
	}
	return Upload{Compressed: compressed.Bytes(), ContentSHA256: sha256.Sum256(body), UncompressedBytes: int64(len(body)), Objects: objects}, nil
}

func inspect(body []byte) ([]Object, error) {
	if fat, err := macho.NewFatFile(bytes.NewReader(body)); err == nil {
		defer func() { _ = fat.Close() }()
		objects := make([]Object, 0, len(fat.Arches))
		seen := make(map[string]struct{}, len(fat.Arches))
		for i, arch := range fat.Arches {
			object, err := inspectFile(arch.File, i)
			if err != nil {
				return nil, fmt.Errorf("architecture %d: %w", i, err)
			}
			if _, duplicate := seen[object.DebugID]; duplicate {
				return nil, fmt.Errorf("duplicate Mach-O UUID %s", object.DebugID)
			}
			seen[object.DebugID] = struct{}{}
			objects = append(objects, object)
		}
		return objects, nil
	}
	file, err := macho.NewFile(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	object, err := inspectFile(file, 0)
	if err != nil {
		return nil, err
	}
	return []Object{object}, nil
}

func inspectFile(file *macho.File, index int) (Object, error) {
	debugID, err := machoUUID(file)
	if err != nil {
		return Object{}, err
	}
	data, err := file.DWARF()
	if err != nil {
		return Object{}, fmt.Errorf("read DWARF: %w", err)
	}
	reader := data.Reader()
	entries := 0
	for {
		entry, err := reader.Next()
		if err != nil {
			return Object{}, fmt.Errorf("read DWARF entries: %w", err)
		}
		if entry == nil {
			break
		}
		entries++
		if entry.Tag != 0 {
			if _, err := data.Ranges(entry); err != nil {
				return Object{}, fmt.Errorf("read DWARF ranges: %w", err)
			}
		}
	}
	if entries == 0 {
		return Object{}, fmt.Errorf("DWARF contains no entries")
	}
	var vmaddr uint64
	for _, load := range file.Loads {
		segment, ok := load.(*macho.Segment)
		if !ok || segment.Name != "__TEXT" {
			continue
		}
		if segment.Memsz != 0 {
			vmaddr = segment.Addr
			break
		}
	}
	return Object{DebugID: debugID, CPU: cpuName(file.Cpu), Index: index, VMAddr: vmaddr}, nil
}

func cpuName(cpu macho.Cpu) string {
	switch cpu {
	case macho.CpuArm64:
		return "arm64"
	case macho.CpuAmd64:
		return "x86_64"
	case macho.Cpu386:
		return "x86"
	default:
		return strings.TrimPrefix(strings.ToLower(cpu.String()), "cpu")
	}
}

func machoUUID(file *macho.File) (string, error) {
	for _, load := range file.Loads {
		raw := load.Raw()
		if len(raw) < 24 || file.ByteOrder.Uint32(raw[:4]) != lcUUID {
			continue
		}
		parsed, err := uuid.FromBytes(raw[8:24])
		if err != nil {
			return "", err
		}
		return parsed.String(), nil
	}
	return "", fmt.Errorf("Mach-O has no LC_UUID")
}

func decompress(stored Stored) ([]byte, error) {
	if stored.UncompressedBytes <= 0 || stored.UncompressedBytes > MaxDecodedBytes {
		return nil, fmt.Errorf("stored native debug file size is invalid")
	}
	zr, err := gzip.NewReader(bytes.NewReader(stored.Compressed))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	body, err := io.ReadAll(io.LimitReader(zr, stored.UncompressedBytes+1))
	if err != nil || int64(len(body)) != stored.UncompressedBytes {
		return nil, fmt.Errorf("stored native debug file length mismatch")
	}
	return body, nil
}

func openObject(body []byte, index int) (*macho.File, func() error, error) {
	if fat, err := macho.NewFatFile(bytes.NewReader(body)); err == nil {
		if index < 0 || index >= len(fat.Arches) {
			_ = fat.Close()
			return nil, nil, fmt.Errorf("invalid architecture index")
		}
		return fat.Arches[index].File, fat.Close, nil
	}
	if index != 0 {
		return nil, nil, fmt.Errorf("invalid architecture index")
	}
	file, err := macho.NewFile(bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	return file, file.Close, nil
}
