package scrub

import (
	"encoding/json"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"unsafe"
)

func mustNew(tb testing.TB, config Config) *Scrubber {
	tb.Helper()
	return mustNewWithGlobal(tb, config, nil)
}

func mustNewWithGlobal(tb testing.TB, config Config, global []Rule) *Scrubber {
	tb.Helper()
	scrubber, err := NewWithGlobal(config, global)
	if err != nil {
		tb.Fatalf("NewWithGlobal: %v", err)
	}
	return scrubber
}

func scrubbed(t *testing.T, payload string, config Config, redactIP bool) map[string]any {
	t.Helper()
	out, err := mustNew(t, config).Payload([]byte(payload), redactIP)
	if err != nil {
		t.Fatalf("Payload error: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("output is not JSON: %v: %s", err, out)
	}
	return decoded
}

func TestPayloadRedactsSensitiveKeysAtAnyDepth(t *testing.T) {
	out := scrubbed(t, `{"request":{"data":{"password":"hunter2","name":"ann"}},"extra":{"items":[{"api_key":"k"},{"ok":1}]}}`, DefaultConfig(), false)
	data := out["request"].(map[string]any)["data"].(map[string]any)
	if data["password"] != Filtered || data["name"] != "ann" {
		t.Fatalf("request.data = %v", data)
	}
	items := out["extra"].(map[string]any)["items"].([]any)
	if items[0].(map[string]any)["api_key"] != Filtered || items[1].(map[string]any)["ok"] != float64(1) {
		t.Fatalf("items = %v", items)
	}
}

func TestPayloadMatchesKeysCaseInsensitivelyAsSubstring(t *testing.T) {
	out := scrubbed(t, `{"X-Api-Key":"a","db_PASSWORD":"b","Cookie":"c","Set-Cookie":"d","harmless":"e"}`, DefaultConfig(), false)
	for _, key := range []string{"X-Api-Key", "db_PASSWORD", "Cookie", "Set-Cookie"} {
		if out[key] != Filtered {
			t.Errorf("%s = %v, want filtered", key, out[key])
		}
	}
	if out["harmless"] != "e" {
		t.Errorf("harmless = %v", out["harmless"])
	}
}

func TestPayloadReplacesWholeContainerUnderSensitiveKey(t *testing.T) {
	out := scrubbed(t, `{"credentials":{"user":"u","pass":"p"}}`, DefaultConfig(), false)
	if out["credentials"] != Filtered {
		t.Fatalf("credentials = %v", out["credentials"])
	}
}

func TestPayloadRedactsHeaderPairs(t *testing.T) {
	out := scrubbed(t, `{"request":{"headers":[["Authorization","Bearer x"],["Accept","json"]]}}`, DefaultConfig(), false)
	headers := out["request"].(map[string]any)["headers"].([]any)
	if headers[0].([]any)[1] != Filtered || headers[1].([]any)[1] != "json" {
		t.Fatalf("headers = %v", headers)
	}
}

func TestPayloadRedactsCardNumbersInStrings(t *testing.T) {
	out := scrubbed(t, `{"message":"charge 4111 1111 1111 1111 failed","order":"1234567890123456","note":"ref 4111-1111-1111-1112"}`, DefaultConfig(), false)
	if out["message"] != "charge [Filtered] failed" {
		t.Errorf("message = %v", out["message"])
	}
	if out["order"] != "1234567890123456" {
		t.Errorf("Luhn-invalid number must survive, got %v", out["order"])
	}
	if out["note"] != "ref 4111-1111-1111-1112" {
		t.Errorf("Luhn-invalid dashed number must survive, got %v", out["note"])
	}
}

func TestPayloadSafeFieldsAreNeverScrubbed(t *testing.T) {
	config := DefaultConfig()
	config.SafeFields = []string{"Token"}
	out := scrubbed(t, `{"token":"visible","password":"hidden"}`, config, false)
	if out["token"] != "visible" || out["password"] != Filtered {
		t.Fatalf("out = %v", out)
	}
}

func TestPayloadExtraFieldsAreScrubbed(t *testing.T) {
	config := DefaultConfig()
	config.ExtraFields = []string{"SSN"}
	out := scrubbed(t, `{"user_ssn":"123"}`, config, false)
	if out["user_ssn"] != Filtered {
		t.Fatalf("out = %v", out)
	}
}

func TestPayloadRedactsUserIPOnlyWhenAsked(t *testing.T) {
	const payload = `{"user":{"id":"7","ip_address":"10.0.0.1"}}`
	kept := scrubbed(t, payload, DefaultConfig(), false)["user"].(map[string]any)
	if kept["ip_address"] != "10.0.0.1" {
		t.Fatalf("ip must be kept when not asked: %v", kept)
	}
	redacted := scrubbed(t, payload, DefaultConfig(), true)["user"].(map[string]any)
	if redacted["ip_address"] != Filtered || redacted["id"] != "7" {
		t.Fatalf("user = %v", redacted)
	}
}

func TestPayloadDisabledReturnsInputUnchanged(t *testing.T) {
	const payload = `{"password":"x","user":{"ip_address":"1.1.1.1"}}`
	out, err := mustNew(t, Config{Enabled: false}).Payload([]byte(payload), true)
	if err != nil || string(out) != payload {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestPayloadWithNothingToScrubKeepsOriginalBytes(t *testing.T) {
	const payload = `{"b":1,  "a":"<x>"}`
	out, err := mustNew(t, DefaultConfig()).Payload([]byte(payload), false)
	if err != nil || string(out) != payload {
		t.Fatalf("out = %s, err = %v", out, err)
	}
}

func TestPayloadPreservesLargeIntegersAndMarkup(t *testing.T) {
	out, err := mustNew(t, DefaultConfig()).Payload([]byte(`{"password":"x","n":12345678901234567890,"h":"<b>&"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"h":"<b>&","n":12345678901234567890,"password":"[Filtered]"}` {
		t.Fatalf("out = %s", out)
	}
}

func TestPayloadRejectsNonJSONWithoutOutput(t *testing.T) {
	if _, err := mustNew(t, DefaultConfig()).Payload([]byte(`not json`), false); err == nil {
		t.Fatal("want error")
	}
}

func TestStringRedactsACardFollowedByDigits(t *testing.T) {
	for name, text := range map[string]string{
		"cvv after space": "pay 4111 1111 1111 1111 123 now",
		"cvv after dash":  "pay 4111111111111111-123 now",
	} {
		t.Run(name, func(t *testing.T) {
			got := mustNew(t, DefaultConfig()).String(text)
			if strings.Contains(got, "4111") || !strings.Contains(got, Filtered) {
				t.Fatalf("got %q, want the card number redacted", got)
			}
		})
	}
}

func TestStringRedactsCardNumbers(t *testing.T) {
	if got := mustNew(t, DefaultConfig()).String("pay 4111111111111111 now"); got != "pay [Filtered] now" {
		t.Fatalf("got %q", got)
	}
	if got := mustNew(t, Config{}).String("pay 4111111111111111 now"); got != "pay 4111111111111111 now" {
		t.Fatalf("disabled got %q", got)
	}
}

func TestConfigNormalizeValidates(t *testing.T) {
	cases := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{"empty ok", Config{Enabled: true}, false},
		{"blank entry", Config{ExtraFields: []string{"  "}}, true},
		{"too long entry", Config{SafeFields: []string{string(make([]byte, MaxFieldNameBytes+1))}}, true},
		{"too many entries", Config{ExtraFields: make([]string, MaxFields+1)}, true},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := testCase.config.Normalize()
			if (err != nil) != testCase.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, testCase.wantErr)
			}
		})
	}
}

