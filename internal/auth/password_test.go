package auth

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

func TestHashPasswordUsesDeterministicArgon2idPHCEncoding(t *testing.T) {
	password := "correct horse"
	salt := []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	wantKey := argon2.IDKey([]byte(password), salt, 3, 64*1024, 2, 32)
	want := fmt.Sprintf(
		"$argon2id$v=19$m=65536,t=3,p=2$%s$%s",
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(wantKey),
	)

	got, err := HashPassword(password, bytes.NewReader(salt))
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if got != want {
		t.Fatalf("HashPassword() = %q, want %q", got, want)
	}
}

func TestVerifyPasswordAcceptsValidPassword(t *testing.T) {
	hash, err := HashPassword("correct horse", bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	ok, err := VerifyPassword(hash, "correct horse")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword() = false, want true")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("correct horse", bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	ok, err := VerifyPassword(hash, "wrong password")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword() = true, want false")
	}
}

func TestVerifyPasswordRejectsMalformedAndUnsupportedHashes(t *testing.T) {
	salt := base64.RawStdEncoding.EncodeToString(make([]byte, 16))
	key := base64.RawStdEncoding.EncodeToString(make([]byte, 32))
	tests := map[string]string{
		"not PHC":             "not-a-password-hash",
		"oversized PHC":       strings.Repeat("x", 257),
		"wrong algorithm":     "$argon2i$v=19$m=65536,t=3,p=2$" + salt + "$" + key,
		"wrong version":       "$argon2id$v=16$m=65536,t=3,p=2$" + salt + "$" + key,
		"extra parameter":     "$argon2id$v=19$m=65536,t=3,p=2,x=1$" + salt + "$" + key,
		"huge memory":         "$argon2id$v=19$m=4294967295,t=3,p=2$" + salt + "$" + key,
		"overflowing memory":  "$argon2id$v=19$m=999999999999999999999999,t=3,p=2$" + salt + "$" + key,
		"noncanonical memory": "$argon2id$v=19$m=065536,t=3,p=2$" + salt + "$" + key,
		"noncanonical time":   "$argon2id$v=19$m=65536,t=03,p=2$" + salt + "$" + key,
		"noncanonical lanes":  "$argon2id$v=19$m=65536,t=3,p=02$" + salt + "$" + key,
		"wrong iterations":    "$argon2id$v=19$m=65536,t=4,p=2$" + salt + "$" + key,
		"wrong parallelism":   "$argon2id$v=19$m=65536,t=3,p=255$" + salt + "$" + key,
		"bad salt encoding":   "$argon2id$v=19$m=65536,t=3,p=2$!$" + key,
		"wrong salt length":   "$argon2id$v=19$m=65536,t=3,p=2$" + base64.RawStdEncoding.EncodeToString(make([]byte, 15)) + "$" + key,
		"wrong key length":    "$argon2id$v=19$m=65536,t=3,p=2$" + salt + "$" + base64.RawStdEncoding.EncodeToString(make([]byte, 31)),
		"padded key encoding": "$argon2id$v=19$m=65536,t=3,p=2$" + salt + "$" + base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}

	for name, hash := range tests {
		t.Run(name, func(t *testing.T) {
			ok, err := VerifyPassword(hash, "correct horse")
			if ok {
				t.Fatal("VerifyPassword() = true, want false")
			}
			if !errors.Is(err, errInvalidPasswordHash) {
				t.Fatalf("VerifyPassword() error = %v, want stable invalid-hash error", err)
			}
		})
	}
}

func TestHashPasswordFailsWhenEntropyIsEmpty(t *testing.T) {
	hash, err := HashPassword("correct horse", bytes.NewReader(nil))
	if hash != "" {
		t.Fatalf("HashPassword() hash = %q, want empty", hash)
	}
	if err == nil {
		t.Fatal("HashPassword() error = nil, want entropy error")
	}
	if !errors.Is(err, io.EOF) {
		t.Fatalf("HashPassword() error = %v, want %v", err, io.EOF)
	}
}

type partialErrorReader struct {
	err error
}

