package command

import (
	"flag"
	"io"
	"strings"
)

// groupOptions carries the flags the group subcommands share. Not every
// field is meaningful for every mode: create and delete use only Name;
// members and projects use Name plus one of Add/Remove.
type groupOptions struct {
	Name   string
	Add    string
	Remove string
}

// ParseGroupArguments parses the arguments that follow "group" on the
// command line — e.g. {"create", "--name", ...} for `faultline group create
// ...` — into a dispatch mode ("group-list", "group-create",
// "group-delete", "group-members", or "group-projects") and the flags for
// that mode. It is exported so internal/command's own tests can exercise it
// directly (see group_test.go), rather than only indirectly through Run.
func ParseGroupArguments(args []string) (string, groupOptions, bool) {
	if len(args) == 0 {
		return "", groupOptions{}, false
	}
	switch args[0] {
	case "list":
		return parseGroupListArguments(args[1:])
	case "create":
		return parseGroupNameOnlyArguments(args[1:], "group-create")
	case "delete":
		return parseGroupNameOnlyArguments(args[1:], "group-delete")
	case "members":
		return parseGroupChangeArguments(args[1:], "group-members", "email")
	case "projects":
		return parseGroupChangeArguments(args[1:], "group-projects", "project slug")
	default:
		return "", groupOptions{}, false
	}
}

func parseGroupListArguments(args []string) (string, groupOptions, bool) {
	if len(args) != 0 {
		return "", groupOptions{}, false
	}
	return "group-list", groupOptions{}, true
}

func parseGroupNameOnlyArguments(args []string, mode string) (string, groupOptions, bool) {
	if !validFlagTokens(args, "name") {
		return "", groupOptions{}, false
	}

	flags := flag.NewFlagSet("faultline group "+strings.TrimPrefix(mode, "group-"), flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var name singleStringFlag
	flags.Var(&name, "name", "group name")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", groupOptions{}, false
	}
	trimmedName := strings.TrimSpace(name.value)
	if trimmedName == "" {
		return "", groupOptions{}, false
	}
	return mode, groupOptions{Name: trimmedName}, true
}

// parseGroupChangeArguments parses the shared shape of "members" and
// "projects": a group name plus exactly one of --add / --remove. valueLabel
// only affects the flags' usage text (e.g. "email" vs "project slug").
func parseGroupChangeArguments(args []string, mode, valueLabel string) (string, groupOptions, bool) {
	if !validFlagTokens(args, "name", "add", "remove") {
		return "", groupOptions{}, false
	}

	flags := flag.NewFlagSet("faultline group "+strings.TrimPrefix(mode, "group-"), flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var name, add, remove singleStringFlag
	flags.Var(&name, "name", "group name")
	flags.Var(&add, "add", "add "+valueLabel)
	flags.Var(&remove, "remove", "remove "+valueLabel)
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return "", groupOptions{}, false
	}
	trimmedName := strings.TrimSpace(name.value)
	if trimmedName == "" {
		return "", groupOptions{}, false
	}
	// Exactly one of --add / --remove: a CLI operator states the one change
	// they want, unlike the HTTP PUT this shares a write path with, which
	// replaces the whole set.
	if (add.value == "") == (remove.value == "") {
		return "", groupOptions{}, false
	}
	return mode, groupOptions{Name: trimmedName, Add: add.value, Remove: remove.value}, true
}
