package api

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/artifactauth"
	nativedebug "github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
)

const (
	assembleBodyLimit = 1 << 20
	// sentry-cli names each multipart part after the encoding it chose, and it
	// chooses from the `compression` list we advertise. The recorded capture
	// in internal/sentrycli/testdata/ carries `file_gzip` with a gzip body
	// against the ["gzip"] this server advertises; `file` is accepted too
	// because it is the plain name the protocol uses when compression is off,
	// and rejecting it costs nothing. The filename is always the SHA-1 of the
	// *decompressed* chunk.
	chunkPartName     = "file"
	chunkPartNameGzip = "file_gzip"
)

// SentryCLIHandler serves the chunk-upload, artifact-bundle and release
// endpoints sentry-cli — and therefore every @sentry/* bundler plugin —
// requires. Every route authenticates on a project artifact bearer token; the
// {org} path segment is accepted and discarded, because Faultline has no
// organizations and the token alone decides what a caller may write.
type SentryCLIHandler struct {
	artifacts    ArtifactService
	projects     ProjectResolver
	chunks       sentrycli.ChunkStore
	sourceMaps   sourcemap.Repository
	releases     sentrycli.ReleaseStore
	mappings     proguard.Repository
	nativeFiles  nativedebug.Repository
	decodeNative func([]byte) (nativedebug.Upload, error)
	reportError  func(error)

	// sweepMu and lastSweep throttle sweepAbandonedChunks to at most once per
	// chunkSweepInterval across every request this handler serves.
	sweepMu   sync.Mutex
	lastSweep time.Time
}

func NewSentryCLIHandler(artifacts ArtifactService, projects ProjectResolver, chunks sentrycli.ChunkStore, sourceMaps sourcemap.Repository, releases sentrycli.ReleaseStore, mappings proguard.Repository, options Options) *SentryCLIHandler {
	return NewSentryCLIHandlerWithNative(artifacts, projects, chunks, sourceMaps, releases, mappings, nil, options)
}

func NewSentryCLIHandlerWithNative(artifacts ArtifactService, projects ProjectResolver, chunks sentrycli.ChunkStore, sourceMaps sourcemap.Repository, releases sentrycli.ReleaseStore, mappings proguard.Repository, nativeFiles nativedebug.Repository, options Options) *SentryCLIHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &SentryCLIHandler{
		artifacts: artifacts, projects: projects, chunks: chunks,
		sourceMaps: sourceMaps, releases: releases, mappings: mappings, nativeFiles: nativeFiles,
		decodeNative: nativedebug.DecodeUpload, reportError: reportError,
	}
}

// authenticate resolves the artifact bearer token to its project. It is the
// whole authorization story for these routes: they are gatePublic, so no
// session middleware ran and there is no scope in the request context.
func (h *SentryCLIHandler) authenticate(w http.ResponseWriter, r *http.Request) (artifactauth.Token, bool) {
	raw, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid artifact token")
		return artifactauth.Token{}, false
	}
	if h.artifacts == nil {
		h.reportError(errors.New("artifact service is not configured"))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return artifactauth.Token{}, false
	}
	token, err := h.artifacts.Authenticate(r.Context(), raw)
	if errors.Is(err, artifactauth.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, "invalid artifact token")
		return artifactauth.Token{}, false
	}
	if err != nil {
		h.reportError(fmt.Errorf("authenticate artifact token: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return artifactauth.Token{}, false
	}
	return token, true
}

// ChunkUploadOptions advertises what this server accepts. sentry-cli aborts
// the whole upload on a 404 here — there is no legacy fallback — so this route
// existing is what makes the bundler plugins work at all. The advertised url
// is this request's own path: the CLI requires the key — omitting it aborts
// the upload with "could not parse JSON response" — and a relative path is
// the only one a self-hosted server can state honestly.
func (h *SentryCLIHandler) ChunkUploadOptions(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, sentrycli.DefaultChunkOptions(r.URL.Path))
}

