package artifactauth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeRepository struct {
	created    Token
	createErr  error
	listed     []Token
	listErr    error
	revokeErr  error
	found      Token
	findErr    error
	touchErr   error
	touchedID  int64
	findPrefix string
	revokeSlug string
	revokedID  int64
}

func (f *fakeRepository) Create(_ context.Context, token Token) (Token, error) {
	f.created = token
	if f.createErr != nil {
		return Token{}, f.createErr
	}
	if token.ID == 0 {
		token.ID = 1
	}
	return token, nil
}

func (f *fakeRepository) ListByProjectSlug(context.Context, string) ([]Token, error) {
	return f.listed, f.listErr
}

func (f *fakeRepository) Revoke(_ context.Context, slug string, id int64) error {
	f.revokeSlug, f.revokedID = slug, id
	return f.revokeErr
}

func (f *fakeRepository) FindByPrefix(_ context.Context, prefix string) (Token, error) {
	f.findPrefix = prefix
	return f.found, f.findErr
}

func (f *fakeRepository) Touch(_ context.Context, id int64) error {
	f.touchedID = id
	return f.touchErr
}

func TestCreateDisclosesRawTokenOnce(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, Options{Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64))})

	token, raw, err := service.Create(t.Context(), 9, "production CI")
	want := sha256.Sum256([]byte(raw))
	if err != nil || !strings.HasPrefix(raw, "fl_art_") || token.TokenHash != want {
		t.Fatalf("Create = %+v raw=%q err=%v", token, raw, err)
	}
	if repo.created.TokenHash != want || repo.created.ProjectID != 9 || repo.created.Name != "production CI" {
		t.Fatalf("persisted token = %+v", repo.created)
	}
}

// TestAuthenticateAcceptsTokenWithUnderscoreInBase64 guards a real-token regression:
// base64url secrets frequently contain '_', which must not be mistaken for the
// token's field delimiter. All-0xFF random bytes encode to all underscores.
func TestAuthenticateAcceptsTokenWithUnderscoreInBase64(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, Options{Random: bytes.NewReader(bytes.Repeat([]byte{0xff}, 64))})

	created, raw, err := service.Create(t.Context(), 9, "production CI")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.Contains(raw, "_") {
		t.Fatalf("expected an underscore-bearing base64 token, got %q", raw)
	}
	repo.found = created

	authenticated, err := service.Authenticate(t.Context(), raw)
	if err != nil {
		t.Fatalf("Authenticate round-trip failed: %v (raw=%q)", err, raw)
	}
	if authenticated.ProjectID != 9 {
		t.Fatalf("authenticated token = %+v", authenticated)
	}
	if repo.findPrefix != created.TokenPrefix {
		t.Fatalf("looked up prefix %q, want %q", repo.findPrefix, created.TokenPrefix)
	}
}

func TestCreateRejectsBlankAndOverlongNames(t *testing.T) {
	service := NewService(&fakeRepository{}, Options{Random: bytes.NewReader(bytes.Repeat([]byte{7}, 128))})

	for _, name := range []string{"", " \t ", strings.Repeat("a", 101)} {
		if _, _, err := service.Create(t.Context(), 9, name); err == nil {
			t.Fatalf("Create(%q) error = nil, want validation error", name)
		}
	}
}

func TestCreateAllowsOneHundredCharacterName(t *testing.T) {
	service := NewService(&fakeRepository{}, Options{Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64))})

	if _, _, err := service.Create(t.Context(), 9, strings.Repeat("界", 100)); err != nil {
		t.Fatalf("Create 100-character name: %v", err)
	}
}

func TestAuthenticateRejectsMalformedToken(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, Options{})

	if _, err := service.Authenticate(t.Context(), "fl_art_not-base64_foo"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate malformed error = %v, want ErrUnauthorized", err)
	}
	if repo.findPrefix != "" || repo.touchedID != 0 {
		t.Fatalf("repository used for malformed token: prefix=%q touch=%d", repo.findPrefix, repo.touchedID)
	}
}

func TestAuthenticateRejectsWrongSecret(t *testing.T) {
	creator := NewService(&fakeRepository{}, Options{Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64))})
	stored, raw, err := creator.Create(t.Context(), 9, "CI")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	parts := strings.Split(raw, "_")
	wrong := strings.Join([]string{parts[0], parts[1], parts[2], strings.Repeat("A", len(parts[3]))}, "_")
	repo := &fakeRepository{found: stored}

	if _, err := NewService(repo, Options{}).Authenticate(t.Context(), wrong); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate wrong secret error = %v, want ErrUnauthorized", err)
	}
	if repo.touchedID != 0 {
		t.Fatalf("Touch called for wrong secret with id %d", repo.touchedID)
	}
}

func TestAuthenticateRejectsRevokedToken(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, Options{Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64))})
	stored, raw, err := service.Create(t.Context(), 9, "CI")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	revoked := time.Now()
	stored.RevokedAt = &revoked
	repo.found = stored

	if _, err := service.Authenticate(t.Context(), raw); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Authenticate revoked error = %v, want ErrUnauthorized", err)
	}
	if repo.touchedID != 0 {
		t.Fatalf("Touch called for revoked token with id %d", repo.touchedID)
	}
}

func TestAuthenticateTouchesSuccessfulToken(t *testing.T) {
	repo := &fakeRepository{}
	service := NewService(repo, Options{Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64))})
	stored, raw, err := service.Create(t.Context(), 9, "CI")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	repo.found = stored

	got, err := service.Authenticate(t.Context(), raw)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	if got.ID != stored.ID || repo.touchedID != stored.ID {
		t.Fatalf("Authenticate = %+v, touched %d, want id %d", got, repo.touchedID, stored.ID)
	}
}
