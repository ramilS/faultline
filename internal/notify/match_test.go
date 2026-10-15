package notify

import (
	"reflect"
	"testing"
)

func TestMatch(t *testing.T) {
	everything := ChannelFilter{ID: 1, NotifyNew: true, NotifyRegression: true, MinLevel: "debug"}
	newIssue := IssueChange{IssueID: 10, Trigger: TriggerNew, Level: "error", Environments: []string{"staging", "production"}}

	cases := map[string]struct {
		channel ChannelFilter
		change  IssueChange
		want    []Delivery
	}{
		"an unfiltered channel takes a new issue and reports its first environment": {
			everything, newIssue,
			[]Delivery{{ChannelID: 1, IssueID: 10, Trigger: TriggerNew, Environment: "production"}},
		},
		"an unfiltered channel takes an issue whose events carried no environment": {
			everything, IssueChange{IssueID: 10, Trigger: TriggerNew, Level: "error"},
			[]Delivery{{ChannelID: 1, IssueID: 10, Trigger: TriggerNew, Environment: ""}},
		},
		"a channel muted for new issues skips one": {
			ChannelFilter{ID: 1, NotifyRegression: true, MinLevel: "debug"}, newIssue, nil,
		},
		"a channel muted for regressions skips one": {
			ChannelFilter{ID: 1, NotifyNew: true, MinLevel: "debug"},
			IssueChange{IssueID: 10, Trigger: TriggerRegression, Level: "error"}, nil,
		},
		"a regression reaches a channel that wants regressions": {
			ChannelFilter{ID: 1, NotifyRegression: true, MinLevel: "debug"},
			IssueChange{IssueID: 10, Trigger: TriggerRegression, Level: "error"},
			[]Delivery{{ChannelID: 1, IssueID: 10, Trigger: TriggerRegression}},
		},
		"a level below the channel minimum is skipped": {
			ChannelFilter{ID: 1, NotifyNew: true, MinLevel: "error"},
			IssueChange{IssueID: 10, Trigger: TriggerNew, Level: "warning"}, nil,
		},
		"a level equal to the channel minimum passes": {
			ChannelFilter{ID: 1, NotifyNew: true, MinLevel: "warning"},
			IssueChange{IssueID: 10, Trigger: TriggerNew, Level: "warning"},
			[]Delivery{{ChannelID: 1, IssueID: 10, Trigger: TriggerNew}},
		},
		"an unknown issue level is ranked as error rather than dropped": {
			ChannelFilter{ID: 1, NotifyNew: true, MinLevel: "error"},
			IssueChange{IssueID: 10, Trigger: TriggerNew, Level: "critical"},
			[]Delivery{{ChannelID: 1, IssueID: 10, Trigger: TriggerNew}},
		},
		"an environment filter reports the environment it matched": {
			ChannelFilter{ID: 1, NotifyNew: true, MinLevel: "debug", Environments: []string{"staging"}}, newIssue,
			[]Delivery{{ChannelID: 1, IssueID: 10, Trigger: TriggerNew, Environment: "staging"}},
		},
		"an environment filter with no overlap skips the change": {
			ChannelFilter{ID: 1, NotifyNew: true, MinLevel: "debug", Environments: []string{"development"}}, newIssue, nil,
		},
		"an environment filter skips an issue whose events carried no environment": {
			ChannelFilter{ID: 1, NotifyNew: true, MinLevel: "debug", Environments: []string{"production"}},
			IssueChange{IssueID: 10, Trigger: TriggerNew, Level: "error"}, nil,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := Match([]ChannelFilter{tc.channel}, []IssueChange{tc.change})
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Match = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestMatchFansEveryChangeOutToEveryAcceptingChannel(t *testing.T) {
	channels := []ChannelFilter{
		{ID: 1, NotifyNew: true, NotifyRegression: true, MinLevel: "debug"},
		{ID: 2, NotifyNew: true, MinLevel: "fatal"},
	}
	changes := []IssueChange{
		{IssueID: 10, Trigger: TriggerNew, Level: "fatal"},
		{IssueID: 11, Trigger: TriggerRegression, Level: "fatal"},
	}

	want := []Delivery{
		{ChannelID: 1, IssueID: 10, Trigger: TriggerNew},
		{ChannelID: 2, IssueID: 10, Trigger: TriggerNew},
		{ChannelID: 1, IssueID: 11, Trigger: TriggerRegression},
	}
	if got := Match(channels, changes); !reflect.DeepEqual(got, want) {
		t.Fatalf("Match = %+v, want %+v", got, want)
	}
}
