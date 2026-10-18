package sentrycli

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/google/uuid"
)

const (
	manifestName = "manifest.json"
	// maxBundleEntries and maxEntryBytes bound inflation. A bundle declares its
	// own contents, so both are checked against the manifest before a single
	// entry is read, and again while reading — the declared size of a zip entry
	// is not evidence of its real size.
	maxBundleEntries = 2000
	maxEntryBytes    = 50 << 20 // matches sourcemap.MaxDecodedBytes and the source_map_blobs CHECK.
	maxManifestBytes = 4 << 20

	// maxAggregateDecompressedBytes bounds the sum of decompressed bytes across
	// every source-map entry returned from one Unpack call. The per-entry and
	// entry-count caps above are each necessary but not sufficient on their
	// own: maxBundleEntries entries at maxEntryBytes each compose to ~97.7 GiB
	// in the worst case, and the assemble endpoint's own compressed-size limit
	// does not close that gap either — deflate reaches roughly 1000:1 on
	// crafted input, so a compressed upload well under that limit can still
	// decompress past 100 GiB. This cap is enforced against bytes actually
	// read out of the decompressor (see readEntry), never against a declared
	// size. 500 MiB is 10x the single-blob ceiling (maxEntryBytes) and far
	// above any realistic bundle: source maps are JSON and compress roughly
	// 8:1, so legitimate content would need several GiB of raw map text to
	// approach it.
	maxAggregateDecompressedBytes = 500 << 20 // 524288000 bytes.
)

// ErrInvalidBundle reports a malformed or hostile artifact bundle: a zip that
// won't open, a missing or unparsable manifest.json, an entry name that
// escapes the archive, an entry supplying neither address a caller could use,
// or an entry that exceeds a size or count guard. Every error Unpack returns
// wraps this sentinel, so a caller that only needs "was the bundle rejected"
// can check ErrInvalidBundle alone.
//
// ErrBundleTooLarge additionally wraps this sentinel on the size- and
// count-guard paths specifically (never on a merely malformed one), so a
// caller that needs to distinguish "too large" (map to HTTP 413) from
// "malformed" (map to 400) can check it instead.
var (
	ErrInvalidBundle  = errors.New("invalid artifact bundle")
	ErrBundleTooLarge = errors.New("artifact bundle too large")
)

// BundleFile is one source map lifted out of an artifact bundle, with the
// addresses the bundle supplies for it. DebugID is empty when the manifest
// carries no per-file debug-id header, which is normal for a bundle sentry-cli
// assembled without running `sourcemaps inject` first. URL is the map's own
// "~/path" form. GeneratedURL is the "~/path" of the MINIFIED file this map
// describes — the address a browser stack frame actually names — and is empty
// when no manifest entry claims this map. At least one of DebugID or URL is
// always set for a returned entry; GeneratedURL carries no such guarantee.
type BundleFile struct {
	DebugID      string
	URL          string
	GeneratedURL string
	Map          []byte
}

type manifestEntry struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

type bundleManifest struct {
	Files map[string]manifestEntry `json:"files"`
}

