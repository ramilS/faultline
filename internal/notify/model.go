// Package notify sends a project's new and regressed issues to the
// destinations (Telegram chats, webhooks) its owner configured.
package notify

import (
	"errors"
	"time"
)

var (
	ErrChannelNotFound = errors.New("notification channel not found")
	// ErrInvalidChannel wraps every validation failure; its message is safe
	// to return to the caller as-is.
	ErrInvalidChannel = errors.New("invalid notification channel")
	// ErrTooManyChannels reports a project already at MaxChannelsPerProject.
	ErrTooManyChannels = errors.New("too many notification channels")
)

// MaxChannelsPerProject bounds how many channels a project may hold, so one
// issue cannot fan out into an unbounded number of deliveries.
const MaxChannelsPerProject = 20

type Kind string

const (
	KindTelegram Kind = "telegram"
	KindWebhook  Kind = "webhook"
)

// Trigger is the issue transition a notification reports.
type Trigger string

const (
	TriggerNew        Trigger = "new"
	TriggerRegression Trigger = "regression"
	// TriggerTest marks the message the dashboard's "send test" button sends.
	// It is never queued: deliveries only ever carry new or regression.
	TriggerTest Trigger = "test"
)

// Channel is a configured destination as the dashboard sees it. The secret
// itself never leaves storage through this type; HasSecret only says whether
// one is set.
type Channel struct {
	ID               int64
	ProjectID        int64
	Name             string
	Kind             Kind
	Target           string
	HasSecret        bool
	Enabled          bool
	NotifyNew        bool
	NotifyRegression bool
	Environments     []string
	MinLevel         string
	LastSuccessAt    *time.Time
	LastError        string
	LastErrorAt      *time.Time
	CreatedAt        time.Time
}

// ChannelInput is a create or full update of a channel. A nil Secret on
// update keeps the stored one, so the dashboard can save other fields without
// ever having read the secret back.
type ChannelInput struct {
	Name             string
	Kind             Kind
	Target           string
	Secret           *string
	Enabled          bool
	NotifyNew        bool
	NotifyRegression bool
	Environments     []string
	MinLevel         string
}

// Destination is everything a Sender needs to reach a channel, including the
// secret. It exists only between storage and the sender.
type Destination struct {
	Kind   Kind
	Target string
	Secret string
}

// Notification is the rendered-independent content of one message.
// IssueURL is empty when the instance has no public URL configured.
type Notification struct {
	Trigger      Trigger
	ProjectSlug  string
	ProjectName  string
	ProjectEmoji string
	IssueID      int64
	Title        string
	Culprit      string
	Level        string
	Environment  string
	// EventID is the event that caused the notification, or "" for one
	// queued before deliveries recorded it.
	EventID  string
	IssueURL string
}

// levelRanks orders the levels ingest accepts (see ingest.validLevel).
const levelInfo = "info"

var levelRanks = map[string]int{"debug": 0, levelInfo: 1, "warning": 2, "error": 3, "fatal": 4}

func validLevel(level string) bool {
	_, ok := levelRanks[level]
	return ok
}

// levelAtLeast reports whether level meets minimum. An unknown issue level
// is ranked as error: ingest rejects unknown levels, so one reaching here is
// a bug, and dropping its notification silently would hide it.
func levelAtLeast(level, minimum string) bool {
	rank, ok := levelRanks[level]
	if !ok {
		rank = levelRanks["error"]
	}
	return rank >= levelRanks[minimum]
}
