package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/platform/config"
	migration "github.com/ramilS/faultline/internal/platform/migrate"
)

func TestParseUserArgumentsRejectsUnknownSubcommand(t *testing.T) {
	if _, _, ok := ParseUserArguments([]string{"promote", "--email", "a@example.com"}); ok {
		t.Fatal("ok = true, want false for an unknown subcommand")
	}
}

func TestParseUserArgumentsRejectsARepeatedFlag(t *testing.T) {
	args := []string{"deactivate", "--email", "a@example.com", "--email", "b@example.com"}
	if _, _, ok := ParseUserArguments(args); ok {
		t.Fatal("ok = true, want false — a repeated --email is ambiguous, not a last-wins")
	}
}

func TestParseUserArgumentsAcceptsList(t *testing.T) {
	mode, options, ok := ParseUserArguments([]string{"list"})
	if !ok || mode != "user-list" || options.Email != "" {
		t.Fatalf("mode = %q, options = %+v, ok = %v", mode, options, ok)
	}
}

func TestParseUserArgumentsRejectsEmailSwallowingAFollowingFlag(t *testing.T) {
	testCases := [][]string{
		{"deactivate", "--email", "--force"},
		{"password", "--email", "--force"},
	}
	for _, args := range testCases {
		if _, _, ok := ParseUserArguments(args); ok {
			t.Fatalf("ParseUserArguments(%v) ok = true, want false — --email must not swallow a following flag", args)
		}
	}
}

func TestParseUserArgumentsAcceptsDeactivateAndPassword(t *testing.T) {
	mode, options, ok := ParseUserArguments([]string{"deactivate", "--email", "a@example.com"})
	if !ok || mode != "user-deactivate" || options.Email != "a@example.com" {
		t.Fatalf("mode = %q, options = %+v, ok = %v", mode, options, ok)
	}

	mode, options, ok = ParseUserArguments([]string{"password", "--email", "a@example.com"})
	if !ok || mode != "user-password" || options.Email != "a@example.com" {
		t.Fatalf("mode = %q, options = %+v, ok = %v", mode, options, ok)
	}
}

func TestUserDeactivateWithEmailSwallowingForceFailsCleanly(t *testing.T) {
	dependencies := adminCommandDependencies(&fakePool{}, &fakeUserRepository{}, &fakeUserAdmin{})

	var stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "deactivate", "--email", "--force"}, io.Discard, &stderr, dependencies)

	if code != 2 {
		t.Fatalf("run() = %d, want 2; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "faultline user deactivate --email <email>") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestUserListPrintsTabAlignedTable(t *testing.T) {
	admin := &fakeUserAdmin{listUsers: []auth.User{
		{ID: 1, Email: "owner@example.com", Name: "Owner", Role: auth.RoleOwner},
		{ID: 2, Email: "member@example.com", Name: "Member", Role: auth.RoleMember, DeactivatedAt: &pastTimestamp},
	}}
	dependencies := adminCommandDependencies(&fakePool{}, &fakeUserRepository{}, admin)

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "list"}, &stdout, &stderr, dependencies)

	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}
	want := "EMAIL\tNAME\tROLE\tSTATUS\n" +
		"owner@example.com\tOwner\towner\tactive\n" +
		"member@example.com\tMember\tmember\tdeactivated\n"
	if got := collapseColumns(stdout.String()); got != collapseColumns(want) {
		t.Fatalf("stdout = %q, want columns matching %q", stdout.String(), want)
	}
}

func TestUserListReportsFailure(t *testing.T) {
	admin := &fakeUserAdmin{listErr: errors.New("connection reset by peer")}
	dependencies := adminCommandDependencies(&fakePool{}, &fakeUserRepository{}, admin)

	var stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "list"}, io.Discard, &stderr, dependencies)

	if code != 1 || !strings.Contains(stderr.String(), "could not list users") {
		t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
	}
}

