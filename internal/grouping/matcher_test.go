package grouping

import "testing"

func TestParseMatcherFieldsAndExactPattern(t *testing.T) {
	for _, field := range []string{"error.type", "message", "logger", "tags.job"} {
		matcher, err := ParseMatcher(field + ":checkout-failed")
		if err != nil {
			t.Fatalf("ParseMatcher(%q): %v", field, err)
		}
		if matcher.Field != field || matcher.Pattern != "checkout-failed" || matcher.Kind != PatternExact {
			t.Fatalf("ParseMatcher(%q) = %+v", field, matcher)
		}
	}
}

func TestParseMatcherGlobKinds(t *testing.T) {
	cases := []struct {
		raw     string
		pattern string
		kind    PatternKind
	}{
		{"error.type:DbTimeout*", "DbTimeout", PatternPrefix},
		{"error.type:*Timeout", "Timeout", PatternSuffix},
		{"error.type:*Timeout*", "Timeout", PatternContains},
		{"error.type:*", "", PatternContains},
	}
	for _, tc := range cases {
		matcher, err := ParseMatcher(tc.raw)
		if err != nil || matcher.Pattern != tc.pattern || matcher.Kind != tc.kind {
			t.Fatalf("ParseMatcher(%q) = %+v, %v, want pattern=%q kind=%v", tc.raw, matcher, err, tc.pattern, tc.kind)
		}
	}
}

func TestParseMatcherQuotedPatternRequiredForWhitespace(t *testing.T) {
	matcher, err := ParseMatcher(`message:"payment failed*"`)
	if err != nil || matcher.Pattern != "payment failed" || matcher.Kind != PatternPrefix {
		t.Fatalf("quoted matcher = %+v, %v", matcher, err)
	}
	if _, err := ParseMatcher("message:payment failed"); err == nil {
		t.Fatal("expected error: unquoted pattern with whitespace")
	}
}

func TestParseMatcherRejectsUnknownFieldEmptyPatternAndExtraWildcards(t *testing.T) {
	cases := []string{
		"unknown.field:value",
		"message:",
		"message:",
		"message:*mid*extra*",
		"tags.:value",
		"novaluehere",
	}
	for _, raw := range cases {
		if _, err := ParseMatcher(raw); err == nil {
			t.Fatalf("ParseMatcher(%q): expected error", raw)
		}
	}
}

func TestParseMatcherFingerprintFieldRequiresExactHex(t *testing.T) {
	hex64 := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	matcher, err := ParseMatcher("fingerprint:" + hex64)
	if err != nil || !matcher.IsFingerprint() || matcher.Pattern != hex64 || matcher.Kind != PatternExact {
		t.Fatalf("fingerprint matcher = %+v, %v", matcher, err)
	}
	if _, err := ParseMatcher("fingerprint:" + hex64[:63]); err == nil {
		t.Fatal("expected error: short hex")
	}
	if _, err := ParseMatcher("fingerprint:" + hex64[:63] + "G"); err == nil {
		t.Fatal("expected error: non-hex character")
	}
	if _, err := ParseMatcher("fingerprint:" + hex64 + "*"); err == nil {
		t.Fatal("expected error: wildcard on fingerprint field")
	}
}

func TestParsedMatcherMatch(t *testing.T) {
	prefix, _ := ParseMatcher("error.type:DbTimeout*")
	if !prefix.Match("DbTimeoutException") || prefix.Match("SlowDbTimeout") {
		t.Fatalf("prefix match wrong: %+v", prefix)
	}
	suffix, _ := ParseMatcher("error.type:*Timeout")
	if !suffix.Match("DbTimeout") || suffix.Match("DbTimeoutException") {
		t.Fatalf("suffix match wrong: %+v", suffix)
	}
	contains, _ := ParseMatcher("error.type:*Timeout*")
	if !contains.Match("SlowDbTimeoutException") {
		t.Fatalf("contains match wrong: %+v", contains)
	}
	exact, _ := ParseMatcher("error.type:DbTimeout")
	if !exact.Match("DbTimeout") || exact.Match("DbTimeoutException") {
		t.Fatalf("exact match wrong: %+v", exact)
	}
}
