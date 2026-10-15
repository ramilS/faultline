package notify

import (
	"context"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	maxNameRunes        = 100
	maxTargetLength     = 2048
	maxSecretLength     = 256
	maxEnvironments     = 20
	maxEnvironmentRunes = 64
)

var (
	// telegramChatID accepts a numeric chat id (negative for groups and
	// channels) or a public @username, the two forms sendMessage takes.
	telegramChatID = regexp.MustCompile(`^(-?\d{1,20}|@[A-Za-z][A-Za-z0-9_]{3,31})$`)
	// telegramToken is the "<bot id>:<secret>" shape BotFather issues.
	telegramToken = regexp.MustCompile(`^\d{6,}:[A-Za-z0-9_-]{30,}$`)
)

// ChannelRepository persists channels. Every method is scoped by projectID,
// so a channel id from another project behaves as not found.
type ChannelRepository interface {
	List(ctx context.Context, projectID int64) ([]Channel, error)
	// Get returns the channel with the destination it is sent to, secret
	// included.
	Get(ctx context.Context, projectID, channelID int64) (Channel, Destination, error)
	// Create must enforce MaxChannelsPerProject atomically with the insert,
	// returning ErrTooManyChannels.
	Create(ctx context.Context, projectID int64, input ChannelInput) (Channel, error)
	// Update replaces every field but Kind; a nil Secret keeps the stored
	// one.
	Update(ctx context.Context, projectID, channelID int64, input ChannelInput) (Channel, error)
	Delete(ctx context.Context, projectID, channelID int64) error
}

// ProjectRef names the project a test message is sent for.
type ProjectRef struct {
	Slug string
	Name string
}

type ServiceOptions struct {
	// PublicURL is the dashboard's external base URL, linked from the test
	// message; empty means no link.
	PublicURL string
	// AllowPrivateNetworks must match the Sender's, so a webhook URL the
	// sender would refuse is rejected when it is saved rather than on its
	// first delivery.
	AllowPrivateNetworks bool
}

// Service validates and stores channels, and sends test messages.
type Service struct {
	repository   ChannelRepository
	transport    Transport
	publicURL    string
	allowPrivate bool
}

func NewService(repository ChannelRepository, transport Transport, options ServiceOptions) *Service {
	return &Service{repository: repository, transport: transport, publicURL: options.PublicURL, allowPrivate: options.AllowPrivateNetworks}
}

