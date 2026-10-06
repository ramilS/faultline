package auth

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeIdentities struct {
	bySubject   map[string]User
	linked      []int64
	created     []NewIdentityUser
	createErr   error
	linkErr     error
	appearAfter int // lookups to miss before bySubject is consulted
	findCalls   int
}

func (r *fakeIdentities) FindUserBySubject(_ context.Context, _, subject string) (User, error) {
	r.findCalls++
	if r.findCalls <= r.appearAfter {
		return User{}, ErrNotFound
	}
	if user, ok := r.bySubject[subject]; ok {
		return user, nil
	}
	return User{}, ErrNotFound
}

func (r *fakeIdentities) Link(_ context.Context, _, subject string, userID int64) error {
	if r.linkErr != nil {
		return r.linkErr
	}
	r.linked = append(r.linked, userID)
	if r.bySubject == nil {
		r.bySubject = map[string]User{}
	}
	r.bySubject[subject] = User{ID: userID}
	return nil
}

func (r *fakeIdentities) CreateWithIdentity(_ context.Context, _, subject string, input NewIdentityUser) (User, error) {
	if r.createErr != nil {
		return User{}, r.createErr
	}
	r.created = append(r.created, input)
	user := User{ID: 100, Email: input.Email, Name: input.Name, Role: input.Role}
	if r.bySubject == nil {
		r.bySubject = map[string]User{}
	}
	r.bySubject[subject] = user
	return user, nil
}

func newIdentityService(users UserRepository, identities IdentityRepository, sessions SessionRepository) *Service {
	return NewService(users, sessions, ServiceOptions{
		Clock:      &fakeClock{now: serviceTestNow},
		Entropy:    bytes.NewReader(bytes.Repeat([]byte{9}, 256)),
		Identities: identities,
	})
}

var testClaims = IdentityClaims{
	Subject:       "sub-1",
	Email:         "Person@Example.com",
	EmailVerified: true,
	HostedDomain:  "example.com",
	Name:          "Person",
}

func TestLoginWithIdentityProvisionsMemberOnFirstSignIn(t *testing.T) {
	users := &fakeUsers{findErr: ErrNotFound}
	identities := &fakeIdentities{}
	sessions := &fakeSessions{}
	service := newIdentityService(users, identities, sessions)

	user, token, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims)
	if err != nil {
		t.Fatalf("LoginWithIdentity() error = %v", err)
	}
	if token == "" {
		t.Fatal("no session token issued")
	}
	if len(identities.created) != 1 {
		t.Fatalf("created %d accounts, want 1", len(identities.created))
	}
	if identities.created[0].Role != RoleMember {
		t.Fatalf("Role = %q, want member", identities.created[0].Role)
	}
	if identities.created[0].Email != "person@example.com" {
		t.Fatalf("Email = %q, want the normalized address", identities.created[0].Email)
	}
	if user.ID == 0 {
		t.Fatal("returned user has no id")
	}
	if got := sessions.creates[0].ExpiresAt; !got.Equal(serviceTestNow.Add(SSOSessionLifetime)) {
		t.Fatalf("ExpiresAt = %v, want the SSO lifetime", got)
	}
}

func TestLoginWithIdentitySecondSignInCreatesNoAccount(t *testing.T) {
	identities := &fakeIdentities{bySubject: map[string]User{"sub-1": {ID: 7, Email: "person@example.com"}}}
	service := newIdentityService(&fakeUsers{findErr: ErrNotFound}, identities, &fakeSessions{})

	user, _, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims)
	if err != nil {
		t.Fatalf("LoginWithIdentity() error = %v", err)
	}
	if user.ID != 7 {
		t.Fatalf("user id = %d, want 7", user.ID)
	}
	if len(identities.created) != 0 {
		t.Fatalf("created %d accounts, want 0", len(identities.created))
	}
}

func TestLoginWithIdentityLinksExistingAccountByEmail(t *testing.T) {
	users := &fakeUsers{user: User{ID: 3, Email: "person@example.com", Role: RoleMember, PasswordHash: "hash"}}
	identities := &fakeIdentities{}
	service := newIdentityService(users, identities, &fakeSessions{})

	user, _, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims)
	if err != nil {
		t.Fatalf("LoginWithIdentity() error = %v", err)
	}
	if user.ID != 3 {
		t.Fatalf("user id = %d, want the existing account", user.ID)
	}
	if len(identities.linked) != 1 || identities.linked[0] != 3 {
		t.Fatalf("linked = %v, want [3]", identities.linked)
	}
	if len(identities.created) != 0 {
		t.Fatal("an account was created instead of linked")
	}
}

