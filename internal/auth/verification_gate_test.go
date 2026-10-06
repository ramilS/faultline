package auth

import (
	"errors"
	"testing"
	"time"
)

// TestServiceAndAdministrationDefaultToTheSameVerificationGate pins the
// production-relevant invariant directly: internal/command/run.go
// constructs a Service and an Administration independently, each with a
// zero-value ServiceOptions, so the only thing that makes their combined
// Argon2id concurrency bounded together (rather than each getting its own
// budget) is that they resolve to the identical gate instance.
func TestServiceAndAdministrationDefaultToTheSameVerificationGate(t *testing.T) {
	service := NewService(&fakeUsers{}, &fakeSessions{}, ServiceOptions{})
	admin := NewAdministration(&fakeUsers{}, &fakeSessions{}, ServiceOptions{})

	if service.verificationGate != admin.verificationGate {
		t.Fatal("Service and Administration must default to the same password-verification gate, or their combined concurrency can exceed the bound either alone would enforce")
	}
}

// TestChangeOwnPasswordBoundsPasswordVerificationConcurrency mirrors
// TestServiceLoginBoundsPasswordVerificationConcurrency, proving the gate is
// actually applied on the ChangeOwnPassword path itself, not only shared in
// principle.
func TestChangeOwnPasswordBoundsPasswordVerificationConcurrency(t *testing.T) {
	users := &fakeUsers{byID: map[int64]User{5: {ID: 5, PasswordHash: dummyPasswordHash}}}
	admin := NewAdministration(users, &fakeSessions{}, ServiceOptions{PasswordVerificationLimit: 1})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	admin.verifyPassword = func(_, _ string) (bool, error) {
		entered <- struct{}{}
		<-release
		return false, nil
	}

	firstResult := make(chan error, 1)
	go func() {
		firstResult <- admin.ChangeOwnPassword(t.Context(), 5, "whatever", "a-new-password-12", [32]byte{})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("configured verification slot was not entered")
	}

	if err := admin.ChangeOwnPassword(t.Context(), 5, "whatever", "a-new-password-12", [32]byte{}); !errors.Is(err, ErrAuthenticationBusy) {
		t.Fatalf("ChangeOwnPassword() while saturated = %v, want ErrAuthenticationBusy", err)
	}
	if users.passwordWrites != 0 {
		t.Fatalf("passwordWrites = %d, want 0 — a saturated call must not write", users.passwordWrites)
	}

	release <- struct{}{}
	if err := <-firstResult; !errors.Is(err, ErrCurrentPasswordMismatch) {
		t.Fatalf("first ChangeOwnPassword() error = %v, want ErrCurrentPasswordMismatch", err)
	}
}

// TestChangeOwnPasswordSharesTheVerificationGateWithLogin is the strongest
// proof of sharing: a Login holding the gate's only slot must make a
// concurrent ChangeOwnPassword see ErrAuthenticationBusy too. This could
// not pass if Administration had its own independent gate.
func TestChangeOwnPasswordSharesTheVerificationGateWithLogin(t *testing.T) {
	gate := newPasswordVerificationGate(1)

	loginUsers := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	service := NewService(loginUsers, &fakeSessions{}, ServiceOptions{verificationGate: gate})
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	service.verifyPassword = func(_, _ string) (bool, error) {
		entered <- struct{}{}
		<-release
		return false, nil
	}

	loginResult := make(chan error, 1)
	go func() {
		_, _, err := service.Login(t.Context(), "admin@example.com", "password")
		loginResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("Login did not enter the verification slot")
	}

	adminUsers := &fakeUsers{byID: map[int64]User{5: {ID: 5, PasswordHash: dummyPasswordHash}}}
	admin := NewAdministration(adminUsers, &fakeSessions{}, ServiceOptions{verificationGate: gate})

	if err := admin.ChangeOwnPassword(t.Context(), 5, "whatever", "a-new-password-12", [32]byte{}); !errors.Is(err, ErrAuthenticationBusy) {
		t.Fatalf("ChangeOwnPassword while Login holds the shared slot = %v, want ErrAuthenticationBusy", err)
	}

	release <- struct{}{}
	if err := <-loginResult; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
}
