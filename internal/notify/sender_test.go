package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testBotToken = "123456789:AAEhBP0av28fsJz-2kqEs_a1b2c3d4e5f6g"

var sampleNotification = Notification{Trigger: TriggerNew, ProjectSlug: "shop", ProjectName: "Shop", IssueID: 7, Title: "boom", Level: "error"}

type capturedRequest struct {
	path   string
	header http.Header
	body   []byte
}

func recordingServer(t *testing.T, status int, reply string, header http.Header) (*httptest.Server, *capturedRequest) {
	t.Helper()
	captured := &capturedRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured.path = r.URL.Path
		captured.header = r.Header.Clone()
		captured.body, _ = io.ReadAll(r.Body)
		maps.Copy(w.Header(), header)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, reply)
	}))
	t.Cleanup(server.Close)
	return server, captured
}

// loopbackSender reaches an httptest server, which listens on loopback —
// the very address the production webhook client refuses. Everything else
// about the client (timeout, no redirects) matches production.
func loopbackSender(options SenderOptions) *Sender {
	options.Client = newClient(http.DefaultTransport)
	return NewSender(options)
}

func telegramSender(server *httptest.Server) *Sender {
	return NewSender(SenderOptions{TelegramAPI: server.URL})
}

func TestTelegramSendPostsSendMessageWithHTMLAndNoPreview(t *testing.T) {
	server, captured := recordingServer(t, http.StatusOK, `{"ok":true,"result":{}}`, nil)

	err := telegramSender(server).Send(t.Context(), Destination{Kind: KindTelegram, Target: "-1001234567890", Secret: testBotToken}, sampleNotification, 1)

	if err != nil {
		t.Fatalf("Send = %v", err)
	}
	if want := "/bot" + testBotToken + "/sendMessage"; captured.path != want {
		t.Fatalf("path = %q, want %q", captured.path, want)
	}
	var body map[string]any
	if err := json.Unmarshal(captured.body, &body); err != nil {
		t.Fatal(err)
	}
	if body["chat_id"] != "-1001234567890" || body["parse_mode"] != "HTML" || body["text"] != TelegramText(sampleNotification) {
		t.Fatalf("body = %s", captured.body)
	}
	if preview, _ := body["link_preview_options"].(map[string]any); preview["is_disabled"] != true {
		t.Fatalf("link_preview_options = %v, want is_disabled true", body["link_preview_options"])
	}
}

func TestTelegramSendClassifiesRejections(t *testing.T) {
	cases := map[string]struct {
		status        int
		reply         string
		wantMessage   string
		wantPermanent bool
		wantRetry     time.Duration
	}{
		"an unknown chat is permanent and carries Telegram's reason": {
			http.StatusBadRequest, `{"ok":false,"error_code":400,"description":"Bad Request: chat not found"}`,
			"telegram: HTTP 400: Bad Request: chat not found", true, 0,
		},
		"flood control is retried after Telegram's own delay": {
			http.StatusTooManyRequests, `{"ok":false,"error_code":429,"description":"Too Many Requests: retry after 17","parameters":{"retry_after":17}}`,
			"telegram: HTTP 429: Too Many Requests: retry after 17", false, 17 * time.Second,
		},
		"a server error is retried": {
			http.StatusBadGateway, `<html>bad gateway</html>`, "telegram: HTTP 502", false, 0,
		},
		"a 200 that is not ok is still a failure": {
			http.StatusOK, `{"ok":false,"description":"odd"}`, "telegram: HTTP 200: odd", true, 0,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server, _ := recordingServer(t, tc.status, tc.reply, nil)

			err := telegramSender(server).Send(t.Context(), Destination{Kind: KindTelegram, Target: "1", Secret: testBotToken}, sampleNotification, 1)

			var failure *SendError
			if !errors.As(err, &failure) {
				t.Fatalf("Send = %v, want *SendError", err)
			}
			if failure.Message != tc.wantMessage || failure.Permanent != tc.wantPermanent || failure.RetryAfter != tc.wantRetry {
				t.Fatalf("SendError = %+v, want message %q permanent %v retry %v", failure, tc.wantMessage, tc.wantPermanent, tc.wantRetry)
			}
		})
	}
}

// The error is stored and shown in the dashboard, and net/http's own error
// for a failed request quotes the whole URL — the bot token is in its path.
func TestTelegramTransportErrorDoesNotLeakTheToken(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unreachable := "http://" + listener.Addr().String()
	listener.Close()

	err = NewSender(SenderOptions{TelegramAPI: unreachable}).Send(t.Context(), Destination{Kind: KindTelegram, Target: "1", Secret: testBotToken}, sampleNotification, 1)

	var failure *SendError
	if !errors.As(err, &failure) || failure.Permanent {
		t.Fatalf("Send = %#v, want a retryable *SendError", err)
	}
	if strings.Contains(failure.Message, testBotToken) || strings.Contains(failure.Message, "/bot") {
		t.Fatalf("error message leaks the token: %q", failure.Message)
	}
	if !strings.HasPrefix(failure.Message, "telegram: ") {
		t.Fatalf("message = %q, want a telegram: prefix", failure.Message)
	}
}

