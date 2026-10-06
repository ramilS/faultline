// Package sourcemap validates and stores release-scoped browser source maps.
package sourcemap

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

const (
	MaxCompressedBytes = 10 << 20
	MaxDecodedBytes    = 50 << 20
)

var (
	ErrInvalidPath = errors.New("invalid generated path")
	ErrInvalidMap  = errors.New("invalid source map")
	ErrTooLarge    = errors.New("source map too large")
	ErrConflict    = errors.New("source map conflict")
	ErrNotFound    = errors.New("source map not found")
)

// DecodedUpload contains the validated decompressed source map and its
// compressed storage representation.
type DecodedUpload struct {
	JSON       []byte
	Compressed []byte
	SHA256     [32]byte
}

// Map is one addressable source map: a ref row joined to the blob holding its
// content. ID is the ref's id and BlobID the content's, which is why the parsed
// cache keys on BlobID — two refs on one blob must share a single parse.
type Map struct {
	ID                int64
	BlobID            int64
	ReleaseID         int64
	GeneratedPath     string
	ContentSHA256     [32]byte
	CompressedMap     []byte
	UncompressedBytes int64
	UploadedAt        time.Time
}

// BundleEntry is one source map extracted from an artifact bundle, carrying
// whichever addresses the bundle supplies. GeneratedPath is empty when the
// bundle entry declares no url; DebugID is empty when the entry carries only
// a legacy url and no debug-id header. At least one of the two must be set —
// PutBundle rejects an entry that supplies neither.
type BundleEntry struct {
	DebugID       string
	GeneratedPath string
	Upload        DecodedUpload
}

// Repository stores immutable source maps addressable by debug id or by
// release and generated path.
type Repository interface {
	Put(context.Context, int64, string, string, DecodedUpload) (Map, bool, error)
	Find(context.Context, int64, string, string) (Map, error)
	FindByDebugID(context.Context, int64, string) (Map, error)
	PutBundle(context.Context, int64, string, []BundleEntry) error
}

// EventInput is the read-time symbolication request for one stored event.
type EventInput struct {
	ProjectID int64
	Release   string
	Payload   json.RawMessage
}

// Position is a single stack-frame location. Line and Column are one-based to
// match browser display semantics.
type Position struct {
	Function string
	Filename string
	Line     int
	Column   int
	InApp    bool
}

// ContextLine is one line of original source surrounding a mapped frame.
type ContextLine struct {
	Line  int
	Text  string
	Focus bool
}

// Frame pairs the reported generated location with its resolved original
// location. Original is nil when no source map resolves the frame.
type Frame struct {
	Generated Position
	Original  *Position
	Context   []ContextLine
}

// Exception mirrors the browser exception payload with resolved frames,
// preserving frame and cause order.
type Exception struct {
	Type   string
	Value  string
	Frames []Frame
	Cause  *Exception
}

// Resolver produces a symbolicated exception for a stored event. A nil result
// with a nil error means the event carried no exception to resolve.
type Resolver interface {
	Resolve(context.Context, EventInput) (*Exception, error)
}
