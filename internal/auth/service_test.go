package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var serviceTestNow = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakeUsers struct {
	created        []NewUser
	user           User
	byID           map[int64]User
	findErr        error
	makeErr        error
	passwordWrites int
}

func (r *fakeUsers) Create(_ context.Context, user NewUser) (User, error) {
	r.created = append(r.created, user)
	if r.makeErr != nil {
		return User{}, r.makeErr
	}
	// Create does not return a password hash (see UserRepository), so the
	// stored fixture's own hash is cleared on the way out.
	userOut := r.user
	userOut.PasswordHash = ""
	if userOut.Email == "" {
		userOut = User{ID: 1, Email: user.Email, Name: user.Name}
	}
	return userOut, nil
}

func (r *fakeUsers) FindByEmail(_ context.Context, email string) (User, error) {
	if r.findErr != nil {
		return User{}, r.findErr
	}
	if email != r.user.Email {
		return User{}, ErrNotFound
	}
	return r.user, nil
}

func (r *fakeUsers) FindByID(_ context.Context, id int64) (User, error) {
	if user, ok := r.byID[id]; ok {
		return user, nil
	}
	return User{}, ErrNotFound
}

func (r *fakeUsers) List(context.Context) ([]User, error) { return nil, nil }

func (r *fakeUsers) ListActive(context.Context) ([]User, error) { return nil, nil }

func (r *fakeUsers) ListAssignableForProject(context.Context, ProjectScope, string) ([]User, error) {
	return nil, nil
}

func (r *fakeUsers) Update(context.Context, int64, UserChanges) (User, error) {
	return User{}, ErrNotFound
}

func (r *fakeUsers) UpdatePasswordHash(context.Context, int64, string) error {
	r.passwordWrites++
	return nil
}

type fakeSessions struct {
	creates      []NewSession
	createErrors []error
	foundSession Session
	foundUser    User
	findErr      error
	touches      []time.Time
	touchErr     error
	deleted      [][32]byte
	deleteErr    error
	cleanupTimes []time.Time
	cleanupErr   error
}

func (r *fakeSessions) Create(_ context.Context, session NewSession) error {
	r.creates = append(r.creates, session)
	if i := len(r.creates) - 1; i < len(r.createErrors) {
		return r.createErrors[i]
	}
	return nil
}

func (r *fakeSessions) FindByTokenHash(_ context.Context, hash [32]byte) (Session, User, error) {
	if r.findErr != nil {
		return Session{}, User{}, r.findErr
	}
	if hash != r.foundSession.TokenHash {
		return Session{}, User{}, ErrNotFound
	}
	return r.foundSession, r.foundUser, nil
}

func (r *fakeSessions) Touch(_ context.Context, _ [32]byte, now time.Time) error {
	r.touches = append(r.touches, now)
	return r.touchErr
}

func (r *fakeSessions) DeleteByTokenHash(_ context.Context, hash [32]byte) error {
	r.deleted = append(r.deleted, hash)
	return r.deleteErr
}

func (r *fakeSessions) DeleteExpired(_ context.Context, now time.Time) error {
	r.cleanupTimes = append(r.cleanupTimes, now)
	return r.cleanupErr
}

func (r *fakeSessions) DeleteByUserID(context.Context, int64) error { return nil }

func (r *fakeSessions) DeleteByUserIDExcept(context.Context, int64, [32]byte) error { return nil }

func newServiceForTest(users *fakeUsers, sessions *fakeSessions, entropy io.Reader, report func(error)) *Service {
	return NewService(users, sessions, ServiceOptions{
		Clock:       &fakeClock{now: serviceTestNow},
		Entropy:     entropy,
		ReportError: report,
	})
}

