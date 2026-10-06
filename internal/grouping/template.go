package grouping

import "strings"

// EvaluationInput is the minimal per-event detail the rule DSL can match or
// substitute: the raw (pre-normalization) event fields plus its tags.
type EvaluationInput struct {
	ErrorType string
	Message   string
	Logger    string
	Tags      map[string]string
}

// field resolves name (one of the substitutable field names) against this
// input. ok is false when the field is absent or empty, which the caller
// treats as "unresolvable".
func (in EvaluationInput) field(name string) (string, bool) {
	switch name {
	case FieldErrorType:
		return in.ErrorType, in.ErrorType != ""
	case FieldMessage:
		return in.Message, in.Message != ""
	case FieldLogger:
		return in.Logger, in.Logger != ""
	default:
		if key, ok := isTagField(name); ok {
			value, present := in.Tags[key]
			return value, present && value != ""
		}
		return "", false
	}
}

// TemplatePart is one comma-separated segment of a rule template: either a
// literal (Field == "") or a {field} substitution.
type TemplatePart struct {
	Literal string
	Field   string
}

// ParsedTemplate is a validated, ready-to-resolve rule template.
type ParsedTemplate struct {
	Parts []TemplatePart
}

// ParseTemplate parses a comma-separated template: each part is trimmed; a
// part wrapped in {} substitutes the named field's value (error.type, message,
// logger, tags.<key> only — never fingerprint, which is nonsensical as a
// substitution source); every other part is a literal. At least one
// non-empty part is required.
func ParseTemplate(raw string) (ParsedTemplate, error) {
	rawParts := strings.Split(raw, ",")
	parts := make([]TemplatePart, 0, len(rawParts))
	for _, rawPart := range rawParts {
		trimmed := strings.TrimSpace(rawPart)
		if trimmed == "" {
			return ParsedTemplate{}, ErrInvalidTemplate
		}
		if strings.HasPrefix(trimmed, "{") {
			if !strings.HasSuffix(trimmed, "}") || len(trimmed) < 2 {
				return ParsedTemplate{}, ErrInvalidTemplate
			}
			field := trimmed[1 : len(trimmed)-1]
			if !isSubstitutableField(field) {
				return ParsedTemplate{}, ErrInvalidTemplate
			}
			parts = append(parts, TemplatePart{Field: field})
			continue
		}
		parts = append(parts, TemplatePart{Literal: trimmed})
	}
	// strings.Split always yields at least one element, and every iteration
	// above either appends to parts or returns early, so parts is never empty
	// here — no separate "at least one part" check is reachable.
	return ParsedTemplate{Parts: parts}, nil
}

func isSubstitutableField(field string) bool {
	switch field {
	case FieldErrorType, FieldMessage, FieldLogger:
		return true
	default:
		_, isTag := isTagField(field)
		return isTag
	}
}

// Resolve substitutes every {field} part against input, joins every part
// (literal or resolved), and hashes the result exactly like the deterministic
// paths (hashParts in fingerprint.go). If any {field} reference cannot be
// resolved, ok is false and the caller must skip this rule.
func (t ParsedTemplate) Resolve(input EvaluationInput) (hash string, ok bool) {
	resolved := make([]string, len(t.Parts))
	for index, part := range t.Parts {
		if part.Field == "" {
			resolved[index] = part.Literal
			continue
		}
		value, found := input.field(part.Field)
		if !found {
			return "", false
		}
		resolved[index] = value
	}
	return hashParts(resolved...), true
}
