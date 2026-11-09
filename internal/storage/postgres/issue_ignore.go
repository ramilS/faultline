package postgres

import (
	"time"

	"github.com/ramilS/faultline/internal/issue"
)

type ignoreColumns struct {
	until           *time.Time
	timesSeenTarget *int64
	usersSeenTarget *int64
	threshold       *int64
}

func (c ignoreColumns) active(timesSeen, usersSeen int64) *issue.ActiveIgnore {
	switch {
	case c.until != nil:
		return &issue.ActiveIgnore{Kind: issue.IgnoreForDuration, Until: c.until.UTC()}
	case c.timesSeenTarget != nil && c.threshold != nil:
		return countedIgnore(issue.IgnoreForEvents, *c.timesSeenTarget, *c.threshold, timesSeen)
	case c.usersSeenTarget != nil && c.threshold != nil:
		return countedIgnore(issue.IgnoreForUsers, *c.usersSeenTarget, *c.threshold, usersSeen)
	default:
		return nil
	}
}

func countedIgnore(kind issue.IgnoreKind, target, threshold, seen int64) *issue.ActiveIgnore {
	return &issue.ActiveIgnore{Kind: kind, Count: threshold, SoFar: seen - (target - threshold)}
}
