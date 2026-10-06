// Package scrub redacts secrets from event payloads before they are stored.
package scrub

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Filtered replaces every redacted value.
const Filtered = "[Filtered]"

const (
	// MaxFields bounds each of a project's field lists.
	MaxFields = 50
	// MaxFieldNameBytes bounds one field name.
	MaxFieldNameBytes = 64
)

// defaultSensitiveNames are matched as case-insensitive substrings of a key.
// The first twelve are the names Sentry's default scrubber documents; "cookie"
// and "api-key" are added because Cookie, Set-Cookie and X-Api-Key headers
// carry session and API secrets under names the documented list misses.
var defaultSensitiveNames = []string{
	"password", "secret", "passwd", "api_key", "apikey", "auth", "credentials",
	"mysql_pwd", "privatekey", "private_key", "token", "bearer", "cookie", "api-key",
}

const (
	minCardDigits = 13
	maxCardDigits = 19
)

// cardPattern finds a run of 13-19 digits with optional single space or dash
// separators; Luhn validation then decides whether it is really a card number.
var cardPattern = regexp.MustCompile(`\b\d(?:[ -]?\d){12,18}\b`)

// ErrInvalidConfig marks a configuration that failed validation.
var ErrInvalidConfig = errors.New("invalid data scrubbing configuration")

// Config is a project's scrubbing settings.
type Config struct {
	Enabled     bool     `json:"enabled"`
	ExtraFields []string `json:"extra_fields"`
	SafeFields  []string `json:"safe_fields"`
	Rules       []Rule   `json:"rules"`
}

// DefaultConfig is what a project without stored settings, or whose settings
// could not be read, is scrubbed with.
func DefaultConfig() Config {
	return Config{Enabled: true, ExtraFields: []string{}, SafeFields: []string{}, Rules: []Rule{}}
}

// Normalize validates the field lists and returns them trimmed, lowercased and
// deduplicated, always as non-nil slices.
func (c Config) Normalize() (Config, error) {
	extra, err := normalizeFields(c.ExtraFields)
	if err != nil {
		return Config{}, fmt.Errorf("%w: extra fields: %w", ErrInvalidConfig, err)
	}
	safe, err := normalizeFields(c.SafeFields)
	if err != nil {
		return Config{}, fmt.Errorf("%w: safe fields: %w", ErrInvalidConfig, err)
	}
	rules, err := NormalizeRules(c.Rules)
	if err != nil {
		return Config{}, err
	}
	return Config{Enabled: c.Enabled, ExtraFields: extra, SafeFields: safe, Rules: rules}, nil
}

func normalizeFields(fields []string) ([]string, error) {
	if len(fields) > MaxFields {
		return nil, fmt.Errorf("more than %d entries", MaxFields)
	}
	normalized := make([]string, 0, len(fields))
	for _, field := range fields {
		name := strings.ToLower(strings.TrimSpace(field))
		if name == "" || len(name) > MaxFieldNameBytes {
			return nil, fmt.Errorf("entries must be 1-%d bytes", MaxFieldNameBytes)
		}
		if !slices.Contains(normalized, name) {
			normalized = append(normalized, name)
		}
	}
	return normalized, nil
}

// Scrubber redacts secrets using one project's configuration and the
// instance-wide rules. Build it once per settings snapshot and reuse it: the
// name lists are lowercased and the rules compiled at construction, not per
// event.
type Scrubber struct {
	enabled   bool
	sensitive []string
	safe      []string
	global    []compiledRule
	project   []compiledRule
}

// New builds a Scrubber from config alone.
func New(config Config) (*Scrubber, error) {
	return NewWithGlobal(config, nil)
}

// NewDefault builds the Scrubber for DefaultConfig. It has no rules to
// compile, so unlike New it cannot fail.
func NewDefault() *Scrubber {
	return newScrubber(DefaultConfig(), nil, nil)
}

// NewWithGlobal builds a Scrubber that also applies the instance-wide rules.
// A rule that does not compile is an ErrInvalidConfig error rather than being
// skipped, so a protective rule can never be dropped silently.
func NewWithGlobal(config Config, global []Rule) (*Scrubber, error) {
	compiledGlobal, err := compileRules(global)
	if err != nil {
		return nil, fmt.Errorf("%w: global %w", ErrInvalidConfig, err)
	}
	compiledProject, err := compileRules(config.Rules)
	if err != nil {
		return nil, fmt.Errorf("%w: project %w", ErrInvalidConfig, err)
	}
	return newScrubber(config, compiledGlobal, compiledProject), nil
}

