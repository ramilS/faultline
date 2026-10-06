package access_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
)

type repositoryStub struct {
	ids    []int64
	err    error
	calls  int
	lastID int64
}

func (r *repositoryStub) ProjectIDsForUser(_ context.Context, userID int64) ([]int64, error) {
	r.calls++
	r.lastID = userID
	return r.ids, r.err
}

// An owner bypasses groups, so the resolver must not query at all: a database
// failure must not be able to narrow an owner's access.
func TestScopeForOwnerIsUnrestrictedWithoutQuerying(t *testing.T) {
	repository := &repositoryStub{err: errors.New("database down")}
	scope, err := access.NewResolver(repository).ScopeFor(
		t.Context(), auth.User{ID: 1, Role: auth.RoleOwner})
	if err != nil {
		t.Fatalf("ScopeFor: %v", err)
	}
	if !scope.IsUnrestricted() {
		t.Error("owner scope is not unrestricted")
	}
	if repository.calls != 0 {
		t.Errorf("repository called %d times for an owner", repository.calls)
	}
}

func TestScopeForMemberIsLimitedToTheirProjects(t *testing.T) {
	repository := &repositoryStub{ids: []int64{4, 9}}
	scope, err := access.NewResolver(repository).ScopeFor(
		t.Context(), auth.User{ID: 7, Role: auth.RoleMember})
	if err != nil {
		t.Fatalf("ScopeFor: %v", err)
	}
	if scope.IsUnrestricted() {
		t.Error("member scope is unrestricted")
	}
	if ids := scope.ProjectIDs(); len(ids) != 2 || ids[0] != 4 || ids[1] != 9 {
		t.Errorf("project ids = %v, want [4 9]", ids)
	}
	if repository.lastID != 7 {
		t.Errorf("queried user id = %d, want 7", repository.lastID)
	}
}

func TestScopeForMemberWithNoGroupsIsEmpty(t *testing.T) {
	scope, err := access.NewResolver(&repositoryStub{}).ScopeFor(
		t.Context(), auth.User{ID: 7, Role: auth.RoleMember})
	if err != nil {
		t.Fatalf("ScopeFor: %v", err)
	}
	if scope.IsUnrestricted() || len(scope.ProjectIDs()) != 0 {
		t.Errorf("scope = %+v, want empty and restricted", scope)
	}
}

// A failed lookup must surface as an error, never as an empty-but-valid scope
// that a handler would treat as "this member legitimately sees nothing".
func TestScopeForMemberPropagatesRepositoryFailure(t *testing.T) {
	repository := &repositoryStub{err: errors.New("database down")}
	_, err := access.NewResolver(repository).ScopeFor(
		t.Context(), auth.User{ID: 7, Role: auth.RoleMember})
	if err == nil {
		t.Fatal("ScopeFor returned no error on a failed lookup")
	}
}