func TestServiceCreateUserNormalizesAndValidatesInput(t *testing.T) {
	users := &fakeUsers{}
	service := newServiceForTest(users, &fakeSessions{}, bytes.NewReader(make([]byte, 16)), nil)

	user, err := service.CreateUser(t.Context(), " ADMIN@Example.COM ", "  Administrator  ", "correct horse", RoleOwner)
	if err != nil {
		t.Fatalf("CreateUser() error = %v", err)
	}
	if user.Email != "admin@example.com" || user.Name != "Administrator" {
		t.Fatalf("CreateUser() = %+v", user)
	}
	if len(users.created) != 1 || users.created[0].Email != "admin@example.com" || users.created[0].Name != "Administrator" || users.created[0].Role != RoleOwner {
		t.Fatalf("repository Create() calls = %+v", users.created)
	}
	ok, err := VerifyPassword(users.created[0].PasswordHash, "correct horse")
	if err != nil || !ok {
		t.Fatalf("stored password verification = (%v, %v)", ok, err)
	}

	for name, input := range map[string][4]string{
		"empty email":    {" ", "Administrator", "correct horse", "member"},
		"invalid email":  {"not-an-email", "Administrator", "correct horse", "member"},
		"empty name":     {"admin@example.com", " \t", "correct horse", "member"},
		"short password": {"admin@example.com", "Administrator", "too short", "member"},
		"invalid role":   {"admin@example.com", "Administrator", "correct horse", "admin"},
	} {
		t.Run(name, func(t *testing.T) {
			before := len(users.created)
			if _, err := service.CreateUser(t.Context(), input[0], input[1], input[2], Role(input[3])); err == nil {
				t.Fatal("CreateUser() error = nil, want validation error")
			}
			if len(users.created) != before {
				t.Fatal("invalid user reached repository")
			}
		})
	}
}

func TestServiceCreateUserPreservesConflictAsStableError(t *testing.T) {
	users := &fakeUsers{makeErr: ErrConflict}
	service := newServiceForTest(users, &fakeSessions{}, bytes.NewReader(make([]byte, 16)), nil)
	_, err := service.CreateUser(t.Context(), "admin@example.com", "Administrator", "correct horse", RoleMember)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CreateUser() error = %v, want ErrConflict", err)
	}
}

func TestServiceLoginCreatesOpaqueSevenDaySession(t *testing.T) {
	passwordHash, err := HashPassword("correct horse", bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatal(err)
	}
	users := &fakeUsers{user: User{ID: 42, Email: "admin@example.com", Name: "Administrator", PasswordHash: passwordHash}}
	sessions := &fakeSessions{}
	tokenBytes := make([]byte, 32)
	for i := range tokenBytes {
		tokenBytes[i] = byte(i)
	}
	service := newServiceForTest(users, sessions, bytes.NewReader(tokenBytes), nil)

	user, token, err := service.Login(t.Context(), " ADMIN@example.com ", "correct horse")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if user.ID != 42 {
		t.Fatalf("Login() user = %+v", user)
	}
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || !bytes.Equal(decoded, tokenBytes) {
		t.Fatalf("Login() token = %q, decoded %x, error %v", token, decoded, err)
	}
	if strings.Contains(token, "=") {
		t.Fatalf("Login() token %q contains padding", token)
	}
	if len(sessions.creates) != 1 {
		t.Fatalf("session creates = %d, want 1", len(sessions.creates))
	}
	created := sessions.creates[0]
	if created.UserID != 42 || created.TokenHash != sha256.Sum256(tokenBytes) || created.CreatedAt != serviceTestNow || created.LastSeenAt != serviceTestNow || created.ExpiresAt != serviceTestNow.Add(7*24*time.Hour) {
		t.Fatalf("created session = %+v", created)
	}
	if len(sessions.cleanupTimes) != 1 || sessions.cleanupTimes[0] != serviceTestNow {
		t.Fatalf("cleanup times = %v", sessions.cleanupTimes)
	}
}

func TestServiceLoginUsesGenericCredentialsErrorAndDummyHash(t *testing.T) {
	users := &fakeUsers{findErr: ErrNotFound}
	service := newServiceForTest(users, &fakeSessions{}, bytes.NewReader(make([]byte, 32)), nil)
	var verifiedHash string
	service.verifyPassword = func(hash, _ string) (bool, error) {
		verifiedHash = hash
		return false, nil
	}
	_, _, err := service.Login(t.Context(), "missing@example.com", "anything")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	if verifiedHash != dummyPasswordHash {
		t.Fatalf("VerifyPassword hash = %q, want dummy hash", verifiedHash)
	}

	users.findErr = nil
	users.user = User{Email: "admin@example.com", PasswordHash: dummyPasswordHash}
	service.verifyPassword = func(_, _ string) (bool, error) { return false, errors.New("malformed hash") }
	_, _, err = service.Login(t.Context(), "admin@example.com", "wrong password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() malformed hash error = %v, want generic credentials error", err)
	}
}

