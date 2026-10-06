package issue_test

import (
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/issue"
)

func TestWindowValidate(t *testing.T) {
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		window  issue.Window
		wantErr bool
	}{
		{name: "zero window is valid", window: issue.Window{}},
		{name: "trailing window is valid", window: issue.Window{Trailing: 24 * time.Hour}},
		{
			name:   "absolute window inside the cap is valid",
			window: issue.Window{Start: base.Add(-48 * time.Hour), End: base},
		},
		{
			name:    "trailing and absolute together is invalid",
			window:  issue.Window{Trailing: time.Hour, Start: base.Add(-time.Hour), End: base},
			wantErr: true,
		},
		{
			name:    "negative trailing duration is invalid",
			window:  issue.Window{Trailing: -time.Hour},
			wantErr: true,
		},
		{
			name:    "start without end is invalid",
			window:  issue.Window{Start: base},
			wantErr: true,
		},
		{
			name:    "end before start is invalid",
			window:  issue.Window{Start: base, End: base.Add(-time.Hour)},
			wantErr: true,
		},
		{
			name:    "absolute window past the cap is invalid",
			window:  issue.Window{Start: base.Add(-issue.MaxAbsoluteWindow - time.Minute), End: base},
			wantErr: true,
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.window.Validate()
			if testCase.wantErr && err == nil {
				t.Fatalf("Validate() = nil, want error")
			}
			if !testCase.wantErr && err != nil {
				t.Fatalf("Validate() = %v, want nil", err)
			}
		})
	}
}

func TestWindowIsAbsolute(t *testing.T) {
	base := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	if (issue.Window{Trailing: time.Hour}).IsAbsolute() {
		t.Fatal("trailing window reported as absolute")
	}
	if !(issue.Window{Start: base.Add(-time.Hour), End: base}).IsAbsolute() {
		t.Fatal("absolute window not reported as absolute")
	}
}
