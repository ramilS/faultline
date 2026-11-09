package project

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"unicode"
)

const (
	maxSourceLinkTemplateLength = 2048
	maxSourceRootLength         = 512
	sourceLinkPathPlaceholder   = "{path}"
)

type SourceLinksError struct {
	Field  string
	Reason string
}

func (e *SourceLinksError) Error() string {
	return e.Field + " " + e.Reason
}

// SourceLinks is applied to frames in the dashboard by sourceLink in
// web/src/features/issue-detail/frames.ts; the server only stores and
// validates it. An empty Template means the project has no links.
type SourceLinks struct {
	Template   string
	StackRoot  string
	SourceRoot string
}

func ValidateSourceLinks(links SourceLinks) error {
	if err := validateSourceLinkTemplate(links.Template); err != nil {
		return err
	}
	if err := validateSourceRoot("source_stack_root", links.StackRoot); err != nil {
		return err
	}
	return validateSourceRoot("source_root", links.SourceRoot)
}

func validateSourceLinkTemplate(template string) error {
	if template == "" {
		return nil
	}
	if len(template) > maxSourceLinkTemplateLength || strings.ContainsFunc(template, unicode.IsSpace) || strings.ContainsFunc(template, unicode.IsControl) {
		return invalidSourceLinks("source_link_template", "must be a single URL of at most 2048 characters")
	}
	if !strings.Contains(template, sourceLinkPathPlaceholder) {
		return invalidSourceLinks("source_link_template", "must contain {path}")
	}
	parsed, err := url.Parse(template)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
		return invalidSourceLinks("source_link_template", "must be an absolute https:// or http:// URL")
	}
	if parsed.User != nil {
		return invalidSourceLinks("source_link_template", "must not contain credentials")
	}
	return nil
}

func validateSourceRoot(field, root string) error {
	if len(root) > maxSourceRootLength || strings.ContainsFunc(root, unicode.IsControl) {
		return invalidSourceLinks(field, "must be a path of at most 512 characters")
	}
	return nil
}

func invalidSourceLinks(field, reason string) error {
	return &SourceLinksError{Field: field, Reason: reason}
}

func (s *Service) UpdateSourceLinks(ctx context.Context, slug string, links SourceLinks) (Project, error) {
	links = SourceLinks{
		Template:   strings.TrimSpace(links.Template),
		StackRoot:  strings.TrimSpace(links.StackRoot),
		SourceRoot: strings.TrimSpace(links.SourceRoot),
	}
	if err := ValidateSourceLinks(links); err != nil {
		return Project{}, err
	}
	updated, err := s.projects.UpdateSourceLinks(ctx, slug, links)
	if err != nil {
		return Project{}, fmt.Errorf("update source links: %w", err)
	}
	return updated, nil
}
