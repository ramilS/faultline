package scrub

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"
)

type dashboardPreset struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
	Example     string `json:"example"`
}

// The dashboard's preset gallery reads this file.
const dashboardPresetsPath = "../../web/src/features/settings/scrubPresets.json"

var presetNearMisses = map[string]string{
	"Telegram bot token":              "https://api.telegram.org/bot12345:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g/getMe",
	"AWS access key ID":               "AKIAIOSFODNN7EXAMPL",
	"GitHub token":                    "ghx_abcdefghijklmnopqrstuvwxyz0123456789",
	"Email address":                   "jane.doe@localhost",
	"JWT":                             "eyJhbGciOiJIUzI1NiJ9.payload.signature",
	"Authorization: Bearer value":     "Authorization: Basic dXNlcjpwYXNz",
	"Slack token":                     "xoxz-EXAMPLE-TOKEN",
	"Stripe secret or restricted key": "pk_live_EXAMPLE0000",
	"PEM private key block":           "-----BEGIN PUBLIC KEY-----\nMIIBexample\n-----END PUBLIC KEY-----",
	"IPv4 address":                    "999.10.0.1",
}

func loadDashboardPresets(t *testing.T) []dashboardPreset {
	t.Helper()
	raw, err := os.ReadFile(dashboardPresetsPath)
	if err != nil {
		t.Fatalf("read presets: %v", err)
	}
	var presets []dashboardPreset
	if err := json.Unmarshal(raw, &presets); err != nil {
		t.Fatalf("decode presets: %v", err)
	}
	return presets
}

func TestEveryDashboardPresetHasANearMissCase(t *testing.T) {
	var names []string
	for _, preset := range loadDashboardPresets(t) {
		names = append(names, preset.Name)
	}
	for name := range presetNearMisses {
		if !slices.Contains(names, name) {
			t.Errorf("near miss for %q, which is not a preset", name)
		}
	}
	for _, name := range names {
		if _, ok := presetNearMisses[name]; !ok {
			t.Errorf("preset %q has no near-miss case", name)
		}
	}
}

func TestDashboardPresetsRedactTheirExampleAndNotANearMiss(t *testing.T) {
	for _, preset := range loadDashboardPresets(t) {
		t.Run(preset.Name, func(t *testing.T) {
			rule := Rule{Name: preset.Name, Description: preset.Description, Pattern: preset.Pattern, Replacement: preset.Replacement}
			if _, err := NormalizeRules([]Rule{rule}); err != nil {
				t.Fatalf("preset does not validate under RE2: %v", err)
			}
			scrubber := mustNewWithGlobal(t, Config{Enabled: false}, []Rule{rule})

			scrubbed := scrubber.String("before " + preset.Example + " after")
			if !strings.HasPrefix(scrubbed, "before ") || !strings.HasSuffix(scrubbed, " after") || !strings.Contains(scrubbed, Filtered) {
				t.Fatalf("example scrubbed to %q, want [Filtered] between the surrounding words", scrubbed)
			}
			nearMiss := presetNearMisses[preset.Name]
			if got := scrubber.String(nearMiss); got != nearMiss {
				t.Fatalf("near miss %q scrubbed to %q, want it unchanged", nearMiss, got)
			}
		})
	}
}
