package command

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/ramilS/faultline/internal/auth"
)

// userOptions carries the flags common to the user subcommands. Not every
// field is meaningful for every mode: create uses Email and Name;
// deactivate and password use only Email.
type userOptions struct {
	Email string
	Name  string
}

type singleStringFlag struct {
	value string
	set   bool
}

func (value *singleStringFlag) String() string { return value.value }

func (value *singleStringFlag) Set(input string) error {
	if value.set {
		return errors.New("flag may only be provided once")
	}
	value.value = input
	value.set = true
	return nil
}

// ParseUserArguments parses the arguments that follow "user" on the command
// line — e.g. {"create", "--email", ...} for `faultline user create ...` —
// into a dispatch mode ("user-create", "user-list", "user-deactivate", or
// "user-password") and the flags for that mode. It is exported so
// internal/command's own tests can exercise it directly (see
// user_admin_test.go), rather than only indirectly through Run.
func ParseUserArguments(args []string) (string, userOptions, bool) {
	if len(args) == 0 {
		return "", userOptions{}, false
	}
	switch args[0] {
	case "create":
		return parseUserCreateArguments(args[1:])
	case "list":
		return parseUserListArguments(args[1:])
	case "deactivate":
		return parseUserEmailOnlyArguments(args[1:], "user-deactivate")
	case "password":
		return parseUserEmailOnlyArguments(args[1:], "user-password")
	default:
		return "", userOptions{}, false
	}
}

func parseUserCreateArguments(args []string) (string, userOptions, bool) {
	if !validFlagTokens(args, "email", "name") {
		return "", userOptions{}, false
	}

	flags := flag.NewFlagSet("faultline user create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var email, name singleStringFlag
	flags.Var(&email, "email", "administrator email")
	flags.Var(&name, "name", "administrator name")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", userOptions{}, false
	}
	options := userOptions{Email: email.value, Name: name.value}
	if strings.TrimSpace(options.Email) == "" || strings.TrimSpace(options.Name) == "" {
		return "", userOptions{}, false
	}
	options.Name = strings.TrimSpace(options.Name)
	return "user-create", options, true
}

func parseUserListArguments(args []string) (string, userOptions, bool) {
	if len(args) != 0 {
		return "", userOptions{}, false
	}
	return "user-list", userOptions{}, true
}

func parseUserEmailOnlyArguments(args []string, mode string) (string, userOptions, bool) {
	if !validFlagTokens(args, "email") {
		return "", userOptions{}, false
	}

	flags := flag.NewFlagSet("faultline user "+strings.TrimPrefix(mode, "user-"), flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var email singleStringFlag
	flags.Var(&email, "email", "user email")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", userOptions{}, false
	}
	if strings.TrimSpace(email.value) == "" {
		return "", userOptions{}, false
	}
	return mode, userOptions{Email: email.value}, true
}

// validFlagTokens reports whether args is entirely made up of the given
// flags in "--name value" or "--name=value" form. A flag's value is
// rejected if it looks like another flag (starts with "-") — this, not the
// flag.FlagSet parse below, is what stops "--email --force" from silently
// treating --force as the email: flag.FlagSet alone would consume it.
func validFlagTokens(args []string, names ...string) bool {
	for index := 0; index < len(args); {
		token := args[index]
		matched := false
		for _, name := range names {
			switch {
			case token == "--"+name:
				if index+1 >= len(args) || strings.HasPrefix(args[index+1], "-") {
					return false
				}
				index += 2
				matched = true
			case strings.HasPrefix(token, "--"+name+"="):
				index++
				matched = true
			}
			if matched {
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

func runUserCreate(
	ctx context.Context,
	options userOptions,
	stdout io.Writer,
	stderr io.Writer,
	database *runtimeDatabase,
	dependencies commandDependencies,
) int {
	password, err := promptPassword(stderr, "Password: ", dependencies)
	if err != nil {
		fmt.Fprintln(stderr, "faultline: could not read password")
		return 1
	}
	defer clearBytes(password)

	confirmation, err := promptPassword(stderr, "Confirm password: ", dependencies)
	if err != nil {
		fmt.Fprintln(stderr, "faultline: could not read password confirmation")
		return 1
	}
	defer clearBytes(confirmation)

	if !bytes.Equal(password, confirmation) {
		fmt.Fprintln(stderr, "faultline: passwords do not match")
		return 1
	}
	if err := auth.ValidatePassword(string(password)); err != nil {
		fmt.Fprintf(stderr, "faultline: %v\n", err)
		return 1
	}

	creator := dependencies.newUserCreator(database.Users, database.Sessions)
	// CLI user creation is the only path that produces an owner: the
	// migration's bootstrap promotion only fires when the users table is
	// already non-empty, which a fresh install never is.
	user, err := creator.CreateUser(ctx, options.Email, options.Name, string(password), auth.RoleOwner)
	if err != nil {
		if errors.Is(err, auth.ErrConflict) {
			fmt.Fprintln(stderr, "faultline: a user with that email already exists")
		} else {
			fmt.Fprintln(stderr, "faultline: could not create user")
		}
		return 1
	}

	fmt.Fprintf(stdout, "created user %s (ID %d)\n", user.Email, user.ID)
	return 0
}

func promptPassword(output io.Writer, prompt string, dependencies commandDependencies) ([]byte, error) {
	fmt.Fprint(output, prompt)
	password, err := dependencies.readPassword(dependencies.passwordFD)
	fmt.Fprintln(output)
	return password, err
}

func clearBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
