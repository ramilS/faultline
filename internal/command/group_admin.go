package command

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/group"
	"github.com/ramilS/faultline/internal/project"
)

func runGroupList(ctx context.Context, stdout, stderr io.Writer, database *runtimeDatabase) int {
	service := group.NewService(database.Groups)
	groups, err := service.List(ctx)
	if err != nil {
		fmt.Fprintln(stderr, "faultline: could not list groups")
		return 1
	}

	writer := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(writer, "NAME\tMEMBERS\tPROJECTS")
	for _, current := range groups {
		fmt.Fprintf(writer, "%s\t%d\t%d\n", current.Name, current.MemberCount, current.ProjectCount)
	}
	if err := writer.Flush(); err != nil {
		fmt.Fprintln(stderr, "faultline: could not print group list")
		return 1
	}
	return 0
}

func runGroupCreate(ctx context.Context, options groupOptions, stdout, stderr io.Writer, database *runtimeDatabase) int {
	service := group.NewService(database.Groups)
	created, err := service.Create(ctx, options.Name)
	if err != nil {
		return reportGroupFailure(stderr, "create group", err)
	}
	fmt.Fprintf(stdout, "created group %s (ID %d)\n", created.Name, created.ID)
	return 0
}

func runGroupDelete(ctx context.Context, options groupOptions, stdout, stderr io.Writer, database *runtimeDatabase) int {
	service := group.NewService(database.Groups)
	found, err := findGroupByName(ctx, service, options.Name)
	if err != nil {
		return reportGroupFailure(stderr, "find group", err)
	}
	if err := service.Delete(ctx, found.ID); err != nil {
		return reportGroupFailure(stderr, "delete group", err)
	}
	fmt.Fprintf(stdout, "deleted group %s\n", found.Name)
	return 0
}

// runGroupMembers resolves the group by name and the target user by email,
// then reads the group's current member set, applies the single --add or
// --remove change, and calls ReplaceMembers with the result. This is the
// same write path internal/api's PutMembers uses (see
// internal/api/groups.go), so the CLI and HTTP surfaces cannot diverge on
// the transaction or group-existence check that ReplaceMembers owns.
func runGroupMembers(ctx context.Context, options groupOptions, stdout, stderr io.Writer, database *runtimeDatabase) int {
	service := group.NewService(database.Groups)
	found, err := findGroupByName(ctx, service, options.Name)
	if err != nil {
		return reportGroupFailure(stderr, "find group", err)
	}
	detail, err := service.Find(ctx, found.ID)
	if err != nil {
		return reportGroupFailure(stderr, "find group", err)
	}

	email := options.Add
	if email == "" {
		email = options.Remove
	}
	user, err := database.Users.FindByEmail(ctx, auth.NormalizeEmail(email))
	if err != nil {
		return reportUserLookupFailure(stderr, err)
	}

	if options.Add != "" {
		updated := withID(detail.UserIDs, user.ID)
		noOp := len(updated) == len(detail.UserIDs)
		if _, err := service.ReplaceMembers(ctx, found.ID, updated); err != nil {
			return reportGroupFailure(stderr, "replace group members", err)
		}
		if noOp {
			fmt.Fprintf(stdout, "%s is already a member of group %s\n", user.Email, found.Name)
		} else {
			fmt.Fprintf(stdout, "added %s to group %s\n", user.Email, found.Name)
		}
		return 0
	}

	remaining, ok := withoutID(detail.UserIDs, user.ID)
	if !ok {
		fmt.Fprintln(stderr, "faultline: user is not a member of that group")
		return 1
	}
	if _, err := service.ReplaceMembers(ctx, found.ID, remaining); err != nil {
		return reportGroupFailure(stderr, "replace group members", err)
	}
	fmt.Fprintf(stdout, "removed %s from group %s\n", user.Email, found.Name)
	return 0
}