// UploadChunks stages every multipart part under its declared checksum, after
// verifying the body hashes to the name it was uploaded under.
func (h *SentryCLIHandler) UploadChunks(w http.ResponseWriter, r *http.Request) {
	token, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	h.sweepAbandonedChunks(r.Context())
	boundary, ok := multipartBoundary(r.Header.Get("Content-Type"))
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid chunk upload")
		return
	}
	options := sentrycli.DefaultChunkOptions(r.URL.Path)
	r.Body = http.MaxBytesReader(w, r.Body, options.MaxRequestSize)
	reader := multipart.NewReader(r.Body, boundary)

	bodies, ok := h.readChunkParts(w, reader, options)
	if !ok {
		return
	}
	if err := h.chunks.Save(r.Context(), token.ProjectID, bodies); err != nil {
		// Over the staging ceiling is the caller's doing, not a server fault:
		// answer 413 and say what recovers it, rather than reporting it as an
		// internal error the operator would have to act on.
		if errors.Is(err, sentrycli.ErrStagingQuotaExceeded) {
			writeError(w, http.StatusRequestEntityTooLarge, "too many staged upload chunks for this project; assemble them or wait for them to expire")
			return
		}
		h.reportError(fmt.Errorf("save upload chunks: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, struct{}{})
}

// readChunkParts drains every part of a chunk-upload multipart body into a
// checksum-addressed map, writing the HTTP response itself and returning
// ok=false the moment any part fails validation.
//
// Two independent amplification guards, both load-bearing:
//
//   - partsConsumed bounds the number of parts this loop will decompress to
//     ChunksPerRequest, checked BEFORE a recognized part is opened or a
//     single byte of it is read. It counts parts, never len(bodies): bodies
//     is keyed by checksum, so parts repeating one checksum collapse into a
//     single entry there and a request of any number of parts all naming one
//     checksum would never grow it past 1.
//   - aggregateBytes tracks decompressed bytes summed across every accepted
//     part in this request, capped at ChunkSize*ChunksPerRequest, checked
//     after each part is read.
//
// The second is unreachable through this method while the first holds: at
// most ChunksPerRequest parts of at most ChunkSize bytes sum to exactly
// aggregateBytes' ceiling and never past it. It is kept as an explicit
// backstop against a change to either bound, which is also why no test
// asserts its rejection branch — constructing one means breaking one of the
// two bounds whose product defines the ceiling.
func (h *SentryCLIHandler) readChunkParts(w http.ResponseWriter, reader *multipart.Reader, options sentrycli.ChunkOptions) (map[sentrycli.Checksum][]byte, bool) {
	bodies := make(map[sentrycli.Checksum][]byte)
	maxAggregateBytes := options.ChunkSize * int64(options.ChunksPerRequest)
	var aggregateBytes int64
	var partsConsumed int
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			return bodies, true
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid chunk upload")
			return nil, false
		}
		gzipped := part.FormName() == chunkPartNameGzip
		if part.FormName() != chunkPartName && !gzipped {
			// Deliberately discarded: sentry-cli's protocol may carry other
			// named fields this server does not need.
			_ = part.Close()
			continue
		}
		// Checked before the part is read, and incremented below for every
		// recognized part regardless of what it turns out to contain, so a
		// part failing decompression or its checksum still counts against the
		// ceiling. Never len(bodies): that map is keyed by checksum and
		// collapses repeats.
		if partsConsumed >= options.ChunksPerRequest {
			_ = part.Close()
			writeError(w, http.StatusBadRequest, "too many chunks in one request")
			return nil, false
		}
		partsConsumed++
		body, checksum, ok := h.readChunkPart(w, part, gzipped, options)
		if !ok {
			return nil, false
		}
		aggregateBytes += int64(len(body))
		if aggregateBytes > maxAggregateBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "chunk upload exceeds the maximum decompressed size")
			return nil, false
		}
		bodies[checksum] = body
	}
}