func (r partialErrorReader) Read(p []byte) (int, error) {
	copy(p, []byte{1, 2, 3})
	return 3, r.err
}

func TestHashPasswordReturnsErrorAfterPartialSaltRead(t *testing.T) {
	wantErr := errors.New("entropy unavailable")
	hash, err := HashPassword("correct horse", partialErrorReader{err: wantErr})
	if hash != "" {
		t.Fatalf("HashPassword() hash = %q, want empty", hash)
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("HashPassword() error = %v, want %v", err, wantErr)
	}
}

func TestValidatePasswordRejectsInvalidUTF8(t *testing.T) {
	password := strings.Repeat("a", passwordMinimumRunes) + string([]byte{0xff})
	if err := ValidatePassword(password); !errors.Is(err, ErrPasswordInvalidUTF8) {
		t.Fatalf("ValidatePassword() error = %v, want %v", err, ErrPasswordInvalidUTF8)
	}
}

func TestValidatePasswordCountsUnicodeCodePoints(t *testing.T) {
	if err := ValidatePassword("парольпароль"); err != nil {
		t.Fatalf("ValidatePassword(12 runes) error = %v", err)
	}
	if err := ValidatePassword("парольпарол"); err == nil {
		t.Fatal("ValidatePassword(11 runes) error = nil, want error")
	}
}

func TestNormalizeEmailTrimsAndLowercases(t *testing.T) {
	got := NormalizeEmail(" \tADMIN@Example.COM\n")
	if want := "admin@example.com"; got != want {
		t.Fatalf("NormalizeEmail() = %q, want %q", got, want)
	}
}

func TestAuthenticationRepositoryContracts(t *testing.T) {
	var users UserRepository = userRepositoryStub{}
	var sessions SessionRepository = sessionRepositoryStub{}

	user, err := users.Create(t.Context(), NewUser{
		Email:        "admin@example.com",
		Name:         "Administrator",
		PasswordHash: "hash",
	})
	if err != nil || user.Email != "admin@example.com" {
		t.Fatalf("Create() = (%+v, %v)", user, err)
	}

	now := time.Unix(1_700_000_000, 0).UTC()
	if err := sessions.Create(t.Context(), NewSession{
		UserID:     1,
		TokenHash:  [32]byte{1},
		CreatedAt:  now,
		ExpiresAt:  now.Add(time.Hour),
		LastSeenAt: now,
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
}

type userRepositoryStub struct{}

func (userRepositoryStub) Create(_ context.Context, user NewUser) (User, error) {
	return User{ID: 1, Email: user.Email, Name: user.Name}, nil
}

func (userRepositoryStub) FindByEmail(context.Context, string) (User, error) {
	return User{}, ErrNotFound
}

func (userRepositoryStub) FindByID(context.Context, int64) (User, error) {
	return User{}, ErrNotFound
}

func (userRepositoryStub) List(context.Context) ([]User, error) { return nil, nil }

func (userRepositoryStub) ListActive(context.Context) ([]User, error) { return nil, nil }

func (userRepositoryStub) ListAssignableForProject(context.Context, ProjectScope, string) ([]User, error) {
	return nil, nil
}

func (userRepositoryStub) Update(context.Context, int64, UserChanges) (User, error) {
	return User{}, ErrNotFound
}

func (userRepositoryStub) UpdatePasswordHash(context.Context, int64, string) error { return nil }

type sessionRepositoryStub struct{}

func (sessionRepositoryStub) Create(context.Context, NewSession) error { return nil }

func (sessionRepositoryStub) FindByTokenHash(context.Context, [32]byte) (Session, User, error) {
	return Session{}, User{}, ErrNotFound
}

func (sessionRepositoryStub) Touch(context.Context, [32]byte, time.Time) error { return nil }

func (sessionRepositoryStub) DeleteByTokenHash(context.Context, [32]byte) error { return nil }

func (sessionRepositoryStub) DeleteExpired(context.Context, time.Time) error { return nil }

func (sessionRepositoryStub) DeleteByUserID(context.Context, int64) error { return nil }

func (sessionRepositoryStub) DeleteByUserIDExcept(context.Context, int64, [32]byte) error {
	return nil
}
