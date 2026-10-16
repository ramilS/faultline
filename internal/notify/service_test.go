package notify

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

type memoryChannels struct {
	channels    map[int64]Channel
	secrets     map[int64]string
	lastCreate  *ChannelInput
	lastUpdate  *ChannelInput
	createCalls int
}

func newMemoryChannels(existing ...Channel) *memoryChannels {
	repository := &memoryChannels{channels: map[int64]Channel{}, secrets: map[int64]string{}}
	for _, channel := range existing {
		repository.channels[channel.ID] = channel
	}
	return repository
}

func (m *memoryChannels) List(context.Context, int64) ([]Channel, error) { return nil, nil }

func (m *memoryChannels) Get(_ context.Context, _, channelID int64) (Channel, Destination, error) {
	channel, ok := m.channels[channelID]
	if !ok {
		return Channel{}, Destination{}, ErrChannelNotFound
	}
	return channel, Destination{Kind: channel.Kind, Target: channel.Target, Secret: m.secrets[channelID]}, nil
}

func (m *memoryChannels) Create(_ context.Context, projectID int64, input ChannelInput) (Channel, error) {
	m.createCalls++
	m.lastCreate = &input
	return Channel{ID: 1, ProjectID: projectID, Name: input.Name, Kind: input.Kind}, nil
}

func (m *memoryChannels) Update(_ context.Context, _, channelID int64, input ChannelInput) (Channel, error) {
	m.lastUpdate = &input
	return m.channels[channelID], nil
}

func (m *memoryChannels) Delete(context.Context, int64, int64) error { return nil }

func validTelegram() ChannelInput {
	return ChannelInput{
		Name: "Ops chat", Kind: KindTelegram, Target: "-1001234567890", Secret: new("123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g"),
		Enabled: true, NotifyNew: true, NotifyRegression: true, Environments: []string{}, MinLevel: "error",
	}
}

func validWebhook() ChannelInput {
	return ChannelInput{
		Name: "Hook", Kind: KindWebhook, Target: "https://hooks.example/faultline",
		Enabled: true, NotifyNew: true, Environments: []string{}, MinLevel: "debug",
	}
}

func TestCreateRejectsInvalidInput(t *testing.T) {
	cases := map[string]struct {
		mutate  func(*ChannelInput)
		message string
	}{
		"blank name":            {func(i *ChannelInput) { i.Name = "   " }, "name must be"},
		"no trigger selected":   {func(i *ChannelInput) { i.NotifyNew, i.NotifyRegression = false, false }, "choose at least one"},
		"unknown level":         {func(i *ChannelInput) { i.MinLevel = "critical" }, "min_level must be"},
		"unknown kind":          {func(i *ChannelInput) { i.Kind = "slack" }, "kind must be"},
		"blank environment":     {func(i *ChannelInput) { i.Environments = []string{"prod", " "} }, "each environment"},
		"telegram chat by name": {func(i *ChannelInput) { i.Target = "ops chat" }, "chat id must be"},
		"telegram no token":     {func(i *ChannelInput) { i.Secret = nil }, "bot token is required"},
		"telegram bad token":    {func(i *ChannelInput) { i.Secret = new("not-a-token") }, "bot token must look like"},
		"oversized secret":      {func(i *ChannelInput) { i.Secret = new(strings.Repeat("a", maxSecretLength+1)) }, "secret must be at most"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			repository := newMemoryChannels()
			input := validTelegram()
			tc.mutate(&input)

			_, err := NewService(repository, nil, ServiceOptions{}).Create(t.Context(), 1, input)

			var invalid *ValidationError
			if !errors.As(err, &invalid) || !errors.Is(err, ErrInvalidChannel) || !strings.HasPrefix(invalid.Message, tc.message) {
				t.Fatalf("Create = %v, want a ValidationError starting %q", err, tc.message)
			}
			if repository.createCalls != 0 {
				t.Fatal("an invalid channel reached the repository")
			}
		})
	}
}

func TestCreateRejectsWebhookURLsThatAreNotAbsoluteHTTP(t *testing.T) {
	for _, target := range []string{"hooks.example/x", "ftp://hooks.example/x", "/relative", "https://", "javascript:alert(1)"} {
		input := validWebhook()
		input.Target = target

		_, err := NewService(newMemoryChannels(), nil, ServiceOptions{}).Create(t.Context(), 1, input)

		if !errors.Is(err, ErrInvalidChannel) {
			t.Errorf("Create(%q) = %v, want ErrInvalidChannel", target, err)
		}
	}
}

func TestCreateNormalizesInput(t *testing.T) {
	repository := newMemoryChannels()
	input := validTelegram()
	input.Name = "  Ops chat "
	input.Target = " @faultline_alerts "
	input.Environments = []string{" production", "production", "staging "}

	if _, err := NewService(repository, nil, ServiceOptions{}).Create(t.Context(), 1, input); err != nil {
		t.Fatalf("Create = %v", err)
	}

	stored := repository.lastCreate
	if stored.Name != "Ops chat" || stored.Target != "@faultline_alerts" || !reflect.DeepEqual(stored.Environments, []string{"production", "staging"}) {
		t.Fatalf("stored input = %+v", stored)
	}
}

