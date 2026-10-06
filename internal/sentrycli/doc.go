// Package sentrycli implements the subset of Sentry's chunk-upload and
// artifact-bundle protocol that sentry-cli — and therefore every @sentry/*
// bundler plugin, which wraps the same binary — requires to upload source maps.
//
// Chunks are addressed by SHA-1 because sentry-cli fixes that choice; the
// assembled map content is addressed by SHA-256 the way the rest of Faultline
// addresses artifacts. The two are distinct types here so neither can be
// passed where the other belongs.
//
// The wire formats this package parses are pinned by real captured bytes in
// testdata/, recorded from the sentry-cli version in testdata/VERSION. They
// are not inferred from documentation.
//
// This package holds no HTTP handlers and no SQL: it is the protocol
// vocabulary — capability negotiation, chunk checksums and reassembly, and
// the storage ports — that internal/storage/postgres and internal/api build
// the chunk-upload surface from.
package sentrycli