func TestServiceLoginBoundsPasswordVerificationConcurrency(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	service := NewService(users, &fakeSessions{}, ServiceOptions{
		Clock:                     &fakeClock{now: serviceTestNow},
		Entropy:                   bytes.NewReader(make([]byte, 32)),
		PasswordVerificationLimit: 2,
	})
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	service.verifyPassword = func(_, _ string) (bool, error) {
		entered <- struct{}{}
		<-release
		return false, nil
	}

	results := make(chan error, 2)
	for range 2 {
		go func() {
			_, _, err := service.Login(t.Context(), "admin@example.com", "password")
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("configured verification slot was not entered")
		}
	}

	_, _, err := service.Login(t.Context(), "admin@example.com", "password")
	if !errors.Is(err, ErrAuthenticationBusy) {
		t.Fatalf("Login() while saturated error = %v, want ErrAuthenticationBusy", err)
	}
	select {
	case <-entered:
		t.Fatal("saturated Login() ran password verifier")
	default:
	}

	release <- struct{}{}
	if err := <-results; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("released Login() error = %v, want ErrInvalidCredentials", err)
	}

	recovered := make(chan error, 1)
	go func() {
		_, _, err := service.Login(t.Context(), "admin@example.com", "password")
		recovered <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("verification capacity did not recover")
	}
	for range 2 {
		release <- struct{}{}
	}
	if err := <-results; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second Login() error = %v, want ErrInvalidCredentials", err)
	}
	if err := <-recovered; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("recovered Login() error = %v, want ErrInvalidCredentials", err)
	}
}

func TestServiceLoginUsesDefaultFourPasswordVerificationSlotsForRealAndDummyUsers(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	service := NewService(users, &fakeSessions{}, ServiceOptions{})
	entered := make(chan string, 5)
	release := make(chan struct{})
	service.verifyPassword = func(hash, _ string) (bool, error) {
		entered <- hash
		<-release
		return false, nil
	}

	results := make(chan error, 4)
	for i := range 4 {
		email := "admin@example.com"
		if i%2 == 1 {
			email = "missing@example.com"
		}
		go func() {
			_, _, err := service.Login(t.Context(), email, "password")
			results <- err
		}()
	}
	for range 4 {
		select {
		case <-entered:
		case <-time.After(time.Second):
			t.Fatal("default verification slot was not entered")
		}
	}
	_, _, err := service.Login(t.Context(), "missing@example.com", "password")
	if !errors.Is(err, ErrAuthenticationBusy) {
		t.Fatalf("fifth Login() error = %v, want ErrAuthenticationBusy", err)
	}
	select {
	case <-entered:
		t.Fatal("fifth Login() ran password verifier")
	default:
	}
	for range 4 {
		release <- struct{}{}
	}
	for range 4 {
		if err := <-results; !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
		}
	}
}

func TestServiceLoginReleasesPasswordVerificationSlotAfterErrorAndPanic(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	service := NewService(users, &fakeSessions{}, ServiceOptions{PasswordVerificationLimit: 1})
	service.verifyPassword = func(_, _ string) (bool, error) {
		return false, errors.New("verifier failed")
	}
	if _, _, err := service.Login(t.Context(), "admin@example.com", "password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() verifier error = %v, want ErrInvalidCredentials", err)
	}

	service.verifyPassword = func(_, _ string) (bool, error) { panic("verifier panic") }
	func() {
		defer func() {
			if recovered := recover(); recovered != "verifier panic" {
				t.Fatalf("Login() panic = %v, want verifier panic", recovered)
			}
		}()
		_, _, _ = service.Login(t.Context(), "admin@example.com", "password")
	}()

	service.verifyPassword = func(_, _ string) (bool, error) { return false, nil }
	if _, _, err := service.Login(t.Context(), "admin@example.com", "password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() after verifier panic = %v, want ErrInvalidCredentials", err)
	}
}

type blockingCreateSessions struct {
	createStarted chan struct{}
	releaseCreate chan struct{}
}

func (r *blockingCreateSessions) Create(context.Context, NewSession) error {
	r.createStarted <- struct{}{}
	<-r.releaseCreate
	return nil
}

func (*blockingCreateSessions) FindByTokenHash(context.Context, [32]byte) (Session, User, error) {
	return Session{}, User{}, ErrNotFound
}