func (s *Service) List(ctx context.Context, projectID int64) ([]Channel, error) {
	channels, err := s.repository.List(ctx, projectID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	return channels, nil
}

func (s *Service) Create(ctx context.Context, projectID int64, input ChannelInput) (Channel, error) {
	normalized, err := s.normalizeInput(input, true)
	if err != nil {
		return Channel{}, err
	}
	created, err := s.repository.Create(ctx, projectID, normalized)
	if err != nil {
		return Channel{}, fmt.Errorf("create notification channel: %w", err)
	}
	return created, nil
}

// Update applies input to an existing channel. The channel's kind cannot
// change: a Telegram token and a webhook signing key are different secrets,
// and switching kind while keeping the stored one would misuse it.
func (s *Service) Update(ctx context.Context, projectID, channelID int64, input ChannelInput) (Channel, error) {
	existing, _, err := s.repository.Get(ctx, projectID, channelID)
	if err != nil {
		return Channel{}, fmt.Errorf("update notification channel: %w", err)
	}
	if input.Kind != "" && input.Kind != existing.Kind {
		return Channel{}, invalid("kind cannot be changed")
	}
	input.Kind = existing.Kind
	normalized, err := s.normalizeInput(input, false)
	if err != nil {
		return Channel{}, err
	}
	updated, err := s.repository.Update(ctx, projectID, channelID, normalized)
	if err != nil {
		return Channel{}, fmt.Errorf("update notification channel: %w", err)
	}
	return updated, nil
}

func (s *Service) Delete(ctx context.Context, projectID, channelID int64) error {
	if err := s.repository.Delete(ctx, projectID, channelID); err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	return nil
}

// SendTest sends a sample message to the channel right away, bypassing the
// queue, so the dashboard can show whether the configuration works. A
// delivery failure comes back as *SendError.
func (s *Service) SendTest(ctx context.Context, projectID, channelID int64, project ProjectRef) error {
	_, destination, err := s.repository.Get(ctx, projectID, channelID)
	if err != nil {
		return fmt.Errorf("send test notification: %w", err)
	}
	notification := Notification{
		Trigger:     TriggerTest,
		ProjectSlug: project.Slug,
		ProjectName: project.Name,
		Title:       "This is a test notification from Faultline",
		Level:       "info",
		IssueURL:    s.projectURL(project.Slug),
	}
	return s.transport.Send(ctx, destination, notification, 0)
}

func (s *Service) projectURL(slug string) string {
	if s.publicURL == "" {
		return ""
	}
	return strings.TrimRight(s.publicURL, "/") + "/" + url.PathEscape(slug) + "/issues"
}

// normalizeInput trims and validates input. creating requires a secret for
// Telegram; on update a nil secret means "keep the stored one".
func (s *Service) normalizeInput(input ChannelInput, creating bool) (ChannelInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	input.Target = strings.TrimSpace(input.Target)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > maxNameRunes {
		return ChannelInput{}, invalid(fmt.Sprintf("name must be 1 to %d characters", maxNameRunes))
	}
	if !input.NotifyNew && !input.NotifyRegression {
		return ChannelInput{}, invalid("choose at least one of new issues and regressions")
	}
	if !validLevel(input.MinLevel) {
		return ChannelInput{}, invalid("min_level must be one of debug, info, warning, error, fatal")
	}
	environments, err := normalizeEnvironments(input.Environments)
	if err != nil {
		return ChannelInput{}, err
	}
	input.Environments = environments
	if input.Secret != nil {
		trimmed := strings.TrimSpace(*input.Secret)
		input.Secret = &trimmed
		if len(trimmed) > maxSecretLength {
			return ChannelInput{}, invalid(fmt.Sprintf("secret must be at most %d characters", maxSecretLength))
		}
	}
	switch input.Kind {
	case KindTelegram:
		err = validateTelegram(input, creating)
	case KindWebhook:
		err = validateWebhook(input, s.allowPrivate)
	default:
		err = invalid("kind must be telegram or webhook")
	}
	if err != nil {
		return ChannelInput{}, err
	}
	return input, nil
}

func validateTelegram(input ChannelInput, creating bool) error {
	if !telegramChatID.MatchString(input.Target) {
		return invalid("chat id must be a number such as -1001234567890 or a @channel_username")
	}
	if input.Secret == nil {
		if creating {
			return invalid("bot token is required")
		}
		return nil
	}
	if !telegramToken.MatchString(*input.Secret) {
		return invalid("bot token must look like 123456789:AA... as issued by @BotFather")
	}
	return nil
}

// validateWebhook refuses, up front, a URL whose host is plainly an address
// the sender's dial guard would refuse. It is early feedback only: a hostname
// is checked against what it resolves to at send time, not here.
func validateWebhook(input ChannelInput, allowPrivate bool) error {
	if len(input.Target) > maxTargetLength {
		return invalid(fmt.Sprintf("url must be at most %d characters", maxTargetLength))
	}
	parsed, err := url.Parse(input.Target)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return invalid("url must be an absolute http or https URL")
	}
	host := strings.ToLower(parsed.Hostname())
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return invalid("url must not point at this server (localhost)")
	}
	address, parseErr := netip.ParseAddr(host)
	isLiteralAddress := parseErr == nil
	if !isLiteralAddress || allowedAddress(address, allowPrivate) {
		return nil
	}
	if allowedAddress(address, true) {
		return invalid("url points at a private network address; set FAULTLINE_WEBHOOK_ALLOW_PRIVATE=true on the server to allow it")
	}
	return invalid("url must not point at a loopback, link-local or multicast address")
}

func normalizeEnvironments(environments []string) ([]string, error) {
	normalized := make([]string, 0, len(environments))
	for _, environment := range environments {
		environment = strings.TrimSpace(environment)
		if environment == "" || utf8.RuneCountInString(environment) > maxEnvironmentRunes {
			return nil, invalid(fmt.Sprintf("each environment must be 1 to %d characters", maxEnvironmentRunes))
		}
		if !slices.Contains(normalized, environment) {
			normalized = append(normalized, environment)
		}
	}
	if len(normalized) > maxEnvironments {
		return nil, invalid(fmt.Sprintf("at most %d environments", maxEnvironments))
	}
	return normalized, nil
}

func invalid(message string) error {
	return &ValidationError{Message: message}
}

// ValidationError is a rejected channel configuration. Message is written
// for the person filling the form and is safe to return as-is; the error
// matches ErrInvalidChannel under errors.Is.
type ValidationError struct {
	Message string
}

func (e *ValidationError) Error() string { return "invalid notification channel: " + e.Message }

func (e *ValidationError) Unwrap() error { return ErrInvalidChannel }
