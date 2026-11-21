//go:build integration

package postgres_test

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/digestlink"
	"github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestDigestLinkRepository(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()
	projects := postgres.NewProjectRepository(pool).Projects
	repository := postgres.NewDigestLinkRepository(pool)
	web := createProject(t, ctx, projects, "digest-web")
	other := createProject(t, ctx, projects, "digest-other")
	issueID := seedIssueForProject(t, pool, web.ID)
	now := time.Date(2026, 11, 21, 9, 0, 0, 0, time.UTC)

	create := func(t *testing.T, raw string, scope access.Scope, expiresAt time.Time) error {
		t.Helper()
		return repository.Create(ctx, scope, digestlink.Link{IssueID: issueID, TokenHash: sha256.Sum256([]byte(raw)), ExpiresAt: expiresAt})
	}

	t.Run("finds an active link with its issue's project", func(t *testing.T) {
		if err := create(t, "active", access.Limited([]int64{web.ID}), now.Add(time.Hour)); err != nil {
			t.Fatalf("create: %v", err)
		}
		target, err := repository.FindActive(ctx, sha256.Sum256([]byte("active")), now)
		if err != nil || target != (digestlink.Target{IssueID: issueID, ProjectID: web.ID}) {
			t.Fatalf("target = %+v, err = %v", target, err)
		}
	})

	t.Run("does not find an expired link", func(t *testing.T) {
		if err := create(t, "expired", access.Unrestricted(), now.Add(-time.Second)); err != nil {
			t.Fatalf("create: %v", err)
		}
		if _, err := repository.FindActive(ctx, sha256.Sum256([]byte("expired")), now); !errors.Is(err, digestlink.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("refuses a link to an issue outside the scope", func(t *testing.T) {
		if err := create(t, "foreign", access.Limited([]int64{other.ID}), now.Add(time.Hour)); !errors.Is(err, digestlink.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
	})

	t.Run("revoking from outside the scope is not found and revokes nothing", func(t *testing.T) {
		if err := repository.RevokeAll(ctx, access.Limited([]int64{other.ID}), issueID, now); !errors.Is(err, digestlink.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound", err)
		}
		if _, err := repository.FindActive(ctx, sha256.Sum256([]byte("active")), now); err != nil {
			t.Fatalf("foreign revoke revoked the link: %v", err)
		}
	})

	t.Run("revokes every link of the issue", func(t *testing.T) {
		if err := repository.RevokeAll(ctx, access.Limited([]int64{web.ID}), issueID, now); err != nil {
			t.Fatalf("revoke: %v", err)
		}
		if _, err := repository.FindActive(ctx, sha256.Sum256([]byte("active")), now); !errors.Is(err, digestlink.ErrNotFound) {
			t.Fatalf("err = %v, want ErrNotFound after revoke", err)
		}
	})
}