func (*blockingCreateSessions) Touch(context.Context, [32]byte, time.Time) error  { return nil }
func (*blockingCreateSessions) DeleteByTokenHash(context.Context, [32]byte) error { return nil }
func (*blockingCreateSessions) DeleteExpired(context.Context, time.Time) error    { return nil }
func (*blockingCreateSessions) DeleteByUserID(context.Context, int64) error       { return nil }

func (*blockingCreateSessions) DeleteByUserIDExcept(context.Context, int64, [32]byte) error {
	return nil
}

func TestServiceLoginReleasesVerificationSlotBeforeSessionCreate(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	sessions := &blockingCreateSessions{createStarted: make(chan struct{}, 1), releaseCreate: make(chan struct{})}
	service := NewService(users, sessions, ServiceOptions{
		Entropy:                   bytes.NewReader(make([]byte, 64)),
		PasswordVerificationLimit: 1,
	})
	verifierEntered := make(chan struct{}, 2)
	var verificationCalls atomic.Int32
	service.verifyPassword = func(_, _ string) (bool, error) {
		verifierEntered <- struct{}{}
		return verificationCalls.Add(1) == 1, nil
	}

	firstResult := make(chan error, 1)
	go func() {
		_, _, err := service.Login(t.Context(), "admin@example.com", "password")
		firstResult <- err
	}()
	select {
	case <-verifierEntered:
	case <-time.After(time.Second):
		t.Fatal("first verifier was not entered")
	}
	select {
	case <-sessions.createStarted:
	case <-time.After(time.Second):
		t.Fatal("first session create did not block")
	}

	secondResult := make(chan error, 1)
	go func() {
		_, _, err := service.Login(t.Context(), "admin@example.com", "password")
		secondResult <- err
	}()
	select {
	case <-verifierEntered:
	case err := <-secondResult:
		t.Fatalf("second Login() returned before verifier admission: %v", err)
	case <-time.After(time.Second):
		t.Fatal("second Login() was not admitted to verifier while session create blocked")
	}
	if err := <-secondResult; !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("second Login() error = %v, want ErrInvalidCredentials", err)
	}

	close(sessions.releaseCreate)
	if err := <-firstResult; err != nil {
		t.Fatalf("first Login() error = %v", err)
	}
}

func TestDummyPasswordHashIsValidAndExercisesRealVerifier(t *testing.T) {
	ok, err := VerifyPassword(dummyPasswordHash, "not the dummy password")
	if err != nil {
		t.Fatalf("VerifyPassword(dummyPasswordHash) error = %v, want valid hash", err)
	}
	if ok {
		t.Fatal("VerifyPassword(dummyPasswordHash) = true for wrong candidate")
	}
}

type oneByteReader struct{ value byte }

func (r oneByteReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	p[0] = r.value
	return 1, nil
}

func TestServiceLoginReadsTokenEntropyAcrossShortChunks(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	sessions := &fakeSessions{}
	service := newServiceForTest(users, sessions, oneByteReader{value: 0x5a}, nil)
	service.verifyPassword = func(_, _ string) (bool, error) { return true, nil }

	_, token, err := service.Login(t.Context(), "admin@example.com", "correct horse")
	if err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	want := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x5a}, 32))
	if token != want {
		t.Fatalf("Login() token = %q, want %q", token, want)
	}
}

func TestServiceLoginRetriesTokenConflictsExactlyThreeTimes(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	sessions := &fakeSessions{createErrors: []error{ErrConflict, ErrConflict, ErrConflict}}
	service := newServiceForTest(users, sessions, bytes.NewReader(make([]byte, 96)), nil)
	service.verifyPassword = func(_, _ string) (bool, error) { return true, nil }

	_, token, err := service.Login(t.Context(), "admin@example.com", "correct horse")
	if err == nil || errors.Is(err, ErrConflict) {
		t.Fatalf("Login() = token %q, error %v; want internal collision error", token, err)
	}
	if got := len(sessions.creates); got != 3 {
		t.Fatalf("session create attempts = %d, want 3", got)
	}
}