// Unpack returns every source map in an assembled artifact bundle. A minified
// source's content is discarded -- the resolver needs only maps -- but its
// manifest entry is not: its url and "sourcemap" header supply the only link
// between a map and the generated file it describes, which is the address the
// resolver looks maps up by. See generatedURLIndex. The manifest's top-level
// debug_id addresses the bundle itself, never a file, and is not read here.
func Unpack(zipped io.ReaderAt, size int64) ([]BundleFile, error) {
	archive, err := zip.NewReader(zipped, size)
	if err != nil {
		return nil, fmt.Errorf("%w: open zip: %w", ErrInvalidBundle, err)
	}
	// Checked against the archive's own central-directory count before a
	// single *zip.File is indexed: zip.NewReader has already materialized one
	// struct per record by the time we get here (unavoidable given the
	// mandated archive/zip), but a second full-size map keyed on
	// attacker-chosen names must not also be built for a bundle this large,
	// declared manifest size aside — a small compressed upload can carry
	// millions of central-directory records.
	if len(archive.File) > maxBundleEntries {
		return nil, fmt.Errorf("%w: %w: %d archive entries exceed the %d cap",
			ErrInvalidBundle, ErrBundleTooLarge, len(archive.File), maxBundleEntries)
	}
	index := make(map[string]*zip.File, len(archive.File))
	for _, file := range archive.File {
		index[file.Name] = file
	}

	manifest, err := readManifest(index)
	if err != nil {
		return nil, err
	}
	if len(manifest.Files) > maxBundleEntries {
		return nil, fmt.Errorf("%w: %w: %d manifest entries exceed the %d cap",
			ErrInvalidBundle, ErrBundleTooLarge, len(manifest.Files), maxBundleEntries)
	}

	generatedURLs := generatedURLIndex(manifest.Files)

	files := make([]BundleFile, 0, len(manifest.Files))
	var aggregateBytes int64
	for name, entry := range manifest.Files {
		if err := checkEntryName(name); err != nil {
			return nil, err
		}
		if entry.Type != "source_map" {
			continue
		}
		// A missing debug-id is normal input — sentry-cli emits none unless
		// `sourcemaps inject` ran first — but an entry with neither address
		// is malformed. sourcemap.Repository.PutBundle rejects that shape
		// anyway; rejecting it here, while the entry name is still in hand,
		// beats silently dropping the entry, which would look like a
		// successful upload that quietly lost a file.
		debugID := entry.Headers["debug-id"]
		if debugID == "" && entry.URL == "" {
			return nil, fmt.Errorf("%w: entry %q supplies neither a debug id nor a url", ErrInvalidBundle, name)
		}
		// A debug id is stored in a Postgres uuid column, so anything pgx
		// cannot parse fails inside the driver and surfaces as a 500 on a
		// value the uploading client chose. uuid.Parse is what the read side
		// (postgres.FindByDebugID) already applies, and it accepts the same
		// 36- and 32-character forms pgx does.
		if debugID != "" {
			if _, err := uuid.Parse(debugID); err != nil {
				return nil, fmt.Errorf("%w: entry %q declares a malformed debug id: %w", ErrInvalidBundle, name, err)
			}
		}
		zipEntry, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("%w: manifest names absent entry %q", ErrInvalidBundle, name)
		}
		content, err := readEntry(zipEntry, maxEntryBytes)
		if err != nil {
			return nil, err
		}
		// Checked per entry, immediately after inflation, so a bundle that
		// crosses the cap is rejected as soon as it does rather than after
		// every remaining entry has also been inflated.
		aggregateBytes += int64(len(content))
		if aggregateBytes > maxAggregateDecompressedBytes {
			return nil, fmt.Errorf("%w: %w: aggregate decompressed size across %d entries exceeds %d bytes",
				ErrInvalidBundle, ErrBundleTooLarge, len(files)+1, maxAggregateDecompressedBytes)
		}
		files = append(files, BundleFile{
			DebugID:      debugID,
			URL:          entry.URL,
			GeneratedURL: generatedURLs[entry.URL],
			Map:          content,
		})
	}
	return files, nil
}

// generatedURLIndex maps a source map's url to the url of the minified file
// that names it. sentry-cli records that link as a "sourcemap" header on the
// minified entry, holding a reference resolved against that entry's own url
// exactly as a browser resolves `//# sourceMappingURL=`: "~/app.js" plus
// "app.js.map" addresses "~/app.js.map", which is the map entry's url.
//
// The join is keyed on that reference and never on the debug-id header the
// two entries share: a bundle assembled without a prior `sourcemaps inject`
// carries no debug ids at all, and that is exactly the bundle whose only
// usable address is the release+path one this index feeds.
// Entry type is deliberately not filtered: the "sourcemap" header is what
// makes an entry a generated file here, and trusting the header over a type
// string keeps one recorded constant out of the join.
func generatedURLIndex(files map[string]manifestEntry) map[string]string {
	index := make(map[string]string, len(files))
	for _, entry := range files {
		reference := entry.Headers["sourcemap"]
		if reference == "" || entry.URL == "" {
			continue
		}
		mapURL := resolveSourcemapReference(entry.URL, reference)
		// Map iteration order is unspecified, so two minified entries naming
		// one map must not resolve to a different url from one call to the
		// next: the lexicographically smallest url wins either way.
		if claimed, exists := index[mapURL]; exists && claimed <= entry.URL {
			continue
		}
		index[mapURL] = entry.URL
	}
	return index
}

