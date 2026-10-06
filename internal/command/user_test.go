package command

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/platform/config"
	migration "github.com/ramilS/faultline/internal/platform/migrate"
)

type fakeUserCreator struct {
	user     auth.User
	err      error
	email    string
	name     string
	password string
	role     auth.Role
	calls    int
}

func (creator *fakeUserCreator) CreateUser(_ context.Context, email, name, password string, role auth.Role) (auth.User, error) {
	creator.calls++
	creator.email, creator.name, creator.password, creator.role = email, name, password, role
	return creator.user, creator.err
}

func TestUserCreatePromptsTwiceAndCreatesNormalizedUser(t *testing.T) {
	pool := &fakePool{}
	creator := &fakeUserCreator{user: auth.User{ID: 42, Email: "admin@example.com", Name: "Administrator"}}
	passwords := [][]byte{[]byte("correct horse"), []byte("correct horse")}
	var promptCalls []int
	dependencies := userCommandDependencies(pool, creator)
	dependencies.readPassword = func(fd int) ([]byte, error) {
		promptCalls = append(promptCalls, fd)
		password := passwords[len(promptCalls)-1]
		return password, nil
	}
	dependencies.passwordFD = 17

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "create", "--email", " Admin@Example.COM ", "--name", "  Administrator  "}, &stdout, &stderr, dependencies)

	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if len(promptCalls) != 2 || promptCalls[0] != 17 || promptCalls[1] != 17 {
		t.Fatalf("password prompt calls = %v, want [17 17]", promptCalls)
	}
	if creator.calls != 1 {
		t.Fatalf("CreateUser calls = %d, want 1", creator.calls)
	}
	if creator.email != " Admin@Example.COM " || creator.name != "Administrator" {
		t.Fatalf("CreateUser arguments = (%q, %q), want raw email and trimmed name", creator.email, creator.name)
	}
	if creator.password != "correct horse" {
		t.Fatalf("CreateUser password = %q, want prompted password", creator.password)
	}
	// `faultline user create` is the only path that produces an owner in a
	// fresh install: the migration's bootstrap promotion only fires once the
	// users table is non-empty. If this regresses to auth.RoleMember, a new
	// deployment ends up with nobody able to administer it.
	if creator.role != auth.RoleOwner {
		t.Fatalf("CreateUser role = %q, want %q", creator.role, auth.RoleOwner)
	}
	if got := stdout.String(); got != "created user admin@example.com (ID 42)\n" {
		t.Fatalf("stdout = %q", got)
	}
	if got := stderr.String(); got != "Password: \nConfirm password: \n" {
		t.Fatalf("stderr = %q", got)
	}
	if !pool.closed {
		t.Fatal("database pool was not closed")
	}
}

