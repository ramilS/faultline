package ingest

import "testing"

func TestMapSentryEventDerivesDeviceAndOSTagsFromContexts(t *testing.T) {
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"contexts":{"device":{"model":"Pixel 7","family":"Pixel"},"os":{"name":"Android","version":"14","build":"UP1A.231005.007"}}}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if event.Tags[deviceTagKey] != "Pixel 7" {
		t.Fatalf("device tag: got %q, want %q", event.Tags[deviceTagKey], "Pixel 7")
	}
	if event.Tags[osTagKey] != "Android 14" {
		t.Fatalf("os tag: got %q, want %q", event.Tags[osTagKey], "Android 14")
	}
}

func TestMapSentryEventKeepsExplicitTagsOverContextTags(t *testing.T) {
	payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc",
		"tags":{"device":"tablet","os":"custom"},
		"contexts":{"device":{"model":"Pixel 7"},"os":{"name":"Android","version":"14"}}}`)

	event, err := MapSentryEvent(payload, mapReceivedAt)

	if err != nil {
		t.Fatalf("map: %v", err)
	}
	if event.Tags[deviceTagKey] != "tablet" || event.Tags[osTagKey] != "custom" {
		t.Fatalf("got tags %v, want the event's own device and os tags", event.Tags)
	}
}

func TestMapSentryEventDerivesOSTagFromTheContextFields(t *testing.T) {
	cases := []struct {
		name     string
		contexts string
		want     string
	}{
		{"name only", `{"os":{"name":"linux"}}`, "linux"},
		{"precomputed os field wins", `{"os":{"os":"iOS 17.4.1","name":"iOS","version":"17.4"}}`, "iOS 17.4.1"},
		{"numeric version", `{"os":{"name":"Android","version":14}}`, "Android 14"},
		{"version without name", `{"os":{"version":"14"}}`, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","contexts":` + tc.contexts + `}`)

			event, err := MapSentryEvent(payload, mapReceivedAt)

			if err != nil {
				t.Fatalf("map: %v", err)
			}
			got, present := event.Tags[osTagKey]
			if tc.want == "" && present {
				t.Fatalf("os tag: got %q, want none", got)
			}
			if got != tc.want {
				t.Fatalf("os tag: got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMapSentryEventIngestsMalformedDeviceAndOSContextsWithoutTags(t *testing.T) {
	cases := map[string]string{
		"contexts are strings": `{"device":"Pixel 7","os":"Android"}`,
		"fields are objects":   `{"device":{"model":{"x":1}},"os":{"name":["Android"]}}`,
		"fields are blank":     `{"device":{"model":"  "},"os":{"name":""}}`,
	}
	for name, contexts := range cases {
		t.Run(name, func(t *testing.T) {
			payload := []byte(`{"event_id":"9ec79c33ec9942ab8353589fcb2e04dc","contexts":` + contexts + `}`)

			event, err := MapSentryEvent(payload, mapReceivedAt)

			if err != nil {
				t.Fatalf("map: %v", err)
			}
			if len(event.Tags) != 0 {
				t.Fatalf("got tags %v, want none", event.Tags)
			}
		})
	}
}