// readChunkPart consumes one multipart part already identified as a chunk
// field (file or file_gzip) and returns its declared checksum and verified,
// decompressed body.
func (h *SentryCLIHandler) readChunkPart(w http.ResponseWriter, part *multipart.Part, gzipped bool, options sentrycli.ChunkOptions) ([]byte, sentrycli.Checksum, bool) {
	defer func() { _ = part.Close() }()

	declared, err := sentrycli.ParseChecksum(part.FileName())
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chunk checksum")
		return nil, sentrycli.Checksum{}, false
	}
	var source io.Reader = part
	if gzipped {
		decompressor, err := gzip.NewReader(part)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid chunk upload")
			return nil, sentrycli.Checksum{}, false
		}
		defer func() { _ = decompressor.Close() }()
		source = decompressor
	}
	// The limit applies to the DECOMPRESSED bytes. Limiting the compressed
	// stream instead would let a small gzip part expand past ChunkSize.
	body, err := io.ReadAll(io.LimitReader(source, options.ChunkSize+1))
	if err != nil || int64(len(body)) > options.ChunkSize {
		writeError(w, http.StatusRequestEntityTooLarge, "chunk too large")
		return nil, sentrycli.Checksum{}, false
	}
	// The checksum is the chunk's address. Storing a body under a name it
	// does not hash to would let one upload poison another's content.
	if sentrycli.ComputeChecksum(body) != declared {
		writeError(w, http.StatusBadRequest, "chunk checksum does not match its body")
		return nil, sentrycli.Checksum{}, false
	}
	return body, declared, true
}

type assembleRequest struct {
	Checksum string   `json:"checksum"`
	Chunks   []string `json:"chunks"`
	Projects []string `json:"projects"`
	Version  *string  `json:"version"`
	// Dist is decoded and discarded: Faultline addresses a release+path ref by
	// (release_id, generated_path) alone, so two dists of one release collide
	// on that key and the last upload wins. Distributions are a design
	// non-goal; accepting the field costs nothing and refusing it would fail a
	// build that sets one.
	Dist *string `json:"dist"`
}

type assembleResponse struct {
	State         string   `json:"state"`
	MissingChunks []string `json:"missingChunks"`
	// Detail is always nil: a real Sentry server emits "detail":null on every
	// response, including the terminal "ok" one, and this field exists to
	// match that wire shape rather than to carry a value this handler
	// produces.
	Detail *string `json:"detail"`
}

