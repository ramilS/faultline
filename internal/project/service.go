package project

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/ramilS/faultline/internal/access"
)

const rawKeyBytes = 32

// Service applies pure project and ingest-key policy before repositories are
// called. It has no HTTP or database dependency.
type Service struct {
	projects ProjectRepository
	keys     KeyRepository
}

func NewService(projects ProjectRepository, keys KeyRepository) *Service {
	return &Service{projects: projects, keys: keys}
}

func ValidateCreateProject(input CreateProject) error {
	if !validSlug(input.Slug) {
		return fmt.Errorf("invalid project slug")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > maxProjectNameLength {
		return fmt.Errorf("invalid project name")
	}
	switch input.Platform {
	case PlatformJavaScript, PlatformJVMSpring, PlatformJVMKtor, PlatformKotlinMultiplatform, PlatformScala,
		PlatformNestJS, PlatformPython, PlatformPythonDjango, PlatformPythonFastAPI, PlatformPythonFlask, PlatformGo,
		PlatformRuby, PlatformRubyRails, PlatformPHP, PlatformPHPLaravel, PlatformPHPSymfony, PlatformRust,
		PlatformAndroid, PlatformIOS, PlatformOther:
		return nil
	default:
		return fmt.Errorf("invalid project platform")
	}
}

func ValidateCreateKey(input CreateKey) error {
	if input.RateLimitRPS <= 0 || input.RateLimitRPS > maxRateLimitRPS {
		return fmt.Errorf("invalid key rate limit rps")
	}
	if input.RateLimitBurst <= 0 || input.RateLimitBurst > maxRateLimitBurst {
		return fmt.Errorf("invalid key rate limit burst")
	}
	return nil
}

func validSlug(slug string) bool {
	if slug == "" || len(slug) > 64 || slug[0] == '-' || slug[len(slug)-1] == '-' {
		return false
	}
	for _, char := range slug {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func (s *Service) CreateProject(ctx context.Context, input CreateProject) (Project, error) {
	input.Name = strings.TrimSpace(input.Name)
	if err := ValidateCreateProject(input); err != nil {
		return Project{}, err
	}
	project, err := s.projects.Create(ctx, input)
	if err != nil {
		return Project{}, fmt.Errorf("create project: %w", err)
	}
	return project, nil
}

func (s *Service) ListProjects(ctx context.Context, scope access.Scope) ([]Project, error) {
	projects, err := s.projects.List(ctx, scope)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	return projects, nil
}

// FindProject resolves a project by slug, translating a missing project to
// ErrNotFound so callers can map it to a 404.
func (s *Service) FindProject(ctx context.Context, scope access.Scope, slug string) (Project, error) {
	found, err := s.projects.FindBySlug(ctx, scope, slug)
	if err != nil {
		return Project{}, fmt.Errorf("find project: %w", err)
	}
	return found, nil
}

// CreateKey creates a random 32-byte public ingest credential. It remains
// listable for SDK configuration.
func (s *Service) CreateKey(ctx context.Context, projectID int64, input CreateKey) (Key, string, error) {
	input.ProjectID = projectID
	if input.ProjectID <= 0 {
		return Key{}, "", fmt.Errorf("invalid project id")
	}
	if err := ValidateCreateKey(input); err != nil {
		return Key{}, "", err
	}
	raw, err := generateRawKey()
	if err != nil {
		return Key{}, "", fmt.Errorf("generate ingest key: %w", err)
	}
	input.PublicKey = raw
	key, err := s.keys.Create(ctx, input)
	if err != nil {
		return Key{}, "", fmt.Errorf("create ingest key: %w", err)
	}
	return key, raw, nil
}

func (s *Service) ListKeys(ctx context.Context, scope access.Scope, projectSlug string) ([]Key, error) {
	keys, err := s.keys.ListByProjectSlug(ctx, scope, projectSlug)
	if err != nil {
		return nil, fmt.Errorf("list ingest keys: %w", err)
	}
	return keys, nil
}

func (s *Service) RevokeKey(ctx context.Context, scope access.Scope, projectSlug string, keyID int64) error {
	if keyID <= 0 {
		return fmt.Errorf("invalid key id")
	}
	if err := s.keys.Revoke(ctx, scope, projectSlug, keyID); err != nil {
		return fmt.Errorf("revoke ingest key: %w", err)
	}
	return nil
}

func generateRawKey() (string, error) {
	raw := make([]byte, rawKeyBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
