package scrub

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

type fakeRepository struct {
	globalRules []Rule
	globalErr   error
	updates     int
}

func (f *fakeRepository) Get(context.Context, int64) (Config, error)  { return Config{}, nil }
func (f *fakeRepository) Update(context.Context, int64, Config) error { return nil }
func (f *fakeRepository) GlobalRules(context.Context) ([]Rule, error) {
	return f.globalRules, f.globalErr
}
func (f *fakeRepository) UpdateGlobalRules(_ context.Context, rules []Rule) error {
	f.updates++
	f.globalRules = rules
	return nil
}

func TestUpdateGlobalRefusesInvalidRulesWithoutTouchingTheRepository(t *testing.T) {
	repository := &fakeRepository{}

	_, err := NewService(repository).UpdateGlobal(t.Context(), []Rule{{Name: "bad", Pattern: `(`}})

	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("error = %v, want ErrInvalidConfig", err)
	}
	if repository.updates != 0 {
		t.Fatalf("repository updated %d times, want 0", repository.updates)
	}
}

func TestUpdateGlobalStoresAndReturnsTheNormalizedRules(t *testing.T) {
	repository := &fakeRepository{}
	want := []Rule{{Name: "Telegram", Pattern: `bot\d+`}}

	got, err := NewService(repository).UpdateGlobal(t.Context(), []Rule{{Name: "  Telegram ", Pattern: `bot\d+`}})

	if err != nil {
		t.Fatalf("UpdateGlobal: %v", err)
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(repository.globalRules, want) {
		t.Fatalf("returned %v, stored %v; want both %v", got, repository.globalRules, want)
	}
}

func TestGetGlobalWrapsRepositoryErrors(t *testing.T) {
	failure := errors.New("connection reset")

	_, err := NewService(&fakeRepository{globalErr: failure}).GetGlobal(t.Context())

	if !errors.Is(err, failure) {
		t.Fatalf("error = %v, want it to wrap %v", err, failure)
	}
}