func TestConfigNormalizeTrimsLowercasesAndDeduplicates(t *testing.T) {
	got, err := Config{Enabled: true, ExtraFields: []string{" SSN ", "ssn", "Pin"}}.Normalize()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.ExtraFields) != 2 || got.ExtraFields[0] != "ssn" || got.ExtraFields[1] != "pin" {
		t.Fatalf("ExtraFields = %v", got.ExtraFields)
	}
	if got.SafeFields == nil {
		t.Fatal("SafeFields must be a non-nil empty slice so it serializes as []")
	}
}

func TestStringRedactsSecretsEmbeddedInText(t *testing.T) {
	cases := map[string]struct{ text, want string }{
		"url query token":        {"GET /cb?state=x&token=abc123&page=2 failed", "GET /cb?state=x&token=[Filtered]&page=2 failed"},
		"password in message":    {"login failed password=hunter2 for ann", "login failed password=[Filtered] for ann"},
		"compound key":           {"db_password=s3cret;host=db", "db_password=[Filtered];host=db"},
		"bearer token":           {"sent Authorization Bearer eyJhbGciOiJIUzI1NiJ9.abc.def to api", "sent Authorization Bearer [Filtered] to api"},
		"harmless pairs kept":    {"GET /list?page=2&sort=name", "GET /list?page=2&sort=name"},
		"prose about bearer":     {"invalid Bearer token supplied", "invalid Bearer token supplied"},
		"empty value kept":       {"token= and password=", "token= and password="},
		"quoted value untouched": {`password="x"`, `password="x"`},
	}
	scrubber := mustNew(t, DefaultConfig())
	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			if got := scrubber.String(testCase.text); got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestStringEmbeddedSecretsHonourSafeAndExtraFields(t *testing.T) {
	config := DefaultConfig()
	config.SafeFields = []string{"token"}
	config.ExtraFields = []string{"ssn"}
	got := mustNew(t, config).String("?token=visible&user_ssn=123&password=hidden")
	if got != "?token=visible&user_ssn=[Filtered]&password=[Filtered]" {
		t.Fatalf("got %q", got)
	}
}

func TestPayloadRedactsSecretsEmbeddedInStringValues(t *testing.T) {
	out := scrubbed(t, `{"request":{"url":"https://x.test/cb?token=abc&ok=1","query_string":"password=p&ok=1"},"message":"Bearer abcdefghijklmnopqrstuv"}`, DefaultConfig(), false)
	request := out["request"].(map[string]any)
	if request["url"] != "https://x.test/cb?token=[Filtered]&ok=1" || request["query_string"] != "password=[Filtered]&ok=1" {
		t.Fatalf("request = %v", request)
	}
	if out["message"] != "Bearer [Filtered]" {
		t.Fatalf("message = %v", out["message"])
	}
}

func TestPayloadRedactsClientIPsOutsideTheUserOnlyWhenAsked(t *testing.T) {
	const payload = `{"request":{"env":{"REMOTE_ADDR":"10.0.0.1","SERVER_NAME":"x"},"headers":{"X-Forwarded-For":"10.0.0.1, 10.0.0.2","x-real-ip":"10.0.0.1","Accept":"json"}}}`
	kept := scrubbed(t, payload, DefaultConfig(), false)["request"].(map[string]any)
	if kept["env"].(map[string]any)["REMOTE_ADDR"] != "10.0.0.1" {
		t.Fatalf("ip must be kept when not asked: %v", kept)
	}
	redacted := scrubbed(t, payload, DefaultConfig(), true)["request"].(map[string]any)
	env := redacted["env"].(map[string]any)
	headers := redacted["headers"].(map[string]any)
	if env["REMOTE_ADDR"] != Filtered || env["SERVER_NAME"] != "x" {
		t.Fatalf("env = %v", env)
	}
	if headers["X-Forwarded-For"] != Filtered || headers["x-real-ip"] != Filtered || headers["Accept"] != "json" {
		t.Fatalf("headers = %v", headers)
	}
}

func TestPayloadRedactsClientIPHeaderPairsWhenAsked(t *testing.T) {
	out := scrubbed(t, `{"request":{"headers":[["X-Forwarded-For","10.0.0.1"],["Accept","json"]]}}`, DefaultConfig(), true)
	headers := out["request"].(map[string]any)["headers"].([]any)
	if headers[0].([]any)[1] != Filtered || headers[1].([]any)[1] != "json" {
		t.Fatalf("headers = %v", headers)
	}
}

func TestPayloadRedactsClientIPsEvenWhenSensitiveScrubbingIsSafeListed(t *testing.T) {
	config := DefaultConfig()
	config.SafeFields = []string{"remote_addr"}
	out := scrubbed(t, `{"env":{"REMOTE_ADDR":"10.0.0.1"}}`, config, true)
	if out["env"].(map[string]any)["REMOTE_ADDR"] != Filtered {
		t.Fatalf("out = %v", out)
	}
}

const benchmarkPayload = `{"message":"GET /cb?token=abc&page=2 failed","request":{"url":"https://x.test/a?b=1","headers":{"Accept":"json","Authorization":"Bearer abcdefghijklmnopqrstuv"},"env":{"REMOTE_ADDR":"10.0.0.1"}},"user":{"id":"7","ip_address":"10.0.0.1"},"exception":{"values":[{"type":"Error","value":"boom","stacktrace":{"frames":[{"filename":"app.js","lineno":10,"function":"run"},{"filename":"lib.js","lineno":20,"function":"call"}]}}]}}`

func BenchmarkScrubberPayload(b *testing.B) {
	scrubber := mustNew(b, DefaultConfig())
	b.ReportAllocs()
	for b.Loop() {
		if _, err := scrubber.Payload([]byte(benchmarkPayload), true); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScrubberPayloadWithRules(b *testing.B) {
	rules := func(prefix string) []Rule {
		out := make([]Rule, 25)
		for i := range out {
			out[i] = Rule{Name: fmt.Sprintf("%s-%d", prefix, i), Pattern: fmt.Sprintf("tenant-%d-[a-z0-9]{8}", i)}
		}
		return out
	}
	config := DefaultConfig()
	config.Rules = rules("project")
	scrubber := mustNewWithGlobal(b, config, rules("global"))
	b.ReportAllocs()
	for b.Loop() {
		if _, err := scrubber.Payload([]byte(benchmarkPayload), true); err != nil {
			b.Fatal(err)
		}
	}
}

const telegramToken = "123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g"

var telegramRule = Rule{Name: "Telegram bot token", Pattern: `bot\d{6,}:[A-Za-z0-9_-]{30,}`, Replacement: "bot[Filtered]"}

func TestPayloadAppliesRulesEverywhereAStringAppears(t *testing.T) {
	payload := `{
		"breadcrumbs":{"values":[{"category":"http","data":{"url":"https://api.telegram.org/bot` + telegramToken + `/sendMessage"}}]},
		"request":{"url":"https://api.telegram.org/bot` + telegramToken + `/getMe"},
		"exception":{"values":[{"value":"POST https://api.telegram.org/bot` + telegramToken + `/sendMessage failed"}]}}`
	config := DefaultConfig()
	config.Rules = []Rule{telegramRule}

	out, err := mustNew(t, config).Payload([]byte(payload), false)
	if err != nil {
		t.Fatalf("Payload error: %v", err)
	}
	if strings.Contains(string(out), "AAEhBP0av28") {
		t.Fatalf("token survived: %s", out)
	}
	for _, want := range []string{
		`https://api.telegram.org/bot[Filtered]/sendMessage`,
		`https://api.telegram.org/bot[Filtered]/getMe`,
		`POST https://api.telegram.org/bot[Filtered]/sendMessage failed`,
	} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %q: %s", want, out)
		}
	}
}

func TestStringAppliesRules(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{telegramRule}
	got := mustNew(t, config).String("GET /bot" + telegramToken + "/getMe")
	if got != "GET /bot[Filtered]/getMe" {
		t.Fatalf("String = %q", got)
	}
}

func TestRuleReplacementExpandsGroupsAndDefaultsToFiltered(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{
		{Name: "keep prefix", Pattern: `(acct-)\d+`, Replacement: "${1}[Filtered]"},
		{Name: "default", Pattern: `ssn:\d{9}`},
	}
	got := mustNew(t, config).String("acct-42 ssn:123456789")
	if got != "acct-[Filtered] [Filtered]" {
		t.Fatalf("String = %q", got)
	}
}

func TestGlobalRulesRunBeforeProjectRules(t *testing.T) {
	global := []Rule{{Name: "global", Pattern: `alpha`, Replacement: "beta"}}
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "project", Pattern: `beta`, Replacement: "gamma"}}

	if got := mustNewWithGlobal(t, config, global).String("alpha"); got != "gamma" {
		t.Fatalf("String = %q, want gamma (global then project)", got)
	}
}

