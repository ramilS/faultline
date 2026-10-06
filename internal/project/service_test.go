package project

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/access"
)

type fakeProjects struct {
	created   []CreateProject
	project   Project
	createErr error
	listErr   error
	findErr   error
}

func (r *fakeProjects) Create(_ context.Context, input CreateProject) (Project, error) {
	r.created = append(r.created, input)
	if r.createErr != nil {
		return Project{}, r.createErr
	}
	if r.project.ID != 0 {
		return r.project, nil
	}
	return Project{ID: 1, Slug: input.Slug, Name: input.Name, Platform: input.Platform}, nil
}

func (r *fakeProjects) List(context.Context, access.Scope) ([]Project, error) { return nil, r.listErr }

func (r *fakeProjects) FindBySlug(_ context.Context, _ access.Scope, slug string) (Project, error) {
	if r.findErr != nil {
		return Project{}, r.findErr
	}
	if r.project.ID != 0 {
		return r.project, nil
	}
	return Project{ID: 1, Slug: slug}, nil
}

type fakeKeys struct {
	created   []CreateKey
	key       Key
	createErr error
	listErr   error
	revoked   []int64
	revokeErr error
}

func (r *fakeKeys) Create(_ context.Context, input CreateKey) (Key, error) {
	r.created = append(r.created, input)
	if r.createErr != nil {
		return Key{}, r.createErr
	}
	if r.key.ID != 0 {
		return r.key, nil
	}
	return Key{ID: 1, ProjectID: input.ProjectID, PublicKey: input.PublicKey, RateLimitRPS: input.RateLimitRPS, RateLimitBurst: input.RateLimitBurst}, nil
}

func (r *fakeKeys) ListByProjectSlug(context.Context, access.Scope, string) ([]Key, error) {
	return nil, r.listErr
}
func (r *fakeKeys) Revoke(_ context.Context, _ access.Scope, _ string, id int64) error {
	r.revoked = append(r.revoked, id)
	return r.revokeErr
}

func TestValidateCreateProjectRejectsInvalidFields(t *testing.T) {
	valid := CreateProject{Slug: "billing-api", Name: "Billing API", Platform: PlatformJavaScript}
	cases := []struct {
		name  string
		input CreateProject
	}{
		{"empty slug", CreateProject{Slug: "", Name: valid.Name, Platform: valid.Platform}},
		{"space in slug", CreateProject{Slug: "billing api", Name: valid.Name, Platform: valid.Platform}},
		{"uppercase slug", CreateProject{Slug: "Billing", Name: valid.Name, Platform: valid.Platform}},
		{"path-like slug", CreateProject{Slug: "../billing", Name: valid.Name, Platform: valid.Platform}},
		{"empty name", CreateProject{Slug: valid.Slug, Name: " \t", Platform: valid.Platform}},
		{"overlong name", CreateProject{Slug: valid.Slug, Name: strings.Repeat("a", maxProjectNameLength+1), Platform: valid.Platform}},
		{"unknown platform", CreateProject{Slug: valid.Slug, Name: valid.Name, Platform: "cobol"}},
		{"picker label instead of value", CreateProject{Slug: valid.Slug, Name: valid.Name, Platform: "NestJS"}},
		{"wrong-cased value", CreateProject{Slug: valid.Slug, Name: valid.Name, Platform: "iOS"}},
		{"value with trailing space", CreateProject{Slug: valid.Slug, Name: valid.Name, Platform: "ios "}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCreateProject(tc.input); err == nil {
				t.Fatal("ValidateCreateProject() error = nil")
			}
		})
	}
	if err := ValidateCreateProject(valid); err != nil {
		t.Fatalf("ValidateCreateProject(valid) error = %v", err)
	}
}

// declaredPlatforms reads the Platform* constants from model.go itself, so a
// constant added there without a matching case in ValidateCreateProject fails
// the test below instead of shipping a value the picker offers and the API
// rejects. The web suite compares the same constants with the picker.
func declaredPlatforms(t *testing.T) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "model.go", nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}
	var platforms []string
	ast.Inspect(file, func(node ast.Node) bool {
		spec, ok := node.(*ast.ValueSpec)
		if !ok || len(spec.Names) != 1 || !strings.HasPrefix(spec.Names[0].Name, "Platform") || len(spec.Values) != 1 {
			return true
		}
		if literal, ok := spec.Values[0].(*ast.BasicLit); ok && literal.Kind == token.STRING {
			value, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("unquote %s: %v", spec.Names[0].Name, err)
			}
			platforms = append(platforms, value)
		}
		return true
	})
	if len(platforms) == 0 {
		t.Fatal("no Platform constants found in model.go")
	}
	return platforms
}

