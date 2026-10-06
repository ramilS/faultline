package sentrycli_test

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"errors"
	"hash/crc32"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/sentrycli"
)

// The one test that can fail for the reason this feature exists: the bundle
// was produced by a real sentry-cli, so nothing in our own code defined the
// format on both sides.
//
// The assertions pin the exact values the recorded capture in testdata/
// contains, not just their shape. Keying an entry off the manifest's
// top-level bundle debug_id (6f71be4e-...) instead of its own
// files[...].headers["debug-id"] (003d8ac6-...) would compile and pass a
// round-trip test built from our own hand-constructed fixtures -- it would
// only ever fail against a real event. This test, run against real captured
// bytes, is the one place that trap can actually be caught.
func TestUnpackReadsARealSentryCLIBundle(t *testing.T) {
	zipped, err := os.ReadFile("testdata/bundle.zip")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %d entries, want 1 (the minified files/_/_/app.js must be dropped)", len(files))
	}
	if files[0].DebugID != "003d8ac6-6080-5f78-8e37-8aa45ca5ee8a" {
		t.Fatalf("debug id = %q, want the file-level header id, never the bundle-level 6f71be4e-49e4-593f-bbc4-78e5afcc31ca", files[0].DebugID)
	}
	if files[0].URL != "~/app.js.map" {
		t.Fatalf("url = %q, want ~/app.js.map", files[0].URL)
	}
	if files[0].GeneratedURL != "~/app.js" {
		t.Fatalf("generated url = %q, want ~/app.js: a browser stack frame names the minified file, never the map", files[0].GeneratedURL)
	}
	if !bytes.Contains(files[0].Map, []byte(`"mappings"`)) {
		t.Fatalf("entry %q is not a source map", files[0].URL)
	}
}

// Minified sources ride along in the bundle and must be discarded: the
// resolver needs only .map, and storing the rest doubles storage for nothing.
func TestUnpackDiscardsNonSourceMapEntries(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/app.js.map":{"type":"source_map","url":"~/app.js.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}},
		"files/_/_/app.js":{"type":"minified_source","url":"~/app.js","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
		"files/_/_/app.js":     "console.log(1)",
	})

	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %d entries, want 1", len(files))
	}
	if files[0].URL != "~/app.js.map" {
		t.Fatalf("url = %q, want ~/app.js.map", files[0].URL)
	}
}

// A manifest entry with no debug-id header is normal input (sentry-cli emits
// none unless `sourcemaps inject` ran first) and must still be returned,
// carrying its url so the caller can address it by release+path instead.
func TestUnpackKeepsEntriesWithoutDebugID(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/app.js.map":{"type":"source_map","url":"~/app.js.map"}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
	})

	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %d entries, want 1", len(files))
	}
	if files[0].DebugID != "" {
		t.Fatalf("debug id = %q, want empty", files[0].DebugID)
	}
	if files[0].URL != "~/app.js.map" {
		t.Fatalf("url = %q, want ~/app.js.map", files[0].URL)
	}
}

