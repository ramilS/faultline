package command

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/group"
	"github.com/ramilS/faultline/internal/project"
)

func TestGroupListPrintsTabAlignedTable(t *testing.T) {
	repository := &fakeGroupRepository{groups: []group.Group{
		{ID: 1, Name: "backend", MemberCount: 2, ProjectCount: 1},
		{ID: 2, Name: "frontend", MemberCount: 0, ProjectCount: 0},
	}}
	database := &runtimeDatabase{Groups: repository}

	var stdout, stderr bytes.Buffer
	code := runGroupList(t.Context(), &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupList() = %d, want 0; stderr = %q", code, stderr.String())
	}
	want := "NAME MEMBERS PROJECTS backend 2 1 frontend 0 0"
	if got := collapseColumns(stdout.String()); got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
}

func TestGroupListReportsFailure(t *testing.T) {
	repository := &fakeGroupRepository{listErr: errors.New("connection reset by peer")}
	database := &runtimeDatabase{Groups: repository}

	var stdout, stderr bytes.Buffer
	code := runGroupList(t.Context(), &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupList() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: could not list groups\n" {
		t.Fatalf("stderr = %q", got)
	}
}

func TestGroupCreateSucceeds(t *testing.T) {
	repository := &fakeGroupRepository{createResult: group.Group{ID: 9, Name: "backend"}}
	database := &runtimeDatabase{Groups: repository}

	var stdout, stderr bytes.Buffer
	code := runGroupCreate(t.Context(), groupOptions{Name: "backend"}, &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupCreate() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got := stdout.String(); got != "created group backend (ID 9)\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestGroupCreateRejectsDuplicateName(t *testing.T) {
	repository := &fakeGroupRepository{createErr: fmt.Errorf("create group: %w", group.ErrConflict)}
	database := &runtimeDatabase{Groups: repository}

	var stdout, stderr bytes.Buffer
	code := runGroupCreate(t.Context(), groupOptions{Name: "backend"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupCreate() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: a group with that name already exists\n" {
		t.Fatalf("stderr = %q", got)
	}
}

// TestGroupCreateReportsUnrecognizedFailure drives reportGroupFailure's
// default arm: a repository error that is none of group's sentinel errors
// must still produce a curated "faultline: could not ..." diagnostic and a
// non-zero exit, not a raw error dump or a false success.
func TestGroupCreateReportsUnrecognizedFailure(t *testing.T) {
	repository := &fakeGroupRepository{createErr: errors.New("connection reset by peer")}
	database := &runtimeDatabase{Groups: repository}

	var stdout, stderr bytes.Buffer
	code := runGroupCreate(t.Context(), groupOptions{Name: "backend"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupCreate() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: could not create group\n" {
		t.Fatalf("stderr = %q", got)
	}
}

// TestGroupDeleteRejectsUnknownNameWithoutCallingDelete pins that deleting a
// group that does not exist fails rather than reporting success on a no-op —
// the same class of defect the HTTP group endpoints guard against. Asserting deleteCalls == 0 alongside the exit code pins
// that the CLI never reaches the repository's Delete for a name with no
// match.
func TestGroupDeleteRejectsUnknownNameWithoutCallingDelete(t *testing.T) {
	repository := &fakeGroupRepository{groups: []group.Group{{ID: 1, Name: "backend"}}}
	database := &runtimeDatabase{Groups: repository}

	var stdout, stderr bytes.Buffer
	code := runGroupDelete(t.Context(), groupOptions{Name: "missing"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupDelete() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: no group with that name\n" {
		t.Fatalf("stderr = %q", got)
	}
	if repository.deleteCalls != 0 {
		t.Fatalf("Delete calls = %d, want 0", repository.deleteCalls)
	}
}

func TestGroupDeleteSucceeds(t *testing.T) {
	repository := &fakeGroupRepository{groups: []group.Group{{ID: 1, Name: "backend"}}}
	database := &runtimeDatabase{Groups: repository}

	var stdout, stderr bytes.Buffer
	code := runGroupDelete(t.Context(), groupOptions{Name: "backend"}, &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupDelete() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if repository.deleteID != 1 {
		t.Fatalf("Delete id = %d, want 1", repository.deleteID)
	}
	if got := stdout.String(); got != "deleted group backend\n" {
		t.Fatalf("stdout = %q", got)
	}
}

// TestGroupMembersAddGoesThroughReplaceMembers pins correctness point 1: the
// CLI must read the current member set and call ReplaceMembers with the
// single change applied, rather than a direct insert. group.Repository has
// no other write method for membership, so recording exactly one
// ReplaceMembers call with the right resulting set is the whole guarantee.
func TestGroupMembersAddGoesThroughReplaceMembers(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}, UserIDs: []int64{5}}},
	}
	users := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"new@example.com": {ID: 7, Email: "new@example.com"},
	}}
	database := &runtimeDatabase{Groups: repository, Users: users}

	var stdout, stderr bytes.Buffer
	code := runGroupMembers(t.Context(), groupOptions{Name: "backend", Add: "new@example.com"}, &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupMembers() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if repository.replaceMembersCalls != 1 {
		t.Fatalf("ReplaceMembers calls = %d, want 1", repository.replaceMembersCalls)
	}
	if got, want := repository.replaceMembersIDs, []int64{5, 7}; !equalIDs(got, want) {
		t.Fatalf("ReplaceMembers ids = %v, want %v", got, want)
	}
	if got := stdout.String(); got != "added new@example.com to group backend\n" {
		t.Fatalf("stdout = %q", got)
	}
}

