package scrub

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeRulesValidates(t *testing.T) {
	cases := map[string]struct {
		rule    Rule
		wantErr string
	}{
		"valid telegram rule":                {Rule{Name: "Telegram", Pattern: `bot\d{6,}:[A-Za-z0-9_-]{30,}`, Replacement: "bot[Filtered]"}, ""},
		"empty replacement is allowed":       {Rule{Name: "x", Pattern: `secret-\d+`}, ""},
		"group reference in braces":          {Rule{Name: "x", Pattern: `(bot)\d+`, Replacement: "${1}[Filtered]"}, ""},
		"named group reference":              {Rule{Name: "x", Pattern: `(?P<p>bot)\d+`, Replacement: "${p}[Filtered]"}, ""},
		"escaped dollar":                     {Rule{Name: "x", Pattern: `k\d+`, Replacement: "$$1"}, ""},
		"blank name":                         {Rule{Name: "  ", Pattern: `a`}, "name"},
		"long name":                          {Rule{Name: strings.Repeat("n", 65), Pattern: `a`}, "name"},
		"empty pattern":                      {Rule{Name: "x"}, "pattern"},
		"long pattern":                       {Rule{Name: "x", Pattern: strings.Repeat("a", 513)}, "pattern"},
		"long replacement":                   {Rule{Name: "x", Pattern: `a`, Replacement: strings.Repeat("r", 129)}, "replacement"},
		"lookahead is not RE2":               {Rule{Name: "x", Pattern: `a(?=b)`}, "RE2"},
		"unclosed group":                     {Rule{Name: "x", Pattern: `(abc`}, "RE2"},
		"matches empty string":               {Rule{Name: "x", Pattern: `a*`}, "empty"},
		"word boundary matches empty":        {Rule{Name: "x", Pattern: `\b`}, "empty"},
		"optional ssn matches empty":         {Rule{Name: "x", Pattern: `\b(?:\d{3}-\d{2}-\d{4})?\b`}, "empty"},
		"missing group":                      {Rule{Name: "x", Pattern: `bot\d+`, Replacement: "${1}"}, "group"},
		"go reads $1x as a group named 1x":   {Rule{Name: "x", Pattern: `(bot)\d+`, Replacement: "$1x"}, "${1}"},
		"empty braces":                       {Rule{Name: "x", Pattern: `a`, Replacement: "${}"}, "group"},
		"whole match reference":              {Rule{Name: "x", Pattern: `(a)b`, Replacement: "${0}"}, ""},
		"first group reference":              {Rule{Name: "x", Pattern: `(a)b`, Replacement: "${1}"}, ""},
		"negative group number":              {Rule{Name: "x", Pattern: `(a)b`, Replacement: "${-1}"}, "unknown group"},
		"signed group number":                {Rule{Name: "x", Pattern: `(a)b`, Replacement: "${+1}"}, "unknown group"},
		"leading zero expands to nothing":    {Rule{Name: "x", Pattern: `(a)b`, Replacement: "${01}"}, "${1}"},
		"eight group references":             {Rule{Name: "x", Pattern: `(a)b`, Replacement: strings.Repeat("${1}", 8)}, ""},
		"nine group references":              {Rule{Name: "x", Pattern: `(a)b`, Replacement: strings.Repeat("${1}", 9)}, "group references"},
		"escaped dollars are not references": {Rule{Name: "x", Pattern: `(a)b`, Replacement: strings.Repeat("$$", 9)}, ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := NormalizeRules([]Rule{tc.rule})
			if tc.wantErr == "" {
				if err != nil || len(got) != 1 {
					t.Fatalf("NormalizeRules = %v, %v; want one rule", got, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want ErrInvalidConfig mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeRulesRefusesEveryPatternThatCanMatchEmpty(t *testing.T) {
	cases := map[string]bool{
		`\b`:                           true,
		`a|\b`:                         true,
		`\S+|\b`:                       true,
		`[a-z]+\b|\b`:                  true,
		`a*`:                           true,
		`(a|)`:                         true,
		`\b(?:\d{3}-\d{2}-\d{4})?\b`:   true,
		`$`:                            true,
		`(?m)^`:                        true,
		`x?\b`:                         true,
		`(?:)`:                         true,
		`\d+`:                          false,
		`bot\d{6,}:[A-Za-z0-9_-]{30,}`: false,
		`^secret$`:                     false,
		`(?i)password=\S+`:             false,
		`(?m)^token:.*$`:               false,
		`(?i)\bsk_live_\w+`:            false,
		`\bfoo\b`:                      false,
		`(a|b)+`:                       false,
	}
	for pattern, wantRefused := range cases {
		t.Run(pattern, func(t *testing.T) {
			_, err := NormalizeRules([]Rule{{Name: "x", Pattern: pattern}})
			if !wantRefused {
				if err != nil {
					t.Fatalf("NormalizeRules(%q) = %v, want accepted", pattern, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "empty") {
				t.Fatalf("NormalizeRules(%q) = %v, want an empty-match error", pattern, err)
			}
		})
	}
}

// Mirrors the presets in web/src/features/settings/scrubRules.ts: each must be
// valid RE2 and redact its own example.
func TestWebPresetsAreValidRE2AndRedactTheirExamples(t *testing.T) {
	cases := map[string]struct {
		rule          Rule
		example, want string
	}{
		"telegram": {
			Rule{Name: "Telegram bot token", Pattern: `bot\d{6,}:[A-Za-z0-9_-]{30,}`, Replacement: "bot[Filtered]"},
			"https://api.telegram.org/bot123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g/getMe",
			"https://api.telegram.org/bot[Filtered]/getMe",
		},
		"aws access key id": {
			Rule{Name: "AWS access key ID", Pattern: `\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`},
			"key AKIAIOSFODNN7EXAMPLE in config",
			"key [Filtered] in config",
		},
		"github token": {
			Rule{Name: "GitHub token", Pattern: `\bgh[pousr]_[A-Za-z0-9]{36,}\b`},
			"token ghp_abcdefghijklmnopqrstuvwxyz0123456789 leaked",
			"token [Filtered] leaked",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rules, err := NormalizeRules([]Rule{tc.rule})
			if err != nil {
				t.Fatalf("NormalizeRules: %v", err)
			}
			config := DefaultConfig()
			config.Rules = rules
			if got := mustNew(t, config).String(tc.example); got != tc.want {
				t.Fatalf("String = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNewRefusesARuleThatDoesNotCompile(t *testing.T) {
	bad := []Rule{{Name: "bad", Pattern: `(`}}
	config := DefaultConfig()
	config.Rules = bad
	if _, err := New(config); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NewWithGlobal(DefaultConfig(), bad); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("NewWithGlobal error = %v, want ErrInvalidConfig", err)
	}
}

func TestNormalizeRulesBoundsTheCount(t *testing.T) {
	rules := make([]Rule, MaxRules+1)
	for i := range rules {
		rules[i] = Rule{Name: "r", Pattern: `a`}
	}
	if _, err := NormalizeRules(rules); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
	if _, err := NormalizeRules(rules[:MaxRules]); err != nil {
		t.Fatalf("MaxRules rules refused: %v", err)
	}
}

func TestNormalizeRulesReturnsNonNilAndTrimsNames(t *testing.T) {
	empty, err := NormalizeRules(nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("NormalizeRules(nil) = %#v, %v; want empty non-nil", empty, err)
	}
	got, err := NormalizeRules([]Rule{{Name: "  Telegram ", Pattern: `a`}})
	if err != nil || got[0].Name != "Telegram" {
		t.Fatalf("NormalizeRules = %#v, %v; want trimmed name", got, err)
	}
}

func TestConfigNormalizeValidatesRules(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "bad", Pattern: `(`}}
	if _, err := config.Normalize(); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
	if rules := DefaultConfig().Rules; rules == nil || len(rules) != 0 {
		t.Fatalf("DefaultConfig().Rules = %#v, want empty non-nil", rules)
	}
}

// Regexp.Expand reads an unbraced name as the longest run of Unicode letters,
// digits and underscores, so the validator must read the same run.
func TestGroupReferencesAreReadTheWayExpandReadsThem(t *testing.T) {
	cases := map[string]struct {
		replacement string
		wantErr     string
	}{
		"number then accented letter": {"$1é", "${1}"},
		"number then ascii letter":    {"$1x", "${1}"},
		"accented letter name":        {"$é", "unknown group"},
		"cjk name":                    {"$名", "unknown group"},
		"braced number then letter":   {"${1}é", ""},
		"number at end":               {"acct $1", ""},
		"number before dash":          {"$1-", ""},
		"number before space":         {"$1 ", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NormalizeRules([]Rule{{Name: "x", Pattern: `(acct-)\d+`, Replacement: tc.replacement}})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("NormalizeRules(%q) = %v, want accepted", tc.replacement, err)
				}
				return
			}
			if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("NormalizeRules(%q) = %v, want ErrInvalidConfig mentioning %q", tc.replacement, err, tc.wantErr)
			}
		})
	}
}