func newScrubber(config Config, global, project []compiledRule) *Scrubber {
	sensitive := make([]string, 0, len(defaultSensitiveNames)+len(config.ExtraFields))
	sensitive = append(sensitive, defaultSensitiveNames...)
	for _, field := range config.ExtraFields {
		sensitive = append(sensitive, strings.ToLower(field))
	}
	safe := make([]string, len(config.SafeFields))
	for index, field := range config.SafeFields {
		safe[index] = strings.ToLower(field)
	}
	return &Scrubber{enabled: config.Enabled, sensitive: sensitive, safe: safe, global: global, project: project}
}

func compileRules(rules []Rule) ([]compiledRule, error) {
	compiled := make([]compiledRule, 0, len(rules))
	for index, rule := range rules {
		c, err := rule.compile()
		if err != nil {
			return nil, fmt.Errorf("rule %d: %w", index+1, err)
		}
		compiled = append(compiled, c)
	}
	return compiled, nil
}

// minGrowthAllowance is the least a string may grow while rules rewrite it.
const minGrowthAllowance = 4096

// maxRuleMatchInts bounds the submatch indexes one rule may collect over a
// string before any replacement: each match holds 2*(NumSubexp+1) ints, 8 bytes
// each, plus a 24-byte slice header, and the group count is bounded only by
// the pattern's length. A rule may therefore match at most
// maxRuleMatchInts/(2*(NumSubexp+1)) times (50,000 without capture groups, at
// least once), and past that the string is replaced by Filtered. The indexes
// then hold at most 800 KB of ints plus 1.2 MB of headers per string, the
// worst case being the rule without groups; with the regexp's own working
// memory and slice growth that rule allocates about 7 MiB in total per string.
const maxRuleMatchInts = 100_000

// growthLimit bounds the size rules may grow a string to: its own length plus
// the larger of that length and minGrowthAllowance. Without it a replacement
// that a later rule matches again multiplies the text with every rule.
func growthLimit(text string) int {
	return len(text) + max(len(text), minGrowthAllowance)
}

// payloadGrowthAllowance is how much rule-driven growth a payload may have
// beyond its own size, summed over all of its strings. The per-string limit
// alone would let many tiny strings each grow by minGrowthAllowance.
const payloadGrowthAllowance = 64 << 10

// growthBudget is the growth one call may still spend, from rules and from
// strings that fail closed to Filtered. It belongs to the call and never to the
// Scrubber, which goroutines share. Failing closed happens even when the budget
// is spent, so a call's total growth is bounded by the budget plus at most
// len(Filtered) bytes per string that fails closed.
type growthBudget struct {
	remaining int
}

func payloadBudget(payload []byte) *growthBudget {
	return &growthBudget{remaining: len(payload) + payloadGrowthAllowance}
}

// stringBudget leaves a lone string with only its own growthLimit.
func stringBudget(text string) *growthBudget {
	return &growthBudget{remaining: growthLimit(text) - len(text)}
}

func (b *growthBudget) limitFor(text string) int {
	return min(growthLimit(text), len(text)+b.remaining)
}

func (b *growthBudget) spend(before, after string) {
	b.remaining = max(0, b.remaining-max(0, len(after)-len(before)))
}

func (b *growthBudget) failClosed(text string) (string, bool) {
	b.spend(text, Filtered)
	return Filtered, true
}

// applyRules rewrites every non-empty match of each rule. It reports overflow
// when the text would grow past limit, and the caller must then discard it.
func applyRules(text string, rules []compiledRule, limit int) (result string, changed, overflow bool) {
	for _, rule := range rules {
		replaced, ok := rule.replaceAll(text, limit)
		if !ok {
			return text, changed, true
		}
		if replaced != text {
			text, changed = replaced, true
		}
	}
	return text, changed, false
}