func TestUserCreateRejectsInvalidArgumentsBeforeConfigurationOrDatabase(t *testing.T) {
	testCases := []struct {
		name string
		args []string
	}{
		{name: "missing create", args: []string{"user"}},
		{name: "unknown action", args: []string{"user", "delete"}},
		{name: "missing email", args: []string{"user", "create", "--name", "Administrator"}},
		{name: "empty email", args: []string{"user", "create", "--email", "", "--name", "Administrator"}},
		{name: "missing name", args: []string{"user", "create", "--email", "admin@example.com"}},
		{name: "empty name", args: []string{"user", "create", "--email", "admin@example.com", "--name", "   "}},
		{name: "unknown flag", args: []string{"user", "create", "--email", "admin@example.com", "--name", "Administrator", "--role", "admin"}},
		{name: "extra argument", args: []string{"user", "create", "--email", "admin@example.com", "--name", "Administrator", "extra"}},
		{name: "repeated email", args: []string{"user", "create", "--email", "first@example.com", "--email", "second@example.com", "--name", "Administrator"}},
		{name: "repeated name", args: []string{"user", "create", "--email", "admin@example.com", "--name", "First", "--name", "Second"}},
		{name: "single dash email", args: []string{"user", "create", "-email", "admin@example.com", "--name", "Administrator"}},
		{name: "single dash name", args: []string{"user", "create", "--email", "admin@example.com", "-name", "Administrator"}},
		{name: "standalone terminator", args: []string{"user", "create", "--"}},
		{name: "trailing terminator", args: []string{"user", "create", "--email", "admin@example.com", "--name", "Administrator", "--"}},
		{name: "malformed email flag", args: []string{"user", "create", "---email", "admin@example.com", "--name", "Administrator"}},
		{name: "malformed name flag", args: []string{"user", "create", "--email", "admin@example.com", "--names", "Administrator"}},
		{name: "name swallows unknown flag", args: []string{"user", "create", "--email", "admin@example.com", "--name", "--role"}},
		{name: "name swallows email flag", args: []string{"user", "create", "--name", "--email=first@example.com", "--email", "admin@example.com"}},
		{name: "email swallows name flag", args: []string{"user", "create", "--email", "--name=First", "--name", "Administrator"}},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configurationCalls, databaseCalls := 0, 0
			dependencies := userCommandDependencies(&fakePool{}, &fakeUserCreator{})
			dependencies.loadConfiguration = func() (config.Config, error) {
				configurationCalls++
				return validConfig(), nil
			}
			dependencies.openDatabase = func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
				databaseCalls++
				return nil, errors.New("unexpected database open")
			}

			var stderr bytes.Buffer
			if code := run(t.Context(), testCase.args, io.Discard, &stderr, dependencies); code != 2 {
				t.Fatalf("run() = %d, want 2; stderr = %q", code, stderr.String())
			}
			if configurationCalls != 0 || databaseCalls != 0 {
				t.Fatalf("configuration/database calls = %d/%d, want 0/0", configurationCalls, databaseCalls)
			}
			if !strings.Contains(stderr.String(), "usage: faultline user create --email <email> --name <name>") {
				t.Fatalf("stderr = %q", stderr.String())
			}
		})
	}
}

func TestUserCreateAcceptsEqualsFlagForms(t *testing.T) {
	mode, options, valid := ParseUserArguments([]string{"create", "--email=admin@example.com", "--name=-Administrator"})
	if !valid {
		t.Fatal("ParseUserArguments() rejected supported equals forms")
	}
	if mode != "user-create" || options.Email != "admin@example.com" || options.Name != "-Administrator" {
		t.Fatalf("mode = %q, options = %#v", mode, options)
	}
}

func TestUserCreateRejectsPasswordMismatchWithoutLeakage(t *testing.T) {
	pool := &fakePool{}
	creator := &fakeUserCreator{}
	dependencies := userCommandDependencies(pool, creator)
	passwords := [][]byte{[]byte("first secret"), []byte("second secret")}
	calls := 0
	dependencies.readPassword = func(int) ([]byte, error) {
		password := passwords[calls]
		calls++
		return password, nil
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), userCreateArgs(), &stdout, &stderr, dependencies)

	if code != 1 || !strings.Contains(stderr.String(), "passwords do not match") {
		t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
	}
	if calls != 2 || creator.calls != 0 {
		t.Fatalf("prompt/CreateUser calls = %d/%d, want 2/0", calls, creator.calls)
	}
	assertNoSecret(t, stdout.String()+stderr.String(), "first secret", "second secret")
	if !pool.closed {
		t.Fatal("database pool was not closed")
	}
}

func TestUserCreateRejectsInvalidPasswordsWithoutLeakage(t *testing.T) {
	testCases := []struct {
		name     string
		password []byte
		message  string
	}{
		{name: "short", password: []byte("too short"), message: "password must be at least 12 Unicode code points"},
		{name: "invalid UTF-8", password: []byte{0xff, 0xfe, 's', 'e', 'c', 'r', 'e', 't'}, message: "password must be valid UTF-8"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := &fakePool{}
			creator := &fakeUserCreator{}
			dependencies := userCommandDependencies(pool, creator)
			dependencies.readPassword = passwordSequence(testCase.password, testCase.password)

			var stdout, stderr bytes.Buffer
			code := run(t.Context(), userCreateArgs(), &stdout, &stderr, dependencies)

			if code != 1 || !strings.Contains(stderr.String(), testCase.message) {
				t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
			}
			if creator.calls != 0 {
				t.Fatalf("CreateUser calls = %d, want 0", creator.calls)
			}
			assertNoSecret(t, stdout.String()+stderr.String(), string(testCase.password))
			if !pool.closed {
				t.Fatal("database pool was not closed")
			}
		})
	}
}