// The map-to-minified-file join must survive a bundle carrying no debug ids
// at all — the shape `sourcemaps upload` emits with no prior `sourcemaps
// inject`, and the only shape for which the release+path address is the sole
// way the resolver can ever reach the map. A join keyed on the shared
// debug-id header would return an empty GeneratedURL here. The sourcemap
// header is relative to the minified entry's own url, so a nested url must
// resolve within its own directory rather than at the bundle root.
func TestUnpackJoinsMapToItsMinifiedSourceWithoutDebugIDs(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/assets/app.js.map":{"type":"source_map","url":"~/assets/app.js.map"},
		"files/_/_/assets/app.js":{"type":"minified_source","url":"~/assets/app.js","headers":{"sourcemap":"app.js.map"}}
	}}`, map[string]string{
		"files/_/_/assets/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
		"files/_/_/assets/app.js":     "console.log(1)",
	})

	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %d entries, want 1", len(files))
	}
	if files[0].DebugID != "" {
		t.Fatalf("debug id = %q, want empty: this bundle declares none", files[0].DebugID)
	}
	if files[0].GeneratedURL != "~/assets/app.js" {
		t.Fatalf("generated url = %q, want ~/assets/app.js", files[0].GeneratedURL)
	}
}

// A map no minified entry names has no generated address at all. Inventing
// one from the map's own url is precisely the defect this join replaced, so
// the field must stay empty and let the caller fall back to the debug id.
func TestUnpackLeavesGeneratedURLEmptyWhenNoEntryNamesTheMap(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/app.js.map":{"type":"source_map","url":"~/app.js.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}},
		"files/_/_/other.js":{"type":"minified_source","url":"~/other.js","headers":{"sourcemap":"other.js.map"}}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
		"files/_/_/other.js":   "console.log(1)",
	})

	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %d entries, want 1", len(files))
	}
	if files[0].GeneratedURL != "" {
		t.Fatalf("generated url = %q, want empty", files[0].GeneratedURL)
	}
}

// The top-level manifest debug_id is the bundle's own id, not a per-file
// debug id: in testdata/manifest.json the two differ. Keying an entry off it
// would compile and never match a real event.
func TestUnpackIgnoresTopLevelBundleDebugID(t *testing.T) {
	zipped := buildBundle(t, `{"debug_id":"6f71be4e-49e4-593f-bbc4-78e5afcc31ca","files":{
		"files/_/_/app.js.map":{"type":"source_map","url":"~/app.js.map"}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
	})

	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %d entries, want 1", len(files))
	}
	if files[0].DebugID == "6f71be4e-49e4-593f-bbc4-78e5afcc31ca" {
		t.Fatalf("entry picked up the bundle-level debug_id instead of its own header")
	}
	if files[0].DebugID != "" {
		t.Fatalf("debug id = %q, want empty (no per-file header was given)", files[0].DebugID)
	}
}

// A manifest entry naming a path outside the archive must be refused rather
// than followed: the name is attacker-controlled once an artifact token leaks.
func TestUnpackRejectsPathTraversalInManifest(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"../../etc/passwd":{"type":"source_map","url":"~/a.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}
	}}`, map[string]string{"../../etc/passwd": `{"version":3,"sources":[],"names":[],"mappings":""}`})

	err := errUnpack(t, zipped)
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, unexpectedly matched ErrBundleTooLarge (this is a malformed name, not a size guard)", err)
	}
}

// An absolute path in the manifest must be refused just like a relative
// traversal: both let a manifest name a location outside the bundle.
func TestUnpackRejectsAbsolutePathInManifest(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"/etc/passwd":{"type":"source_map","url":"~/a.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}
	}}`, map[string]string{"/etc/passwd": `{"version":3,"sources":[],"names":[],"mappings":""}`})

	err := errUnpack(t, zipped)
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, unexpectedly matched ErrBundleTooLarge (this is a malformed name, not a size guard)", err)
	}
}

// A Windows-style backslash path must be refused: path.Clean alone treats
// backslashes as ordinary characters and would let one slip past a
// slash-only traversal check.
func TestUnpackRejectsBackslashPathInManifest(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"..\\..\\etc\\passwd":{"type":"source_map","url":"~/a.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}
	}}`, map[string]string{"..\\..\\etc\\passwd": `{"version":3,"sources":[],"names":[],"mappings":""}`})

	err := errUnpack(t, zipped)
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, unexpectedly matched ErrBundleTooLarge (this is a malformed name, not a size guard)", err)
	}
}

// A name that is benign as written but resolves to a traversal once
// normalized (e.g. "files/../../etc/passwd") must also be refused: checking
// only the raw string would miss it.
func TestUnpackRejectsPathThatNormalizesToTraversal(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/../../etc/passwd":{"type":"source_map","url":"~/a.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}
	}}`, map[string]string{"files/../../etc/passwd": `{"version":3,"sources":[],"names":[],"mappings":""}`})

	err := errUnpack(t, zipped)
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, unexpectedly matched ErrBundleTooLarge (this is a malformed name, not a size guard)", err)
	}
}

// A source_map entry supplying neither a debug-id header nor a url is
// malformed, not the normal "no inject ran" shape -- sourcemap.PutBundle
// rejects an entry carrying neither address anyway, and rejecting it here,
// while the entry name is still in hand, beats silently dropping it, which
// would look like a successful upload that quietly lost a file.
func TestUnpackRejectsEntryWithNeitherDebugIDNorURL(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/app.js.map":{"type":"source_map"}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
	})

	err := errUnpack(t, zipped)
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, unexpectedly matched ErrBundleTooLarge (this is a malformed entry, not a size guard)", err)
	}
}

