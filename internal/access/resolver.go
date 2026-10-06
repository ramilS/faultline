package access

import (
	"context"
	"fmt"

	"github.com/ramilS/faultline/internal/auth"
)

// Repository resolves project access from durable storage.
type Repository interface {
	// ProjectIDsForUser returns the ids of every project reachable by userID
	// through group membership, ascending and deduplicated. A user in no
	// group yields an empty slice and no error — that is a valid answer, not
	// a failure.
	ProjectIDsForUser(ctx context.Context, userID int64) ([]int64, error)
}

// Resolver turns an authenticated user into the scope their request runs
// under. It is the single place the role-to-scope policy lives, so no handler
// can implement a second, divergent version of it.
type Resolver struct {
	repository Repository
}

func NewResolver(repository Repository) *Resolver {
	return &Resolver{repository: repository}
}

// ScopeFor returns user's scope: unrestricted for an owner, otherwise the
// projects reachable through their groups.
//
// An owner is answered without touching the repository. That is not an
// optimisation: it means a database failure cannot silently narrow an owner's
// access, which is the access the deployment relies on to repair itself.
func (r *Resolver) ScopeFor(ctx context.Context, user auth.User) (Scope, error) {
	if user.IsOwner() {
		return Unrestricted(), nil
	}
	ids, err := r.repository.ProjectIDsForUser(ctx, user.ID)
	if err != nil {
		return Scope{}, fmt.Errorf("resolve project access: %w", err)
	}
	return Limited(ids), nil
}
