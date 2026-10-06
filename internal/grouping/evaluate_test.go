package grouping

import "testing"

func textRule(id int64, position int, matcher, template string) Rule {
	return Rule{ID: id, ProjectID: 1, Position: position, Matcher: matcher, Template: template}
}

func TestEvaluateRulesPass1MatchOverridesFingerprintKeepsTitleAndCulprit(t *testing.T) {
	deterministic := Result{Fingerprint: "deterministic-hash", Title: "Kept Title", Culprit: "kept.culprit"}
	rules := []Rule{textRule(1, 1, `message:"payment failed*"`, "payments,payment-failure")}
	event := EvaluationInput{Message: "payment failed for order 42"}

	got := EvaluateRules(rules, event, deterministic)
	want := hashParts("payments", "payment-failure")
	if got.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q", got.Fingerprint, want)
	}
	if got.Title != deterministic.Title || got.Culprit != deterministic.Culprit {
		t.Fatalf("title/culprit = %q/%q, want unchanged from deterministic", got.Title, got.Culprit)
	}
}

func TestEvaluateRulesPass1PositionOrderPrecedence(t *testing.T) {
	deterministic := Result{Fingerprint: "deterministic-hash"}
	rules := []Rule{
		textRule(1, 1, "logger:billing", "first-match"),
		textRule(2, 2, "logger:billing", "second-match"),
	}
	got := EvaluateRules(rules, EvaluationInput{Logger: "billing"}, deterministic)
	if got.Fingerprint != hashParts("first-match") {
		t.Fatalf("fingerprint = %q, want the first rule in position order to win", got.Fingerprint)
	}
}

func TestEvaluateRulesPass1SkipsUnresolvedTemplateAndFallsThroughToNextRule(t *testing.T) {
	deterministic := Result{Fingerprint: "deterministic-hash"}
	rules := []Rule{
		textRule(1, 1, "logger:billing", "{tags.missing}"),
		textRule(2, 2, "logger:billing", "fallback-match"),
	}
	got := EvaluateRules(rules, EvaluationInput{Logger: "billing"}, deterministic)
	if got.Fingerprint != hashParts("fallback-match") {
		t.Fatalf("fingerprint = %q, want the second rule after the first's template was unresolvable", got.Fingerprint)
	}
}

func TestEvaluateRulesPass2OverridesAfterNoPass1Match(t *testing.T) {
	// The fixture must be exactly 64 hex characters: ParseMatcher requires
	// that (see hexFingerprintPattern in matcher.go), and a shorter literal
	// would make this fingerprint-kind rule fail to parse and silently never
	// match.
	hex64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	deterministic := Result{Fingerprint: hex64, Title: "T", Culprit: "c"}
	rules := []Rule{textRule(1, 1, "fingerprint:"+hex64, "merged,issue-9")}
	got := EvaluateRules(rules, EvaluationInput{}, deterministic)
	// The rule template "merged,issue-9" is comma-split into two literal parts
	// by ParseTemplate, so it resolves via hashParts("merged", "issue-9") — two
	// arguments, not the single joined string hashParts("merged,issue-9") would
	// hash. (The single-argument form hashes a different byte sequence and
	// never matches.)
	want := hashParts("merged", "issue-9")
	if got.Fingerprint != want || got.Title != "T" || got.Culprit != "c" {
		t.Fatalf("got = %+v, want fingerprint %q with unchanged title/culprit", got, want)
	}
}

func TestEvaluateRulesPass1MatchDoesNotExposeItselfToAFingerprintRuleKeyedOnTheOldDeterministicHash(t *testing.T) {
	hex64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	deterministic := Result{Fingerprint: hex64}
	rules := []Rule{
		textRule(1, 1, "logger:billing", "text-match"),
		// This fingerprint-kind rule matches the deterministic fingerprint, but
		// pass 1 already overrode it to hashParts("text-match") before pass 2
		// runs, so this alias's pattern (keyed on the stale deterministic hash)
		// does not match and must not apply.
		textRule(2, 2, "fingerprint:"+hex64, "should-not-apply"),
	}
	got := EvaluateRules(rules, EvaluationInput{Logger: "billing"}, deterministic)
	if got.Fingerprint != hashParts("text-match") {
		t.Fatalf("fingerprint = %q, want pass-1's result unmodified by an alias keyed on the old deterministic hash", got.Fingerprint)
	}
}