func TestValidateCreateProjectAcceptsEveryPlatform(t *testing.T) {
	for _, platform := range declaredPlatforms(t) {
		t.Run(platform, func(t *testing.T) {
			input := CreateProject{Slug: "billing-api", Name: "Billing API", Platform: platform}
			if err := ValidateCreateProject(input); err != nil {
				t.Fatalf("ValidateCreateProject(%q) error = %v", platform, err)
			}
		})
	}
}

func TestValidateCreateKeyRejectsOutOfRangeRates(t *testing.T) {
	cases := []struct {
		name  string
		input CreateKey
	}{
		{"zero rps", CreateKey{RateLimitRPS: 0, RateLimitBurst: 1}},
		{"negative rps", CreateKey{RateLimitRPS: -1, RateLimitBurst: 1}},
		{"too large rps", CreateKey{RateLimitRPS: maxRateLimitRPS + 1, RateLimitBurst: 1}},
		{"zero burst", CreateKey{RateLimitRPS: 1, RateLimitBurst: 0}},
		{"negative burst", CreateKey{RateLimitRPS: 1, RateLimitBurst: -1}},
		{"too large burst", CreateKey{RateLimitRPS: 1, RateLimitBurst: maxRateLimitBurst + 1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := ValidateCreateKey(tc.input); err == nil {
				t.Fatal("ValidateCreateKey() error = nil")
			}
		})
	}
	if err := ValidateCreateKey(CreateKey{RateLimitRPS: 200, RateLimitBurst: 1000}); err != nil {
		t.Fatalf("ValidateCreateKey(valid) error = %v", err)
	}
}

func TestCreateProjectValidatesAndDelegates(t *testing.T) {
	repo := &fakeProjects{}
	service := NewService(repo, &fakeKeys{})
	project, err := service.CreateProject(t.Context(), CreateProject{Slug: "billing-api", Name: " Billing API ", Platform: PlatformJavaScript})
	if err != nil {
		t.Fatalf("CreateProject() error = %v", err)
	}
	if project.Slug != "billing-api" || len(repo.created) != 1 || repo.created[0].Name != "Billing API" {
		t.Fatalf("project = %+v, repository input = %+v", project, repo.created)
	}
	if _, err := service.CreateProject(t.Context(), CreateProject{Slug: "bad slug", Name: "Billing", Platform: PlatformJavaScript}); err == nil {
		t.Fatal("invalid project reached repository")
	}
}

func TestCreateProjectPreservesConflict(t *testing.T) {
	service := NewService(&fakeProjects{createErr: ErrConflict}, &fakeKeys{})
	_, err := service.CreateProject(t.Context(), CreateProject{Slug: "billing", Name: "Billing", Platform: PlatformOther})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateProject() error = %v, want ErrConflict", err)
	}
}

func TestCreateKeyGeneratesDistinctLowercaseHexAndDelegates(t *testing.T) {
	keys := &fakeKeys{}
	service := NewService(&fakeProjects{}, keys)
	first, rawFirst, err := service.CreateKey(t.Context(), 42, CreateKey{RateLimitRPS: 200, RateLimitBurst: 1000})
	if err != nil {
		t.Fatalf("first CreateKey() error = %v", err)
	}
	second, rawSecond, err := service.CreateKey(t.Context(), 42, CreateKey{RateLimitRPS: 200, RateLimitBurst: 1000})
	if err != nil {
		t.Fatalf("second CreateKey() error = %v", err)
	}
	for _, raw := range []string{rawFirst, rawSecond} {
		if len(raw) != 64 {
			t.Fatalf("key length = %d, want 64", len(raw))
		}
		if raw != strings.ToLower(raw) {
			t.Fatalf("key = %q, want lowercase", raw)
		}
		for _, char := range raw {
			if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
				t.Fatalf("key = %q, want hex", raw)
			}
		}
	}
	if rawFirst == rawSecond {
		t.Fatal("generated keys are equal")
	}
	if first.PublicKey != rawFirst || second.PublicKey != rawSecond {
		t.Fatalf("created keys = %+v, %+v", first, second)
	}
	if len(keys.created) != 2 || keys.created[0].ProjectID != 42 {
		t.Fatalf("repository inputs = %+v", keys.created)
	}
}

func TestRevokeKeyPreservesNotFoundAndDoesNotExposeRawKey(t *testing.T) {
	keys := &fakeKeys{revokeErr: ErrNotFound}
	service := NewService(&fakeProjects{}, keys)
	err := service.RevokeKey(t.Context(), access.Unrestricted(), "billing", 99)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("RevokeKey() error = %v, want ErrNotFound", err)
	}
	if len(keys.revoked) != 1 || keys.revoked[0] != 99 {
		t.Fatalf("revoked = %v", keys.revoked)
	}
	if _, exposed := reflect.TypeFor[Key]().FieldByName("RawKey"); exposed {
		t.Fatal("Key must not expose raw key material")
	}
}