func TestGlobalRulesApplyWhenProjectScrubbingIsOff(t *testing.T) {
	global := []Rule{telegramRule}
	config := Config{Enabled: false, ExtraFields: []string{}, SafeFields: []string{}, Rules: []Rule{{Name: "project", Pattern: `ssn:\d+`}}}
	scrubber := mustNewWithGlobal(t, config, global)

	text := "bot" + telegramToken + " ssn:123 password=hunter2"
	if got := scrubber.String(text); got != "bot[Filtered] ssn:123 password=hunter2" {
		t.Fatalf("String = %q: want only the global rule applied", got)
	}
	out, err := scrubber.Payload([]byte(`{"password":"p","url":"x/bot`+telegramToken+`","user":{"ip_address":"1.2.3.4"}}`), true)
	if err != nil {
		t.Fatalf("Payload error: %v", err)
	}
	if string(out) != `{"password":"p","url":"x/bot[Filtered]","user":{"ip_address":"1.2.3.4"}}` {
		t.Fatalf("Payload = %s", out)
	}
}

func TestDisabledScrubbingWithoutGlobalRulesStillReturnsTheInputUnchanged(t *testing.T) {
	config := Config{Enabled: false, Rules: []Rule{telegramRule}}
	payload := []byte(`{"url":"bot` + telegramToken + `"}`)
	out, err := mustNew(t, config).Payload(payload, false)
	if err != nil || string(out) != string(payload) {
		t.Fatalf("Payload = %s, %v; want the input unchanged", out, err)
	}
}