func TestUserCreateReportsPromptFailureWithoutLeakingReaderError(t *testing.T) {
	pool := &fakePool{}
	dependencies := userCommandDependencies(pool, &fakeUserCreator{})
	dependencies.readPassword = func(int) ([]byte, error) {
		return nil, errors.New("terminal failed near super-secret-value")
	}

	var stderr bytes.Buffer
	if code := run(t.Context(), userCreateArgs(), io.Discard, &stderr, dependencies); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "could not read password") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	assertNoSecret(t, stderr.String(), "super-secret-value")
	if !pool.closed {
		t.Fatal("database pool was not closed")
	}
}

func TestUserCreateReportsConfirmationFailureWithoutLeakage(t *testing.T) {
	pool := &fakePool{}
	creator := &fakeUserCreator{}
	dependencies := userCommandDependencies(pool, creator)
	calls := 0
	dependencies.readPassword = func(int) ([]byte, error) {
		calls++
		if calls == 1 {
			return []byte("first-secret-value"), nil
		}
		return nil, errors.New("terminal failed near second-secret-value")
	}

	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), userCreateArgs(), &stdout, &stderr, dependencies); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "could not read password confirmation") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if calls != 2 || creator.calls != 0 {
		t.Fatalf("prompt/CreateUser calls = %d/%d, want 2/0", calls, creator.calls)
	}
	assertNoSecret(t, stdout.String()+stderr.String(), "first-secret-value", "second-secret-value")
	if !pool.closed {
		t.Fatal("database pool was not closed")
	}
}

func TestUserCreateReportsDuplicateAndDatabaseFailuresWithoutLeakage(t *testing.T) {
	testCases := []struct {
		name    string
		err     error
		message string
	}{
		{name: "duplicate", err: errors.Join(errors.New("insert included password hash super-secret-value"), auth.ErrConflict), message: "a user with that email already exists"},
		{name: "database", err: errors.New("insert included password hash super-secret-value"), message: "could not create user"},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			pool := &fakePool{}
			creator := &fakeUserCreator{err: testCase.err}
			dependencies := userCommandDependencies(pool, creator)
			dependencies.readPassword = passwordSequence([]byte("super-secret-value"), []byte("super-secret-value"))

			var stdout, stderr bytes.Buffer
			code := run(t.Context(), userCreateArgs(), &stdout, &stderr, dependencies)

			if code != 1 || !strings.Contains(stderr.String(), testCase.message) {
				t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
			}
			assertNoSecret(t, stdout.String()+stderr.String(), "super-secret-value", "password hash")
			if !pool.closed {
				t.Fatal("database pool was not closed")
			}
		})
	}
}

func TestUserCreateReportsDatabaseOpenFailure(t *testing.T) {
	dependencies := userCommandDependencies(&fakePool{}, &fakeUserCreator{})
	dependencies.openDatabase = func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
		return nil, errors.New("connection refused")
	}
	dependencies.readPassword = func(int) ([]byte, error) {
		t.Fatal("password reader called before database opened")
		return nil, nil
	}

	var stderr bytes.Buffer
	if code := run(t.Context(), userCreateArgs(), io.Discard, &stderr, dependencies); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "database: connection refused") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func userCommandDependencies(pool *fakePool, creator *fakeUserCreator) commandDependencies {
	return commandDependencies{
		loadConfiguration: func() (config.Config, error) { return validConfig(), nil },
		openDatabase: func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return &runtimeDatabase{Pool: pool}, nil
		},
		serveHTTP:      func(context.Context, string, http.Handler) error { return errors.New("unexpected server") },
		runMigration:   func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
		newUserCreator: func(auth.UserRepository, auth.SessionRepository) userCreator { return creator },
		readPassword:   passwordSequence([]byte("correct horse"), []byte("correct horse")),
		passwordFD:     0,
	}
}

func userCreateArgs() []string {
	return []string{"user", "create", "--email", "admin@example.com", "--name", "Administrator"}
}

func passwordSequence(passwords ...[]byte) passwordReader {
	call := 0
	return func(int) ([]byte, error) {
		password := passwords[call]
		call++
		return password, nil
	}
}

func assertNoSecret(t *testing.T, output string, secrets ...string) {
	t.Helper()
	for _, secret := range secrets {
		if strings.Contains(output, secret) {
			t.Fatalf("output leaked %q: %q", secret, output)
		}
	}
}
