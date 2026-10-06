package storage

import "errors"

// ErrArtifactQuotaExceeded reports an upload that would take a project past
// the cap on the source maps and ProGuard/R8 mappings it may store.
var ErrArtifactQuotaExceeded = errors.New("project artifact storage quota exceeded")
