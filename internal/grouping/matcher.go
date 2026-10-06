package grouping

import (
	"regexp"
	"strings"
)

var hexFingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// PatternKind is how a matcher's pattern is compared against an event field.
type PatternKind int

const (
	PatternExact PatternKind = iota
	PatternPrefix
	PatternSuffix
	PatternContains
)

// ParsedMatcher is a validated, ready-to-evaluate matcher.
type ParsedMatcher struct {
	Field   string
	Pattern string
	Kind    PatternKind
}

// IsFingerprint reports whether this matcher is the reserved post-deterministic
// fingerprint-alias kind (pass 2), as opposed to a pre-deterministic text-field
// matcher (pass 1).
func (m ParsedMatcher) IsFingerprint() bool {
	return m.Field == FieldFingerprint
}

// Match reports whether value satisfies the matcher's pattern and kind.
// Matching is always case-sensitive.
func (m ParsedMatcher) Match(value string) bool {
	switch m.Kind {
	case PatternPrefix:
		return strings.HasPrefix(value, m.Pattern)
	case PatternSuffix:
		return strings.HasSuffix(value, m.Pattern)
	case PatternContains:
		return strings.Contains(value, m.Pattern)
	default:
		return value == m.Pattern
	}
}

// ParseMatcher parses and validates a "<field>:<pattern>" matcher string.
// field must be one of error.type, message, logger, tags.<key>, or the
// reserved fingerprint kind. pattern is a bare token or a double-quoted string
// (required when it contains whitespace); at most one leading and/or one
// trailing '*' is honored (prefix*/*suffix/*mid*/exact) — no other wildcard
// positions. The fingerprint field additionally requires exactly 64 lowercase
// hex characters and forbids wildcards entirely.
func ParseMatcher(raw string) (ParsedMatcher, error) {
	field, rawPattern, ok := strings.Cut(raw, ":")
	if !ok || field == "" {
		return ParsedMatcher{}, ErrInvalidMatcher
	}
	if _, isTag := isTagField(field); !isTag {
		switch field {
		case FieldErrorType, FieldMessage, FieldLogger, FieldFingerprint:
		default:
			return ParsedMatcher{}, ErrInvalidMatcher
		}
	}

	value, err := unquotePattern(rawPattern)
	if err != nil {
		return ParsedMatcher{}, err
	}

	if field == FieldFingerprint {
		// hexFingerprintPattern is anchored to exactly 64 lowercase hex
		// characters, so any '*' in value already fails this match on its own
		// — a separate strings.Contains(value, "*") check can never fire.
		if !hexFingerprintPattern.MatchString(value) {
			return ParsedMatcher{}, ErrInvalidMatcher
		}
		return ParsedMatcher{Field: field, Pattern: value, Kind: PatternExact}, nil
	}

	pattern, kind, err := parseGlob(value)
	if err != nil {
		return ParsedMatcher{}, err
	}
	return ParsedMatcher{Field: field, Pattern: pattern, Kind: kind}, nil
}

func unquotePattern(rawPattern string) (string, error) {
	if rawPattern == "" {
		return "", ErrInvalidMatcher
	}
	if strings.HasPrefix(rawPattern, `"`) {
		if len(rawPattern) < 2 || !strings.HasSuffix(rawPattern, `"`) {
			return "", ErrInvalidMatcher
		}
		return rawPattern[1 : len(rawPattern)-1], nil
	}
	if strings.ContainsAny(rawPattern, " \t\r\n") {
		return "", ErrInvalidMatcher
	}
	return rawPattern, nil
}

func parseGlob(value string) (string, PatternKind, error) {
	if value == "" {
		return "", PatternExact, ErrInvalidMatcher
	}
	hasLeading := strings.HasPrefix(value, "*")
	hasTrailing := strings.HasSuffix(value, "*")
	inner := value
	if hasLeading {
		inner = inner[1:]
	}
	if hasTrailing && inner != "" {
		inner = inner[:len(inner)-1]
	}
	if strings.Contains(inner, "*") {
		return "", PatternExact, ErrInvalidMatcher
	}
	switch {
	case hasLeading && hasTrailing:
		return inner, PatternContains, nil
	case hasTrailing:
		return inner, PatternPrefix, nil
	case hasLeading:
		return inner, PatternSuffix, nil
	default:
		return inner, PatternExact, nil
	}
}