// replaceAll replaces the non-empty matches of the rule in text. It returns
// text itself when no non-empty match was replaced, the rewritten text (which
// may be empty) otherwise, and false when the rule matches more than its
// matchLimit times or an expansion could take the result past limit. Each
// expansion is bounded before it is written: every group reference copies at
// most the whole match.
func (r compiledRule) replaceAll(text string, limit int) (string, bool) {
	matches := r.pattern.FindAllStringSubmatchIndex(text, r.matchLimit+1)
	if len(matches) > r.matchLimit {
		return text, false
	}
	var out []byte
	replaced := false
	last := 0
	for _, m := range matches {
		if m[0] == m[1] {
			continue
		}
		expansionBound := len(r.replacement) + r.refs*(m[1]-m[0])
		if len(out)+m[0]-last+expansionBound+len(text)-m[1] > limit {
			return text, false
		}
		out = append(out, text[last:m[0]]...)
		out = r.pattern.ExpandString(out, r.replacement, text, m)
		last = m[1]
		replaced = true
	}
	if !replaced {
		return text, true
	}
	return string(append(out, text[last:]...)), true
}

// active reports whether the scrubber can change anything: global rules apply
// even when the project has scrubbing off.
func (s *Scrubber) active() bool { return s.enabled || len(s.global) > 0 }

func (s *Scrubber) isSensitiveKey(key string) bool {
	lowered := strings.ToLower(key)
	if slices.Contains(s.safe, lowered) {
		return false
	}
	return slices.ContainsFunc(s.sensitive, func(name string) bool { return strings.Contains(lowered, name) })
}

// clientIPKeys are the request fields that carry the caller's address, compared
// after lowercasing, mapping "-" to "_" and dropping a CGI "http_" prefix.
var clientIPKeys = []string{"remote_addr", "x_forwarded_for", "x_real_ip"}

func isClientIPKey(key string) bool {
	normalized := strings.TrimPrefix(strings.ReplaceAll(strings.ToLower(key), "-", "_"), "http_")
	return slices.Contains(clientIPKeys, normalized)
}

// redacts reports whether the value under key must be replaced. Client IP
// fields follow the project's store_user_ip choice, so the safe list does not
// exempt them.
func (s *Scrubber) redacts(key string, redactIP bool) bool {
	return s.enabled && (s.isSensitiveKey(key) || redactIP && isClientIPKey(key))
}

// RedactsKey reports whether a value stored under key, such as an event tag,
// must be replaced outright: the key is sensitive and not safe-listed, and
// project scrubbing is on.
func (s *Scrubber) RedactsKey(key string) bool { return s.redacts(key, false) }

// Payload returns payload with sensitive values redacted and the scrubbing
// rules applied to every string in it. When redactIP is set, the event's
// user.ip_address and the client address fields of the request (REMOTE_ADDR,
// X-Forwarded-For, X-Real-IP) are redacted too. With project scrubbing off only
// the instance-wide rules apply. Rule-driven growth is bounded per string and
// across the payload, and a string past either bound becomes Filtered. A
// payload with nothing to redact is returned byte for byte; an unparseable one
// yields an error and the caller keeps the original.
func (s *Scrubber) Payload(payload json.RawMessage, redactIP bool) (json.RawMessage, error) {
	if !s.active() {
		return payload, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var root any
	if err := decoder.Decode(&root); err != nil {
		return nil, fmt.Errorf("decode payload: %w", err)
	}
	changed := false
	if tree, ok := root.(map[string]any); ok && redactIP && s.enabled {
		if user, ok := tree["user"].(map[string]any); ok {
			if _, present := user["ip_address"]; present {
				user["ip_address"] = Filtered
				changed = true
			}
		}
	}
	root, walked := s.walk(root, redactIP, payloadBudget(payload))
	if !changed && !walked {
		return payload, nil
	}
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(root); err != nil {
		return nil, fmt.Errorf("encode payload: %w", err)
	}
	return bytes.TrimRight(out.Bytes(), "\n"), nil
}

// String redacts secrets in a free-text value such as an event message: the
// scrubbing rules, card numbers, bearer tokens, and the value of any sensitive
// name=value pair.
func (s *Scrubber) String(text string) string {
	if !s.active() {
		return text
	}
	scrubbed, _ := s.scrubText(text, stringBudget(text))
	return scrubbed
}

func (s *Scrubber) walk(value any, redactIP bool, budget *growthBudget) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for key, child := range typed {
			if s.redacts(key, redactIP) {
				typed[key] = Filtered
				changed = true
				continue
			}
			replaced, childChanged := s.walk(child, redactIP, budget)
			typed[key] = replaced
			changed = changed || childChanged
		}
		return typed, changed
	case []any:
		if key, secret, ok := headerPair(typed); ok && s.redacts(key, redactIP) && secret != Filtered {
			typed[1] = Filtered
			return typed, true
		}
		changed := false
		for index, child := range typed {
			replaced, childChanged := s.walk(child, redactIP, budget)
			typed[index] = replaced
			changed = changed || childChanged
		}
		return typed, changed
	case string:
		return s.scrubText(typed, budget)
	default:
		return value, false
	}
}