func TestPayloadWithRulesButNothingMatchingKeepsOriginalBytes(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{telegramRule}
	payload := []byte(`{"a":  "b" }`)
	out, err := mustNew(t, config).Payload(payload, false)
	if err != nil || string(out) != string(payload) {
		t.Fatalf("Payload = %s, %v; want the original bytes", out, err)
	}
}

func TestHeaderPairsAreRedactedOnlyWhenProjectScrubbingIsOn(t *testing.T) {
	const payload = `{"headers":[["Authorization","x"]]}`
	off := Config{Enabled: false, ExtraFields: []string{}, SafeFields: []string{}, Rules: []Rule{}}
	cases := map[string]struct {
		scrubber *Scrubber
		want     string
	}{
		"on":                         {mustNew(t, DefaultConfig()), `{"headers":[["Authorization","[Filtered]"]]}`},
		"off with global rules only": {mustNewWithGlobal(t, off, []Rule{telegramRule}), payload},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := tc.scrubber.Payload([]byte(payload), false)
			if err != nil || string(out) != tc.want {
				t.Fatalf("Payload = %s, %v; want %s", out, err, tc.want)
			}
		})
	}
}

func TestRulesRunBeforeTheBuiltInScrubbers(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "login", Pattern: `password=\w+`, Replacement: "credential removed"}}

	if got := mustNew(t, config).String("login password=hunter2 failed"); got != "login credential removed failed" {
		t.Fatalf("String = %q, want the rule to see the secret before the built-ins redact it", got)
	}
}