// The mirror image of the test above, and the reason its guard is a
// conjunction: an entry carrying a debug id but no url has one usable address
// and is ordinary output of `sourcemaps inject` on files the CLI has no url
// for. Turning that `&&` into an `||` would reject this bundle, and no other
// test in this file would notice.
func TestUnpackKeepsEntriesWithADebugIDAndNoURL(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/app.js.map":{"type":"source_map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
	})

	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("extracted %d entries, want 1", len(files))
	}
	if files[0].DebugID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("debug id = %q, want the entry's own", files[0].DebugID)
	}
	if files[0].URL != "" {
		t.Fatalf("url = %q, want empty", files[0].URL)
	}
}

// A bundle whose manifest declares more entries than the cap must be refused
// before any are inflated: the cost of a zip bomb is paid during inflation,
// not after. This fixture's manifest names 2001 files that do not actually
// exist in the archive (only manifest.json itself is a real zip entry) --
// deliberately, so that the archive-entry-count guard, tested separately
// below, cannot be what fires here; only the manifest's own declared entry
// count can trip this guard.
func TestUnpackRejectsTooManyManifestEntries(t *testing.T) {
	manifest := bytes.NewBufferString(`{"files":{`)
	for i := range 2001 {
		name := "files/_/_/f" + strconv.Itoa(i) + ".map"
		if i > 0 {
			manifest.WriteString(",")
		}
		manifest.WriteString(`"` + name + `":{"type":"source_map","url":"~/f` + strconv.Itoa(i) + `.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}`)
	}
	manifest.WriteString(`}}`)

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	mustWrite(t, writer, "manifest.json", manifest.Bytes())
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	err := errUnpack(t, buffer.Bytes())
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if !errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, want ErrBundleTooLarge", err)
	}
	if !strings.Contains(err.Error(), "manifest entries") {
		t.Fatalf("error = %v, want the manifest-entry-count guard to have fired", err)
	}
}

// The entry-count cap must bound the archive's own central-directory record
// count, not just the manifest's declared file list. A bundle can carry many more physical zip entries than its manifest
// ever names -- zip.NewReader materializes a *zip.File per record regardless
// of what the manifest says, and Unpack's own index map is keyed on those
// same attacker-chosen names. This fixture's manifest declares only one
// entry (far under the cap); the archive's real entry count is what must
// trip the guard.
func TestUnpackRejectsTooManyArchiveEntries(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	mustWrite(t, writer, "manifest.json", []byte(`{"files":{"files/_/_/app.js.map":{"type":"source_map","url":"~/app.js.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}}}`))
	mustWrite(t, writer, "files/_/_/app.js.map", []byte(`{"version":3,"sources":[],"names":[],"mappings":""}`))
	for i := range 2001 {
		mustWrite(t, writer, "junk/"+strconv.Itoa(i), []byte("x"))
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	err := errUnpack(t, buffer.Bytes())
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if !errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, want ErrBundleTooLarge", err)
	}
	// Distinguishes this from the manifest-entry-count guard, which this
	// fixture's one-entry manifest would never trip on its own -- proving the
	// archive-level guard fired, not a coincidentally-also-true one.
	if !strings.Contains(err.Error(), "archive entries") {
		t.Fatalf("error = %v, want the archive-entry-count guard to have fired", err)
	}
}

// An entry whose real inflated content exceeds the per-entry cap must be
// rejected once actually read, even though this fixture's own declared
// UncompressedSize64 is honest (an ordinary zip.Writer always computes it
// correctly from what was written). This proves the mechanical cap --
// io.LimitReader plus the post-read length check -- rejects large content it
// actually reads; it does not by itself prove independence from trusting the
// header value, since an honest header would pass a header-only check
// identically. TestUnpackDoesNotTrustTheDeclaredUncompressedSize below
// proves that half separately, with a dishonest header.
func TestUnpackRejectsEntryExceedingByteCapOnInflation(t *testing.T) {
	const oversized = "files/_/_/big.map"
	content := bytes.Repeat([]byte("a"), 50<<20+1)

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	mustWrite(t, writer, "manifest.json", []byte(`{"files":{"`+oversized+`":{"type":"source_map","url":"~/big.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}}}`))
	mustWrite(t, writer, oversized, content)
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	err := errUnpack(t, buffer.Bytes())
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if !errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, want ErrBundleTooLarge", err)
	}
}