func TestUserDeactivateLooksUpByEmailAndDeactivatesUsingActorZero(t *testing.T) {
	repository := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"owner@example.com": {ID: 7, Email: "owner@example.com", Name: "Owner", Role: auth.RoleOwner},
	}}
	admin := &fakeUserAdmin{setDeactivatedResult: auth.User{ID: 7, Email: "owner@example.com"}}
	dependencies := adminCommandDependencies(&fakePool{}, repository, admin)

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "deactivate", "--email", " Owner@Example.COM "}, &stdout, &stderr, dependencies)

	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if repository.lookupEmail != "owner@example.com" {
		t.Fatalf("FindByEmail called with %q, want normalized email", repository.lookupEmail)
	}
	// actorID must be 0: the CLI has no session, so no real user (IDs start
	// at 1) can ever match it — the self-deactivation guard in
	// auth.Administration.SetDeactivated therefore never fires here, which
	// is intended (see cliActorID's doc comment).
	if admin.setDeactivatedActorID != 0 || admin.setDeactivatedID != 7 || !admin.setDeactivatedValue {
		t.Fatalf("SetDeactivated(actor=%d, id=%d, deactivated=%v), want (0, 7, true)",
			admin.setDeactivatedActorID, admin.setDeactivatedID, admin.setDeactivatedValue)
	}
	if got := stdout.String(); got != "deactivated user owner@example.com (ID 7)\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestUserDeactivateRejectsUnknownEmail(t *testing.T) {
	admin := &fakeUserAdmin{}
	dependencies := adminCommandDependencies(&fakePool{}, &fakeUserRepository{}, admin)

	var stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "deactivate", "--email", "ghost@example.com"}, io.Discard, &stderr, dependencies)

	if code != 1 || !strings.Contains(stderr.String(), "no user with that email") {
		t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
	}
	if admin.setDeactivatedCalls != 0 {
		t.Fatalf("SetDeactivated calls = %d, want 0", admin.setDeactivatedCalls)
	}
}

func TestUserDeactivateSurfacesLastOwnerAsAClearRefusal(t *testing.T) {
	repository := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"owner@example.com": {ID: 1, Email: "owner@example.com"},
	}}
	admin := &fakeUserAdmin{setDeactivatedErr: fmt.Errorf("set deactivated: %w", auth.ErrLastOwner)}
	dependencies := adminCommandDependencies(&fakePool{}, repository, admin)

	var stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "deactivate", "--email", "owner@example.com"}, io.Discard, &stderr, dependencies)

	if code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "refusing to deactivate the last owner") {
		t.Fatalf("stderr = %q, want a clear last-owner refusal", stderr.String())
	}
	if !strings.Contains(stderr.String(), "promote another user to owner") {
		t.Fatalf("stderr = %q, want actionable guidance", stderr.String())
	}
}

func TestUserPasswordPromptsTwiceAndSetsPassword(t *testing.T) {
	repository := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"owner@example.com": {ID: 9, Email: "owner@example.com"},
	}}
	admin := &fakeUserAdmin{}
	dependencies := adminCommandDependencies(&fakePool{}, repository, admin)
	dependencies.readPassword = passwordSequence([]byte("a new correct horse"), []byte("a new correct horse"))

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "password", "--email", "owner@example.com"}, &stdout, &stderr, dependencies)

	if code != 0 {
		t.Fatalf("run() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if admin.setPasswordID != 9 || admin.setPasswordValue != "a new correct horse" {
		t.Fatalf("SetPassword(id=%d, password=%q), want (9, %q)", admin.setPasswordID, admin.setPasswordValue, "a new correct horse")
	}
	if got := stderr.String(); got != "New password: \nConfirm new password: \n" {
		t.Fatalf("stderr = %q", got)
	}
	if got := stdout.String(); got != "updated password for owner@example.com\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestUserPasswordRejectsMismatchWithoutLeakage(t *testing.T) {
	repository := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"owner@example.com": {ID: 9, Email: "owner@example.com"},
	}}
	admin := &fakeUserAdmin{}
	dependencies := adminCommandDependencies(&fakePool{}, repository, admin)
	dependencies.readPassword = passwordSequence([]byte("first secret value"), []byte("second secret value"))

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "password", "--email", "owner@example.com"}, &stdout, &stderr, dependencies)

	if code != 1 || !strings.Contains(stderr.String(), "passwords do not match") {
		t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
	}
	if admin.setPasswordCalls != 0 {
		t.Fatalf("SetPassword calls = %d, want 0", admin.setPasswordCalls)
	}
	assertNoSecret(t, stdout.String()+stderr.String(), "first secret value", "second secret value")
}

