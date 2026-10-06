package group_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/group"
)

type repositoryStub struct {
	created   group.NewGroup
	createErr error
	replaced  []int64
	renamed   []int64

	renameErr error
}

func (r *repositoryStub) List(_ context.Context) ([]group.Group, error) {
	return nil, nil
}

func (r *repositoryStub) Find(_ context.Context, _ int64) (group.Detail, error) {
	return group.Detail{}, nil
}

func (r *repositoryStub) Create(_ context.Context, input group.NewGroup) (group.Group, error) {
	r.created = input
	return group.Group{ID: 1, Name: input.Name}, r.createErr
}

func (r *repositoryStub) Rename(_ context.Context, id int64, name string) (group.Group, error) {
	r.renamed = []int64{id}
	return group.Group{ID: id, Name: name}, r.renameErr
}

func (r *repositoryStub) Delete(_ context.Context, _ int64) error {
	return nil
}

func (r *repositoryStub) ReplaceMembers(_ context.Context, _ int64, userIDs []int64) error {
	r.replaced = userIDs
	return nil
}

func (r *repositoryStub) ReplaceProjects(_ context.Context, _ int64, projectIDs []int64) error {
	r.replaced = projectIDs
	return nil
}

func TestCreateTrimsSurroundingWhitespace(t *testing.T) {
	repository := &repositoryStub{}
	created, err := group.NewService(repository).Create(t.Context(), "  backend  ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if repository.created.Name != "backend" {
		t.Errorf("stored name = %q, want %q", repository.created.Name, "backend")
	}
	if created.Name != "backend" {
		t.Errorf("returned name = %q, want %q", created.Name, "backend")
	}
}

func TestCreateRejectsANameOverTheLengthLimit(t *testing.T) {
	_, err := group.NewService(&repositoryStub{}).Create(
		t.Context(), strings.Repeat("a", 65))
	if !errors.Is(err, group.ErrInvalidName) {
		t.Fatalf("err = %v, want group.ErrInvalidName", err)
	}
}

func TestCreateRejectsABlankName(t *testing.T) {
	for _, name := range []string{"", "   "} {
		_, err := group.NewService(&repositoryStub{}).Create(t.Context(), name)
		if !errors.Is(err, group.ErrInvalidName) {
			t.Errorf("Create(%q): err = %v, want group.ErrInvalidName", name, err)
		}
	}
}

func TestCreateReportsADuplicateNameAsConflict(t *testing.T) {
	repository := &repositoryStub{createErr: group.ErrConflict}
	_, err := group.NewService(repository).Create(t.Context(), "backend")
	if !errors.Is(err, group.ErrConflict) {
		t.Fatalf("err = %v, want group.ErrConflict", err)
	}
}

// Duplicates would make ReplaceMembers insert the same primary key twice and
// fail the whole replacement, so the service folds them before the write. The
// returned applied set is what a caller echoes back, so it must reflect the
// dedupe too, not just what was stored.
func TestReplaceMembersDeduplicatesUserIDs(t *testing.T) {
	repository := &repositoryStub{}
	applied, err := group.NewService(repository).ReplaceMembers(
		t.Context(), 1, []int64{4, 4, 9})
	if err != nil {
		t.Fatalf("ReplaceMembers: %v", err)
	}
	if len(repository.replaced) != 2 {
		t.Errorf("stored ids = %v, want two distinct", repository.replaced)
	}
	if len(applied) != 2 || applied[0] != 4 || applied[1] != 9 {
		t.Errorf("applied ids = %v, want [4 9]", applied)
	}
}

func TestReplaceProjectsDeduplicatesProjectIDs(t *testing.T) {
	repository := &repositoryStub{}
	applied, err := group.NewService(repository).ReplaceProjects(
		t.Context(), 1, []int64{7, 7, 3})
	if err != nil {
		t.Fatalf("ReplaceProjects: %v", err)
	}
	if len(repository.replaced) != 2 {
		t.Errorf("stored ids = %v, want two distinct", repository.replaced)
	}
	if len(applied) != 2 || applied[0] != 7 || applied[1] != 3 {
		t.Errorf("applied ids = %v, want [7 3]", applied)
	}
}

func TestRenameRejectsABlankName(t *testing.T) {
	repository := &repositoryStub{}
	_, err := group.NewService(repository).Rename(t.Context(), 1, "   ")
	if !errors.Is(err, group.ErrInvalidName) {
		t.Fatalf("err = %v, want group.ErrInvalidName", err)
	}
	if repository.renamed != nil {
		t.Errorf("repository.Rename was called, want it skipped for an invalid name")
	}
}

// The service wraps a repository conflict with fmt.Errorf, so this exercises
// that errors.Is still matches through the wrap — the same guarantee
// TestCreateReportsADuplicateNameAsConflict proves for Create.
func TestRenameReportsADuplicateNameAsConflict(t *testing.T) {
	repository := &repositoryStub{renameErr: group.ErrConflict}
	_, err := group.NewService(repository).Rename(t.Context(), 1, "backend")
	if !errors.Is(err, group.ErrConflict) {
		t.Fatalf("err = %v, want group.ErrConflict", err)
	}
}