// AssembleBundle reassembles a bundle from staged chunks and stores its source
// maps, synchronously. Sentry assembles asynchronously and the client polls;
// Faultline answers terminally on the first call because an async state
// machine would need a job table and a worker — this binary promises no
// background workers.
//
// The work is bounded above by maxFileSize, but that bound is enforced here,
// not merely advertised: sentrycli.Concat sums a checksum's body length once
// per occurrence in chunks[], so a request naming one legitimately-staged
// small chunk many thousands of times would otherwise multiply it into an
// allocation far past any file this server could ever have accepted, with
// nothing upstream to stop it — an out-of-memory fatal error (not a
// recoverable panic) reachable with any valid artifact token and a ~1 MiB
// request. parseAssembleChunks below is what actually enforces the bound; see
// its doc comment.
func (h *SentryCLIHandler) AssembleBundle(w http.ResponseWriter, r *http.Request) {
	token, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var input assembleRequest
	if err := decodeSingleJSONIgnoringUnknownFields(w, r, assembleBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid assemble request")
		return
	}
	if !h.ownsEveryProject(w, r, token, input.Projects) {
		return
	}
	options := sentrycli.DefaultChunkOptions(r.URL.Path)
	wanted, ok := h.parseAssembleChunks(w, input.Chunks, options)
	if !ok {
		return
	}
	total, err := sentrycli.ParseChecksum(input.Checksum)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid bundle checksum")
		return
	}

	staged, err := h.chunks.Load(r.Context(), token.ProjectID, wanted)
	if err != nil {
		h.reportError(fmt.Errorf("load upload chunks: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if missing := missingChunks(wanted, staged); len(missing) > 0 {
		// Not an error status: missingChunks is how sentry-cli learns what to
		// re-upload. A 4xx here aborts the upload instead of recovering it.
		writeJSON(w, http.StatusOK, assembleResponse{State: "not_found", MissingChunks: missing})
		return
	}
	// Belt-and-suspenders on top of parseAssembleChunks' count cap and
	// UploadChunks' per-chunk ChunkSize cap: those two together already bound
	// what a *conforming* upload could have staged, but this checks what is
	// actually staged for these checksums, independent of how it got there.
	if stagedBytes := sumStagedBytes(staged); stagedBytes > options.MaxFileSize {
		writeError(w, http.StatusRequestEntityTooLarge, "assembled bundle exceeds the maximum file size")
		return
	}

	assembled, err := sentrycli.Concat(wanted, staged, total)
	if err != nil {
		writeError(w, http.StatusBadRequest, "assembled bundle does not match its checksum")
		return
	}
	files, err := sentrycli.Unpack(assembled)
	if err != nil {
		h.handleUnpackError(w, err)
		return
	}

	entries, err := bundleEntries(files)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid artifact bundle")
		return
	}
	version := ""
	if input.Version != nil {
		version = strings.TrimSpace(*input.Version)
	}
	if err := h.sourceMaps.PutBundle(r.Context(), token.ProjectID, version, entries); err != nil {
		if errors.Is(err, storage.ErrArtifactQuotaExceeded) {
			writeError(w, http.StatusRequestEntityTooLarge, artifactQuotaMessage)
			return
		}
		h.reportError(fmt.Errorf("store artifact bundle: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err := h.chunks.Delete(r.Context(), token.ProjectID, wanted); err != nil {
		// The bundle is stored; the staging rows are now garbage the TTL sweep
		// will collect. Reporting without failing keeps a storage hiccup from
		// making a successful upload look failed to the CLI.
		h.reportError(fmt.Errorf("delete consumed upload chunks: %w", err))
	}
	writeJSON(w, http.StatusOK, assembleResponse{State: "ok", MissingChunks: []string{}})
}

// handleUnpackError maps Unpack's error onto a status: ErrBundleTooLarge
// (always also wrapping ErrInvalidBundle on the size/count-guard paths) maps
// to 413; every other ErrInvalidBundle — plain malformed input, and the one
// pinned exception where a bundle's header understates its real size, which
// sentrycli.readEntry's doc describes, so the guard never has a chance to
// classify it as oversized — maps to 400. Neither branch echoes
// Unpack's error text: it embeds attacker-chosen entry names, safe for logs
// (%q-escaped) but not for an HTTP response body.
func (h *SentryCLIHandler) handleUnpackError(w http.ResponseWriter, err error) {
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "artifact bundle too large")
		return
	}
	if errors.Is(err, sentrycli.ErrInvalidBundle) {
		writeError(w, http.StatusBadRequest, "invalid artifact bundle")
		return
	}
	h.reportError(fmt.Errorf("unpack artifact bundle: %w", err))
	writeError(w, http.StatusInternalServerError, "internal server error")
}

// ownsEveryProject enforces the whole authorization model for assemble: the
// request names its target projects in the body, not the path, so each one is
// checked against the token's project.
func (h *SentryCLIHandler) ownsEveryProject(w http.ResponseWriter, r *http.Request, token artifactauth.Token, slugs []string) bool {
	if len(slugs) == 0 {
		writeError(w, http.StatusBadRequest, "assemble request names no project")
		return false
	}
	for _, slug := range slugs {
		resolved, ok := resolveProjectInScope(w, r, access.Unrestricted(), slug, h.projects, h.reportError)
		if !ok {
			return false
		}
		if resolved.ID != token.ProjectID {
			writeError(w, http.StatusForbidden, "artifact token does not belong to project")
			return false
		}
	}
	return true
}

