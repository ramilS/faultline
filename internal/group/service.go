package group

import (
	"context"
	"fmt"
	"strings"
)

// Service applies group naming and membership policy before the repository
// is called. It has no HTTP or database dependency.
type Service struct {
	groups Repository
}

func NewService(groups Repository) *Service {
	return &Service{groups: groups}
}

func (s *Service) List(ctx context.Context) ([]Group, error) {
	groups, err := s.groups.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	return groups, nil
}

func (s *Service) Find(ctx context.Context, id int64) (Detail, error) {
	detail, err := s.groups.Find(ctx, id)
	if err != nil {
		return Detail{}, fmt.Errorf("find group: %w", err)
	}
	return detail, nil
}

func (s *Service) Create(ctx context.Context, name string) (Group, error) {
	name, err := validateGroupName(name)
	if err != nil {
		return Group{}, err
	}
	created, err := s.groups.Create(ctx, NewGroup{Name: name})
	if err != nil {
		return Group{}, fmt.Errorf("create group: %w", err)
	}
	return created, nil
}

func (s *Service) Rename(ctx context.Context, id int64, name string) (Group, error) {
	name, err := validateGroupName(name)
	if err != nil {
		return Group{}, err
	}
	renamed, err := s.groups.Rename(ctx, id, name)
	if err != nil {
		return Group{}, fmt.Errorf("rename group: %w", err)
	}
	return renamed, nil
}

func (s *Service) Delete(ctx context.Context, id int64) error {
	if err := s.groups.Delete(ctx, id); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return nil
}

// ReplaceMembers stores the deduplicated userIDs and returns the exact set
// applied, so a caller echoes what was stored rather than what was requested.
func (s *Service) ReplaceMembers(ctx context.Context, id int64, userIDs []int64) ([]int64, error) {
	applied := dedupeIDs(userIDs)
	if err := s.groups.ReplaceMembers(ctx, id, applied); err != nil {
		return nil, fmt.Errorf("replace group members: %w", err)
	}
	return applied, nil
}

// ReplaceProjects stores the deduplicated projectIDs and returns the exact
// set applied, so a caller echoes what was stored rather than what was
// requested.
func (s *Service) ReplaceProjects(ctx context.Context, id int64, projectIDs []int64) ([]int64, error) {
	applied := dedupeIDs(projectIDs)
	if err := s.groups.ReplaceProjects(ctx, id, applied); err != nil {
		return nil, fmt.Errorf("replace group projects: %w", err)
	}
	return applied, nil
}

// validateGroupName trims surrounding whitespace and rejects a blank or
// over-long name, matching the project-name limit in internal/project.
func validateGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxGroupNameLength {
		return "", ErrInvalidName
	}
	return name, nil
}

// dedupeIDs folds duplicate ids before a replace write: ReplaceMembers and
// ReplaceProjects insert one row per id, so a duplicate would violate the
// composite primary key and fail the whole replacement.
func dedupeIDs(ids []int64) []int64 {
	seen := make(map[int64]struct{}, len(ids))
	deduped := make([]int64, 0, len(ids))
	for _, id := range ids {
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		deduped = append(deduped, id)
	}
	return deduped
}
