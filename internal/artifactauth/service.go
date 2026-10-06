package artifactauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"
)

const (
	tokenMarker       = "fl_art"
	tokenRandomBytes  = 32
	maximumNameLength = 100
)

// tokenPartLength is the exact character length of a base64url-encoded token
// part, used to split a raw token unambiguously.
var tokenPartLength = base64.RawURLEncoding.EncodedLen(tokenRandomBytes)

// Options configures a Service.
type Options struct {
	Random io.Reader
}

// Service applies artifact-token lifecycle and authentication policy.
type Service struct {
	repository Repository
	random     io.Reader
}

func NewService(repository Repository, options Options) *Service {
	random := options.Random
	if random == nil {
		random = rand.Reader
	}
	return &Service{repository: repository, random: random}
}

// Create creates a project-scoped token and returns its raw value exactly once.
func (s *Service) Create(ctx context.Context, projectID int64, name string) (Token, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > maximumNameLength {
		return Token{}, "", fmt.Errorf("artifact token name must be 1-%d characters", maximumNameLength)
	}

	prefix, err := randomPart(s.random)
	if err != nil {
		return Token{}, "", fmt.Errorf("generate token prefix: %w", err)
	}
	secret, err := randomPart(s.random)
	if err != nil {
		return Token{}, "", fmt.Errorf("generate token secret: %w", err)
	}
	raw := tokenMarker + "_" + prefix + "_" + secret
	token := Token{
		ProjectID:   projectID,
		Name:        name,
		TokenPrefix: prefix,
		TokenHash:   sha256.Sum256([]byte(raw)),
	}
	created, err := s.repository.Create(ctx, token)
	if err != nil {
		return Token{}, "", fmt.Errorf("create artifact token: %w", err)
	}
	return created, raw, nil
}

func randomPart(random io.Reader) (string, error) {
	bytes := make([]byte, tokenRandomBytes)
	if _, err := io.ReadFull(random, bytes); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(bytes), nil
}

// List returns the tokens for a project slug.
func (s *Service) List(ctx context.Context, projectSlug string) ([]Token, error) {
	tokens, err := s.repository.ListByProjectSlug(ctx, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("list artifact tokens: %w", err)
	}
	return tokens, nil
}

// Revoke idempotently revokes a project token.
func (s *Service) Revoke(ctx context.Context, projectSlug string, tokenID int64) error {
	if err := s.repository.Revoke(ctx, projectSlug, tokenID); err != nil {
		return fmt.Errorf("revoke artifact token: %w", err)
	}
	return nil
}

// Authenticate verifies an active token and records its successful use.
func (s *Service) Authenticate(ctx context.Context, raw string) (Token, error) {
	prefix, ok := parseToken(raw)
	if !ok {
		return Token{}, ErrUnauthorized
	}
	token, err := s.repository.FindByPrefix(ctx, prefix)
	if errors.Is(err, ErrNotFound) {
		return Token{}, ErrUnauthorized
	}
	if err != nil {
		return Token{}, fmt.Errorf("find artifact token: %w", err)
	}
	providedHash := sha256.Sum256([]byte(raw))
	if token.RevokedAt != nil || subtle.ConstantTimeCompare(providedHash[:], token.TokenHash[:]) != 1 {
		return Token{}, ErrUnauthorized
	}
	if err := s.repository.Touch(ctx, token.ID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return Token{}, ErrUnauthorized
		}
		return Token{}, fmt.Errorf("touch artifact token: %w", err)
	}
	return token, nil
}

// parseToken splits a raw token into its prefix and secret by fixed base64
// length rather than on "_": the RawURLEncoding alphabet itself contains "_",
// so a delimiter split would corrupt any token whose random part encodes one.
func parseToken(raw string) (string, bool) {
	rest, ok := strings.CutPrefix(raw, tokenMarker+"_")
	if !ok {
		return "", false
	}
	if len(rest) != tokenPartLength*2+1 || rest[tokenPartLength] != '_' {
		return "", false
	}
	prefix, secret := rest[:tokenPartLength], rest[tokenPartLength+1:]
	if !validTokenPart(prefix) || !validTokenPart(secret) {
		return "", false
	}
	return prefix, true
}

func validTokenPart(part string) bool {
	decoded, err := base64.RawURLEncoding.Strict().DecodeString(part)
	return err == nil && len(decoded) == tokenRandomBytes && base64.RawURLEncoding.EncodeToString(decoded) == part
}
