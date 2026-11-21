package digestlink

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
)

var fixedNow = time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

type fakeRepository struct {
	created    []Link
	createErr  error
	findHash   [32]byte
	findNow    time.Time
	target     Target
	findErr    error
	revokedFor int64
	revokeErr  error
}

func (r *fakeRepository) Create(_ context.Context, _ access.Scope, link Link) error {
	r.created = append(r.created, link)
	return r.createErr
}

func (r *fakeRepository) FindActive(_ context.Context, hash [32]byte, now time.Time) (Target, error) {
	r.findHash, r.findNow = hash, now
	return r.target, r.findErr
}

func (r *fakeRepository) RevokeAll(_ context.Context, _ access.Scope, issueID int64, _ time.Time) error {
	r.revokedFor = issueID
	return r.revokeErr
}

func newTestService(repository *fakeRepository) *Service {
	return NewService(repository, Options{
		Random: bytes.NewReader(bytes.Repeat([]byte{7}, 64)),
		Now:    func() time.Time { return fixedNow },
	})
}

func TestCreateStoresOnlyTheHashAndExpiresAfterTheTTL(t *testing.T) {
	for ttl, lifetime := range map[string]time.Duration{"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour} {
		repository := &fakeRepository{}
		userID := int64(3)

		raw, expiresAt, err := newTestService(repository).Create(t.Context(), access.Unrestricted(), 42, &userID, ttl)
		if err != nil {
			t.Fatalf("%s: create: %v", ttl, err)
		}
		if !strings.HasPrefix(raw, tokenMarker) || len(raw) < len(tokenMarker)+40 {
			t.Errorf("%s: raw token = %q", ttl, raw)
		}
		if want := fixedNow.Add(lifetime); !expiresAt.Equal(want) {
			t.Errorf("%s: expiresAt = %v, want %v", ttl, expiresAt, want)
		}
		link := repository.created[0]
		if link.TokenHash != sha256.Sum256([]byte(raw)) || link.IssueID != 42 || *link.CreatedBy != 3 || !link.ExpiresAt.Equal(expiresAt) {
			t.Errorf("%s: stored link = %+v", ttl, link)
		}
	}
}

func TestCreateRejectsAnUnlistedTTL(t *testing.T) {
	repository := &fakeRepository{}

	_, _, err := newTestService(repository).Create(t.Context(), access.Unrestricted(), 42, nil, "30d")

	if !errors.Is(err, ErrInvalidTTL) || len(repository.created) != 0 {
		t.Fatalf("err = %v, created = %d", err, len(repository.created))
	}
}

func TestCreateOfAnIssueOutsideScopeIsNotFound(t *testing.T) {
	repository := &fakeRepository{createErr: ErrNotFound}

	_, _, err := newTestService(repository).Create(t.Context(), access.Limited([]int64{1}), 42, nil, "1h")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestResolveLooksUpTheHashAtTheCurrentTime(t *testing.T) {
	repository := &fakeRepository{target: Target{IssueID: 42, ProjectID: 9}}
	raw := tokenMarker + "abc"

	target, err := newTestService(repository).Resolve(t.Context(), raw)

	if err != nil || target != (Target{IssueID: 42, ProjectID: 9}) {
		t.Fatalf("target = %+v, err = %v", target, err)
	}
	if repository.findHash != sha256.Sum256([]byte(raw)) || !repository.findNow.Equal(fixedNow) {
		t.Errorf("looked up hash %x at %v", repository.findHash, repository.findNow)
	}
}

func TestResolveRefusesATokenWithoutTheMarker(t *testing.T) {
	repository := &fakeRepository{target: Target{IssueID: 42}}

	_, err := newTestService(repository).Resolve(t.Context(), "fl_art_abc")

	if !errors.Is(err, ErrNotFound) || repository.findNow != (time.Time{}) {
		t.Fatalf("err = %v, repository consulted = %v", err, repository.findNow != (time.Time{}))
	}
}

func TestResolvePassesNotFoundThrough(t *testing.T) {
	repository := &fakeRepository{findErr: ErrNotFound}

	_, err := newTestService(repository).Resolve(t.Context(), tokenMarker+"expired")

	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRevokeAllRevokesTheIssuesLinks(t *testing.T) {
	repository := &fakeRepository{}

	if err := newTestService(repository).RevokeAll(t.Context(), access.Unrestricted(), 42); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if repository.revokedFor != 42 {
		t.Fatalf("revoked for issue %d", repository.revokedFor)
	}
}