// parseAssembleChunks validates the chunks[] list before anything in it
// touches storage or sentrycli.Concat. Two checks, both load-bearing:
//
//   - The count cannot exceed MaxFileSize/ChunkSize (25 at today's advertised
//     values) — not an invented number, but exactly how many maximum-size
//     chunks the largest bundle this server accepts could ever be split into.
//     Any more is already an attack, not a valid upload.
//   - No checksum may repeat. sentrycli.Concat sums a checksum's staged body
//     length once per occurrence in order — a list naming one legitimately
//     staged small chunk thousands of times (still under the count cap only
//     if the cap were absent, which is exactly why the count cap above must
//     run first) multiplies it into an allocation with nothing left to bound
//     it. A byte-identical repeat can in principle occur in real content, but
//     rejecting that vanishingly rare case is the trade this sum-per-occurrence
//     format forces; there is no way to distinguish it from the attack from
//     the request alone.
func (h *SentryCLIHandler) parseAssembleChunks(w http.ResponseWriter, raw []string, options sentrycli.ChunkOptions) ([]sentrycli.Checksum, bool) {
	maxChunks := int(options.MaxFileSize / options.ChunkSize)
	if len(raw) > maxChunks {
		writeError(w, http.StatusBadRequest, "too many chunks in assemble request")
		return nil, false
	}
	wanted, err := parseChecksums(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid chunk checksum")
		return nil, false
	}
	if hasDuplicateChecksum(wanted) {
		writeError(w, http.StatusBadRequest, "duplicate chunk checksum in assemble request")
		return nil, false
	}
	return wanted, true
}

func hasDuplicateChecksum(checksums []sentrycli.Checksum) bool {
	seen := make(map[sentrycli.Checksum]struct{}, len(checksums))
	for _, checksum := range checksums {
		if _, exists := seen[checksum]; exists {
			return true
		}
		seen[checksum] = struct{}{}
	}
	return false
}

func sumStagedBytes(staged map[sentrycli.Checksum][]byte) int64 {
	var total int64
	for _, body := range staged {
		total += int64(len(body))
	}
	return total
}

// bundleEntries converts every unpacked source map into the shape
// sourcemap.Repository.PutBundle stores. GeneratedPath comes from
// BundleFile.GeneratedURL — the MINIFIED file's url — never from the map's
// own url: the resolver looks a release+path ref up by NormalizePath of a
// browser stack frame's filename, and a browser frame names "/app.js", never
// "/app.js.map". A bundle url is "~/assets/app.js"; the "~" stands for any
// host, which sourcemap.NormalizePath's leading-slash requirement would
// otherwise reject outright, so it is stripped here before normalizing.
//
// An entry may end up with no usable release+path address: the bundle named
// no minified file for this map, or its url does not normalize (no leading
// slash, a bare "~", a backslash, or a url that cleans to "/"). That entry
// still uploads on its debug id. One carrying neither address is dropped
// rather than passed on, because sourcemap.BundleEntry requires at least one
// and failing PutBundle over it would fail the whole bundle on input a client
// fully controls.
func bundleEntries(files []sentrycli.BundleFile) ([]sourcemap.BundleEntry, error) {
	entries := make([]sourcemap.BundleEntry, 0, len(files))
	for _, file := range files {
		generatedPath := ""
		if normalized, err := sourcemap.NormalizePath(strings.TrimPrefix(file.GeneratedURL, "~")); err == nil {
			generatedPath = normalized
		}
		if file.DebugID == "" && generatedPath == "" {
			continue
		}
		upload, err := sourcemap.DecodeUpload(bytes.NewReader(file.Map), "")
		if err != nil {
			return nil, fmt.Errorf("decode bundled source map: %w", err)
		}
		entries = append(entries, sourcemap.BundleEntry{
			DebugID: file.DebugID, GeneratedPath: generatedPath, Upload: upload,
		})
	}
	return entries, nil
}

