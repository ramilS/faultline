package postgres

import (
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/storage"
)

func TestIssueChangesLinkTheFirstEventOfEachEnvironment(t *testing.T) {
	firstProduction, secondProduction, staging, untouched := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	batch := storage.Batch{Events: []storage.EventRow{
		{EventID: firstProduction, Fingerprint: "changed", Environment: "production"},
		{EventID: untouched, Fingerprint: "open", Environment: "production"},
		{EventID: secondProduction, Fingerprint: "changed", Environment: "production"},
		{EventID: staging, Fingerprint: "changed", Environment: "staging"},
	}}
	issueIDs := map[string]int64{"changed": 1, "open": 2}
	transitions := map[int64]issueTransition{1: {trigger: notify.TriggerNew, level: "error"}}

	got := issueChanges(batch, issueIDs, transitions)

	want := []notify.IssueChange{{
		IssueID: 1, Trigger: notify.TriggerNew, Level: "error",
		Environments: []string{"production", "staging"},
		EventIDs:     map[string]string{"production": firstProduction.String(), "staging": staging.String()},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("issueChanges = %+v, want %+v", got, want)
	}
}

// The first regressed event was stamped three days earlier by a device clearing
// an offline queue: the mark must date the reopening by when it arrived.
func TestRegressionMarksTakeTheFirstEventOfEachRegressedIssue(t *testing.T) {
	base := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	release := func(version string) *string { return &version }
	batch := storage.Batch{Events: []storage.EventRow{
		{Fingerprint: "new", ReceivedAt: base, Release: release("2.4.0")},
		{Fingerprint: "regressed", Timestamp: base.Add(-72 * time.Hour), ReceivedAt: base.Add(time.Second), Release: release("2.4.1")},
		{Fingerprint: "regressed", ReceivedAt: base.Add(2 * time.Second), Release: release("2.4.2")},
		{Fingerprint: "unreleased", ReceivedAt: base.Add(3 * time.Second)},
	}}
	issueIDs := map[string]int64{"new": 1, "regressed": 2, "unreleased": 3}
	transitions := map[int64]issueTransition{
		1: {trigger: notify.TriggerNew},
		2: {trigger: notify.TriggerRegression},
		3: {trigger: notify.TriggerRegression},
	}

	got := regressionMarks(batch, issueIDs, transitions)

	want := map[int64]issue.Regression{
		2: {At: base.Add(time.Second), Release: "2.4.1"},
		3: {At: base.Add(3 * time.Second), Release: ""},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("regressionMarks = %+v, want %+v", got, want)
	}
}