func TestUserPasswordRejectsUnknownEmailBeforePrompting(t *testing.T) {
	admin := &fakeUserAdmin{}
	dependencies := adminCommandDependencies(&fakePool{}, &fakeUserRepository{}, admin)
	dependencies.readPassword = func(int) ([]byte, error) {
		t.Fatal("password reader called for an unknown email")
		return nil, nil
	}

	var stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "password", "--email", "ghost@example.com"}, io.Discard, &stderr, dependencies)

	if code != 1 || !strings.Contains(stderr.String(), "no user with that email") {
		t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
	}
}

func TestUserPasswordSurfacesValidationFailureWithoutLeakage(t *testing.T) {
	repository := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"owner@example.com": {ID: 9, Email: "owner@example.com"},
	}}
	admin := &fakeUserAdmin{setPasswordErr: auth.ErrPasswordTooShort}
	dependencies := adminCommandDependencies(&fakePool{}, repository, admin)
	dependencies.readPassword = passwordSequence([]byte("too short"), []byte("too short"))

	var stderr bytes.Buffer
	code := run(t.Context(), []string{"user", "password", "--email", "owner@example.com"}, io.Discard, &stderr, dependencies)

	if code != 1 || !strings.Contains(stderr.String(), "password must be at least 12 Unicode code points") {
		t.Fatalf("run() = %d, stderr = %q", code, stderr.String())
	}
	assertNoSecret(t, stderr.String(), "too short")
}

// TestUserPasswordMapsEveryFailureToACuratedMessage covers all three arms of
// reportSetPasswordFailure. The default arm is the one that matters: a
// repository or hashing fault used to print its wrapped internal text
// verbatim, which is not something an operator at a terminal can act on.
func TestUserPasswordMapsEveryFailureToACuratedMessage(t *testing.T) {
	for name, test := range map[string]struct {
		err  error
		want string
	}{
		"too short":    {auth.ErrPasswordTooShort, "faultline: password must be at least 12 Unicode code points\n"},
		"invalid utf8": {auth.ErrPasswordInvalidUTF8, "faultline: password must be valid UTF-8\n"},
		"server fault": {fmt.Errorf("set password: %w", errors.New("connection reset")), "faultline: could not update password\n"},
	} {
		t.Run(name, func(t *testing.T) {
			repository := &fakeUserRepository{usersByEmail: map[string]auth.User{
				"owner@example.com": {ID: 9, Email: "owner@example.com"},
			}}
			admin := &fakeUserAdmin{setPasswordErr: test.err}
			dependencies := adminCommandDependencies(&fakePool{}, repository, admin)
			dependencies.readPassword = passwordSequence([]byte("a new correct horse"), []byte("a new correct horse"))

			var stderr bytes.Buffer
			code := run(t.Context(), []string{"user", "password", "--email", "owner@example.com"}, io.Discard, &stderr, dependencies)

			if code != 1 {
				t.Fatalf("run() = %d, want 1", code)
			}
			// The prompts land on stderr too, so match the trailing line.
			if got := stderr.String(); !strings.HasSuffix(got, test.want) {
				t.Fatalf("stderr = %q, want it to end with %q", got, test.want)
			}
			if strings.Contains(stderr.String(), "connection reset") {
				t.Fatal("stderr leaked the wrapped internal error text")
			}
		})
	}
}