// TestLoginWithIdentityRefusesToLinkAnOwnerAccount pins the total-takeover
// path shut. The email fallback links on the email claim, so whoever
// controls owner@company.com in the Workspace would otherwise sign in with
// Google and receive an owner session without ever knowing the faultline
// password. The refusal must read as ErrInvalidCredentials — the caller
// learns nothing — while the operator gets the detail through ReportError.
func TestLoginWithIdentityRefusesToLinkAnOwnerAccount(t *testing.T) {
	users := &fakeUsers{user: User{ID: 3, Email: "person@example.com", Role: RoleOwner, PasswordHash: "hash"}}
	identities := &fakeIdentities{}
	var reported []error
	service := NewService(users, &fakeSessions{}, ServiceOptions{
		Clock:       &fakeClock{now: serviceTestNow},
		Entropy:     bytes.NewReader(bytes.Repeat([]byte{9}, 256)),
		Identities:  identities,
		ReportError: func(err error) { reported = append(reported, err) },
	})

	_, token, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims)
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("LoginWithIdentity() error = %v, want ErrInvalidCredentials", err)
	}
	if token != "" {
		t.Fatal("an owner account was issued a session through SSO")
	}
	if len(identities.linked) != 0 {
		t.Fatalf("linked = %v, want none — the owner account must not gain an identity", identities.linked)
	}
	if len(identities.created) != 0 {
		t.Fatal("a second account was provisioned instead of refusing")
	}
	if len(reported) != 1 {
		t.Fatalf("reported %d errors, want 1 so an operator can see the refusal", len(reported))
	}
	if !strings.Contains(reported[0].Error(), "person@example.com") {
		t.Fatalf("reported error = %q, want the account it refused", reported[0])
	}
}

func TestLoginWithIdentityRefusesDeactivatedAccount(t *testing.T) {
	deactivated := serviceTestNow
	identities := &fakeIdentities{bySubject: map[string]User{"sub-1": {ID: 7, DeactivatedAt: &deactivated}}}
	service := newIdentityService(&fakeUsers{findErr: ErrNotFound}, identities, &fakeSessions{})

	if _, _, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("LoginWithIdentity() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestLoginWithIdentityWithoutIdentitiesRepositoryErrors(t *testing.T) {
	service := NewService(&fakeUsers{}, &fakeSessions{}, ServiceOptions{
		Clock:   &fakeClock{now: serviceTestNow},
		Entropy: bytes.NewReader(bytes.Repeat([]byte{9}, 256)),
	})

	if _, _, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims); !errors.Is(err, ErrSSONotConfigured) {
		t.Fatalf("LoginWithIdentity() error = %v, want ErrSSONotConfigured", err)
	}
}

func TestLoginWithIdentityResolvesConcurrentLinkConflict(t *testing.T) {
	// The racing request wins the link: this one's Link call always
	// conflicts, and only the retry lookup can see the linked account.
	// appearAfter makes the first lookup miss, which is what forces the
	// retry path to be exercised rather than passing by luck.
	users := &fakeUsers{user: User{ID: 3, Email: "person@example.com", Role: RoleMember, PasswordHash: "hash"}}
	identities := &fakeIdentities{
		linkErr:     ErrConflict,
		appearAfter: 1,
		bySubject:   map[string]User{"sub-1": {ID: 3, Email: "person@example.com"}},
	}
	service := newIdentityService(users, identities, &fakeSessions{})

	user, _, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims)
	if err != nil {
		t.Fatalf("LoginWithIdentity() error = %v", err)
	}
	if user.ID != 3 {
		t.Fatalf("user id = %d, want the account the racing request linked", user.ID)
	}
	if identities.findCalls < 2 {
		t.Fatalf("findCalls = %d, want at least 2 — the retry lookup never happened", identities.findCalls)
	}
	if len(identities.created) != 0 {
		t.Fatal("an account was created instead of linked")
	}
}

func TestLoginWithIdentityResolvesConcurrentFirstSignIn(t *testing.T) {
	// The racing request wins: this one's insert conflicts, and only the
	// retry lookup can see the account. appearAfter makes the first lookup
	// miss, which is what forces the retry path to be exercised.
	identities := &fakeIdentities{
		createErr:   ErrConflict,
		appearAfter: 1,
		bySubject:   map[string]User{"sub-1": {ID: 11, Email: "person@example.com"}},
	}
	service := newIdentityService(&fakeUsers{findErr: ErrNotFound}, identities, &fakeSessions{})

	user, _, err := service.LoginWithIdentity(context.Background(), ProviderGoogle, testClaims)
	if err != nil {
		t.Fatalf("LoginWithIdentity() error = %v", err)
	}
	if user.ID != 11 {
		t.Fatalf("user id = %d, want the account the racing request created", user.ID)
	}
	if identities.findCalls < 2 {
		t.Fatalf("findCalls = %d, want at least 2 — the retry lookup never happened", identities.findCalls)
	}
}
