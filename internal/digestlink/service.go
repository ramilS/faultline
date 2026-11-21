package digestlink

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/ramilS/faultline/internal/access"
)

const (
	tokenMarker      = "fl_dig_"
	tokenRandomBytes = 32
)

var allowedTTLs = map[string]time.Duration{
	"1h":  time.Hour,
	"24h": 24 * time.Hour,
	"7d":  7 * 24 * time.Hour,
}

type Options struct {
	Random io.Reader
	Now    func() time.Time
}

type Service struct {
	repository Repository
	random     io.Reader
	now        func() time.Time
}

func NewService(repository Repository, options Options) *Service {
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, random: random, now: now}
}

func (s *Service) Create(ctx context.Context, scope access.Scope, issueID int64, createdBy *int64, ttl string) (string, time.Time, error) {
	lifetime, ok := allowedTTLs[ttl]
	if !ok {
		return "", time.Time{}, ErrInvalidTTL
	}
	secret := make([]byte, tokenRandomBytes)
	if _, err := io.ReadFull(s.random, secret); err != nil {
		return "", time.Time{}, fmt.Errorf("generate digest link token: %w", err)
	}
	raw := tokenMarker + base64.RawURLEncoding.EncodeToString(secret)
	expiresAt := s.now().UTC().Add(lifetime)
	link := Link{IssueID: issueID, CreatedBy: createdBy, TokenHash: sha256.Sum256([]byte(raw)), ExpiresAt: expiresAt}
	if err := s.repository.Create(ctx, scope, link); err != nil {
		return "", time.Time{}, fmt.Errorf("create digest link: %w", err)
	}
	return raw, expiresAt, nil
}

func (s *Service) Resolve(ctx context.Context, raw string) (Target, error) {
	if !strings.HasPrefix(raw, tokenMarker) {
		return Target{}, ErrNotFound
	}
	target, err := s.repository.FindActive(ctx, sha256.Sum256([]byte(raw)), s.now().UTC())
	if err != nil {
		return Target{}, fmt.Errorf("resolve digest link: %w", err)
	}
	return target, nil
}

func (s *Service) RevokeAll(ctx context.Context, scope access.Scope, issueID int64) error {
	if err := s.repository.RevokeAll(ctx, scope, issueID, s.now().UTC()); err != nil {
		return fmt.Errorf("revoke digest links: %w", err)
	}
	return nil
}
