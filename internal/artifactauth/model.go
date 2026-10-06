// Package artifactauth defines project-scoped artifact token policy.
package artifactauth

import (
	"context"
	"errors"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
)

// Token is the stored representation of an artifact credential.
// TokenHash is never intended for API responses.
type Token struct {
	ID          int64
	ProjectID   int64
	Name        string
	TokenPrefix string
	TokenHash   [32]byte
	CreatedAt   time.Time
	LastUsedAt  *time.Time
	RevokedAt   *time.Time
}

// Repository persists artifact tokens.
type Repository interface {
	Create(context.Context, Token) (Token, error)
	ListByProjectSlug(context.Context, string) ([]Token, error)
	Revoke(context.Context, string, int64) error
	FindByPrefix(context.Context, string) (Token, error)
	Touch(context.Context, int64) error
}