// The test above builds its oversized entry with a plain zip.Writer, which always computes an honest
// UncompressedSize64 -- so it cannot distinguish Unpack from a hypothetical
// implementation that rejects purely by checking that header field
// (`if file.UncompressedSize64 > limit { reject }`) without ever bounding an
// actual read. This fixture uses zip.Writer.CreateRaw, which writes the
// caller's FileHeader verbatim, to declare UncompressedSize64: 10 while the
// entry's real compressed payload inflates to over 50 MiB. A header-trusting
// implementation that reads exactly the declared size (e.g.
// io.ReadFull(reader, make([]byte, file.UncompressedSize64))) would accept
// this entry with no error, silently returning 10 bytes of truncated
// garbage; the task report for this fix records that implementation
// producing exactly that result, and this test failing (RED) against it
// before being restored to the real readEntry (GREEN).
//
// Unpack rejects this entry, but not through the int64(len(content)) > limit
// branch: archive/zip's own checksumReader refuses to yield more decompressed
// bytes than a header's declared UncompressedSize64 and returns a format
// error once the real stream exceeds it, so io.ReadAll itself fails first.
// That surfaces as ErrInvalidBundle only, not ErrBundleTooLarge: the zip is
// internally inconsistent, which is a malformed-input classification here,
// not a size one.
func TestUnpackDoesNotTrustTheDeclaredUncompressedSize(t *testing.T) {
	zipped := buildLyingSizeBundle(t)

	err := errUnpack(t, zipped)
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, unexpectedly matched ErrBundleTooLarge (this entry is internally inconsistent, not merely large)", err)
	}
}

// buildLyingSizeBundle returns a bundle whose one source-map entry declares
// UncompressedSize64: 10 while actually holding a >50 MiB payload once
// inflated. zip.Writer.CreateRaw is the only way to make the header lie: the
// ordinary Create/Write path always computes an honest size from what was
// written, so this cannot be built with buildBundle or mustWrite alone.
func buildLyingSizeBundle(t *testing.T) []byte {
	t.Helper()
	const realSize = 60 << 20 // over the 50 MiB per-entry cap once inflated
	payload := make([]byte, realSize)
	checksum := crc32.ChecksumIEEE(payload)

	var compressed bytes.Buffer
	flateWriter, err := flate.NewWriter(&compressed, flate.BestCompression)
	if err != nil {
		t.Fatalf("new flate writer: %v", err)
	}
	if _, err := flateWriter.Write(payload); err != nil {
		t.Fatalf("compress payload: %v", err)
	}
	if err := flateWriter.Close(); err != nil {
		t.Fatalf("close flate writer: %v", err)
	}

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	raw, err := writer.CreateRaw(&zip.FileHeader{
		Name:               "files/_/_/big.map",
		Method:             zip.Deflate,
		CRC32:              checksum,
		CompressedSize64:   uint64(compressed.Len()),
		UncompressedSize64: 10, // the lie: real inflated content is realSize bytes
	})
	if err != nil {
		t.Fatalf("create raw entry: %v", err)
	}
	if _, err := raw.Write(compressed.Bytes()); err != nil {
		t.Fatalf("write raw entry: %v", err)
	}
	mustWrite(t, writer, "manifest.json",
		[]byte(`{"files":{"files/_/_/big.map":{"type":"source_map","url":"~/big.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}}}`))
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	// The attack's whole point is a small upload: confirm the fixture is
	// actually small before Unpack is even called, since that is the actual
	// shape of the attack this proves resistance to.
	if buffer.Len() > 1<<20 {
		t.Fatalf("fixture compressed to %d bytes, want a small upload", buffer.Len())
	}
	return buffer.Bytes()
}