func TestPrintUsageListsAllUserSubcommands(t *testing.T) {
	var buffer bytes.Buffer
	printUsage(&buffer)
	for _, want := range []string{
		"faultline user create --email <email> --name <name>",
		"faultline user list",
		"faultline user deactivate --email <email>",
		"faultline user password --email <email>",
	} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("usage = %q, want to contain %q", buffer.String(), want)
		}
	}
}

// --- fakes and helpers ---

var pastTimestamp = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

type fakeUserRepository struct {
	usersByEmail map[string]auth.User
	lookupErr    error
	lookupEmail  string
}

func (r *fakeUserRepository) Create(context.Context, auth.NewUser) (auth.User, error) {
	return auth.User{}, errors.New("unexpected Create call")
}

func (r *fakeUserRepository) FindByEmail(_ context.Context, email string) (auth.User, error) {
	r.lookupEmail = email
	if r.lookupErr != nil {
		return auth.User{}, r.lookupErr
	}
	user, ok := r.usersByEmail[email]
	if !ok {
		return auth.User{}, auth.ErrNotFound
	}
	return user, nil
}

func (r *fakeUserRepository) FindByID(context.Context, int64) (auth.User, error) {
	return auth.User{}, errors.New("unexpected FindByID call")
}

func (r *fakeUserRepository) List(context.Context) ([]auth.User, error) { return nil, nil }

func (r *fakeUserRepository) ListActive(context.Context) ([]auth.User, error) { return nil, nil }

func (r *fakeUserRepository) ListAssignableForProject(context.Context, auth.ProjectScope, string) ([]auth.User, error) {
	return nil, nil
}

func (r *fakeUserRepository) Update(context.Context, int64, auth.UserChanges) (auth.User, error) {
	return auth.User{}, errors.New("unexpected Update call")
}

func (r *fakeUserRepository) UpdatePasswordHash(context.Context, int64, string) error {
	return errors.New("unexpected UpdatePasswordHash call")
}

type fakeUserAdmin struct {
	listUsers []auth.User
	listErr   error

	setDeactivatedActorID int64
	setDeactivatedID      int64
	setDeactivatedValue   bool
	setDeactivatedCalls   int
	setDeactivatedResult  auth.User
	setDeactivatedErr     error

	setPasswordID    int64
	setPasswordValue string
	setPasswordCalls int
	setPasswordErr   error
}

func (a *fakeUserAdmin) List(context.Context) ([]auth.User, error) {
	return a.listUsers, a.listErr
}

func (a *fakeUserAdmin) SetDeactivated(_ context.Context, actorID, id int64, deactivated bool) (auth.User, error) {
	a.setDeactivatedCalls++
	a.setDeactivatedActorID, a.setDeactivatedID, a.setDeactivatedValue = actorID, id, deactivated
	if a.setDeactivatedErr != nil {
		return auth.User{}, a.setDeactivatedErr
	}
	return a.setDeactivatedResult, nil
}

func (a *fakeUserAdmin) SetPassword(_ context.Context, id int64, password string) error {
	a.setPasswordCalls++
	a.setPasswordID, a.setPasswordValue = id, password
	return a.setPasswordErr
}

func adminCommandDependencies(pool *fakePool, repository *fakeUserRepository, admin *fakeUserAdmin) commandDependencies {
	return commandDependencies{
		loadConfiguration: func() (config.Config, error) { return validConfig(), nil },
		openDatabase: func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return &runtimeDatabase{Pool: pool, Users: repository, Sessions: authenticatedSessions{}}, nil
		},
		serveHTTP:    func(context.Context, string, http.Handler) error { return errors.New("unexpected server") },
		runMigration: func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
		newUserAdmin: func(auth.UserRepository, auth.SessionRepository) userAdmin { return admin },
		readPassword: func(int) ([]byte, error) { return nil, errors.New("unexpected password prompt") },
		passwordFD:   0,
	}
}

func collapseColumns(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