// TestGroupMembersAddOnExistingMemberReportsNoOp pins that re-adding a user who is already a member must not claim "added"
// (no membership row changes), but it must still exit zero and go through
// ReplaceMembers like any other --add, since group.Service already dedupes
// and the CLI must not add a distinction the domain layer does not make.
func TestGroupMembersAddOnExistingMemberReportsNoOp(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}, UserIDs: []int64{5, 7}}},
	}
	users := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"member@example.com": {ID: 7, Email: "member@example.com"},
	}}
	database := &runtimeDatabase{Groups: repository, Users: users}

	var stdout, stderr bytes.Buffer
	code := runGroupMembers(t.Context(), groupOptions{Name: "backend", Add: "member@example.com"}, &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupMembers() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if repository.replaceMembersCalls != 1 {
		t.Fatalf("ReplaceMembers calls = %d, want 1", repository.replaceMembersCalls)
	}
	if got, want := repository.replaceMembersIDs, []int64{5, 7}; !equalIDs(got, want) {
		t.Fatalf("ReplaceMembers ids = %v, want %v", got, want)
	}
	if got := stdout.String(); got != "member@example.com is already a member of group backend\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestGroupMembersAddRejectsUnknownEmailWithoutWriting(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}}},
	}
	users := &fakeUserRepository{}
	database := &runtimeDatabase{Groups: repository, Users: users}

	var stdout, stderr bytes.Buffer
	code := runGroupMembers(t.Context(), groupOptions{Name: "backend", Add: "ghost@example.com"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupMembers() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: no user with that email\n" {
		t.Fatalf("stderr = %q", got)
	}
	if repository.replaceMembersCalls != 0 {
		t.Fatalf("ReplaceMembers calls = %d, want 0", repository.replaceMembersCalls)
	}
}

// TestGroupMembersRemoveRejectsNonMemberWithoutWriting is correctness point
// 4's other half: removing a user who is not a member must fail loudly, not
// report a cheerful no-op success.
func TestGroupMembersRemoveRejectsNonMemberWithoutWriting(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}, UserIDs: []int64{5}}},
	}
	users := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"outsider@example.com": {ID: 42, Email: "outsider@example.com"},
	}}
	database := &runtimeDatabase{Groups: repository, Users: users}

	var stdout, stderr bytes.Buffer
	code := runGroupMembers(t.Context(), groupOptions{Name: "backend", Remove: "outsider@example.com"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupMembers() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: user is not a member of that group\n" {
		t.Fatalf("stderr = %q", got)
	}
	if repository.replaceMembersCalls != 0 {
		t.Fatalf("ReplaceMembers calls = %d, want 0", repository.replaceMembersCalls)
	}
}