// runGroupProjects mirrors runGroupMembers for a group's project grants,
// resolving the target project by slug and sharing ReplaceProjects as its
// single write path with the HTTP PUT.
func runGroupProjects(ctx context.Context, options groupOptions, stdout, stderr io.Writer, database *runtimeDatabase) int {
	service := group.NewService(database.Groups)
	found, err := findGroupByName(ctx, service, options.Name)
	if err != nil {
		return reportGroupFailure(stderr, "find group", err)
	}
	detail, err := service.Find(ctx, found.ID)
	if err != nil {
		return reportGroupFailure(stderr, "find group", err)
	}

	slug := options.Add
	if slug == "" {
		slug = options.Remove
	}
	target, err := database.Projects.FindBySlug(ctx, access.Unrestricted(), slug)
	if err != nil {
		return reportProjectLookupFailure(stderr, err)
	}

	if options.Add != "" {
		updated := withID(detail.ProjectIDs, target.ID)
		noOp := len(updated) == len(detail.ProjectIDs)
		if _, err := service.ReplaceProjects(ctx, found.ID, updated); err != nil {
			return reportGroupFailure(stderr, "replace group projects", err)
		}
		if noOp {
			fmt.Fprintf(stdout, "project %s is already granted to group %s\n", target.Slug, found.Name)
		} else {
			fmt.Fprintf(stdout, "added project %s to group %s\n", target.Slug, found.Name)
		}
		return 0
	}

	remaining, ok := withoutID(detail.ProjectIDs, target.ID)
	if !ok {
		fmt.Fprintln(stderr, "faultline: project is not in that group")
		return 1
	}
	if _, err := service.ReplaceProjects(ctx, found.ID, remaining); err != nil {
		return reportGroupFailure(stderr, "replace group projects", err)
	}
	fmt.Fprintf(stdout, "removed project %s from group %s\n", target.Slug, found.Name)
	return 0
}

// findGroupByName resolves a group name to its record via List, since
// group.Repository has no lookup-by-name method — Find only takes an id.
// A name with no match reports group.ErrNotFound, the same sentinel Find
// and Delete already use, so reportGroupFailure needs only one case for it.
func findGroupByName(ctx context.Context, service *group.Service, name string) (group.Group, error) {
	groups, err := service.List(ctx)
	if err != nil {
		return group.Group{}, err
	}
	for _, current := range groups {
		if current.Name == name {
			return current, nil
		}
	}
	return group.Group{}, fmt.Errorf("find group %q: %w", name, group.ErrNotFound)
}

// containsID reports whether id is already present in ids.
func containsID(ids []int64, id int64) bool {
	return slices.Contains(ids, id)
}

// withID returns ids with id present, without duplicating an id already
// there — adding an existing member or project is a no-op success, not an
// error.
func withID(ids []int64, id int64) []int64 {
	if containsID(ids, id) {
		return ids
	}
	return append(append([]int64{}, ids...), id)
}

// withoutID returns ids with id removed and true, or ids unchanged and false
// when id was not present — the caller must treat false as a failure rather
// than a silent no-op success.
func withoutID(ids []int64, id int64) ([]int64, bool) {
	for index, current := range ids {
		if current == id {
			remaining := make([]int64, 0, len(ids)-1)
			remaining = append(remaining, ids[:index]...)
			remaining = append(remaining, ids[index+1:]...)
			return remaining, true
		}
	}
	return ids, false
}

// reportGroupFailure maps a group-service error to a curated stderr
// diagnostic. operation names the failing step for the fallback message,
// used only when err is not one of the domain's sentinel errors.
func reportGroupFailure(stderr io.Writer, operation string, err error) int {
	switch {
	case errors.Is(err, group.ErrNotFound):
		fmt.Fprintln(stderr, "faultline: no group with that name")
	case errors.Is(err, group.ErrConflict):
		fmt.Fprintln(stderr, "faultline: a group with that name already exists")
	case errors.Is(err, group.ErrInvalidName):
		fmt.Fprintln(stderr, "faultline: invalid group name")
	case errors.Is(err, group.ErrUnknownReference):
		fmt.Fprintln(stderr, "faultline: unknown user or project reference")
	default:
		fmt.Fprintf(stderr, "faultline: could not %s\n", operation)
	}
	return 1
}

func reportProjectLookupFailure(stderr io.Writer, err error) int {
	if errors.Is(err, project.ErrNotFound) {
		fmt.Fprintln(stderr, "faultline: no project with that slug")
		return 1
	}
	fmt.Fprintln(stderr, "faultline: could not look up project")
	return 1
}