func parseChecksums(raw []string) ([]sentrycli.Checksum, error) {
	parsed := make([]sentrycli.Checksum, 0, len(raw))
	for _, value := range raw {
		checksum, err := sentrycli.ParseChecksum(value)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, checksum)
	}
	return parsed, nil
}

func missingChunks(wanted []sentrycli.Checksum, staged map[sentrycli.Checksum][]byte) []string {
	missing := make([]string, 0)
	for _, checksum := range wanted {
		if _, ok := staged[checksum]; !ok {
			missing = append(missing, checksum.String())
		}
	}
	return missing
}

func multipartBoundary(contentType string) (string, bool) {
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") {
		return "", false
	}
	boundary, ok := parameters["boundary"]
	return boundary, ok && boundary != ""
}

const (
	chunkTTL           = 24 * time.Hour
	chunkSweepInterval = time.Hour
)

// sweepAbandonedChunks deletes chunks no assemble ever consumed. It runs from
// the upload path rather than a background worker: Faultline ships as one
// binary with no worker, and an abandoned upload is precisely a case where no
// later request for that project will arrive. Throttled because one upload
// sends many chunk requests back to back and DeleteExpired's DELETE still
// costs a round trip per call, even though created_at is indexed
// (upload_chunks_gc_idx) and this is an index range scan, not a sequential
// scan of the table.
//
// DeleteExpired is deliberately global — it takes no project id, so this
// sweep bounds every project's abandoned chunks by age alone. What a single
// project may hold before that age cutoff arrives is bounded instead at the
// store, against sentrycli.MaxStagedBytesPerProject.
//
// A sweep failure is reported, never surfaced to the caller: the request that
// happened to trigger the sweep is unrelated to the chunks it is deleting,
// and failing that request over a GC hiccup would turn a maintenance detail
// into an upload failure.
func (h *SentryCLIHandler) sweepAbandonedChunks(ctx context.Context) {
	h.sweepMu.Lock()
	now := time.Now()
	if !h.lastSweep.IsZero() && now.Before(h.lastSweep.Add(chunkSweepInterval)) {
		h.sweepMu.Unlock()
		return
	}
	h.lastSweep = now
	h.sweepMu.Unlock()

	if _, err := h.chunks.DeleteExpired(ctx, now.Add(-chunkTTL)); err != nil {
		h.reportError(fmt.Errorf("sweep abandoned upload chunks: %w", err))
	}
}

const releaseBodyLimit = 64 << 10

// releaseVersionField names the {version} path segment CreateRelease's
// idempotent sibling routes carry and UpdateRelease/PreviousReleaseWithCommits
// read, and doubles as the JSON key both handlers echo back.
const releaseVersionField = "version"

// releaseRequest names the fields the bundler plugin's default build sends
// across the create and finalize calls, as the recorded capture in
// internal/sentrycli/testdata/ shows them: version and projects on both,
// dateStarted only on create, dateReleased only on finalize. Any other key
// the CLI sends is ignored — releaseRequest is decoded leniently, because
// Faultline does not own this protocol's schema.
type releaseRequest struct {
	Version  string   `json:"version"`
	Projects []string `json:"projects"`
	// DateStarted is accepted and discarded: sentry-cli sends it on every
	// release create, and Faultline tracks no "started" timestamp separate
	// from the release row itself.
	DateStarted  *string `json:"dateStarted"`
	DateReleased *string `json:"dateReleased"`
	// Refs is declared, rather than left to the lenient decode, so that this
	// comment sits on the field a reader looks for. The bundler plugin calls
	// setCommits on its default configuration (`setCommits !== false` is true
	// for undefined). Faultline tracks no repositories and discards the value;
	// README says so, rather than the acceptance implying support.
	Refs any `json:"refs"`
}