func TestGroupMembersRemoveSucceeds(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}, UserIDs: []int64{5, 7}}},
	}
	users := &fakeUserRepository{usersByEmail: map[string]auth.User{
		"member@example.com": {ID: 7, Email: "member@example.com"},
	}}
	database := &runtimeDatabase{Groups: repository, Users: users}

	var stdout, stderr bytes.Buffer
	code := runGroupMembers(t.Context(), groupOptions{Name: "backend", Remove: "member@example.com"}, &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupMembers() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if got, want := repository.replaceMembersIDs, []int64{5}; !equalIDs(got, want) {
		t.Fatalf("ReplaceMembers ids = %v, want %v", got, want)
	}
	if got := stdout.String(); got != "removed member@example.com from group backend\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestGroupMembersRejectsUnknownGroupName(t *testing.T) {
	repository := &fakeGroupRepository{}
	database := &runtimeDatabase{Groups: repository, Users: &fakeUserRepository{}}

	var stdout, stderr bytes.Buffer
	code := runGroupMembers(t.Context(), groupOptions{Name: "missing", Add: "a@example.com"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupMembers() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: no group with that name\n" {
		t.Fatalf("stderr = %q", got)
	}
}

// TestGroupProjectsAddGoesThroughReplaceProjects mirrors
// TestGroupMembersAddGoesThroughReplaceMembers for project grants.
func TestGroupProjectsAddGoesThroughReplaceProjects(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}, ProjectIDs: []int64{3}}},
	}
	projects := &fakeProjectRepository{projectsBySlug: map[string]project.Project{
		"web": {ID: 4, Slug: "web"},
	}}
	database := &runtimeDatabase{Groups: repository, Projects: projects}

	var stdout, stderr bytes.Buffer
	code := runGroupProjects(t.Context(), groupOptions{Name: "backend", Add: "web"}, &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupProjects() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if repository.replaceProjectsCalls != 1 {
		t.Fatalf("ReplaceProjects calls = %d, want 1", repository.replaceProjectsCalls)
	}
	if got, want := repository.replaceProjectsIDs, []int64{3, 4}; !equalIDs(got, want) {
		t.Fatalf("ReplaceProjects ids = %v, want %v", got, want)
	}
	if got := stdout.String(); got != "added project web to group backend\n" {
		t.Fatalf("stdout = %q", got)
	}
}

// TestGroupProjectsAddOnExistingProjectReportsNoOp mirrors
// TestGroupMembersAddOnExistingMemberReportsNoOp for project grants.
func TestGroupProjectsAddOnExistingProjectReportsNoOp(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}, ProjectIDs: []int64{3, 4}}},
	}
	projects := &fakeProjectRepository{projectsBySlug: map[string]project.Project{
		"web": {ID: 4, Slug: "web"},
	}}
	database := &runtimeDatabase{Groups: repository, Projects: projects}

	var stdout, stderr bytes.Buffer
	code := runGroupProjects(t.Context(), groupOptions{Name: "backend", Add: "web"}, &stdout, &stderr, database)

	if code != 0 {
		t.Fatalf("runGroupProjects() = %d, want 0; stderr = %q", code, stderr.String())
	}
	if repository.replaceProjectsCalls != 1 {
		t.Fatalf("ReplaceProjects calls = %d, want 1", repository.replaceProjectsCalls)
	}
	if got, want := repository.replaceProjectsIDs, []int64{3, 4}; !equalIDs(got, want) {
		t.Fatalf("ReplaceProjects ids = %v, want %v", got, want)
	}
	if got := stdout.String(); got != "project web is already granted to group backend\n" {
		t.Fatalf("stdout = %q", got)
	}
}

func TestGroupProjectsAddRejectsUnknownSlugWithoutWriting(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}}},
	}
	projects := &fakeProjectRepository{}
	database := &runtimeDatabase{Groups: repository, Projects: projects}

	var stdout, stderr bytes.Buffer
	code := runGroupProjects(t.Context(), groupOptions{Name: "backend", Add: "ghost"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupProjects() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: no project with that slug\n" {
		t.Fatalf("stderr = %q", got)
	}
	if repository.replaceProjectsCalls != 0 {
		t.Fatalf("ReplaceProjects calls = %d, want 0", repository.replaceProjectsCalls)
	}
}