func TestIssueSessionAppliesTheGivenLifetime(t *testing.T) {
	clock := &fakeClock{now: serviceTestNow}
	sessions := &fakeSessions{}
	service := NewService(&fakeUsers{}, sessions, ServiceOptions{
		Clock:   clock,
		Entropy: bytes.NewReader(bytes.Repeat([]byte{7}, 64)),
	})

	if _, err := service.issueSession(context.Background(), User{ID: 42}, SSOSessionLifetime); err != nil {
		t.Fatalf("issueSession() error = %v", err)
	}
	if len(sessions.creates) != 1 {
		t.Fatalf("created %d sessions, want 1", len(sessions.creates))
	}
	got := sessions.creates[0].ExpiresAt
	want := serviceTestNow.Add(SSOSessionLifetime)
	if !got.Equal(want) {
		t.Fatalf("ExpiresAt = %v, want %v", got, want)
	}
	if sessions.creates[0].UserID != 42 {
		t.Fatalf("UserID = %d, want 42", sessions.creates[0].UserID)
	}
}

func TestServiceAuthenticateRejectsMalformedRevokedAndExpiredTokens(t *testing.T) {
	now := serviceTestNow
	raw := bytes.Repeat([]byte{7}, 32)
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256(raw)
	tests := []struct {
		name     string
		token    string
		sessions *fakeSessions
	}{
		{"malformed", "not a token", &fakeSessions{}},
		{"revoked", token, &fakeSessions{findErr: ErrNotFound}},
		{"expired", token, &fakeSessions{foundSession: Session{TokenHash: hash, ExpiresAt: now}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := newServiceForTest(&fakeUsers{}, test.sessions, bytes.NewReader(nil), nil)
			_, _, err := service.Authenticate(t.Context(), test.token)
			if !errors.Is(err, ErrInvalidSession) {
				t.Fatalf("Authenticate() error = %v, want ErrInvalidSession", err)
			}
		})
	}
}

func TestServiceAuthenticateTouchesAtFiveMinuteThreshold(t *testing.T) {
	raw := bytes.Repeat([]byte{8}, 32)
	token := base64.RawURLEncoding.EncodeToString(raw)
	hash := sha256.Sum256(raw)
	for name, test := range map[string]struct {
		age         time.Duration
		wantTouches int
	}{
		"below threshold":  {5*time.Minute - time.Nanosecond, 0},
		"at threshold":     {5 * time.Minute, 1},
		"future timestamp": {-time.Minute, 0},
	} {
		t.Run(name, func(t *testing.T) {
			sessions := &fakeSessions{
				foundSession: Session{TokenHash: hash, ExpiresAt: serviceTestNow.Add(time.Hour), LastSeenAt: serviceTestNow.Add(-test.age)},
				foundUser:    User{ID: 1, Email: "admin@example.com"},
			}
			service := newServiceForTest(&fakeUsers{}, sessions, bytes.NewReader(nil), nil)
			session, user, err := service.Authenticate(t.Context(), token)
			if err != nil || user.ID != 1 {
				t.Fatalf("Authenticate() = (%+v, %v)", user, err)
			}
			// The returned session names the very session that authenticated:
			// UserHandler.ChangeOwnPassword spares exactly this token hash when
			// it revokes the account's other sessions.
			if session.TokenHash != hash {
				t.Fatalf("session.TokenHash = %x, want %x", session.TokenHash, hash)
			}
			if len(sessions.touches) != test.wantTouches {
				t.Fatalf("touch count = %d, want %d", len(sessions.touches), test.wantTouches)
			}
		})
	}
}

func TestServiceAuthenticateFailsWhenTouchFails(t *testing.T) {
	raw := bytes.Repeat([]byte{9}, 32)
	hash := sha256.Sum256(raw)
	sessions := &fakeSessions{
		foundSession: Session{TokenHash: hash, ExpiresAt: serviceTestNow.Add(time.Hour), LastSeenAt: serviceTestNow.Add(-5 * time.Minute)},
		foundUser:    User{ID: 1},
		touchErr:     errors.New("database unavailable"),
	}
	service := newServiceForTest(&fakeUsers{}, sessions, bytes.NewReader(nil), nil)
	if _, _, err := service.Authenticate(t.Context(), base64.RawURLEncoding.EncodeToString(raw)); err == nil || errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Authenticate() error = %v, want internal touch error", err)
	}
}