// resolveSourcemapReference resolves a "sourcemap" header against the url of
// the entry carrying it. A reference already rooted — in the bundle's own
// "~/" form, at "/", or at a scheme — addresses a file on its own and
// replaces the base rather than joining to it.
//
// A "/"- or scheme-rooted reference therefore cannot match a manifest url,
// which sentry-cli always writes in the "~/" form, so such an entry yields no
// release+path address. That is left as it is deliberately: sentry-cli 2.58.6
// emits neither shape, and rewriting "/" to "~/" on the strength of Sentry's
// convention alone would narrow real input on an unrecorded assumption. The
// entry still uploads on its debug id, and a map with no address is skipped
// rather than stored somewhere nothing can find it.
func resolveSourcemapReference(base, reference string) string {
	if strings.HasPrefix(reference, "~/") || strings.HasPrefix(reference, "/") || strings.Contains(reference, "://") {
		return reference
	}
	return path.Join(path.Dir(base), reference)
}

// readManifest locates and decodes manifest.json. Its own byte count is
// capped independently of maxEntryBytes: it holds no source map content, so a
// separate, much smaller limit catches a manifest inflated purely to burn
// memory before any per-file guard runs.
func readManifest(index map[string]*zip.File) (bundleManifest, error) {
	manifestFile, ok := index[manifestName]
	if !ok {
		return bundleManifest{}, fmt.Errorf("%w: no %s", ErrInvalidBundle, manifestName)
	}
	raw, err := readEntry(manifestFile, maxManifestBytes)
	if err != nil {
		return bundleManifest{}, err
	}
	var manifest bundleManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return bundleManifest{}, fmt.Errorf("%w: manifest: %w", ErrInvalidBundle, err)
	}
	return manifest, nil
}

// checkEntryName refuses a manifest key that escapes the archive. The bundle
// is uploaded material, so the name is caller-controlled the moment an
// artifact token leaks; this mirrors the discipline in
// sourcemap.NormalizePath. Both the raw name and its cleaned form are
// checked, so a name that is only benign before normalization (e.g.
// "files/../../etc/passwd") is still rejected.
func checkEntryName(name string) error {
	if name == "" || strings.Contains(name, "\\") {
		return fmt.Errorf("%w: entry name %q", ErrInvalidBundle, name)
	}
	cleaned := path.Clean(name)
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("%w: entry name %q escapes the bundle", ErrInvalidBundle, name)
	}
	return nil
}

// readEntry inflates one entry under a hard byte cap. The cap is enforced on
// the inflated stream, not on the entry's declared UncompressedSize64, which a
// hostile archive may understate: io.LimitReader(reader, limit+1) never asks
// the decompressor for more than limit+1 bytes, so an entry honestly (or
// dishonestly) declaring a larger size is still cut off after limit+1 bytes
// actually come out, and the length check below is what classifies the
// entry as ErrBundleTooLarge rather than trusting the header to have said so.
//
// Separately, if a hostile archive's header *understates* the real size (the
// declared UncompressedSize64 is smaller than what actually decompresses),
// archive/zip's own checksumReader refuses to yield more decompressed bytes
// than that declared value and returns a format error instead once the real
// stream exceeds it — so that shape surfaces here as a plain read error
// (ErrInvalidBundle only, not ErrBundleTooLarge: the zip itself is
// internally inconsistent, which is a malformed-input classification, not a
// size classification) rather than as oversized content silently accepted.
func readEntry(file *zip.File, limit int64) ([]byte, error) {
	reader, err := file.Open()
	if err != nil {
		return nil, fmt.Errorf("%w: open %q: %w", ErrInvalidBundle, file.Name, err)
	}
	defer func() { _ = reader.Close() }()

	content, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read %q: %w", ErrInvalidBundle, file.Name, err)
	}
	if int64(len(content)) > limit {
		return nil, fmt.Errorf("%w: %w: entry %q exceeds %d bytes", ErrInvalidBundle, ErrBundleTooLarge, file.Name, limit)
	}
	return content, nil
}
