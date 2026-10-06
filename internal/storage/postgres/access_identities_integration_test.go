//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ramilS/faultline/internal/auth"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestCreateWithIdentityIsAtomicAndFindable(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := context.Background()

	created, err := repository.Identities.CreateWithIdentity(ctx, auth.ProviderGoogle, "subject-1", auth.NewIdentityUser{
		Email: "jit@example.com",
		Name:  "JIT User",
		Role:  auth.RoleMember,
	})
	if err != nil {
		t.Fatalf("CreateWithIdentity() error = %v", err)
	}
	if created.PasswordHash != "" {
		t.Fatalf("PasswordHash = %q, want empty", created.PasswordHash)
	}

	found, err := repository.Identities.FindUserBySubject(ctx, auth.ProviderGoogle, "subject-1")
	if err != nil {
		t.Fatalf("FindUserBySubject() error = %v", err)
	}
	if found.ID != created.ID {
		t.Fatalf("FindUserBySubject id = %d, want %d", found.ID, created.ID)
	}
}

func TestCreateWithIdentityRejectsDuplicateSubject(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := context.Background()

	input := auth.NewIdentityUser{Email: "first@example.com", Name: "First", Role: auth.RoleMember}
	if _, err := repository.Identities.CreateWithIdentity(ctx, auth.ProviderGoogle, "subject-2", input); err != nil {
		t.Fatalf("first CreateWithIdentity() error = %v", err)
	}

	second := auth.NewIdentityUser{Email: "second@example.com", Name: "Second", Role: auth.RoleMember}
	_, err := repository.Identities.CreateWithIdentity(ctx, auth.ProviderGoogle, "subject-2", second)
	if !errors.Is(err, auth.ErrConflict) {
		t.Fatalf("second CreateWithIdentity() error = %v, want ErrConflict", err)
	}

	if _, err := repository.Users.FindByEmail(ctx, "second@example.com"); !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("the rolled-back account is still present: %v", err)
	}
}

func TestFindUserBySubjectReportsNotFound(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)

	_, err := repository.Identities.FindUserBySubject(context.Background(), auth.ProviderGoogle, "absent")
	if !errors.Is(err, auth.ErrNotFound) {
		t.Fatalf("FindUserBySubject() error = %v, want ErrNotFound", err)
	}
}
