// Package migrations exposes Faultline's versioned SQL as an embedded filesystem.
package migrations

import "embed"

// FS contains every up and down migration used by the single binary.
//
//go:embed *.sql
var FS embed.FS
