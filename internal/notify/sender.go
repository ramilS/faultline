package notify

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultTelegramAPI = "https://api.telegram.org"
	sendTimeout        = 10 * time.Second
	// maxResponseBytes bounds how much of a destination's reply is read; only
	// Telegram's short JSON error description is ever used.
	maxResponseBytes = 64 << 10
	maxErrorRunes    = 300
	userAgent        = "Faultline-Notifications"
)

// SendError is a failed delivery. Its message never contains the
// destination URL: a Telegram URL carries the bot token in its path, and a
// webhook URL may carry credentials, while this message is stored and shown
// in the dashboard.
type SendError struct {
	Message string
	// Permanent marks a rejection retrying cannot fix (a bad token, an
	// unknown chat, a 404 endpoint).
	Permanent bool
	// RetryAfter is the destination's own back-off request, zero if none.
	RetryAfter time.Duration
}

func (e *SendError) Error() string { return e.Message }

type SenderOptions struct {
	// AllowPrivateNetworks lets webhooks reach private ranges (10/8,
	// 172.16/12, 192.168/16, fc00::/7, 100.64/10). Loopback and link-local
	// stay refused regardless; see allowedAddress.
	AllowPrivateNetworks bool
	// Client replaces both the Telegram and the guarded webhook client. Only
	// tests set it, to reach an httptest server on loopback.
	Client *http.Client
	// TelegramAPI overrides https://api.telegram.org, for tests.
	TelegramAPI string
	Now         func() time.Time
}

type Sender struct {
	telegramClient *http.Client
	webhookClient  *http.Client
	telegramAPI    string
	now            func() time.Time
}

func NewSender(options SenderOptions) *Sender {
	sender := &Sender{telegramClient: options.Client, webhookClient: options.Client, telegramAPI: options.TelegramAPI, now: options.Now}
	if options.Client == nil {
		sender.telegramClient = newClient(http.DefaultTransport)
		sender.webhookClient = guardedWebhookClient(options.AllowPrivateNetworks)
	}
	if sender.telegramAPI == "" {
		sender.telegramAPI = defaultTelegramAPI
	}
	if sender.now == nil {
		sender.now = time.Now
	}
	return sender
}

// Send delivers n to destination. deliveryID identifies the attempt to a
// webhook receiver so it can drop a redelivery it already processed.
func (s *Sender) Send(ctx context.Context, destination Destination, n Notification, deliveryID int64) error {
	switch destination.Kind {
	case KindTelegram:
		return s.sendTelegram(ctx, destination, n)
	case KindWebhook:
		return s.sendWebhook(ctx, destination, n, deliveryID)
	}
	return &SendError{Message: fmt.Sprintf("unknown channel kind %q", destination.Kind), Permanent: true}
}

type telegramRequest struct {
	ChatID             string                 `json:"chat_id"`
	Text               string                 `json:"text"`
	ParseMode          string                 `json:"parse_mode"`
	LinkPreviewOptions telegramPreviewOptions `json:"link_preview_options"`
}

type telegramPreviewOptions struct {
	IsDisabled bool `json:"is_disabled"`
}

type telegramResponse struct {
	OK          bool   `json:"ok"`
	Description string `json:"description"`
	Parameters  struct {
		RetryAfter int `json:"retry_after"`
	} `json:"parameters"`
}

func (s *Sender) sendTelegram(ctx context.Context, destination Destination, n Notification) error {
	body, err := json.Marshal(telegramRequest{
		ChatID:             destination.Target,
		Text:               TelegramText(n),
		ParseMode:          "HTML",
		LinkPreviewOptions: telegramPreviewOptions{IsDisabled: true},
	})
	if err != nil {
		return &SendError{Message: "telegram: encode message: " + err.Error(), Permanent: true}
	}
	endpoint := s.telegramAPI + "/bot" + destination.Secret + "/sendMessage"
	status, header, reply, err := s.post(ctx, s.telegramClient, endpoint, body, nil)
	if err != nil {
		return transportError("telegram", err)
	}
	var parsed telegramResponse
	_ = json.Unmarshal(reply, &parsed)
	if status >= 200 && status < 300 && parsed.OK {
		return nil
	}
	message := fmt.Sprintf("telegram: HTTP %d", status)
	if parsed.Description != "" {
		message += ": " + truncate(parsed.Description, maxErrorRunes)
	}
	retryAfter := time.Duration(parsed.Parameters.RetryAfter) * time.Second
	if retryAfter == 0 {
		retryAfter = retryAfterHeader(header)
	}
	return &SendError{Message: message, Permanent: !retryableStatus(status), RetryAfter: retryAfter}
}

func (s *Sender) sendWebhook(ctx context.Context, destination Destination, n Notification, deliveryID int64) error {
	sentAt := s.now()
	body, err := WebhookBody(n, deliveryID, sentAt)
	if err != nil {
		return &SendError{Message: "webhook: encode payload: " + err.Error(), Permanent: true}
	}
	headers := http.Header{}
	headers.Set("X-Faultline-Event", WebhookEvent(n.Trigger))
	headers.Set("X-Faultline-Delivery", strconv.FormatInt(deliveryID, 10))
	if destination.Secret != "" {
		timestamp := strconv.FormatInt(sentAt.Unix(), 10)
		headers.Set("X-Faultline-Timestamp", timestamp)
		headers.Set("X-Faultline-Signature", "sha256="+WebhookSignature(destination.Secret, timestamp, body))
	}
	status, header, _, err := s.post(ctx, s.webhookClient, destination.Target, body, headers)
	if err != nil {
		return transportError("webhook", err)
	}
	if status >= 200 && status < 300 {
		return nil
	}
	return &SendError{Message: fmt.Sprintf("webhook: HTTP %d", status), Permanent: !retryableStatus(status), RetryAfter: retryAfterHeader(header)}
}

// WebhookSignature is the hex HMAC-SHA256 of "<timestamp>.<body>" under
// secret. Signing the timestamp with the body lets a receiver reject a
// captured request replayed later.
func WebhookSignature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func (s *Sender) post(ctx context.Context, client *http.Client, endpoint string, body []byte, headers http.Header) (int, http.Header, []byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	maps.Copy(request.Header, headers)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("User-Agent", userAgent)
	response, err := client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	reply, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	closeErr := response.Body.Close()
	if err != nil {
		return 0, nil, nil, err
	}
	if closeErr != nil {
		return 0, nil, nil, closeErr
	}
	return response.StatusCode, response.Header, reply, nil
}

// transportError strips the *url.Error wrapper, whose message repeats the
// full request URL — the Telegram bot token included. A refused destination
// is permanent: the address will not become allowed by retrying.
func transportError(prefix string, err error) *SendError {
	if urlError, ok := errors.AsType[*url.Error](err); ok {
		err = urlError.Err
	}
	if refused, ok := errors.AsType[*refusedDestinationError](err); ok {
		return &SendError{Message: prefix + ": " + refused.Error(), Permanent: true}
	}
	return &SendError{Message: prefix + ": " + truncate(err.Error(), maxErrorRunes)}
}

func retryableStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500
}

func retryAfterHeader(header http.Header) time.Duration {
	seconds, err := strconv.Atoi(header.Get("Retry-After"))
	if err != nil || seconds <= 0 {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
