package project

import (
	"errors"
	"strings"
	"testing"
)

func TestValidateSourceLinks(t *testing.T) {
	for _, test := range []struct {
		name  string
		links SourceLinks
		field string
	}{
		{"not configured", SourceLinks{}, ""},
		{"github template with roots", SourceLinks{Template: "https://github.com/acme/shop/blob/main/{path}#L{line}", StackRoot: "/app/", SourceRoot: "src/"}, ""},
		{"http with release", SourceLinks{Template: "http://git.internal/shop/-/blob/{release}/{path}"}, ""},
		{"roots without a template", SourceLinks{StackRoot: "/app/"}, ""},
		{"javascript scheme", SourceLinks{Template: "javascript:alert(1)//{path}"}, "source_link_template"},
		{"data scheme", SourceLinks{Template: "data:text/html,{path}"}, "source_link_template"},
		{"ftp scheme", SourceLinks{Template: "ftp://git.internal/{path}"}, "source_link_template"},
		{"missing path placeholder", SourceLinks{Template: "https://github.com/acme/shop/blob/main/#L{line}"}, "source_link_template"},
		{"relative url", SourceLinks{Template: "/acme/shop/blob/main/{path}"}, "source_link_template"},
		{"scheme-relative url", SourceLinks{Template: "//github.com/acme/{path}"}, "source_link_template"},
		{"no host", SourceLinks{Template: "https:///{path}"}, "source_link_template"},
		{"embedded credentials", SourceLinks{Template: "https://bot:token@github.com/acme/{path}"}, "source_link_template"},
		{"whitespace inside", SourceLinks{Template: "https://github.com/acme shop/{path}"}, "source_link_template"},
		{"template too long", SourceLinks{Template: "https://github.com/" + strings.Repeat("a", maxSourceLinkTemplateLength) + "/{path}"}, "source_link_template"},
		{"stack root too long", SourceLinks{StackRoot: strings.Repeat("a", maxSourceRootLength+1)}, "source_stack_root"},
		{"control character in source root", SourceLinks{SourceRoot: "src/\n"}, "source_root"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateSourceLinks(test.links)
			if test.field == "" {
				if err != nil {
					t.Fatalf("ValidateSourceLinks() = %v, want nil", err)
				}
				return
			}
			if invalid, ok := errors.AsType[*SourceLinksError](err); !ok || invalid.Field != test.field || !strings.HasPrefix(err.Error(), test.field+" ") {
				t.Fatalf("ValidateSourceLinks() = %v, want a SourceLinksError naming %s", err, test.field)
			}
		})
	}
}

func TestUpdateSourceLinksTrimsValidatesAndDelegatesBySlug(t *testing.T) {
	repo := &fakeProjects{}
	service := NewService(repo, &fakeKeys{})

	updated, err := service.UpdateSourceLinks(t.Context(), "shop", SourceLinks{Template: "  https://github.com/acme/shop/blob/main/{path}  ", StackRoot: " /app/ ", SourceRoot: "src/ "})

	if err != nil {
		t.Fatalf("UpdateSourceLinks() error = %v", err)
	}
	want := SourceLinks{Template: "https://github.com/acme/shop/blob/main/{path}", StackRoot: "/app/", SourceRoot: "src/"}
	if repo.updatedSlug != "shop" || len(repo.sourceLinks) != 1 || repo.sourceLinks[0] != want {
		t.Fatalf("repository slug = %q, links = %+v", repo.updatedSlug, repo.sourceLinks)
	}
	if updated.SourceLinks != want {
		t.Fatalf("updated = %+v", updated)
	}
}

func TestUpdateSourceLinksKeepsAnInvalidSettingAwayFromTheRepository(t *testing.T) {
	repo := &fakeProjects{}

	_, err := NewService(repo, &fakeKeys{}).UpdateSourceLinks(t.Context(), "shop", SourceLinks{Template: "javascript:alert(1)"})

	if _, ok := errors.AsType[*SourceLinksError](err); !ok || len(repo.sourceLinks) != 0 {
		t.Fatalf("error = %v, repository calls = %+v", err, repo.sourceLinks)
	}
}

func TestUpdateSourceLinksPreservesNotFound(t *testing.T) {
	_, err := NewService(&fakeProjects{updateErr: ErrNotFound}, &fakeKeys{}).UpdateSourceLinks(t.Context(), "gone", SourceLinks{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateSourceLinks() error = %v, want ErrNotFound", err)
	}
}