func TestServiceAuthenticateTranslatesTouchNotFoundToInvalidSession(t *testing.T) {
	raw := bytes.Repeat([]byte{11}, 32)
	hash := sha256.Sum256(raw)
	sessions := &fakeSessions{
		foundSession: Session{TokenHash: hash, ExpiresAt: serviceTestNow.Add(time.Hour), LastSeenAt: serviceTestNow.Add(-5 * time.Minute)},
		foundUser:    User{ID: 1},
		touchErr:     ErrNotFound,
	}
	service := newServiceForTest(&fakeUsers{}, sessions, bytes.NewReader(nil), nil)
	_, _, err := service.Authenticate(t.Context(), base64.RawURLEncoding.EncodeToString(raw))
	if !errors.Is(err, ErrInvalidSession) {
		t.Fatalf("Authenticate() error = %v, want ErrInvalidSession", err)
	}
}

func TestServiceLogoutIsIdempotentAndCleansExpiredSessions(t *testing.T) {
	raw := bytes.Repeat([]byte{10}, 32)
	token := base64.RawURLEncoding.EncodeToString(raw)
	sessions := &fakeSessions{deleteErr: ErrNotFound, cleanupErr: errors.New("cleanup failed")}
	var reported []error
	service := newServiceForTest(&fakeUsers{}, sessions, bytes.NewReader(nil), func(err error) { reported = append(reported, err) })

	if err := service.Logout(t.Context(), token); err != nil {
		t.Fatalf("Logout() error = %v", err)
	}
	if len(sessions.deleted) != 1 || sessions.deleted[0] != sha256.Sum256(raw) {
		t.Fatalf("deleted hashes = %x", sessions.deleted)
	}
	if len(sessions.cleanupTimes) != 1 || len(reported) != 1 || !errors.Is(reported[0], sessions.cleanupErr) {
		t.Fatalf("cleanup calls = %v, reports = %v", sessions.cleanupTimes, reported)
	}
	if err := service.Logout(t.Context(), "malformed"); err != nil {
		t.Fatalf("Logout(malformed) error = %v", err)
	}
}

func TestLoginRejectsDeactivatedUser(t *testing.T) {
	deactivated := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	hash, err := HashPassword("correct-horse-battery", bytes.NewReader(make([]byte, 16)))
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	users := &fakeUsers{user: User{
		ID:            7,
		Email:         "gone@example.com",
		Name:          "Gone",
		PasswordHash:  hash,
		Role:          RoleMember,
		DeactivatedAt: &deactivated,
	}}
	service := newServiceForTest(users, &fakeSessions{}, bytes.NewReader(make([]byte, 32)), nil)

	_, _, err = service.Login(t.Context(), "gone@example.com", "correct-horse-battery")

	// The password is correct. Only deactivation may reject it, and it must
	// reject it as invalid credentials — a distinguishable error would let an
	// attacker enumerate disabled accounts.
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("err = %v, want ErrInvalidCredentials", err)
	}
}

func TestServiceLoginReportsCleanupFailureWithoutFailing(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "admin@example.com", PasswordHash: dummyPasswordHash}}
	cleanupErr := errors.New("cleanup unavailable")
	sessions := &fakeSessions{cleanupErr: cleanupErr}
	var reported error
	service := newServiceForTest(users, sessions, bytes.NewReader(make([]byte, 32)), func(err error) { reported = err })
	service.verifyPassword = func(_, _ string) (bool, error) { return true, nil }
	if _, _, err := service.Login(t.Context(), "admin@example.com", "correct horse"); err != nil {
		t.Fatalf("Login() error = %v", err)
	}
	if !errors.Is(reported, cleanupErr) {
		t.Fatalf("reported error = %v, want cleanup error", reported)
	}
}

func TestLoginRejectsAccountWithoutPassword(t *testing.T) {
	users := &fakeUsers{user: User{ID: 1, Email: "sso@example.com", PasswordHash: ""}}
	verifications := 0
	service := NewService(users, &fakeSessions{}, ServiceOptions{Clock: &fakeClock{now: serviceTestNow}})
	service.verifyPassword = func(hash, password string) (bool, error) {
		verifications++
		if hash == "" {
			t.Fatal("verification was attempted against an empty hash")
		}
		return false, nil
	}

	_, _, err := service.Login(context.Background(), "sso@example.com", "any-password")
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("Login() error = %v, want ErrInvalidCredentials", err)
	}
	if verifications != 1 {
		t.Fatalf("verifications = %d, want 1 — the empty hash must still cost one verification", verifications)
	}
}
