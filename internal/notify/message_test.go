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

	want := "<b>🆕 New issue</b>\n<b>📁 Shop &amp; Co</b>\n\n" +
		"<blockquote>TypeError: &lt;b&gt;x&lt;/b&gt; is undefined</blockquote>\n\n" +
		"<b>Location:</b> <code>render(&lt;App/&gt;)</code>\n" +
		"<b>Level:</b> ❔ <code>error &amp; &lt;fatal&gt;</code>\n" +
		"<b>Environment:</b> <code>prod &amp; &lt;blue&gt;</code>\n\n" +
		`<a href="https://faultline.example/shop/issues/7?a=1&amp;b=2">Open in Faultline →</a>`
	if text != want {
		t.Fatalf("TelegramText =\n%s\nwant\n%s", text, want)
	}
}

func TestTelegramTextOmitsAbsentCulpritEnvironmentAndLink(t *testing.T) {
	text := TelegramText(Notification{Trigger: TriggerRegression, ProjectName: "Shop", Title: "boom", Level: "fatal"})

	if want := "<b>🔁 Regression</b>\n<b>📁 Shop</b>\n\n<blockquote>boom</blockquote>\n\n<b>Level:</b> 💀 <code>fatal</code>"; text != want {
		t.Fatalf("TelegramText = %q, want %q", text, want)
	}
}

func TestTelegramTextShowsTheDisplayLabelsOverTheRawOnes(t *testing.T) {
	text := TelegramText(Notification{
		Trigger: TriggerNew, Title: "bu4: boom", Culprit: "Muslicast+0xfa6408",
		DisplayTitle: "SourceException: boom", DisplayCulprit: "PlaybackController.invoke", Level: "error",
	})

	if !strings.Contains(text, "<blockquote>SourceException: boom</blockquote>") || !strings.Contains(text, "<code>PlaybackController.invoke</code>") {
		t.Fatalf("TelegramText = %q, want the display labels", text)
	}
	if strings.Contains(text, "bu4") || strings.Contains(text, "Muslicast+") {
		t.Fatalf("TelegramText = %q, still shows a raw label", text)
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
		Title: "bu4: boom", Culprit: "a.b.c", DisplayTitle: "SourceException: boom", DisplayCulprit: "Source.open (Source.kt)",
		Level: "error", Environment: "production",
		EventID:  "0b7e3a52-8c5e-4c43-9a4e-6f0a3f0f2b11",
		IssueURL: "https://faultline.example/shop/issues/7",
	}, 42, sentAt)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"event":"issue.regression","delivery_id":42,"sent_at":"2026-10-15T06:30:00Z",` +
		`"project":{"slug":"shop","name":"Shop"},` +
		`"issue":{"id":7,"title":"bu4: boom","culprit":"a.b.c","display_title":"SourceException: boom","display_culprit":"Source.open (Source.kt)",` +
		`"level":"error","url":"https://faultline.example/shop/issues/7"},` +
		`"environment":"production","event_id":"0b7e3a52-8c5e-4c43-9a4e-6f0a3f0f2b11"}`
	if string(body) != want {
		t.Fatalf("body =\n%s\nwant\n%s", body, want)
	}
}

func TestWebhookBodySendsANullURLWithoutAPublicURL(t *testing.T) {
	body, err := WebhookBody(Notification{Trigger: TriggerNew, IssueID: 7}, 1, time.Unix(0, 0))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `"url":null`) || !strings.Contains(string(body), `"event_id":null`) {
		t.Fatalf("body = %s, want issue.url and event_id null", body)
	}
}

func TestIssueURL(t *testing.T) {
	const eventID = "0b7e3a52-8c5e-4c43-9a4e-6f0a3f0f2b11"
	cases := map[string]struct {
		publicURL, slug, eventID, environment string
		want                                  string
	}{
		"no public URL means no link":     {"", "shop", eventID, "production", ""},
		"a trailing slash is not doubled": {"https://faultline.example/", "shop", "", "", "https://faultline.example/shop/issues/7"},
		"the slug is path-escaped":        {"https://faultline.example", "a/b", "", "", "https://faultline.example/a%2Fb/issues/7"},
		"the link opens on the event, under its environment": {
			"https://faultline.example", "shop", eventID, "production",
			"https://faultline.example/shop/issues/7?environment=production&event=" + eventID,
		},
		"an event without an environment links to the event alone": {
			"https://faultline.example", "shop", eventID, "",
			"https://faultline.example/shop/issues/7?event=" + eventID,
		},
		"the environment is query-escaped": {
			"https://faultline.example", "shop", "", "a&b c",
			"https://faultline.example/shop/issues/7?environment=a%26b+c",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := IssueURL(tc.publicURL, tc.slug, 7, tc.eventID, tc.environment); got != tc.want {
				t.Fatalf("IssueURL = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTelegramIdentityAndSeverity(t *testing.T) {
	for _, tc := range []struct{ level, icon string }{{"fatal", "💀"}, {"error", "🔴"}, {"warning", "⚠️"}, {"info", "ℹ️"}, {"debug", "🐛"}, {"unknown", "❔"}} {
		text := TelegramText(Notification{Trigger: TriggerTest, ProjectName: "Shop", ProjectEmoji: "<&", Level: tc.level})
		if !strings.Contains(text, "🧪 Test notification") || !strings.Contains(text, "&lt;&amp; Shop") || !strings.Contains(text, "Level:</b> "+tc.icon+" <code>"+tc.level+"</code>") {
			t.Fatalf("unexpected message %s", text)
		}
	}
}

func TestEveryTriggerHasItsOwnWording(t *testing.T) {
	cases := []struct {
		trigger      Trigger
		heading      string
		webhookEvent string
	}{
		{TriggerNew, "🆕 New issue", "issue.new"},
		{TriggerRegression, "🔁 Regression", "issue.regression"},
		{TriggerEscalation, "📈 Ignored issue is back", "issue.escalation"},
		{TriggerTest, "🧪 Test notification", "issue.test"},
		{Trigger("unknown"), "❔ Issue", "issue.unknown"},
	}
	for _, tc := range cases {
		t.Run(string(tc.trigger), func(t *testing.T) {
			text := TelegramText(Notification{Trigger: tc.trigger, ProjectName: "Shop", Title: "boom", Level: "error"})
			if !strings.HasPrefix(text, "<b>"+tc.heading+"</b>\n") {
				t.Fatalf("TelegramText starts %q, want heading %q", text, tc.heading)
			}
			if got := WebhookEvent(tc.trigger); got != tc.webhookEvent {
				t.Fatalf("WebhookEvent = %q, want %q", got, tc.webhookEvent)
			}
		})
	}
}