// CreateRelease upserts the release the bundler plugin declares before
// upload. The {org} and {project} path segments (present on the
// project-scoped route the plugin actually calls) are both discarded: the
// write always targets the authenticated token's own project, never a
// project named in the URL.
func (h *SentryCLIHandler) CreateRelease(w http.ResponseWriter, r *http.Request) {
	token, input, ok := h.releaseRequest(w, r)
	if !ok {
		return
	}
	if input.Version == "" {
		writeError(w, http.StatusBadRequest, "invalid release")
		return
	}
	if err := h.releases.Upsert(r.Context(), token.ProjectID, input.Version); err != nil {
		h.reportError(fmt.Errorf("upsert release: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{releaseVersionField: input.Version})
}

// UpdateRelease finalizes a release and discards any commit association. The
// bundler plugin finalizes on every build, so a call carrying an earlier
// dateReleased than a previous one is normal rather than an error case:
// ReleaseStore.Finalize overwrites released_at unconditionally, including out
// of order.
func (h *SentryCLIHandler) UpdateRelease(w http.ResponseWriter, r *http.Request) {
	token, input, ok := h.releaseRequest(w, r)
	if !ok {
		return
	}
	// The path segment is the canonical address of the release being
	// finalized: PUT .../releases/A/ with a body naming B must finalize A, not
	// B. The body's version is a fallback for the org-scoped route, which
	// carries no {version} segment on some sentry-cli paths.
	version := r.PathValue(releaseVersionField)
	if version == "" {
		version = input.Version
	}
	if version == "" {
		writeError(w, http.StatusBadRequest, "invalid release")
		return
	}
	releasedAt := time.Now().UTC()
	if input.DateReleased != nil {
		parsed, err := time.Parse(time.RFC3339, *input.DateReleased)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid release date")
			return
		}
		releasedAt = parsed.UTC()
	}
	if err := h.releases.Finalize(r.Context(), token.ProjectID, version, releasedAt); err != nil {
		h.reportError(fmt.Errorf("finalize release: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{releaseVersionField: version})
}

// releaseRequest authenticates the request and decodes its body, then checks
// every project the body names against the token's own project. It is the
// whole authorization story for create/finalize: the body's projects list is
// what a real Sentry API keys release membership on, so that is what is
// checked, exactly as AssembleBundle's ownsEveryProject checks assemble's own
// body-named projects. The write itself (Upsert/Finalize) always targets
// token.ProjectID regardless of what the body or the {project} path segment
// say, so an absent or empty projects list cannot smuggle a write into
// another project — it can only skip the "did the caller name a project it
// doesn't own" check, and the recorded capture in
// internal/sentrycli/testdata/ never shows the bundler plugin's default build
// sending one without a populated projects list.
func (h *SentryCLIHandler) releaseRequest(w http.ResponseWriter, r *http.Request) (artifactauth.Token, releaseRequest, bool) {
	token, ok := h.authenticate(w, r)
	if !ok {
		return artifactauth.Token{}, releaseRequest{}, false
	}
	var input releaseRequest
	if err := decodeSingleJSONIgnoringUnknownFields(w, r, releaseBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid release")
		return artifactauth.Token{}, releaseRequest{}, false
	}
	if len(input.Projects) > 0 && !h.ownsEveryProject(w, r, token, input.Projects) {
		return artifactauth.Token{}, releaseRequest{}, false
	}
	return token, input, true
}

// ListRepos answers set-commits --auto's first probe. Faultline tracks no
// repositories, but the response must still be a JSON array: against the
// sentry-cli version in internal/sentrycli/testdata/VERSION, returning an
// object instead makes the CLI silently skip the rest of set-commits.
func (h *SentryCLIHandler) ListRepos(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, []struct{}{})
}

// PreviousReleaseWithCommits answers set-commits --auto's second probe. A
// real Sentry server returns the release preceding {version} together with
// its commits; Faultline tracks neither release ordering nor commit history,
// so it returns the requested version alone. sentry-cli's
// own git check runs after this call and fails locally when no repository is
// present, so this response's content is never actually consulted on that
// path — it only has to be a JSON object sentry-cli can parse as a release.
func (h *SentryCLIHandler) PreviousReleaseWithCommits(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.authenticate(w, r); !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{releaseVersionField: r.PathValue(releaseVersionField)})
}
