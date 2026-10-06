package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/ramilS/faultline/internal/auth"
)

// cliActorID is passed as the actor for administrative writes issued from
// the CLI, which has no signed-in session and therefore no acting user.
// users.id is a bigserial starting at 1, so 0 can never match a real
// account: the self-deactivation guard in
// auth.Administration.SetDeactivated (actorID == id) can never fire for a
// CLI-issued deactivation. That is intentional, not an oversight — the
// guard exists to stop an owner locking themselves out through a single UI
// click, not to stop a host operator with database access from recovering a
// broken deployment by deactivating any account, including one that
// happens to be their own dashboard login. The last-active-owner guard is
// unaffected by actorID: auth.UserRepository.Update enforces it atomically
// regardless of who is asking, so it still applies here.
const cliActorID int64 = 0

func runUserList(ctx context.Context, stdout, stderr io.Writer, database *runtimeDatabase, dependencies commandDependencies) int {
	admin := dependencies.newUserAdmin(database.Users, database.Sessions)
	users, err := admin.List(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "faultline: could not list users")
		return 1
	}

	writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "EMAIL\tNAME\tROLE\tSTATUS")
	for _, user := range users {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\n", user.Email, user.Name, user.Role, userStatusLabel(user))
	}
	if err := writer.Flush(); err != nil {
		fmt.Fprintln(stderr, "faultline: could not print user list")
		return 1
	}
	return 0
}

func userStatusLabel(user auth.User) string {
	if user.IsActive() {
		return "active"
	}
	return "deactivated"
}

func runUserDeactivate(ctx context.Context, options userOptions, stdout, stderr io.Writer, database *runtimeDatabase, dependencies commandDependencies) int {
	user, err := database.Users.FindByEmail(ctx, auth.NormalizeEmail(options.Email))
	if err != nil {
		return reportUserLookupFailure(stderr, err)
	}

	admin := dependencies.newUserAdmin(database.Users, database.Sessions)
	updated, err := admin.SetDeactivated(ctx, cliActorID, user.ID, true)
	if err != nil {
		if errors.Is(err, auth.ErrLastOwner) {
			fmt.Fprintln(stderr, "faultline: refusing to deactivate the last owner — promote another user to owner first")
			return 1
		}
		fmt.Fprintln(stderr, "faultline: could not deactivate user")
		return 1
	}

	fmt.Fprintf(stdout, "deactivated user %s (ID %d)\n", updated.Email, updated.ID)
	return 0
}

func runUserPassword(ctx context.Context, options userOptions, stdout, stderr io.Writer, database *runtimeDatabase, dependencies commandDependencies) int {
	user, err := database.Users.FindByEmail(ctx, auth.NormalizeEmail(options.Email))
	if err != nil {
		return reportUserLookupFailure(stderr, err)
	}

	password, err := promptPassword(stderr, "New password: ", dependencies)
	if err != nil {
		fmt.Fprintln(stderr, "faultline: could not read password")
		return 1
	}
	defer clearBytes(password)

	confirmation, err := promptPassword(stderr, "Confirm new password: ", dependencies)
	if err != nil {
		fmt.Fprintln(stderr, "faultline: could not read password confirmation")
		return 1
	}
	defer clearBytes(confirmation)

	if !bytes.Equal(password, confirmation) {
		fmt.Fprintln(stderr, "faultline: passwords do not match")
		return 1
	}

	admin := dependencies.newUserAdmin(database.Users, database.Sessions)
	if err := admin.SetPassword(ctx, user.ID, string(password)); err != nil {
		return reportSetPasswordFailure(stderr, err)
	}

	fmt.Fprintf(stdout, "updated password for %s\n", user.Email)
	return 0
}

// reportSetPasswordFailure prints what the operator can act on, matching the
// curated messages every other path in this file prints. The two password
// policy failures are the only ones a retry can fix, so they are named; the
// rest are server-side faults whose wrapped internal text ("set password:
// hash password: …") tells an operator at a terminal nothing they can use
// and would change under them the next time the internals are reworded.
func reportSetPasswordFailure(stderr io.Writer, err error) int {
	switch {
	case errors.Is(err, auth.ErrPasswordTooShort):
		fmt.Fprintln(stderr, "faultline: password must be at least 12 Unicode code points")
	case errors.Is(err, auth.ErrPasswordInvalidUTF8):
		fmt.Fprintln(stderr, "faultline: password must be valid UTF-8")
	default:
		fmt.Fprintln(stderr, "faultline: could not update password")
	}
	return 1
}

func reportUserLookupFailure(stderr io.Writer, err error) int {
	if errors.Is(err, auth.ErrNotFound) {
		fmt.Fprintln(stderr, "faultline: no user with that email")
		return 1
	}
	fmt.Fprintln(stderr, "faultline: could not look up user")
	return 1
}
