package issue

import (
	"fmt"
	"time"
)

// MaxAbsoluteWindow bounds an absolute start/end range. issue_counts_hourly
// buckets hourly all the way out, so 90 days is ~2160 buckets per issue — the
// point at which a daily rollup becomes the answer rather than a wider scan.
const MaxAbsoluteWindow = 90 * 24 * time.Hour

// Field identifies which issue attribute a Predicate constrains.
type Field int

const (
	FieldStatus Field = iota
	FieldLevel
	FieldAssignee
	FieldEnvironment
	FieldRelease
	FieldTag
	FieldFirstSeen
	FieldLastSeen
	FieldTimesSeen
)

// Operator is the comparison a Predicate applies. Only FieldTimesSeen,
// FieldFirstSeen and FieldLastSeen accept anything other than OpEq.
type Operator int

const (
	OpEq Operator = iota
	OpLT
	OpLTE
	OpGT
	OpGTE
)

// Predicate is one filter clause. Values are OR'd within a single Predicate;
// Predicates are AND'd with each other. That pairing is what expresses
// `!release:[1.0, 2.0]` — "not in this set" — without a boolean tree, which is
// why issue search needs no expression parser.
type Predicate struct {
	Field  Field
	Key    string // tag key; empty for every field other than FieldTag
	Op     Operator
	Negate bool
	Values []string
}

// Window is the time range the list is scoped to. Exactly one form is set: a
// Trailing duration, or a Start/End pair. The zero Window means all time.
type Window struct {
	Trailing   time.Duration
	Start, End time.Time
}

func (w Window) IsZero() bool {
	return w.Trailing == 0 && w.Start.IsZero() && w.End.IsZero()
}

func (w Window) IsAbsolute() bool {
	return !w.Start.IsZero() || !w.End.IsZero()
}

func (w Window) Validate() error {
	if w.IsZero() {
		return nil
	}
	if w.Trailing != 0 {
		if w.IsAbsolute() {
			return fmt.Errorf("window: trailing and absolute range are mutually exclusive")
		}
		if w.Trailing < 0 {
			return fmt.Errorf("window: trailing duration must be positive")
		}
		return nil
	}
	if w.Start.IsZero() || w.End.IsZero() {
		return fmt.Errorf("window: start and end must be supplied together")
	}
	if !w.End.After(w.Start) {
		return fmt.Errorf("window: end must be after start")
	}
	if w.End.Sub(w.Start) > MaxAbsoluteWindow {
		return fmt.Errorf("window: range exceeds the %s maximum", MaxAbsoluteWindow)
	}
	return nil
}
