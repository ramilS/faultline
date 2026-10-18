package scrub

import (
	"fmt"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
)

const (
	// MaxRules bounds each scope's rule list.
	MaxRules = 25
	// MaxRuleNameBytes bounds a rule's name.
	MaxRuleNameBytes = 64
	// MaxPatternBytes bounds a rule's regular expression source.
	MaxPatternBytes = 512
	// MaxReplacementBytes bounds a rule's replacement template.
	MaxReplacementBytes = 128
	// MaxReplacementRefs bounds the group references in a replacement, since
	// each one can copy a whole match.
	MaxReplacementRefs = 8
)

// Rule replaces every match of Pattern, an RE2 regular expression, in any
// string the scrubber sees. Replacement is a Go regexp template: "${1}" is the
// first capture group and "$$" a literal dollar sign. An empty Replacement
// means Filtered.
type Rule struct {
	Name        string `json:"name"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

// groupReference finds "$$", "${name}" and "$name" in a replacement template,
// the forms Regexp.Expand understands. An unbraced name is the longest run of
// the characters Expand reads as one (unicode.IsLetter, unicode.IsDigit and
// "_"), so "$1é" is the group named "1é", not group 1 followed by "é".
var groupReference = regexp.MustCompile(`\$(\$|\{([^}]*)\}|([\p{L}\p{Nd}_]+))`)

// groupNumber is a reference Regexp.Expand treats as a group number. Anything
// else is checked as a group name, so a malformed reference such as ${-1},
// which Expand would copy as literal text, is refused as an unknown group.
var groupNumber = regexp.MustCompile(`^\d+$`)

// NormalizeRules validates rules and returns them with names trimmed, always as
// a non-nil slice.
func NormalizeRules(rules []Rule) ([]Rule, error) {
	if len(rules) > MaxRules {
		return nil, fmt.Errorf("%w: rules: more than %d entries", ErrInvalidConfig, MaxRules)
	}
	normalized := make([]Rule, 0, len(rules))
	for index, rule := range rules {
		rule.Name = strings.TrimSpace(rule.Name)
		if _, err := rule.compile(); err != nil {
			return nil, fmt.Errorf("%w: rule %d: %w", ErrInvalidConfig, index+1, err)
		}
		normalized = append(normalized, rule)
	}
	return normalized, nil
}

type compiledRule struct {
	pattern     *regexp.Regexp
	replacement string
	refs        int
	matchLimit  int
}

func newCompiledRule(pattern *regexp.Regexp, replacement string, refs int) compiledRule {
	matchLimit := max(1, maxRuleMatchInts/(2*(pattern.NumSubexp()+1)))
	return compiledRule{pattern: pattern, replacement: replacement, refs: refs, matchLimit: matchLimit}
}

func (r Rule) compile() (compiledRule, error) {
	if r.Name == "" || len(r.Name) > MaxRuleNameBytes {
		return compiledRule{}, fmt.Errorf("name must be 1-%d bytes", MaxRuleNameBytes)
	}
	if r.Pattern == "" || len(r.Pattern) > MaxPatternBytes {
		return compiledRule{}, fmt.Errorf("pattern must be 1-%d bytes", MaxPatternBytes)
	}
	if len(r.Replacement) > MaxReplacementBytes {
		return compiledRule{}, fmt.Errorf("replacement must be at most %d bytes", MaxReplacementBytes)
	}
	pattern, err := regexp.Compile(r.Pattern)
	if err != nil {
		return compiledRule{}, fmt.Errorf("pattern is not valid RE2: %w", err)
	}
	parsed, err := syntax.Parse(r.Pattern, syntax.Perl)
	if err != nil {
		return compiledRule{}, fmt.Errorf("pattern is not valid RE2: %w", err)
	}
	if nullable(parsed) {
		return compiledRule{}, fmt.Errorf("pattern must not match an empty string")
	}
	replacement := r.Replacement
	if replacement == "" {
		replacement = Filtered
	}
	refs, err := checkGroupReferences(replacement, pattern)
	if err != nil {
		return compiledRule{}, err
	}
	return newCompiledRule(pattern, replacement, refs), nil
}

// nullable reports whether the expression can match an empty string at some
// position of some input. It is decided from the syntax tree rather than by
// probing, because a probe misses an empty alternative that FindAll drops
// right after a non-empty match, as in `a|\b` on "a a".
func nullable(re *syntax.Regexp) bool {
	switch re.Op {
	case syntax.OpEmptyMatch, syntax.OpBeginLine, syntax.OpEndLine, syntax.OpBeginText, syntax.OpEndText,
		syntax.OpWordBoundary, syntax.OpNoWordBoundary, syntax.OpStar, syntax.OpQuest:
		return true
	case syntax.OpRepeat:
		return re.Min == 0 || nullable(re.Sub[0])
	case syntax.OpPlus, syntax.OpCapture:
		return nullable(re.Sub[0])
	case syntax.OpConcat:
		return !slices.ContainsFunc(re.Sub, func(sub *syntax.Regexp) bool { return !nullable(sub) })
	case syntax.OpAlternate:
		return slices.ContainsFunc(re.Sub, nullable)
	case syntax.OpLiteral:
		return len(re.Rune) == 0
	default:
		return false
	}
}

// checkGroupReferences refuses a template that names a group the pattern does
// not have, which Regexp.Expand would silently replace with nothing, or that
// has more than MaxReplacementRefs references. It returns the reference count.
func checkGroupReferences(replacement string, pattern *regexp.Regexp) (int, error) {
	refs := 0
	for _, match := range groupReference.FindAllStringSubmatch(replacement, -1) {
		if match[1] == "$" {
			continue
		}
		refs++
		name := match[2]
		if name == "" {
			name = match[3]
		}
		if groupNumber.MatchString(name) {
			if len(name) > 1 && name[0] == '0' {
				return 0, fmt.Errorf("replacement refers to group %q, which Go reads as a name; write ${1}-style numbers without a leading zero", name)
			}
			if number, err := strconv.Atoi(name); err != nil || number > pattern.NumSubexp() {
				return 0, fmt.Errorf("replacement refers to group %s but the pattern has %d", name, pattern.NumSubexp())
			}
			continue
		}
		if pattern.SubexpIndex(name) < 0 {
			return 0, fmt.Errorf("replacement refers to unknown group %q; write ${1} to follow a group with text", name)
		}
	}
	if refs > MaxReplacementRefs {
		return 0, fmt.Errorf("replacement has %d group references, at most %d allowed", refs, MaxReplacementRefs)
	}
	return refs, nil
}
