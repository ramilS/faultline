package notify

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
)

func TestAllowedAddress(t *testing.T) {
	cases := []struct {
		address      string
		blocked      bool
		privateRange bool
	}{
		{"8.8.8.8", false, false},
		{"2001:4860:4860::8888", false, false},
		{"127.0.0.1", true, false},
		{"127.10.0.1", true, false},
		{"::1", true, false},
		{"::ffff:127.0.0.1", true, false},
		{"169.254.169.254", true, false},
		{"fe80::1", true, false},
		{"0.0.0.0", true, false},
		{"::", true, false},
		{"224.0.0.1", true, false},
		{"ff02::1", true, false},
		{"10.0.0.5", true, true},
		{"172.16.0.1", true, true},
		{"172.31.255.255", true, true},
		{"192.168.1.10", true, true},
		{"fd00::1", true, true},
		{"::ffff:10.0.0.5", true, true},
		{"100.100.100.200", true, true},
		// netip's own predicates see through the IPv4-mapped form; a
		// prefix match does not, which is what Unmap is there for.
		{"::ffff:100.100.100.200", true, true},
		{"172.32.0.1", false, false},
	}
	for _, tc := range cases {
		address := netip.MustParseAddr(tc.address)
		if got := allowedAddress(address, false); got == tc.blocked {
			t.Errorf("allowedAddress(%s, false) = %v, want %v", tc.address, got, !tc.blocked)
		}
		wantWithPrivate := !tc.blocked || tc.privateRange
		if got := allowedAddress(address, true); got != wantWithPrivate {
			t.Errorf("allowedAddress(%s, true) = %v, want %v", tc.address, got, wantWithPrivate)
		}
	}
}

func TestWebhookToALoopbackAddressIsRefusedBeforeConnecting(t *testing.T) {
	for name, target := range map[string]func(string) string{
		"by IP":                         func(url string) string { return url },
		"by a hostname resolving to it": func(url string) string { return strings.Replace(url, "127.0.0.1", "localhost", 1) },
	} {
		t.Run(name, func(t *testing.T) {
			server, captured := recordingServer(t, http.StatusNoContent, "", nil)

			err := NewSender(SenderOptions{AllowPrivateNetworks: true}).Send(t.Context(), Destination{Kind: KindWebhook, Target: target(server.URL)}, sampleNotification, 1)

			failure, ok := errors.AsType[*SendError](err)
			if !ok || !failure.Permanent || !strings.HasPrefix(failure.Message, "webhook: destination address is not allowed: ") {
				t.Fatalf("Send = %#v, want a permanent refusal", err)
			}
			if captured.path != "" {
				t.Fatalf("the refused destination still received %s", captured.path)
			}
		})
	}
}

func TestTelegramIsNotSubjectToTheWebhookGuard(t *testing.T) {
	server, captured := recordingServer(t, http.StatusOK, `{"ok":true}`, nil)

	err := NewSender(SenderOptions{TelegramAPI: server.URL}).Send(t.Context(), Destination{Kind: KindTelegram, Target: "1", Secret: testBotToken}, sampleNotification, 1)

	if err != nil || captured.path == "" {
		t.Fatalf("Send = %v, path = %q; the Telegram client must not apply the webhook guard", err, captured.path)
	}
}