func TestGroupProjectsRemoveRejectsProjectNotInGroup(t *testing.T) {
	repository := &fakeGroupRepository{
		groups:  []group.Group{{ID: 1, Name: "backend"}},
		details: map[int64]group.Detail{1: {Group: group.Group{ID: 1, Name: "backend"}, ProjectIDs: []int64{3}}},
	}
	projects := &fakeProjectRepository{projectsBySlug: map[string]project.Project{
		"mobile": {ID: 9, Slug: "mobile"},
	}}
	database := &runtimeDatabase{Groups: repository, Projects: projects}

	var stdout, stderr bytes.Buffer
	code := runGroupProjects(t.Context(), groupOptions{Name: "backend", Remove: "mobile"}, &stdout, &stderr, database)

	if code != 1 {
		t.Fatalf("runGroupProjects() = %d, want 1", code)
	}
	if got := stderr.String(); got != "faultline: project is not in that group\n" {
		t.Fatalf("stderr = %q", got)
	}
	if repository.replaceProjectsCalls != 0 {
		t.Fatalf("ReplaceProjects calls = %d, want 0", repository.replaceProjectsCalls)
	}
}

func TestPrintUsageListsAllGroupSubcommands(t *testing.T) {
	var buffer bytes.Buffer
	printUsage(&buffer)
	for _, want := range []string{
		"faultline group create --name <name>",
		"faultline group list",
		"faultline group delete --name <name>",
		"faultline group members --name <name> (--add|--remove) <email>",
		"faultline group projects --name <name> (--add|--remove) <project slug>",
	} {
		if !strings.Contains(buffer.String(), want) {
			t.Fatalf("usage = %q, want to contain %q", buffer.String(), want)
		}
	}
}

// --- fakes and helpers ---

func equalIDs(got, want []int64) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

type fakeGroupRepository struct {
	groups  []group.Group
	listErr error

	details map[int64]group.Detail
	findErr error

	createResult group.Group
	createErr    error

	deleteID    int64
	deleteCalls int
	deleteErr   error

	replaceMembersIDs   []int64
	replaceMembersCalls int
	replaceMembersErr   error

	replaceProjectsIDs   []int64
	replaceProjectsCalls int
	replaceProjectsErr   error
}

func (r *fakeGroupRepository) List(context.Context) ([]group.Group, error) {
	return r.groups, r.listErr
}

func (r *fakeGroupRepository) Find(_ context.Context, id int64) (group.Detail, error) {
	if r.findErr != nil {
		return group.Detail{}, r.findErr
	}
	detail, ok := r.details[id]
	if !ok {
		return group.Detail{}, group.ErrNotFound
	}
	return detail, nil
}

func (r *fakeGroupRepository) Create(context.Context, group.NewGroup) (group.Group, error) {
	return r.createResult, r.createErr
}

func (r *fakeGroupRepository) Rename(context.Context, int64, string) (group.Group, error) {
	return group.Group{}, errors.New("unexpected Rename call")
}

func (r *fakeGroupRepository) Delete(_ context.Context, id int64) error {
	r.deleteCalls++
	r.deleteID = id
	return r.deleteErr
}

func (r *fakeGroupRepository) ReplaceMembers(_ context.Context, _ int64, userIDs []int64) error {
	r.replaceMembersCalls++
	r.replaceMembersIDs = userIDs
	return r.replaceMembersErr
}

func (r *fakeGroupRepository) ReplaceProjects(_ context.Context, _ int64, projectIDs []int64) error {
	r.replaceProjectsCalls++
	r.replaceProjectsIDs = projectIDs
	return r.replaceProjectsErr
}

type fakeProjectRepository struct {
	projectsBySlug map[string]project.Project
	findErr        error
}

func (r *fakeProjectRepository) Create(context.Context, project.CreateProject) (project.Project, error) {
	return project.Project{}, errors.New("unexpected Create call")
}

func (r *fakeProjectRepository) List(context.Context, access.Scope) ([]project.Project, error) {
	return nil, errors.New("unexpected List call")
}

func (r *fakeProjectRepository) FindBySlug(_ context.Context, _ access.Scope, slug string) (project.Project, error) {
	if r.findErr != nil {
		return project.Project{}, r.findErr
	}
	found, ok := r.projectsBySlug[slug]
	if !ok {
		return project.Project{}, project.ErrNotFound
	}
	return found, nil
}
