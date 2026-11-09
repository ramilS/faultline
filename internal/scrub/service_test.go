package scrub

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type fakeRepository struct {
	globalRules  []Rule
	globalErr    error
	updates      int
	storesUserIP bool
	latestEvent  json.RawMessage
}

func (f *fakeRepository) StoresUserIP(context.Context, int64) (bool, error) {
	return f.storesUserIP, nil
}

func (f *fakeRepository) HasEvents(context.Context, int64) (bool, error) {
	return f.latestEvent != nil, nil
}

func (f *fakeRepository) LatestEventPayload(context.Context, int64) (json.RawMessage, error) {
	if f.latestEvent == nil {
		return nil, ErrNoEvents
	}
	return f.latestEvent, nil
}

func (f *fakeRepository) Get(context.Context, int64) (Config, error)  { return Config{}, nil }
func (f *fakeRepository) Update(context.Context, int64, Config) error { return nil }
func (f *fakeRepository) GlobalRules(context.Context) ([]Rule, error) {
	return f.globalRules, f.globalErr
}
func (f *fakeRepository) UpdateGlobalRules(_ context.Context, rules []Rule) error {
	f.updates++
	f.globalRules = rules
	return nil
}

func TestUpdateGlobalRefusesInvalidRulesWithoutTouchingTheRepository(t *testing.T) {
	repository := &fakeRepository{}

	_, err := NewService(repository).UpdateGlobal(t.Context(), []Rule{{Name: "bad", Pattern: `(`}})

	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
	if repository.updates != 0 {
		t.Fatalf("repository updated %d times, want 0", repository.updates)
	}
}

func TestUpdateGlobalStoresAndReturnsTheNormalizedRules(t *testing.T) {
	repository := &fakeRepository{}
	want := []Rule{{Name: "Telegram", Pattern: `bot\d+`}}

	got, err := NewService(repository).UpdateGlobal(t.Context(), []Rule{{Name: "  Telegram ", Pattern: `bot\d+`}})

	if err != nil {
		t.Fatalf("UpdateGlobal: %v", err)
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(repository.globalRules, want) {
		t.Fatalf("returned %v, stored %v; want both %v", got, repository.globalRules, want)
	}
}

func TestGetGlobalWrapsRepositoryErrors(t *testing.T) {
	failure := errors.New("connection reset")

	_, err := NewService(&fakeRepository{globalErr: failure}).GetGlobal(t.Context())

	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap %v", err, failure)
	}
}

func TestPreviewProjectAppliesTheDraftAndTheStoredGlobalRulesWithoutStoring(t *testing.T) {
	repository := &fakeRepository{globalRules: []Rule{{Name: "acct", Pattern: `acct-\d+`}}}
	draft := Config{Enabled: true, ExtraFields: []string{" Phone "}, SafeFields: []string{}, Rules: []Rule{{Name: "pin", Pattern: `pin-\d+`}}}

	got, err := NewService(repository).PreviewProject(t.Context(), 7, ProjectPreviewInput{Draft: draft, Sample: `{"phone":"555","note":"acct-1 pin-2"}`})

	if err != nil {
		t.Fatalf("PreviewProject: %v", err)
	}
	want := "{\n  \"note\": \"[Filtered] [Filtered]\",\n  \"phone\": \"[Filtered]\"\n}"
	if got.Scrubbed != want || repository.updates != 0 {
		t.Fatalf("scrubbed = %q, updates = %d; want %q and nothing stored", got.Scrubbed, repository.updates, want)
	}
}

func TestPreviewProjectRunsOnTheLatestEventAndFollowsTheProjectsIPChoice(t *testing.T) {
	for name, test := range map[string]struct {
		storesUserIP bool
		wantIP       bool
	}{
		"project stores user IPs": {storesUserIP: true, wantIP: true},
		"project drops user IPs":  {storesUserIP: false, wantIP: false},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &fakeRepository{storesUserIP: test.storesUserIP, latestEvent: json.RawMessage(`{"user":{"ip_address":"10.0.0.1"},"message":"pin-2"}`)}
			draft := Config{Enabled: true, ExtraFields: []string{}, SafeFields: []string{}, Rules: []Rule{{Name: "pin", Pattern: `pin-\d+`}}}

			got, err := NewService(repository).PreviewProject(t.Context(), 7, ProjectPreviewInput{Draft: draft, LatestEvent: true, Sample: "ignored"})

			if err != nil {
				t.Fatalf("PreviewProject: %v", err)
			}
			if strings.Contains(got.Scrubbed, "pin-2") || strings.Contains(got.Scrubbed, "ignored") {
				t.Fatalf("scrubbed = %q, want the event's message scrubbed by the draft", got.Scrubbed)
			}
			if strings.Contains(got.Scrubbed, "10.0.0.1") != test.wantIP {
				t.Fatalf("scrubbed = %q, want the IP kept: %v", got.Scrubbed, test.wantIP)
			}
		})
	}
}

func TestPreviewProjectReportsAProjectWithoutEvents(t *testing.T) {
	_, err := NewService(&fakeRepository{}).PreviewProject(t.Context(), 7, ProjectPreviewInput{Draft: DefaultConfig(), LatestEvent: true})

	if !errors.Is(err, ErrNoEvents) {
		t.Fatalf("error = %v, want ErrNoEvents", err)
	}
}

func TestPreviewProjectRefusesAnInvalidDraft(t *testing.T) {
	draft := DefaultConfig()
	draft.Rules = []Rule{{Name: "x", Pattern: `a(?=b)`}}

	_, err := NewService(&fakeRepository{}).PreviewProject(t.Context(), 7, ProjectPreviewInput{Draft: draft, Sample: "ab"})

	if !errors.Is(err, ErrInvalidConfig) || !strings.Contains(err.Error(), "RE2") {
		t.Fatalf("error = %v, want ErrInvalidConfig naming RE2", err)
	}
}

func TestPreviewGlobalAppliesTheDraftRulesOverTheDefaults(t *testing.T) {
	got, err := NewService(&fakeRepository{}).PreviewGlobal([]Rule{{Name: "acct", Pattern: `acct-\d+`}}, "acct-1 password=hunter2")

	if err != nil || got.Scrubbed != "[Filtered] password=[Filtered]" {
		t.Fatalf("preview = %+v, %v", got, err)
	}
	if _, err := NewService(&fakeRepository{}).PreviewGlobal([]Rule{{Name: "x", Pattern: `(`}}, "a"); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("invalid draft error = %v, want ErrInvalidConfig", err)
	}
}

func TestPreviewProjectRedactsTheClientIPInASampleUnlessTheProjectStoresIt(t *testing.T) {
	sample := `{"user":{"ip_address":"10.0.0.1"}}`
	for _, storesUserIP := range []bool{false, true} {
		got, err := NewService(&fakeRepository{storesUserIP: storesUserIP}).PreviewProject(t.Context(), 7, ProjectPreviewInput{Draft: DefaultConfig(), Sample: sample})

		if err != nil || strings.Contains(got.Scrubbed, "10.0.0.1") != storesUserIP {
			t.Fatalf("storesUserIP=%v: preview = %q, %v", storesUserIP, got.Scrubbed, err)
		}
	}
}
