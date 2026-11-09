package issue

import (
	"errors"
	"testing"
	"time"
)

func TestNewIgnoreConditionAcceptsEachOfferedChoice(t *testing.T) {
	text := func(value string) *string { return &value }
	count := func(value int64) *int64 { return &value }
	cases := map[string]struct {
		duration      *string
		events, users *int64
		want          IgnoreCondition
	}{
		"for an hour":      {duration: text("1h"), want: IgnoreCondition{Kind: IgnoreForDuration, Duration: time.Hour}},
		"for 24 hours":     {duration: text("24h"), want: IgnoreCondition{Kind: IgnoreForDuration, Duration: 24 * time.Hour}},
		"for 7 days":       {duration: text("7d"), want: IgnoreCondition{Kind: IgnoreForDuration, Duration: 7 * 24 * time.Hour}},
		"for 30 days":      {duration: text("30d"), want: IgnoreCondition{Kind: IgnoreForDuration, Duration: 30 * 24 * time.Hour}},
		"10 more events":   {events: count(10), want: IgnoreCondition{Kind: IgnoreForEvents, Count: 10}},
		"100 more events":  {events: count(100), want: IgnoreCondition{Kind: IgnoreForEvents, Count: 100}},
		"1000 more events": {events: count(1000), want: IgnoreCondition{Kind: IgnoreForEvents, Count: 1000}},
		"10 more users":    {users: count(10), want: IgnoreCondition{Kind: IgnoreForUsers, Count: 10}},
		"100 more users":   {users: count(100), want: IgnoreCondition{Kind: IgnoreForUsers, Count: 100}},
		"1000 more users":  {users: count(1000), want: IgnoreCondition{Kind: IgnoreForUsers, Count: 1000}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := NewIgnoreCondition(tc.duration, tc.events, tc.users)
			if err != nil || got != tc.want {
				t.Fatalf("NewIgnoreCondition = %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestNewIgnoreConditionNamesTheFieldItRejects(t *testing.T) {
	text := func(value string) *string { return &value }
	count := func(value int64) *int64 { return &value }
	cases := map[string]struct {
		duration      *string
		events, users *int64
		field         string
	}{
		"no condition at all":       {field: ""},
		"two conditions":            {duration: text("1h"), events: count(10), field: ""},
		"an unoffered duration":     {duration: text("2h"), field: "duration"},
		"a Go duration string":      {duration: text("1h0m0s"), field: "duration"},
		"an unoffered events count": {events: count(50), field: "events"},
		"a negative events count":   {events: count(-10), field: "events"},
		"an unoffered users count":  {users: count(5), field: "users"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := NewIgnoreCondition(tc.duration, tc.events, tc.users)
			conditionError, ok := errors.AsType[*IgnoreConditionError](err)
			if !ok {
				t.Fatalf("err = %v, want an IgnoreConditionError", err)
			}
			if conditionError.Field != tc.field {
				t.Fatalf("field = %q, want %q", conditionError.Field, tc.field)
			}
		})
	}
}