// TestEvaluateRulesPass2ChainsOntoPass1Result is C-1's discrimination proof:
// it fails if pass 2 is ever restored to short-circuit on a pass-1 match (the
// original bug) or to match against deterministic.Fingerprint instead of
// pass 1's output. This is exactly the shape a merge-generated fingerprint
// alias produces for an issue whose fingerprint a text rule minted: the
// alias's pattern is the text rule's hash, not the deterministic one.
func TestEvaluateRulesPass2ChainsOntoPass1Result(t *testing.T) {
	deterministic := Result{Fingerprint: "deterministic-hash", Title: "T", Culprit: "c"}
	pass1Hash := hashParts("text-match")
	rules := []Rule{
		textRule(1, 1, "logger:billing", "text-match"),
		// Keyed on pass 1's *output* hash, not on deterministic.Fingerprint —
		// exactly what a merge alias for a rule-derived issue looks like.
		textRule(2, 2, "fingerprint:"+pass1Hash, "merged,issue-9"),
	}
	got := EvaluateRules(rules, EvaluationInput{Logger: "billing"}, deterministic)
	want := hashParts("merged", "issue-9")
	if got.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q — pass 2 must chain onto pass 1's result, not skip or match against the stale deterministic hash", got.Fingerprint, want)
	}
	if got.Title != "T" || got.Culprit != "c" {
		t.Fatalf("title/culprit = %q/%q, want unchanged from deterministic", got.Title, got.Culprit)
	}
}

func TestEvaluateRulesNoMatchFallsThroughToDeterministic(t *testing.T) {
	deterministic := Result{Fingerprint: "deterministic-hash", Title: "T", Culprit: "c"}
	rules := []Rule{textRule(1, 1, "logger:other", "no-match")}
	got := EvaluateRules(rules, EvaluationInput{Logger: "billing"}, deterministic)
	if got != deterministic {
		t.Fatalf("got = %+v, want deterministic result unchanged: %+v", got, deterministic)
	}
}

func TestEvaluateRulesPass2PositionOrderPrecedence(t *testing.T) {
	hex64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	deterministic := Result{Fingerprint: hex64}
	rules := []Rule{
		textRule(1, 1, "fingerprint:"+hex64, "first-alias"),
		textRule(2, 2, "fingerprint:"+hex64, "second-alias"),
	}
	got := EvaluateRules(rules, EvaluationInput{}, deterministic)
	if got.Fingerprint != hashParts("first-alias") {
		t.Fatalf("fingerprint = %q, want the first fingerprint-kind rule in position order to win", got.Fingerprint)
	}
}

func TestEvaluateRulesMalformedRuleIsSkippedNotFatal(t *testing.T) {
	deterministic := Result{Fingerprint: "deterministic-hash", Title: "T", Culprit: "c"}
	rules := []Rule{
		textRule(1, 1, "not-a-valid-matcher", "unreachable-template"), // no ":" — ParseMatcher fails
		textRule(2, 2, "logger:billing", "{unclosed"),                 // "{" with no closing "}" — ParseTemplate fails
		textRule(3, 3, "logger:billing", "valid-match"),
	}
	got := EvaluateRules(rules, EvaluationInput{Logger: "billing"}, deterministic)
	want := hashParts("valid-match")
	if got.Fingerprint != want {
		t.Fatalf("fingerprint = %q, want %q — malformed rules must be skipped, not crash or block later rules", got.Fingerprint, want)
	}
	if got.Title != deterministic.Title || got.Culprit != deterministic.Culprit {
		t.Fatalf("title/culprit = %q/%q, want unchanged from deterministic", got.Title, got.Culprit)
	}
}
