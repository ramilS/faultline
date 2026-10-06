package sentrycli

// Byte budgets advertised to sentry-cli. chunkSize and chunksPerRequest bound
// what one request may carry; maxFileSize bounds the assembled bundle, which
// is what makes synchronous assembly safe — the work is bounded above by a
// number this server chose.
const (
	chunkSize        = 8 << 20
	chunksPerRequest = 64
	maxFileSize      = 200 << 20
	maxRequestSize   = 32 << 20
	concurrency      = 4
)

// MaxStagedBytesPerProject bounds what one project holds in chunk staging.
// Nothing else bounds it: chunks are removed when an assemble consumes them,
// and otherwise only by an age sweep that does not run for 24 hours, so an
// upload that never assembles can accumulate with one valid artifact token
// until the disk every project shares is full.
//
// It is checked before a request stages anything, not maintained as an
// invariant: a request admitted just under the ceiling still adds up to
// ChunkSize*ChunksPerRequest, and concurrent requests each observe the same
// pre-insert total, so the reachable maximum is this ceiling plus one
// request's worth per upload in flight. That overshoot is self-limiting --
// each request's worth costs maxRequestSize on the wire -- and the ceiling
// bounds what survives between requests, which is the accumulation this
// exists to stop.
//
// Twice maxFileSize is headroom for a retry that overlaps the bundle it is
// retrying, not a byte budget to be spent: staging is a waypoint for a single
// assemble, and identical chunks collapse on (project_id, checksum) rather
// than accumulating.
const MaxStagedBytesPerProject = 2 * maxFileSize

// ChunkOptions is the capability negotiation response. Field names are fixed
// by sentry-cli's ChunkServerOptions, not by us. URL has no default: the CLI's
// deserializer requires the key, and a missing one aborts the upload before a
// single chunk is sent.
type ChunkOptions struct {
	URL              string   `json:"url"`
	ChunkSize        int64    `json:"chunkSize"`
	ChunksPerRequest int      `json:"chunksPerRequest"`
	MaxFileSize      int64    `json:"maxFileSize"`
	MaxRequestSize   int64    `json:"maxRequestSize"`
	Concurrency      int      `json:"concurrency"`
	HashAlgorithm    string   `json:"hashAlgorithm"`
	Compression      []string `json:"compression"`
	Accept           []string `json:"accept"`
}

// DefaultChunkOptions returns what this server advertises. url is the path the
// CLI should POST chunks to — a relative path works and is what a self-hosted
// server can honestly produce, since it does not know its external origin.
//
// Accept is a routing switch inside sentry-cli, not a description of what this
// server implements. The CLI takes the artifact-bundle path only when both
// artifact_bundles_v2 and release_files appear; with a shorter list it
// silently falls back to the legacy release-file endpoints, which this server
// does not serve. Shortening this list is a functional regression, not a
// tightening — TestDefaultChunkOptionsMarshalToTheKeysSentryCLIReads
// pins the exact list.
func DefaultChunkOptions(url string) ChunkOptions {
	return ChunkOptions{
		URL:              url,
		ChunkSize:        chunkSize,
		ChunksPerRequest: chunksPerRequest,
		MaxFileSize:      maxFileSize,
		MaxRequestSize:   maxRequestSize,
		Concurrency:      concurrency,
		HashAlgorithm:    "sha1",
		Compression:      []string{"gzip"},
		Accept:           []string{"artifact_bundles", "artifact_bundles_v2", "release_files", "debug_files"},
	}
}
