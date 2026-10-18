// Package native ingests Apple Mach-O debug files and resolves native frames.
package native

import (
	"context"
	"errors"
)

const MaxDecodedBytes = 200 << 20

var (
	ErrInvalidDebugFile = errors.New("invalid native debug file")
	ErrTooLarge         = errors.New("native debug file too large")
	ErrConflict         = errors.New("native debug id conflict")
	ErrNotFound         = errors.New("native debug file not found")
)

// Object describes one architecture in a thin or universal Mach-O file.
type Object struct {
	DebugID string
	CPU     string
	Index   int
	VMAddr  uint64
}

// Upload is a validated, compressed debug file. One blob can expose multiple
// independently-addressable architecture UUIDs.
type Upload struct {
	Compressed        []byte
	ContentSHA256     [32]byte
	UncompressedBytes int64
	Objects           []Object
}

// Stored is one project-scoped UUID reference joined to its immutable blob.
type Stored struct {
	BlobID            int64
	DebugID           string
	ObjectIndex       int
	VMAddr            uint64
	Compressed        []byte
	UncompressedBytes int64
}

type Repository interface {
	Put(context.Context, int64, Upload) error
	Find(context.Context, int64, string) (Stored, error)
}