func TestApplyRulesNeverReplacesAnEmptyMatch(t *testing.T) {
	rules := []compiledRule{newCompiledRule(regexp.MustCompile(`\b`), Filtered, 0)}
	got, changed, overflow := applyRules("hello world", rules, 1<<20)
	if got != "hello world" || changed || overflow {
		t.Fatalf("applyRules = %q, %v, %v; want the text untouched", got, changed, overflow)
	}
}

func TestRulesGrowthIsCappedAndFailsClosed(t *testing.T) {
	grow := Rule{Name: "grow", Pattern: `x`, Replacement: strings.Repeat("x", MaxReplacementBytes)}
	config := DefaultConfig()
	config.Rules = []Rule{grow, grow}

	got := mustNew(t, config).String("xxxxxxxxxx")
	if got != Filtered {
		t.Fatalf("String = %d bytes %.40q, want exactly %q", len(got), got, Filtered)
	}
}

// The limit for a 1000-byte input is 1000+4096 = 5096 bytes. The global stage
// stays under it at 5000; the project stage reaches 9000, which only a limit
// recomputed from the 5000-byte intermediate (10000) would allow.
func TestRulesGrowthCapSpansGlobalAndProjectRules(t *testing.T) {
	global := []Rule{{Name: "five", Pattern: `a`, Replacement: strings.Repeat("b", 5)}}
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "nine", Pattern: `bbbbb`, Replacement: strings.Repeat("c", 9)}}

	got := mustNewWithGlobal(t, config, global).String(strings.Repeat("a", 1000))
	if got != Filtered {
		t.Fatalf("String = %d bytes, want exactly %q", len(got), Filtered)
	}
}

// amplifyingConfig grows each "aaaaaaaaaa" to 3840 bytes, under the 4106-byte
// single-string limit, so only a budget shared by the whole payload stops it.
func amplifyingConfig() Config {
	config := DefaultConfig()
	config.Rules = []Rule{
		{Name: "a", Pattern: `a`, Replacement: strings.Repeat("b", 128)},
		{Name: "b", Pattern: `b`, Replacement: "ccc"},
	}
	return config
}

func manyTinyStrings(count int) []byte {
	items := make([]string, count)
	for i := range items {
		items[i] = `"aaaaaaaaaa"`
	}
	return []byte(`{"items":[` + strings.Join(items, ",") + `]}`)
}

