// Package access represents and resolves which projects a request may read.
//
// It is a leaf package: it depends on nothing else in internal/, so both the
// HTTP layer and the storage adapters can hold a Scope without importing each
// other.
package access

import "context"

// Scope is the set of projects a request may read.
//
// The zero value grants nothing and no exported field can widen it: a Scope is
// only obtainable from Unrestricted or Limited. That is deliberate — a
// forgotten initialisation must close access, not open it. In SQL the zero
// value becomes `project_id = ANY('{}')`, which is false for every row.
type Scope struct {
	all        bool
	projectIDs []int64
}

// Unrestricted returns the scope of a caller who may read every project. It is
// the owner role's scope: owners bypass groups entirely.
func Unrestricted() Scope {
	return Scope{all: true}
}

// Limited returns the scope of a caller who may read exactly ids. The slice is
// copied, so a later mutation by the caller cannot widen the returned Scope.
func Limited(ids []int64) Scope {
	scoped := make([]int64, len(ids))
	copy(scoped, ids)
	return Scope{projectIDs: scoped}
}

// IsUnrestricted reports whether every project is readable.
func (s Scope) IsUnrestricted() bool {
	return s.all
}

// ProjectIDs returns the readable project ids as a fresh, always non-nil
// slice.
//
// Non-nil matters at the SQL boundary: a nil slice reaches Postgres as
// `= ANY(NULL)`, which evaluates to NULL rather than false. Both exclude the
// row, but only one of them does so for a reason the next reader can see.
func (s Scope) ProjectIDs() []int64 {
	ids := make([]int64, len(s.projectIDs))
	copy(ids, s.projectIDs)
	return ids
}

type scopeContextKey struct{}

// ContextWithScope carries the scope resolved for this request. It is set once,
// by the session middleware, immediately after the caller is authenticated.
func ContextWithScope(ctx context.Context, scope Scope) context.Context {
	return context.WithValue(ctx, scopeContextKey{}, scope)
}

// ScopeFromContext returns the request's scope. The boolean reports presence:
// callers must treat absence as a failure, never as an empty scope that
// happens to deny everything, because the two have different causes — a
// missing scope means the middleware did not run.
func ScopeFromContext(ctx context.Context) (Scope, bool) {
	scope, ok := ctx.Value(scopeContextKey{}).(Scope)
	return scope, ok
}
