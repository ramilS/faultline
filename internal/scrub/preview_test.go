package scrub

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func joined(segments []Segment) string {
	var text strings.Builder
	for _, segment := range segments {
		text.WriteString(segment.Text)
	}
	return text.String()
}

func TestPreviewSampleMarksWhatEachRedactionWrote(t *testing.T) {
	config := Config{Enabled: true, Rules: []Rule{{Name: "account", Pattern: `(acct-)\d+`, Replacement: "${1}[hidden]"}}}
	scrubber := mustNewWithGlobal(t, config, []Rule{{Name: "Slack", Pattern: `xoxb-[0-9A-Za-z-]{10,}`}})

	got, err := scrubber.PreviewSample("acct-42 used xoxb-1234567890-abc with Bearer abcdefghijklmnopqrstu", false)

	if err != nil {
		t.Fatalf("PreviewSample: %v", err)
	}
	want := []Segment{
		{Text: "acct-[hidden]", Redacted: true},
		{Text: " used "},
		{Text: "[Filtered]", Redacted: true},
		{Text: " with Bearer "},
		{Text: "[Filtered]", Redacted: true},
	}
	if !reflect.DeepEqual(got.Segments, want) {
		t.Fatalf("segments = %+v, want %+v", got.Segments, want)
	}
	if got.Scrubbed != "acct-[hidden] used [Filtered] with Bearer [Filtered]" {
		t.Fatalf("scrubbed = %q", got.Scrubbed)
	}
}

func TestPreviewSampleScrubsAJSONSampleAsAPayload(t *testing.T) {
	scrubber := mustNewWithGlobal(t, Config{Enabled: true, ExtraFields: []string{"phone"}}, nil)

	got, err := scrubber.PreviewSample(` {"phone":"+1 555 0100","city":"Oslo"} `, false)

	if err != nil {
		t.Fatalf("PreviewSample: %v", err)
	}
	wantScrubbed := "{\n  \"city\": \"Oslo\",\n  \"phone\": \"[Filtered]\"\n}"
	if got.Scrubbed != wantScrubbed {
		t.Fatalf("scrubbed = %q, want %q", got.Scrubbed, wantScrubbed)
	}
	want := []Segment{
		{Text: "{\n  \"city\": \"Oslo\",\n  \"phone\": \""},
		{Text: "[Filtered]", Redacted: true},
		{Text: "\"\n}"},
	}
	if !reflect.DeepEqual(got.Segments, want) {
		t.Fatalf("segments = %+v, want %+v", got.Segments, want)
	}
}

func TestPreviewSampleKeepsTextThatOnlyLooksLikeJSONAsText(t *testing.T) {
	scrubber := mustNewWithGlobal(t, Config{Enabled: true, ExtraFields: []string{"phone"}}, nil)

	got, err := scrubber.PreviewSample(`{"phone": "+1 555 0100"`, false)

	if err != nil || got.Scrubbed != `{"phone": "+1 555 0100"` {
		t.Fatalf("preview = %+v, %v; want the unparsable sample scrubbed as text", got, err)
	}
}

func TestPreviewPayloadRedactsTheClientIPOnlyWhenAsked(t *testing.T) {
	scrubber := mustNewWithGlobal(t, DefaultConfig(), nil)
	payload := json.RawMessage(`{"user":{"ip_address":"10.0.0.1"}}`)

	kept, err := scrubber.PreviewPayload(payload, false)
	if err != nil || !strings.Contains(kept.Scrubbed, "10.0.0.1") {
		t.Fatalf("redactIP=false: %+v, %v; want the address kept", kept, err)
	}
	redacted, err := scrubber.PreviewPayload(payload, true)
	if err != nil || strings.Contains(redacted.Scrubbed, "10.0.0.1") {
		t.Fatalf("redactIP=true: %+v, %v; want the address redacted", redacted, err)
	}
}

func TestPreviewMatchesWhatIngestStores(t *testing.T) {
	config := Config{Enabled: true, ExtraFields: []string{"phone"}, Rules: []Rule{{Name: "n", Pattern: `secret-\d+`}}}
	scrubber := mustNewWithGlobal(t, config, nil)
	payload := json.RawMessage(`{"phone":"1","message":"secret-42 card 4111 1111 1111 1111","list":[["Cookie","a=b"]]}`)

	preview, err := scrubber.PreviewPayload(payload, true)
	if err != nil {
		t.Fatalf("PreviewPayload: %v", err)
	}
	stored, err := scrubber.Payload(payload, true)
	if err != nil {
		t.Fatalf("Payload: %v", err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, []byte(preview.Scrubbed)); err != nil {
		t.Fatalf("compact: %v", err)
	}
	if compact.String() != string(stored) || joined(preview.Segments) != preview.Scrubbed {
		t.Fatalf("preview %q (segments %q), stored %q", compact.String(), joined(preview.Segments), stored)
	}
}

func TestPreviewFallsBackToOnePlainSegmentWhenTheMarkingRunDiverges(t *testing.T) {
	for name, test := range map[string]struct {
		rules  []Rule
		sample string
	}{
		"a later rule matches a marker": {
			rules:  []Rule{{Name: "a", Pattern: `secret`}, {Name: "b", Pattern: `[\x{E000}\x{E001}]`, Replacement: "!"}},
			sample: "a secret",
		},
		"the sample carries a marker": {
			rules:  []Rule{{Name: "a", Pattern: `secret`}},
			sample: "a secret ",
		},
	} {
		t.Run(name, func(t *testing.T) {
			scrubber := mustNewWithGlobal(t, Config{Enabled: true, Rules: test.rules}, nil)

			got, err := scrubber.PreviewSample(test.sample, false)

			if err != nil {
				t.Fatalf("PreviewSample: %v", err)
			}
			want := []Segment{{Text: scrubber.String(test.sample)}}
			if got.Scrubbed != scrubber.String(test.sample) || !reflect.DeepEqual(got.Segments, want) {
				t.Fatalf("preview = %+v, want %+v unhighlighted", got, want)
			}
		})
	}
}

func TestPreviewOfAnInactiveScrubberChangesNothing(t *testing.T) {
	scrubber := mustNewWithGlobal(t, Config{Enabled: false}, nil)

	got, err := scrubber.PreviewSample("password=hunter2", false)

	if err != nil || !reflect.DeepEqual(got, Preview{Scrubbed: "password=hunter2", Segments: []Segment{{Text: "password=hunter2"}}}) {
		t.Fatalf("preview = %+v, %v", got, err)
	}
}

func TestPreviewOfAnEmptySampleIsOneEmptySegment(t *testing.T) {
	got, err := NewDefault().PreviewSample("", false)

	if err != nil || !reflect.DeepEqual(got, Preview{Scrubbed: "", Segments: []Segment{{Text: ""}}}) {
		t.Fatalf("preview = %+v, %v", got, err)
	}
}
