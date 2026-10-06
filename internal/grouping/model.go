package grouping

import "errors"

// MaxRulesPerProject bounds evaluation cost per ingested event.
const MaxRulesPerProject = 100

// Rule is a persisted grouping rule row, ordered by Position within a project.
// Its "kind" (pre-deterministic text match vs. post-deterministic fingerprint
// alias) is derived entirely from the matcher's field — there is no separate
// kind column.
type Rule struct {
	ID        int64
	ProjectID int64
	Position  int
	Matcher   string
	Template  string
}

// Field name constants used by both the matcher and template DSLs.
const (
	FieldErrorType   = "error.type"
	FieldMessage     = "message"
	FieldLogger      = "logger"
	FieldFingerprint = "fingerprint"
	tagFieldPrefix   = "tags."
)

var (
	// ErrInvalidMatcher reports a matcher string that fails the grammar
	// ParseMatcher implements.
	ErrInvalidMatcher = errors.New("invalid grouping rule matcher")
	// ErrInvalidTemplate reports a template string that fails the grammar
	// ParseTemplate implements.
	ErrInvalidTemplate = errors.New("invalid grouping rule template")
)

func isTagField(field string) (string, bool) {
	if len(field) > len(tagFieldPrefix) && field[:len(tagFieldPrefix)] == tagFieldPrefix {
		return field[len(tagFieldPrefix):], true
	}
	return "", false
}
