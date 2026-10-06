package sentrycli

import (
	"context"
	"errors"
	"time"
)

// ErrStagingQuotaExceeded reports that a project already holds more staged
// chunk bytes than MaxStagedBytesPerProject allows. It is distinct from every
// other Save failure because it is the caller's doing, not the server's: a
// handler maps it to 413, never to 500.
var ErrStagingQuotaExceeded = errors.New("upload chunk staging quota exceeded")

// ChunkStore stages uploaded chunks until an assemble request consumes them.
// Chunks are short-lived: Delete removes the ones an assemble used, and
// DeleteExpired removes what an abandoned upload left behind.
type ChunkStore interface {
	Save(ctx context.Context, projectID int64, bodies map[Checksum][]byte) error
	Load(ctx context.Context, projectID int64, want []Checksum) (map[Checksum][]byte, error)
	Delete(ctx context.Context, projectID int64, used []Checksum) error
	DeleteExpired(ctx context.Context, before time.Time) (int64, error)
}

// ReleaseStore backs the release endpoints the bundler plugin calls around an
// upload. Finalize records released_at; set-commits is accepted and discarded
// — Faultline tracks no repositories — and so needs no method here.
type ReleaseStore interface {
	Upsert(ctx context.Context, projectID int64, version string) error
	Finalize(ctx context.Context, projectID int64, version string, at time.Time) error
}