func TestPayloadGrowthBudgetFailsLaterStringsClosed(t *testing.T) {
	payload := manyTinyStrings(1000)
	out, err := mustNew(t, amplifyingConfig()).Payload(payload, false)
	if err != nil {
		t.Fatalf("Payload error: %v", err)
	}
	if limit := len(payload) + payloadBudget(payload).remaining + 1<<10; len(out) >= limit {
		t.Fatalf("output = %d bytes, want under %d", len(out), limit)
	}
	var decoded struct{ Items []string }
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("output is not JSON: %v", err)
	}
	if first := decoded.Items[0]; first != strings.Repeat("c", 3840) {
		t.Fatalf("first item = %.40q (%d bytes), want the rules applied", first, len(first))
	}
	if last := decoded.Items[len(decoded.Items)-1]; last != Filtered {
		t.Fatalf("last item = %.40q (%d bytes), want exactly %q", last, len(last), Filtered)
	}
}

func TestPayloadGrowthBudgetLeavesShrinkingRulesAlone(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{telegramRule}
	payload := `{"a":"https://api.telegram.org/bot` + telegramToken + `/getMe","b":["bot` + telegramToken + `","x/bot` + telegramToken + `/sendMessage"]}`

	out, err := mustNew(t, config).Payload([]byte(payload), false)
	if err != nil {
		t.Fatalf("Payload error: %v", err)
	}
	if want := `{"a":"https://api.telegram.org/bot[Filtered]/getMe","b":["bot[Filtered]","x/bot[Filtered]/sendMessage"]}`; string(out) != want {
		t.Fatalf("Payload = %s, want %s", out, want)
	}
}

func TestPayloadGrowthBudgetBelongsToEachCall(t *testing.T) {
	scrubber := mustNew(t, amplifyingConfig())
	payload := manyTinyStrings(100)
	want, err := scrubber.Payload(payload, false)
	if err != nil {
		t.Fatalf("Payload error: %v", err)
	}

	results := make([][]byte, 8)
	var wg sync.WaitGroup
	for i := range results {
		wg.Go(func() { results[i], _ = scrubber.Payload(payload, false) })
	}
	wg.Wait()
	for i, got := range results {
		if string(got) != string(want) {
			t.Fatalf("concurrent call %d = %d bytes, want the same %d bytes as a lone call", i, len(got), len(want))
		}
	}
}

func TestRulesGrowthCapLeavesTheTelegramCaseAlone(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{telegramRule}
	got := mustNew(t, config).String("GET /bot" + telegramToken + "/getMe")
	if got != "GET /bot[Filtered]/getMe" {
		t.Fatalf("String = %q", got)
	}
}

func bytesAllocatedBy(run func()) uint64 {
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	run()
	runtime.ReadMemStats(&after)
	return after.TotalAlloc - before.TotalAlloc
}

const allocationCeiling = 16 << 20

// The race detector inflates allocation several times over, so the bound is
// that a 20 MiB string allocates no more than a 1 MiB one: the match indexes
// are capped by the rule's matchLimit, never proportional to the input.
func TestRuleWithTooManyMatchesFailsClosedWithoutMaterializingThem(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "digit", Pattern: `\d`, Replacement: "#"}}
	scrubber := mustNew(t, config)
	allocatedFor := func(size int) uint64 {
		text := strings.Repeat("7", size)
		var got string
		allocated := bytesAllocatedBy(func() { got = scrubber.String(text) })
		if got != Filtered {
			t.Errorf("String of %d MiB = %d bytes, want exactly %q", size>>20, len(got), Filtered)
		}
		return allocated
	}

	small, large := allocatedFor(1<<20), allocatedFor(20<<20)
	if large > small+1<<20 {
		t.Errorf("allocated %d MiB for 20 MiB and %d MiB for 1 MiB, want no growth with input size", large>>20, small>>20)
	}
}

func TestRuleWithExactlyTheMatchLimitIsStillReplaced(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "digit", Pattern: `\d`, Replacement: "#"}}
	scrubber := mustNew(t, config)

	const matchLimit = 50_000
	if got := scrubber.String(strings.Repeat("7", matchLimit)); got != strings.Repeat("#", matchLimit) {
		t.Fatalf("String = %.40q (%d bytes), want %d hashes", got, len(got), matchLimit)
	}
	if got := scrubber.String(strings.Repeat("7", matchLimit+1)); got != Filtered {
		t.Fatalf("String = %.40q (%d bytes), want exactly %q", got, len(got), Filtered)
	}
}

func TestTemplateExpansionIsMeasuredBeforeItIsWritten(t *testing.T) {
	sixtyFourRefs := newCompiledRule(regexp.MustCompile(`(?s).+`), strings.Repeat("$0", 64), 64)
	text := strings.Repeat("s", 1<<20)

	var got string
	var overflow bool
	allocated := bytesAllocatedBy(func() { got, _, overflow = applyRules(text, []compiledRule{sixtyFourRefs}, growthLimit(text)) })
	if !overflow || got != text {
		t.Errorf("applyRules = %d bytes, overflow %v; want the text untouched and an overflow", len(got), overflow)
	}
	if allocated > allocationCeiling {
		t.Errorf("allocated %d MiB, want under %d MiB", allocated>>20, allocationCeiling>>20)
	}
}