func TestCreateAcceptsAWebhookWithoutASecret(t *testing.T) {
	if _, err := NewService(newMemoryChannels(), nil, ServiceOptions{}).Create(t.Context(), 1, validWebhook()); err != nil {
		t.Fatalf("Create = %v", err)
	}
}

func TestUpdateWithoutSecretKeepsTheStoredToken(t *testing.T) {
	repository := newMemoryChannels(Channel{ID: 3, Kind: KindTelegram})
	input := validTelegram()
	input.Secret = nil

	if _, err := NewService(repository, nil, ServiceOptions{}).Update(t.Context(), 1, 3, input); err != nil {
		t.Fatalf("Update = %v", err)
	}
	if repository.lastUpdate == nil || repository.lastUpdate.Secret != nil {
		t.Fatalf("update input = %+v, want a nil secret passed through", repository.lastUpdate)
	}
}

func TestUpdateRefusesToChangeKind(t *testing.T) {
	repository := newMemoryChannels(Channel{ID: 3, Kind: KindTelegram})

	_, err := NewService(repository, nil, ServiceOptions{}).Update(t.Context(), 1, 3, validWebhook())

	var invalid *ValidationError
	if !errors.As(err, &invalid) || invalid.Message != "kind cannot be changed" || repository.lastUpdate != nil {
		t.Fatalf("Update = %v, want \"kind cannot be changed\" and no write", err)
	}
}

func TestUpdateOfAnUnknownChannelIsNotFound(t *testing.T) {
	_, err := NewService(newMemoryChannels(), nil, ServiceOptions{}).Update(t.Context(), 1, 99, validTelegram())

	if !errors.Is(err, ErrChannelNotFound) {
		t.Fatalf("Update = %v, want ErrChannelNotFound", err)
	}
}

func TestSendTestUsesTheStoredDestination(t *testing.T) {
	repository := newMemoryChannels(Channel{ID: 3, Kind: KindTelegram, Target: "-100"})
	repository.secrets[3] = "123456789:token"
	transport := &recordingTransport{}

	err := NewService(repository, transport, ServiceOptions{PublicURL: "https://faultline.example/"}).SendTest(t.Context(), 1, 3, ProjectRef{Slug: "shop", Name: "Shop", IconEmoji: "🛒"})

	if err != nil {
		t.Fatalf("SendTest = %v", err)
	}
	want := Destination{Kind: KindTelegram, Target: "-100", Secret: "123456789:token"}
	if transport.destination != want || transport.notification.ProjectEmoji != "🛒" || transport.notification.Trigger != TriggerTest || transport.notification.IssueURL != "https://faultline.example/shop/issues" {
		t.Fatalf("sent %+v to %+v", transport.notification, transport.destination)
	}
}

type recordingTransport struct {
	destination  Destination
	notification Notification
}

func (r *recordingTransport) Send(_ context.Context, destination Destination, n Notification, _ int64) error {
	r.destination, r.notification = destination, n
	return nil
}

func TestCreateRefusesWebhookURLsThatPointInside(t *testing.T) {
	cases := []struct {
		target       string
		allowPrivate bool
		message      string
	}{
		{"http://localhost:8080/hook", true, "url must not point at this server"},
		{"http://api.localhost/hook", true, "url must not point at this server"},
		{"http://127.0.0.1/hook", true, "url must not point at a loopback"},
		{"http://[::1]:9000/hook", true, "url must not point at a loopback"},
		{"http://169.254.169.254/latest/meta-data/", true, "url must not point at a loopback"},
		{"http://10.0.0.5/hook", false, "url points at a private network address"},
		{"http://192.168.1.10/hook", false, "url points at a private network address"},
	}
	for _, tc := range cases {
		input := validWebhook()
		input.Target = tc.target

		_, err := NewService(newMemoryChannels(), nil, ServiceOptions{AllowPrivateNetworks: tc.allowPrivate}).Create(t.Context(), 1, input)

		invalid, ok := errors.AsType[*ValidationError](err)
		if !ok || !strings.HasPrefix(invalid.Message, tc.message) {
			t.Errorf("Create(%s, allowPrivate=%v) = %v, want %q", tc.target, tc.allowPrivate, err, tc.message)
		}
	}
}

func TestCreateAcceptsPrivateAddressesWhenAllowedAndAnyHostname(t *testing.T) {
	for _, tc := range []struct {
		target       string
		allowPrivate bool
	}{
		{"http://10.0.0.5/hook", true},
		{"http://n8n:5678/webhook/faultline", false},
		{"https://hooks.example.com/x", false},
	} {
		input := validWebhook()
		input.Target = tc.target

		if _, err := NewService(newMemoryChannels(), nil, ServiceOptions{AllowPrivateNetworks: tc.allowPrivate}).Create(t.Context(), 1, input); err != nil {
			t.Errorf("Create(%s, allowPrivate=%v) = %v", tc.target, tc.allowPrivate, err)
		}
	}
}