// headerPair recognises the [name, value] form some SDKs use for headers.
func headerPair(items []any) (string, string, bool) {
	if len(items) != 2 {
		return "", "", false
	}
	name, nameOK := items[0].(string)
	secret, secretOK := items[1].(string)
	return name, secret, nameOK && secretOK
}

// scrubText fails the whole string closed to Filtered when a rule matches it
// more than its matchLimit times, or when the rules would grow it past its own
// limit or past what is left of budget.
func (s *Scrubber) scrubText(text string, budget *growthBudget) (string, bool) {
	limit := budget.limitFor(text)
	scrubbed, globalChanged, overflow := applyRules(text, s.global, limit)
	if overflow {
		return budget.failClosed(text)
	}
	if !s.enabled {
		budget.spend(text, scrubbed)
		return scrubbed, globalChanged
	}
	scrubbed, projectChanged, overflow := applyRules(scrubbed, s.project, limit)
	if overflow {
		return budget.failClosed(text)
	}
	budget.spend(text, scrubbed)
	scrubbed, cardsChanged := scrubCards(scrubbed)
	scrubbed, bearerChanged := scrubBearerTokens(scrubbed)
	scrubbed, pairsChanged := s.scrubSecretPairs(scrubbed)
	return scrubbed, globalChanged || projectChanged || cardsChanged || bearerChanged || pairsChanged
}

// bearerPattern finds "Bearer <token>". The token must be at least 16 token
// characters so prose such as "invalid Bearer token" is left alone.
var bearerPattern = regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/=-]{16,}`)

func scrubBearerTokens(text string) (string, bool) {
	if !strings.Contains(strings.ToLower(text), "bearer") {
		return text, false
	}
	scrubbed := bearerPattern.ReplaceAllString(text, "${1}"+Filtered)
	return scrubbed, scrubbed != text
}

// pairPattern finds name=value in URL query strings, form bodies and log lines.
// The value ends at whitespace, a quote or a separator, so a quoted value is
// left alone.
var pairPattern = regexp.MustCompile(`([A-Za-z0-9_.\-\[\]]+)=([^\s&#"';,]+)`)

func (s *Scrubber) scrubSecretPairs(text string) (string, bool) {
	if !strings.Contains(text, "=") {
		return text, false
	}
	changed := false
	scrubbed := pairPattern.ReplaceAllStringFunc(text, func(pair string) string {
		name, _, _ := strings.Cut(pair, "=")
		if !s.isSensitiveKey(name) {
			return pair
		}
		changed = true
		return name + "=" + Filtered
	})
	return scrubbed, changed
}

func scrubCards(text string) (string, bool) {
	changed := false
	scrubbed := cardPattern.ReplaceAllStringFunc(text, func(candidate string) string {
		redacted, found := redactLeadingCard(candidate)
		changed = changed || found
		return redacted
	})
	return scrubbed, changed
}

// redactLeadingCard redacts the longest Luhn-valid 13-19 digit prefix of a
// candidate. The pattern is greedy, so a card followed by a space and a CVV
// arrives as one over-long run that fails Luhn as a whole while its leading
// digits are a real card number.
func redactLeadingCard(candidate string) (string, bool) {
	var ends []int
	for index := range len(candidate) {
		if candidate[index] >= '0' && candidate[index] <= '9' {
			ends = append(ends, index+1)
		}
	}
	for digits := min(len(ends), maxCardDigits); digits >= minCardDigits; digits-- {
		end := ends[digits-1]
		if luhnValid(candidate[:end]) {
			return Filtered + candidate[end:], true
		}
	}
	return candidate, false
}

func luhnValid(candidate string) bool {
	sum, double := 0, false
	for index := len(candidate) - 1; index >= 0; index-- {
		char := candidate[index]
		if char < '0' || char > '9' {
			continue
		}
		digit := int(char - '0')
		if double {
			digit *= 2
			if digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		double = !double
	}
	return sum%10 == 0
}
