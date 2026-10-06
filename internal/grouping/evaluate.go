package grouping

// EvaluateRules implements the two-pass custom grouping rule evaluation, chained
// rather than either-or:
//
// Pass 1 scans rules in position order whose matcher field is a text field
// (error.type, message, logger, tags.<key>) against event; the first match
// whose template resolves overrides the fingerprint (starting from
// deterministic.Fingerprint). Pass 2 then always runs — never skipped by a
// pass-1 match — scanning the same list in the same order for
// fingerprint-kind rules matching pass 1's result (or deterministic.Fingerprint
// unchanged, if pass 1 matched nothing); the first match overrides the
// fingerprint again. Title and Culprit always come from deterministic —
// rules only ever change the grouping key.
//
// Chaining pass 2 onto pass 1's output (rather than always matching against
// deterministic.Fingerprint) is what lets a merge-generated fingerprint-kind
// alias rule reach an issue whose fingerprint a pass-1 text rule produced:
// merge aliases key off the fingerprint an event actually resolves to, which
// for a rule-derived issue is pass 1's hash, not the deterministic one. A
// single pass-2 hop suffices even across chained merges, because merge's
// cascade repoint (internal/storage/postgres/issues.go) already flattens
// multi-hop tombstone chains and alias templates onto one live head.
func EvaluateRules(rules []Rule, event EvaluationInput, deterministic Result) Result {
	fingerprint := deterministic.Fingerprint
	if hash, ok := matchTextRules(rules, event); ok {
		fingerprint = hash
	}
	if hash, ok := matchFingerprintRules(rules, event, fingerprint); ok {
		fingerprint = hash
	}
	return Result{Fingerprint: fingerprint, Title: deterministic.Title, Culprit: deterministic.Culprit}
}

func matchTextRules(rules []Rule, event EvaluationInput) (string, bool) {
	for _, rule := range rules {
		matcher, err := ParseMatcher(rule.Matcher)
		if err != nil || matcher.IsFingerprint() {
			continue
		}
		value, ok := event.field(matcher.Field)
		if !ok || !matcher.Match(value) {
			continue
		}
		if hash, ok := resolveRuleTemplate(rule, event); ok {
			return hash, true
		}
	}
	return "", false
}

func matchFingerprintRules(rules []Rule, event EvaluationInput, fingerprint string) (string, bool) {
	for _, rule := range rules {
		matcher, err := ParseMatcher(rule.Matcher)
		if err != nil || !matcher.IsFingerprint() {
			continue
		}
		if matcher.Pattern != fingerprint {
			continue
		}
		if hash, ok := resolveRuleTemplate(rule, event); ok {
			return hash, true
		}
	}
	return "", false
}

// resolveRuleTemplate is defensive: rules are validated at write time, so a
// parse failure here indicates stored data that no longer parses. Treat it
// the same as an unresolvable template — skip this rule, keep evaluating.
func resolveRuleTemplate(rule Rule, event EvaluationInput) (string, bool) {
	template, err := ParseTemplate(rule.Template)
	if err != nil {
		return "", false
	}
	return template.Resolve(event)
}