// The per-entry cap and the entry-count cap compose to a ~97.7 GiB
// worst case (maxBundleEntries entries at just under maxEntryBytes each);
// the aggregate cap closes that gap. This fixture trips only the aggregate
// cap: 11 entries, each comfortably under the 50 MiB per-entry cap, and an
// entry count (11) comfortably under the 2000-entry cap, whose cumulative
// decompressed size (~528 MiB) crosses the 500 MiB aggregate cap. The zip
// stays tiny on the wire because deflate compresses the repeated-byte
// payload at roughly 1000:1 -- the actual shape of the attack this guard
// defends against, not an incidentally-large upload.
func TestUnpackRejectsAggregateDecompressedSizeAcrossEntries(t *testing.T) {
	const perEntryBytes = 48 << 20 // under the 50 MiB per-entry cap
	const entryCount = 11          // 11 * 48MiB ~= 528MiB, over the 500MiB aggregate cap; well under the 2000-entry cap
	payload := make([]byte, perEntryBytes)

	names := make([]string, entryCount)
	for i := range entryCount {
		names[i] = "files/_/_/big" + strconv.Itoa(i) + ".map"
	}

	var manifest bytes.Buffer
	manifest.WriteString(`{"files":{`)
	for i, name := range names {
		if i > 0 {
			manifest.WriteString(",")
		}
		manifest.WriteString(`"` + name + `":{"type":"source_map","url":"~/big` + strconv.Itoa(i) +
			`.map","headers":{"debug-id":"11111111-1111-1111-1111-111111111111"}}`)
	}
	manifest.WriteString(`}}`)

	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	mustWrite(t, writer, "manifest.json", manifest.Bytes())
	for _, name := range names {
		mustWrite(t, writer, name, payload)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	// The attack's whole point is a small upload: confirm the fixture is
	// actually small before asserting on Unpack's behavior, or this test
	// would prove nothing about the shape it claims to cover.
	if buffer.Len() > 10<<20 {
		t.Fatalf("fixture compressed to %d bytes, want a small upload for a realistic zip-bomb shape", buffer.Len())
	}

	err := errUnpack(t, buffer.Bytes())
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	// ErrBundleTooLarge is the programmatic way a caller distinguishes every
	// size/count guard from a malformed-input one; this guard must set it
	// like the others do.
	if !errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, want ErrBundleTooLarge", err)
	}
	// Further distinguishes this guard specifically from the other three
	// ErrBundleTooLarge guards (archive count, manifest count, per-entry
	// size), none of which mention "aggregate" in their message.
	if !strings.Contains(err.Error(), "aggregate") {
		t.Fatalf("error = %v, want the aggregate-size guard to have fired", err)
	}
}

// errUnpack runs Unpack and returns its error, for tests that assert on the
// specific guard that fired rather than merely that Unpack failed.
func errUnpack(t *testing.T, zipped []byte) error {
	t.Helper()
	_, err := sentrycli.Unpack(zipped)
	return err
}

func mustWrite(t *testing.T, writer *zip.Writer, name string, content []byte) {
	t.Helper()
	file, err := writer.Create(name)
	if err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
	if _, err := file.Write(content); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func buildBundle(t *testing.T, manifest string, entries map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	write := func(name, content string) {
		file, err := writer.Create(name)
		if err != nil {
			t.Fatalf("create %s: %v", name, err)
		}
		if _, err := file.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	write("manifest.json", manifest)
	for name, content := range entries {
		write(name, content)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buffer.Bytes()
}

// A debug id is written to a Postgres uuid column. An unvalidated one fails
// inside the driver, which classifies as a server fault (500) on a value the
// uploading client fully controls; rejecting it here classifies it as the
// malformed input it is. ErrBundleTooLarge must NOT wrap it: that sentinel
// selects 413, and nothing here is about size.
func TestUnpackRejectsAMalformedDebugID(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/app.js.map":{"type":"source_map","url":"~/app.js.map","headers":{"debug-id":"nope"}}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
	})

	_, err := sentrycli.Unpack(zipped)
	if !errors.Is(err, sentrycli.ErrInvalidBundle) {
		t.Fatalf("error = %v, want ErrInvalidBundle", err)
	}
	if errors.Is(err, sentrycli.ErrBundleTooLarge) {
		t.Fatalf("error = %v, want no ErrBundleTooLarge: a malformed id is a 400, not a 413", err)
	}
}

// The 32-character form is what pgx accepts alongside the hyphenated one, so
// rejecting it here would refuse a bundle the database would have stored.
func TestUnpackAcceptsAnUnhyphenatedDebugID(t *testing.T) {
	zipped := buildBundle(t, `{"files":{
		"files/_/_/app.js.map":{"type":"source_map","url":"~/app.js.map","headers":{"debug-id":"003d8ac660805f788e378aa45ca5ee8a"}}
	}}`, map[string]string{
		"files/_/_/app.js.map": `{"version":3,"sources":["a.ts"],"names":[],"mappings":"AAAA"}`,
	})

	files, err := sentrycli.Unpack(zipped)
	if err != nil {
		t.Fatalf("unpack: %v", err)
	}
	if len(files) != 1 || files[0].DebugID != "003d8ac660805f788e378aa45ca5ee8a" {
		t.Fatalf("files = %+v, want the unhyphenated debug id preserved verbatim", files)
	}
}