func TestWebhookSendSignsTheBodyWithTheTimestamp(t *testing.T) {
	server, captured := recordingServer(t, http.StatusNoContent, "", nil)
	sentAt := time.Unix(1_760_000_000, 0)
	sender := loopbackSender(SenderOptions{Now: func() time.Time { return sentAt }})

	err := sender.Send(t.Context(), Destination{Kind: KindWebhook, Target: server.URL + "/hook", Secret: "s3cret"}, sampleNotification, 42)

	if err != nil {
		t.Fatalf("Send = %v", err)
	}
	wantBody, _ := WebhookBody(sampleNotification, 42, sentAt)
	if !bytes.Equal(captured.body, wantBody) {
		t.Fatalf("body = %s, want %s", captured.body, wantBody)
	}
	header := captured.header
	if header.Get("X-Faultline-Event") != "issue.new" || header.Get("X-Faultline-Delivery") != "42" || header.Get("X-Faultline-Timestamp") != "1760000000" {
		t.Fatalf("headers = %v", header)
	}
	if want := "sha256=" + WebhookSignature("s3cret", "1760000000", wantBody); header.Get("X-Faultline-Signature") != want {
		t.Fatalf("signature = %q, want %q", header.Get("X-Faultline-Signature"), want)
	}
}

// A vector computed outside Go — hmac.new(b"key", b"1.{}", sha256) in
// Python — pins the scheme itself, so a receiver written against the
// documentation keeps verifying after a refactor of WebhookSignature.
func TestWebhookSignatureMatchesAKnownVector(t *testing.T) {
	got := WebhookSignature("key", "1", []byte("{}"))
	if want := "1ba6b8171186efc613e8bcc0cbdab2748f24984d7c5a84faa2637afa0e40d224"; got != want {
		t.Fatalf("WebhookSignature = %s, want %s", got, want)
	}
}

func TestWebhookSendWithoutSecretSendsNoSignature(t *testing.T) {
	server, captured := recordingServer(t, http.StatusOK, "", nil)

	if err := loopbackSender(SenderOptions{}).Send(t.Context(), Destination{Kind: KindWebhook, Target: server.URL}, sampleNotification, 1); err != nil {
		t.Fatalf("Send = %v", err)
	}
	if captured.header.Get("X-Faultline-Signature") != "" || captured.header.Get("X-Faultline-Timestamp") != "" {
		t.Fatalf("unsigned webhook sent signature headers: %v", captured.header)
	}
}

func TestWebhookSendClassifiesStatuses(t *testing.T) {
	cases := map[string]struct {
		status        int
		header        http.Header
		wantPermanent bool
		wantRetry     time.Duration
	}{
		"not found is permanent":             {http.StatusNotFound, nil, true, 0},
		"a redirect is not followed":         {http.StatusMovedPermanently, http.Header{"Location": {"https://elsewhere.example/"}}, true, 0},
		"a server error is retried":          {http.StatusServiceUnavailable, nil, false, 0},
		"rate limiting honours Retry-After":  {http.StatusTooManyRequests, http.Header{"Retry-After": {"120"}}, false, 2 * time.Minute},
		"a request timeout is retried":       {http.StatusRequestTimeout, nil, false, 0},
		"an unparseable Retry-After is zero": {http.StatusTooManyRequests, http.Header{"Retry-After": {"soon"}}, false, 0},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			server, _ := recordingServer(t, tc.status, "", tc.header)

			err := loopbackSender(SenderOptions{}).Send(t.Context(), Destination{Kind: KindWebhook, Target: server.URL}, sampleNotification, 1)

			var failure *SendError
			if !errors.As(err, &failure) {
				t.Fatalf("Send = %v, want *SendError", err)
			}
			if failure.Permanent != tc.wantPermanent || failure.RetryAfter != tc.wantRetry {
				t.Fatalf("SendError = %+v, want permanent %v retry %v", failure, tc.wantPermanent, tc.wantRetry)
			}
		})
	}
}

func TestWebhookTransportErrorDoesNotQuoteTheURL(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := NewSender(SenderOptions{}).Send(ctx, Destination{Kind: KindWebhook, Target: "https://hooks.example/T000/secret-path"}, sampleNotification, 1)

	var failure *SendError
	if !errors.As(err, &failure) || strings.Contains(failure.Message, "secret-path") {
		t.Fatalf("Send = %v, want a *SendError without the URL", err)
	}
}
