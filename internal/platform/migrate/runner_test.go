package migration

import (
	"bytes"
	"errors"
	"testing"

	gomigrate "github.com/golang-migrate/migrate/v4"
)

type fakeEngine struct {
	upErr      error
	stepsErr   error
	migrateErr error
	version    uint
	dirty      bool
	versionErr error
	steps      []int
	migrations []uint
}

func (f *fakeEngine) Up() error { return f.upErr }
func (f *fakeEngine) Steps(count int) error {
	f.steps = append(f.steps, count)
	return f.stepsErr
}
func (f *fakeEngine) Migrate(version uint) error {
	f.migrations = append(f.migrations, version)
	return f.migrateErr
}
func (f *fakeEngine) Version() (uint, bool, error) { return f.version, f.dirty, f.versionErr }

func TestExecuteUpTreatsNoChangeAsSuccess(t *testing.T) {
	for _, migrationErr := range []error{nil, gomigrate.ErrNoChange} {
		engine := &fakeEngine{upErr: migrationErr}
		if err := execute(engine, Up, &bytes.Buffer{}); err != nil {
			t.Fatalf("execute() error = %v", err)
		}
	}
}

func TestExecuteUpReturnsMigrationFailure(t *testing.T) {
	want := errors.New("database unavailable")
	err := execute(&fakeEngine{upErr: want}, Up, &bytes.Buffer{})
	if !errors.Is(err, want) {
		t.Fatalf("execute() error = %v, want wrapped %v", err, want)
	}
}

func TestExecuteDownStepsBackOnce(t *testing.T) {
	engine := &fakeEngine{}
	if err := execute(engine, Down, &bytes.Buffer{}); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if len(engine.steps) != 1 || engine.steps[0] != -1 {
		t.Fatalf("Steps calls = %v, want [-1]", engine.steps)
	}
}

func TestExecuteVersion(t *testing.T) {
	tests := []struct {
		name   string
		engine *fakeEngine
		want   string
	}{
		{name: "empty", engine: &fakeEngine{versionErr: gomigrate.ErrNilVersion}, want: "0 clean\n"},
		{name: "clean", engine: &fakeEngine{version: 4}, want: "4 clean\n"},
		{name: "dirty", engine: &fakeEngine{version: 3, dirty: true}, want: "3 dirty\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := execute(tt.engine, Version, &output); err != nil {
				t.Fatalf("execute() error = %v", err)
			}
			if output.String() != tt.want {
				t.Fatalf("output = %q, want %q", output.String(), tt.want)
			}
		})
	}
}

func TestExecuteToVersionMigratesToAbsoluteVersion(t *testing.T) {
	engine := &fakeEngine{}
	if err := execute(engine, ToVersion(13), &bytes.Buffer{}); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
	if len(engine.migrations) != 1 || engine.migrations[0] != 13 {
		t.Fatalf("Migrate calls = %v, want [13]", engine.migrations)
	}
}

func TestExecuteToVersionTreatsNoChangeAsSuccess(t *testing.T) {
	engine := &fakeEngine{migrateErr: gomigrate.ErrNoChange}
	if err := execute(engine, ToVersion(13), &bytes.Buffer{}); err != nil {
		t.Fatalf("execute() error = %v", err)
	}
}

func TestExecuteToVersionReturnsMigrationFailure(t *testing.T) {
	want := errors.New("database unavailable")
	err := execute(&fakeEngine{migrateErr: want}, ToVersion(13), &bytes.Buffer{})
	if !errors.Is(err, want) {
		t.Fatalf("execute() error = %v, want wrapped %v", err, want)
	}
}

func TestExecuteRejectsUnknownAction(t *testing.T) {
	err := execute(&fakeEngine{}, Action("explode"), &bytes.Buffer{})
	if err == nil {
		t.Fatal("execute() error = nil")
	}
}
