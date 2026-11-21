// Package digestlink issues expiring, revocable links that serve one issue's digest without a session.
package digestlink

import (
	"context"
	"errors"
	"time"

	"github.com/ramilS/faultline/internal/access"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrInvalidTTL = errors.New("invalid ttl")
)

type Link struct {
	IssueID   int64
	CreatedBy *int64
	TokenHash [32]byte
	ExpiresAt time.Time
}

type Target struct {
	IssueID   int64
	ProjectID int64
}

type Repository interface {
	Create(ctx context.Context, scope access.Scope, link Link) error
	FindActive(ctx context.Context, tokenHash [32]byte, now time.Time) (Target, error)
	RevokeAll(ctx context.Context, scope access.Scope, issueID int64, now time.Time) error
}
