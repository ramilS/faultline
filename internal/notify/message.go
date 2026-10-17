package notify

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	// maxTitleRunes and maxCulpritRunes keep a Telegram message well inside
	// its 4096-character limit even after HTML escaping inflates them.
	maxTitleRunes   = 300
	maxCulpritRunes = 200
)

// IssueURL links to the issue in the dashboard, or returns "" when the
// instance has no public URL to build one from.
//
// It names the event that caused the notification and the environment it was
// reported for, so the page opens on that event under that environment rather
// than on the newest event of any environment: a production alert that opens
// on a staging event shows the reader a different failure from the one they
// were told about. Either is left out when empty.
func IssueURL(publicURL, projectSlug string, issueID int64, eventID, environment string) string {
	if publicURL == "" {
		return ""
	}
	link := strings.TrimRight(publicURL, "/") + "/" + url.PathEscape(projectSlug) + "/issues/" + strconv.FormatInt(issueID, 10)
	query := url.Values{}
	if eventID != "" {
		query.Set("event", eventID)
	}
	if environment != "" {
		query.Set("environment", environment)
	}
	if len(query) == 0 {
		return link
	}
	return link + "?" + query.Encode()
}

func headline(trigger Trigger) string {
	switch trigger {
	case TriggerNew:
		return "New issue"
	case TriggerRegression:
		return "Regression"
	case TriggerTest:
		return "Test notification"
	}
	return "Issue"
}

// TelegramText renders n for sendMessage with parse_mode HTML. Telegram
// requires only <, > and & to be escaped outside tags; html.EscapeString also
// escapes quotes, which Telegram accepts as entities.
func TelegramText(n Notification) string {
	var text strings.Builder
	emoji := n.ProjectEmoji
	if emoji == "" {
		emoji = "📁"
	}
	fmt.Fprintf(&text, "<b>%s %s</b>\n<b>%s %s</b>\n\n", triggerIcon(n.Trigger), headline(n.Trigger), html.EscapeString(emoji), html.EscapeString(n.ProjectName))
	fmt.Fprintf(&text, "<blockquote>%s</blockquote>\n", html.EscapeString(truncate(n.Title, maxTitleRunes)))
	if n.Culprit != "" {
		fmt.Fprintf(&text, "\n<b>Location:</b> <code>%s</code>", html.EscapeString(truncate(n.Culprit, maxCulpritRunes)))
	}
	fmt.Fprintf(&text, "\n<b>Level:</b> %s <code>%s</code>", levelIcon(n.Level), html.EscapeString(n.Level))
	if n.Environment != "" {
		fmt.Fprintf(&text, "\n<b>Environment:</b> <code>%s</code>", html.EscapeString(n.Environment))
	}
	if n.IssueURL != "" {
		fmt.Fprintf(&text, "\n\n<a href=\"%s\">Open in Faultline →</a>", html.EscapeString(n.IssueURL))
	}
	return text.String()
}

// webhookPayload pins the webhook wire shape; receivers read these keys by
// name, so renaming a json tag is a breaking change.
type webhookPayload struct {
	Event       string         `json:"event"`
	DeliveryID  int64          `json:"delivery_id"`
	SentAt      time.Time      `json:"sent_at"`
	Project     webhookProject `json:"project"`
	Issue       webhookIssue   `json:"issue"`
	Environment string         `json:"environment"`
	EventID     *string        `json:"event_id"`
}

type webhookProject struct {
	Slug string `json:"slug"`
	Name string `json:"name"`
}

type webhookIssue struct {
	ID      int64   `json:"id"`
	Title   string  `json:"title"`
	Culprit string  `json:"culprit"`
	Level   string  `json:"level"`
	URL     *string `json:"url"`
}

// WebhookEvent is the event name a webhook receives for trigger, also sent
// as the X-Faultline-Event header.
func WebhookEvent(trigger Trigger) string {
	return "issue." + string(trigger)
}

// WebhookBody renders n as the JSON body of a webhook POST.
func WebhookBody(n Notification, deliveryID int64, sentAt time.Time) ([]byte, error) {
	payload := webhookPayload{
		Event:       WebhookEvent(n.Trigger),
		DeliveryID:  deliveryID,
		SentAt:      sentAt.UTC(),
		Project:     webhookProject{Slug: n.ProjectSlug, Name: n.ProjectName},
		Issue:       webhookIssue{ID: n.IssueID, Title: n.Title, Culprit: n.Culprit, Level: n.Level},
		Environment: n.Environment,
	}
	if n.IssueURL != "" {
		payload.Issue.URL = &n.IssueURL
	}
	if n.EventID != "" {
		payload.EventID = &n.EventID
	}
	return json.Marshal(payload)
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}

func triggerIcon(trigger Trigger) string {
	switch trigger {
	case TriggerNew:
		return "🆕"
	case TriggerRegression:
		return "🔁"
	case TriggerTest:
		return "🧪"
	}
	return "❔"
}
func levelIcon(level string) string {
	switch level {
	case "fatal":
		return "💀"
	case "error":
		return "🔴"
	case "warning":
		return "⚠️"
	case levelInfo:
		return "ℹ️"
	case "debug":
		return "🐛"
	}
	return "❔"
}
