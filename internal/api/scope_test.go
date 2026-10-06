package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
)

type scopeResolverStub struct {
	scope access.Scope
	err   error
}

func (s *scopeResolverStub) ScopeFor(context.Context, auth.User) (access.Scope, error) {
	return s.scope, s.err
}

func TestRequireSessionPutsTheResolvedScopeInContext(t *testing.T) {
	handler := newTestAuthHandler(t, auth.User{ID: 1, Role: auth.RoleMember},
		&scopeResolverStub{scope: access.Limited([]int64{5})})

	var observed access.Scope
	var present bool
	guarded := handler.RequireSession(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		observed, present = access.ScopeFromContext(r.Context())
	}))

	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, authenticatedRequest(t))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	if !present {
		t.Fatal("handler saw no scope in the request context")
	}
	if ids := observed.ProjectIDs(); len(ids) != 1 || ids[0] != 5 {
		t.Errorf("scope project ids = %v, want [5]", ids)
	}
}

// A failed scope resolution must not reach the handler. Serving the request
// with no scope would leave every downstream query to decide for itself what
// "no scope" means, which is exactly the ambiguity the type exists to remove.
func TestRequireSessionRefusesWhenScopeResolutionFails(t *testing.T) {
	handler := newTestAuthHandler(t, auth.User{ID: 1, Role: auth.RoleMember},
		&scopeResolverStub{err: errors.New("database down")})

	reached := false
	guarded := handler.RequireSession(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		reached = true
	}))

	recorder := httptest.NewRecorder()
	guarded.ServeHTTP(recorder, authenticatedRequest(t))

	if reached {
		t.Error("handler ran without a scope")
	}
	if recorder.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", recorder.Code)
	}
}

func newTestAuthHandler(t *testing.T, user auth.User, scopes ScopeResolver) *AuthHandler {
	t.Helper()
	return NewAuthHandler(&authServiceStub{authUser: user}, nil, scopes, Options{})
}

func authenticatedRequest(t *testing.T) *http.Request {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects", http.NoBody)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "token"})
	return request
}
