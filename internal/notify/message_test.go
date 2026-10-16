package notify

import (
	"strings"
	"testing"
	"time"
)

func TestTelegramTextEscapesUserControlledText(t *testing.T) {
	text := TelegramText(Notification{
		Trigger:     TriggerNew,
		ProjectName: "Shop & Co",
		Title:       "TypeError: <b>x</b> is undefined",
		Culprit:     "render(<App/>)",
		Level:       "error & <fatal>",
		Environment: "prod & <blue>",
		IssueURL:    "https://faultline.example/shop/issues/7?a=1&b=2",
	})

	want := "<b>New issue</b>\n<b>Shop &amp; Co</b>\n\n" +
		"<blockquote>TypeError: &lt;b&gt;x&lt;/b&gt; is undefined</blockquote>\n\n" +
		"<b>Location:</b> <code>render(&lt;App/&gt;)</code>\n" +
		"<b>Level:</b> <code>error &amp; &lt;fatal&gt;</code>\n" +
		"<b>Environment:</b> <code>prod &amp; &lt;blue&gt;</code>\n\n" +
		`<a href="https://faultline.example/shop/issues/7?a=1&amp;b=2">Open in Faultline →</a>`
	if text != want {
		t.Fatalf("TelegramText =\n%s\nwant\n%s", text, want)
	}
}

func TestTelegramTextOmitsAbsentCulpritEnvironmentAndLink(t *testing.T) {
	text := TelegramText(Notification{Trigger: TriggerRegression, ProjectName: "Shop", Title: "boom", Level: "fatal"})

	if want := "<b>Regression</b>\n<b>Shop</b>\n\n<blockquote>boom</blockquote>\n\n<b>Level:</b> <code>fatal</code>"; text != want {
		t.Fatalf("TelegramText = %q, want %q", text, want)
	}
}

func TestTelegramTextTruncatesALongTitleByRunes(t *testing.T) {
	text := TelegramText(Notification{Trigger: TriggerNew, Title: strings.Repeat("ж", maxTitleRunes+50), Level: "error"})

	if want := strings.Repeat("ж", maxTitleRunes-1) + "…"; !strings.Contains(text, "<blockquote>"+want+"</blockquote>") {
		t.Fatalf("title was not cut to %d runes ending in an ellipsis: %q", maxTitleRunes, text)
	}
}

func TestWebhookBodyWireShape(t *testing.T) {
	sentAt := time.Date(2026, 10, 15, 9, 30, 0, 0, time.FixedZone("MSK", 3*3600))
	body, err := WebhookBody(Notification{
		Trigger: TriggerRegression, ProjectSlug: "shop", ProjectName: "Shop", IssueID: 7,
		Title: "boom", Culprit: "app.js", Level: "error", Environment: "production",
		IssueURL: "https://faultline.example/shop/issues/7",
	}, 42, sentAt)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"event":"issue.regression","delivery_id":42,"sent_at":"2026-10-15T06:30:00Z",` +
		`"project":{"slug":"shop","name":"Shop"},` +
		`"issue":{"id":7,"title":"boom","culprit":"app.js","level":"error","url":"https://faultline.example/shop/issues/7"},` +
		`"environment":"production"}`
	if string(body) != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}
}

func TestWebhookBodySendsANullURLWithoutAPublicURL(t *testing.T) {
	body, err := WebhookBody(Notification{Trigger: TriggerNew, IssueID: 7}, 1, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"url":null`) {
		t.Fatalf("body = %s, want issue.url null", body)
	}
}

func TestIssueURL(t *testing.T) {
	cases := map[string]struct {
		publicURL, slug string
		want            string
	}{
		"no public URL means no link":     {"", "shop", ""},
		"a trailing slash is not doubled": {"https://faultline.example/", "shop", "https://faultline.example/shop/issues/7"},
		"the slug is path-escaped":        {"https://faultline.example", "a/b", "https://faultline.example/a%2Fb/issues/7"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IssueURL(tc.publicURL, tc.slug, 7); got != tc.want {
				t.Fatalf("IssueURL = %q, want %q", got, tc.want)
			}
		})
	}
}
