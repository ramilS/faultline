package notify

import "slices"

// IssueChange is one issue a batch created or reopened, with the
// environments of the events in that batch that touched it.
type IssueChange struct {
	IssueID      int64
	Trigger      Trigger
	Level        string
	Environments []string
	// EventIDs names, per environment, the first event of the batch that
	// landed on the issue there: the event a notification links to, so the
	// reader opens on what was reported rather than on whatever came last.
	EventIDs map[string]string
}

// ChannelFilter is the part of a channel that decides whether it receives a
// given change.
type ChannelFilter struct {
	ID               int64
	NotifyNew        bool
	NotifyRegression bool
	Environments     []string
	MinLevel         string
}

// Delivery is one message owed to one channel.
type Delivery struct {
	ChannelID   int64
	IssueID     int64
	Trigger     Trigger
	Environment string
	// EventID is the event that caused the change in Environment, or "" when
	// the change carried none.
	EventID string
}

// Match pairs every change with every channel whose filters accept it.
//
// The environment filter is judged on the events of the batch that caused
// the change: an issue first seen in staging does not notify a
// production-only channel, and when production events for it arrive later
// the issue is no longer new, so that channel never hears of it. This is the
// same "created in" semantics Sentry's issue alerts use.
func Match(channels []ChannelFilter, changes []IssueChange) []Delivery {
	var deliveries []Delivery
	for _, change := range changes {
		for _, channel := range channels {
			if !channel.wants(change.Trigger) || !levelAtLeast(change.Level, channel.MinLevel) {
				continue
			}
			environment, ok := matchEnvironment(channel.Environments, change.Environments)
			if !ok {
				continue
			}
			deliveries = append(deliveries, Delivery{ChannelID: channel.ID, IssueID: change.IssueID, Trigger: change.Trigger, Environment: environment, EventID: change.EventIDs[environment]})
		}
	}
	return deliveries
}

func (c ChannelFilter) wants(trigger Trigger) bool {
	switch trigger {
	case TriggerNew:
		return c.NotifyNew
	case TriggerRegression:
		return c.NotifyRegression
	case TriggerTest:
		return false
	}
	return false
}

// matchEnvironment returns the environment to report for a change: the
// first one, in sorted order, that the channel accepts. An empty channel
// list accepts every environment, including none at all.
func matchEnvironment(accepted, seen []string) (string, bool) {
	sorted := slices.Sorted(slices.Values(seen))
	if len(accepted) == 0 {
		if len(sorted) == 0 {
			return "", true
		}
		return sorted[0], true
	}
	for _, environment := range sorted {
		if slices.Contains(accepted, environment) {
			return environment, true
		}
	}
	return "", false
}
