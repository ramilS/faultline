package issue

import (
	"fmt"
	"slices"
	"time"
)

type IgnoreKind string

const (
	IgnoreForDuration IgnoreKind = "duration"
	IgnoreForEvents   IgnoreKind = "events"
	IgnoreForUsers    IgnoreKind = "users"
)

type IgnoreCondition struct {
	Kind     IgnoreKind
	Duration time.Duration
	Count    int64
}

type ActiveIgnore struct {
	Kind  IgnoreKind
	Until time.Time
	Count int64
	SoFar int64
}

type IgnoreConditionError struct {
	Field  string
	Reason string
}

func (e *IgnoreConditionError) Error() string {
	if e.Field == "" {
		return "invalid ignore condition: " + e.Reason
	}
	return fmt.Sprintf("invalid ignore condition %s: %s", e.Field, e.Reason)
}

var ignoreDurations = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
	"30d": 30 * 24 * time.Hour,
}

var ignoreCounts = []int64{10, 100, 1000}

func NewIgnoreCondition(duration *string, events, users *int64) (IgnoreCondition, error) {
	named := 0
	for _, present := range []bool{duration != nil, events != nil, users != nil} {
		if present {
			named++
		}
	}
	if named != 1 {
		return IgnoreCondition{}, &IgnoreConditionError{Reason: "name exactly one of duration, events, users"}
	}
	switch {
	case duration != nil:
		length, ok := ignoreDurations[*duration]
		if !ok {
			return IgnoreCondition{}, &IgnoreConditionError{Field: "duration", Reason: "must be one of 1h, 24h, 7d, 30d"}
		}
		return IgnoreCondition{Kind: IgnoreForDuration, Duration: length}, nil
	case events != nil:
		if !slices.Contains(ignoreCounts, *events) {
			return IgnoreCondition{}, &IgnoreConditionError{Field: "events", Reason: "must be one of 10, 100, 1000"}
		}
		return IgnoreCondition{Kind: IgnoreForEvents, Count: *events}, nil
	default:
		if !slices.Contains(ignoreCounts, *users) {
			return IgnoreCondition{}, &IgnoreConditionError{Field: "users", Reason: "must be one of 10, 100, 1000"}
		}
		return IgnoreCondition{Kind: IgnoreForUsers, Count: *users}, nil
	}
}