func TestValidRuleWithMaxGroupReferencesOnAHugeMatchFailsClosed(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "echo", Pattern: `(?s).+`, Replacement: strings.Repeat("$0", MaxReplacementRefs)}}
	scrubber := mustNew(t, config)
	text := strings.Repeat("s", 1<<20)

	var got string
	allocated := bytesAllocatedBy(func() { got = scrubber.String(text) })
	if got != Filtered {
		t.Errorf("String = %d bytes, want exactly %q", len(got), Filtered)
	}
	if allocated > allocationCeiling {
		t.Errorf("allocated %d MiB, want under %d MiB", allocated>>20, allocationCeiling>>20)
	}
}

func TestRedactsKeyFollowsTheKeyRules(t *testing.T) {
	config := DefaultConfig()
	config.ExtraFields = []string{"ssn"}
	config.SafeFields = []string{"session_token"}
	off := config
	off.Enabled = false
	cases := map[string]struct {
		scrubber *Scrubber
		key      string
		want     bool
	}{
		"default name":                {mustNew(t, config), "Authorization", true},
		"substring of a name":         {mustNew(t, config), "user_token_v2", true},
		"extra field":                 {mustNew(t, config), "SSN", true},
		"safe field wins":             {mustNew(t, config), "session_token", false},
		"ordinary key":                {mustNew(t, config), "service", false},
		"client ip is not a key rule": {mustNew(t, config), "remote_addr", false},
		"disabled scrubbing":          {mustNew(t, off), "password", false},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := tc.scrubber.RedactsKey(tc.key); got != tc.want {
				t.Fatalf("RedactsKey(%q) = %v, want %v", tc.key, got, tc.want)
			}
		})
	}
}

var optionalPrefixRule = Rule{Name: "optional prefix", Pattern: `(x)?secret\d+`, Replacement: "${1}"}

