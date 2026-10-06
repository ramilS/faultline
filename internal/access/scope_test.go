package access_test

import (
	"testing"

	"github.com/ramilS/faultline/internal/access"
)

// The zero value is the whole point of the type: a Scope that was never
// initialised must deny everything, so a forgotten assignment closes access
// instead of opening it.
func TestZeroScopeGrantsNothing(t *testing.T) {
	var scope access.Scope
	if scope.IsUnrestricted() {
		t.Error("zero Scope is unrestricted")
	}
	if got := scope.ProjectIDs(); len(got) != 0 {
		t.Errorf("zero Scope project ids = %v, want empty", got)
	}
	if scope.ProjectIDs() == nil {
		t.Error("ProjectIDs returned nil; a nil slice reaches SQL as = ANY(NULL)")
	}
}

func TestUnrestrictedScope(t *testing.T) {
	scope := access.Unrestricted()
	if !scope.IsUnrestricted() {
		t.Error("Unrestricted() is not unrestricted")
	}
}

func TestLimitedScopeCarriesItsProjects(t *testing.T) {
	scope := access.Limited([]int64{7, 2})
	if scope.IsUnrestricted() {
		t.Error("Limited() is unrestricted")
	}
	ids := scope.ProjectIDs()
	if len(ids) != 2 || ids[0] != 7 || ids[1] != 2 {
		t.Errorf("project ids = %v, want [7 2]", ids)
	}
}

// Limited must not alias its argument, and ProjectIDs must not hand out the
// internal slice: either would let a caller widen a Scope after construction.
func TestLimitedDoesNotAliasCallerSlice(t *testing.T) {
	ids := []int64{1}
	scope := access.Limited(ids)
	ids[0] = 999
	if got := scope.ProjectIDs(); got[0] != 1 {
		t.Errorf("scope observed caller mutation: %v", got)
	}
	returned := scope.ProjectIDs()
	returned[0] = 42
	if got := scope.ProjectIDs(); got[0] != 1 {
		t.Errorf("scope observed mutation of a returned slice: %v", got)
	}
}

func TestLimitedWithNoProjectsGrantsNothing(t *testing.T) {
	scope := access.Limited(nil)
	if scope.IsUnrestricted() {
		t.Error("Limited(nil) is unrestricted")
	}
	if got := scope.ProjectIDs(); len(got) != 0 || got == nil {
		t.Errorf("project ids = %v, want empty non-nil", got)
	}
}

func TestScopeRoundTripsThroughContext(t *testing.T) {
	ctx := access.ContextWithScope(t.Context(), access.Unrestricted())
	scope, ok := access.ScopeFromContext(ctx)
	if !ok || !scope.IsUnrestricted() {
		t.Fatalf("ScopeFromContext = (%v, %v)", scope, ok)
	}
}

// An absent scope must be reported as absent, not silently substituted with a
// zero value that a caller could mistake for a real answer.
func TestScopeMissingFromContext(t *testing.T) {
	if _, ok := access.ScopeFromContext(t.Context()); ok {
		t.Error("ScopeFromContext reported a scope on a bare context")
	}
}