func TestRuleExpandingToNothingRemovesMatchesFromTheStart(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{optionalPrefixRule}
	scrubber := mustNew(t, config)
	cases := map[string]struct{ text, want string }{
		"match at offset zero":        {"secret123", ""},
		"back to back from zero":      {"secret1secret2", ""},
		"match after leading text":    {"id secret123", "id "},
		"unrelated text is untouched": {"nothing here", "nothing here"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := scrubber.String(tc.text); got != tc.want {
				t.Fatalf("String(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestPayloadRuleExpandingToNothingRemovesAWholeValue(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{optionalPrefixRule}
	out, err := mustNew(t, config).Payload([]byte(`{"a":"secret123"}`), false)
	if err != nil || string(out) != `{"a":""}` {
		t.Fatalf("Payload = %s, %v; want {\"a\":\"\"}", out, err)
	}
}

func TestReplaceAllReportsTheEmptyResultOfAReplacement(t *testing.T) {
	rule := newCompiledRule(regexp.MustCompile(`(x)?secret\d+`), "${1}", 1)
	got, ok := rule.replaceAll("secret1secret2", 1<<20)
	if got != "" || !ok {
		t.Fatalf("replaceAll = %q, %v; want \"\", true", got, ok)
	}
	_, changed, overflow := applyRules("secret123", []compiledRule{rule}, 1<<20)
	if !changed || overflow {
		t.Fatalf("applyRules changed %v, overflow %v; want changed and no overflow", changed, overflow)
	}
}

func TestReplaceAllReturnsTheOriginalWhenOnlyEmptyMatchesExist(t *testing.T) {
	rule := newCompiledRule(regexp.MustCompile(`\b`), Filtered, 0)
	text := strings.Clone("hello world")
	got, ok := rule.replaceAll(text, 1<<20)
	if got != text || !ok {
		t.Fatalf("replaceAll = %q, %v; want the text untouched", got, ok)
	}
	if unsafe.StringData(got) != unsafe.StringData(text) {
		t.Fatal("replaceAll allocated a copy although nothing was replaced")
	}
}

// Every "a" overflows its own string limit (1 -> 128 -> 16384 bytes) and fails
// closed whatever the budget, growing 1 byte to len(Filtered). That growth
// comes out of the payload budget, so the doubling string after them, which
// fits the budget on its own, no longer does and fails closed too.
func TestPayloadGrowthBudgetCountsFailingClosed(t *testing.T) {
	rules := []Rule{
		{Name: "a", Pattern: `a`, Replacement: strings.Repeat("b", 128)},
		{Name: "b", Pattern: `b`, Replacement: strings.Repeat("c", 128)},
		{Name: "d", Pattern: `d{20}`, Replacement: strings.Repeat("e", 40)},
	}
	const tinyStrings = 200_000
	items := make([]string, tinyStrings, tinyStrings+1)
	for i := range items {
		items[i] = `"a"`
	}
	items = append(items, `"`+strings.Repeat("d", 100_000)+`"`)
	payload := []byte(`{"items":[` + strings.Join(items, ",") + `]}`)
	budget := payloadBudget(payload).remaining
	projectConfig := DefaultConfig()
	projectConfig.Rules = rules
	scopes := map[string]*Scrubber{
		"project rules": mustNew(t, projectConfig),
		"global rules":  mustNewWithGlobal(t, DefaultConfig(), rules),
	}

	for scope, scrubber := range scopes {
		t.Run(scope, func(t *testing.T) {
			out, err := scrubber.Payload(payload, false)
			if err != nil {
				t.Fatalf("Payload error: %v", err)
			}
			var decoded struct{ Items []string }
			if err := json.Unmarshal(out, &decoded); err != nil {
				t.Fatalf("output is not JSON: %v", err)
			}
			failedClosed := 0
			for i, item := range decoded.Items {
				switch {
				case item == Filtered:
					failedClosed++
				case strings.Trim(item, "ce") != "" || item == "":
					t.Fatalf("item %d = %.40q (%d bytes), want the rules applied or exactly %q", i, item, len(item), Filtered)
				}
			}
			if failedClosed != len(decoded.Items) {
				t.Fatalf("%d of %d items failed closed, want all: the Filtered growth must drain the budget", failedClosed, len(decoded.Items))
			}
			if limit := len(payload) + budget + len(Filtered)*failedClosed; len(out) > limit {
				t.Fatalf("output = %d bytes, want at most %d", len(out), limit)
			}
		})
	}
}

var emptyGroupsRule = Rule{Name: "groups", Pattern: strings.Repeat("()", 250) + "a"}

func TestRuleWithManyGroupsFailsClosedWithoutMaterializingItsSubmatches(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{emptyGroupsRule}
	scrubber := mustNew(t, config)
	text := strings.Repeat("a", 1<<20)

	var got string
	allocated := bytesAllocatedBy(func() { got = scrubber.String(text) })
	if got != Filtered {
		t.Errorf("String = %d bytes, want exactly %q", len(got), Filtered)
	}
	const ceiling = 8 << 20
	if allocated > ceiling {
		t.Errorf("allocated %d MiB, want under %d MiB", allocated>>20, ceiling>>20)
	}
}

// One group makes four ints per match, so the limit is 100,000/4 matches.
func TestRuleWithOneGroupHasAQuarterOfTheSubmatchBudgetInMatches(t *testing.T) {
	config := DefaultConfig()
	config.Rules = []Rule{{Name: "group", Pattern: `(a)`, Replacement: "#"}}
	scrubber := mustNew(t, config)

	const matchLimit = 25_000
	if got := scrubber.String(strings.Repeat("a", matchLimit)); got != strings.Repeat("#", matchLimit) {
		t.Fatalf("String = %.40q (%d bytes), want %d hashes", got, len(got), matchLimit)
	}
	if got := scrubber.String(strings.Repeat("a", matchLimit+1)); got != Filtered {
		t.Fatalf("String = %.40q (%d bytes), want exactly %q", got, len(got), Filtered)
	}
}

func TestRuleWithTooManyGroupsForTwoMatchesStillReplacesOne(t *testing.T) {
	groups := 50_000
	rule := newCompiledRule(regexp.MustCompile(strings.Repeat("()", groups)+"a"), "#", 0)
	if rule.matchLimit != 1 {
		t.Fatalf("matchLimit = %d for %d groups, want the floor of 1", rule.matchLimit, groups)
	}
	if got, ok := rule.replaceAll("a", 1<<20); got != "#" || !ok {
		t.Fatalf("replaceAll(one match) = %q, %v; want \"#\", true", got, ok)
	}
	if _, ok := rule.replaceAll("aa", 1<<20); ok {
		t.Fatal("replaceAll(two matches) succeeded, want it to fail closed")
	}
}
