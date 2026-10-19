package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/artifactcatalog"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/group"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	nativedebug "github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/sourcemap"
)

// Shared stubs and helpers for the app package's tests. Test files in this
// package are split by seam (routing, CORS/ingestion, auth boundary, the
// path-traversal guard); this file holds what more than one of them needs.

type healthChecker struct {
	err   error
	calls int
}

func (h *healthChecker) Ping(context.Context) error {
	h.calls++
	return h.err
}

type authServiceStub struct {
	authUser auth.User
	authErr  error
}

func (*authServiceStub) Login(context.Context, string, string) (auth.User, string, error) {
	return auth.User{}, "", auth.ErrInvalidCredentials
}
func (s *authServiceStub) Authenticate(context.Context, string) (auth.Session, auth.User, error) {
	return auth.Session{}, s.authUser, s.authErr
}
func (*authServiceStub) Logout(context.Context, string) error { return nil }

// unrestrictedScopeResolverStub preserves today's behavior for every app test
// that does not itself exercise project scoping: nothing is scoped yet, so
// resolving to Unrestricted() keeps these tests' semantics unchanged.
type unrestrictedScopeResolverStub struct{}

func (unrestrictedScopeResolverStub) ScopeFor(context.Context, auth.User) (access.Scope, error) {
	return access.Unrestricted(), nil
}

type appUserAdminServiceStub struct {
	list       []auth.User
	assignable []auth.User
	created    auth.User
}

func (s *appUserAdminServiceStub) List(context.Context) ([]auth.User, error) { return s.list, nil }
func (s *appUserAdminServiceStub) ListAssignable(context.Context, auth.ProjectScope, string) ([]auth.User, error) {
	return s.assignable, nil
}
func (s *appUserAdminServiceStub) Create(context.Context, string, string, string, auth.Role) (auth.User, error) {
	return s.created, nil
}
func (*appUserAdminServiceStub) Rename(context.Context, int64, string) (auth.User, error) {
	return auth.User{}, nil
}
func (*appUserAdminServiceStub) SetRole(context.Context, int64, auth.Role) (auth.User, error) {
	return auth.User{}, nil
}
func (*appUserAdminServiceStub) SetDeactivated(context.Context, int64, int64, bool) (auth.User, error) {
	return auth.User{}, nil
}
func (*appUserAdminServiceStub) SetPassword(context.Context, int64, string) error { return nil }
func (*appUserAdminServiceStub) ChangeOwnPassword(context.Context, int64, string, string, [32]byte) error {
	return nil
}

type appProjectServiceStub struct {
	api.ProjectService
	found project.Project
}

func (s *appProjectServiceStub) FindProject(context.Context, access.Scope, string) (project.Project, error) {
	return s.found, nil
}

type appArtifactServiceStub struct {
	authenticated artifactauth.Token
}

type appArtifactCatalogStub struct{}

func (*appArtifactCatalogStub) List(context.Context, int64, artifactcatalog.ListOptions) (artifactcatalog.Page, error) {
	return artifactcatalog.Page{Artifacts: []artifactcatalog.Artifact{}}, nil
}

func (*appArtifactServiceStub) Create(context.Context, int64, string) (artifactauth.Token, string, error) {
	return artifactauth.Token{}, "", nil
}
func (*appArtifactServiceStub) List(context.Context, string) ([]artifactauth.Token, error) {
	return nil, nil
}
func (*appArtifactServiceStub) Revoke(context.Context, string, int64) error { return nil }
func (s *appArtifactServiceStub) Authenticate(context.Context, string) (artifactauth.Token, error) {
	return s.authenticated, nil
}

type appSourceMapRepositoryStub struct {
	inserted bool
}

func (s *appSourceMapRepositoryStub) Put(context.Context, int64, string, string, sourcemap.DecodedUpload) (sourcemap.Map, bool, error) {
	return sourcemap.Map{}, s.inserted, nil
}
func (*appSourceMapRepositoryStub) Find(context.Context, int64, string, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}

func (*appSourceMapRepositoryStub) FindByDebugID(context.Context, int64, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}

func (*appSourceMapRepositoryStub) PutBundle(context.Context, int64, string, []sourcemap.BundleEntry) error {
	return errors.New("unused")
}

// stubArtifactService, stubProjectResolver, stubChunkStore, stubReleaseStore
// and stubSourceMapRepository back
// TestSentryCLIRoutesOutrankTheAPIFallback. Every one of that test's requests
// carries no Authorization header, so SentryCLIHandler's bearerToken check
// rejects them before any of these methods runs; each therefore returns an
// error (or zero value) that would make the test fail loudly if that
// assumption were ever wrong, rather than one that could coincidentally
// satisfy it.
type stubArtifactService struct{}

func (stubArtifactService) Create(context.Context, int64, string) (artifactauth.Token, string, error) {
	return artifactauth.Token{}, "", errors.New("unused")
}
func (stubArtifactService) List(context.Context, string) ([]artifactauth.Token, error) {
	return nil, errors.New("unused")
}
func (stubArtifactService) Revoke(context.Context, string, int64) error { return errors.New("unused") }
func (stubArtifactService) Authenticate(context.Context, string) (artifactauth.Token, error) {
	return artifactauth.Token{}, artifactauth.ErrUnauthorized
}

// stubProjectResolver embeds api.ProjectService so it satisfies
// Dependencies.ProjectService (the full project CRUD interface) while
// overriding only FindProject, the one method sentryCLIRoutes calls, the
// same embedding pattern appProjectServiceStub above uses. Every other
// method panics on a nil embedded interface if ever called, which this
// test's requests never reach.
type stubProjectResolver struct {
	api.ProjectService
}

func (stubProjectResolver) FindProject(context.Context, access.Scope, string) (project.Project, error) {
	return project.Project{}, project.ErrNotFound
}

type stubChunkStore struct{}

func (stubChunkStore) Save(context.Context, int64, map[sentrycli.Checksum][]byte) error {
	return errors.New("unused")
}
func (stubChunkStore) Load(context.Context, int64, []sentrycli.Checksum) (map[sentrycli.Checksum][]byte, error) {
	return nil, errors.New("unused")
}
func (stubChunkStore) Delete(context.Context, int64, []sentrycli.Checksum) error {
	return errors.New("unused")
}
func (stubChunkStore) DeleteExpired(context.Context, time.Time) (int64, error) {
	return 0, errors.New("unused")
}

type stubReleaseStore struct{}

func (stubReleaseStore) Upsert(context.Context, int64, string) error { return errors.New("unused") }
func (stubReleaseStore) Finalize(context.Context, int64, string, time.Time) error {
	return errors.New("unused")
}

type stubSourceMapRepository struct{}

func (stubSourceMapRepository) Put(context.Context, int64, string, string, sourcemap.DecodedUpload) (sourcemap.Map, bool, error) {
	return sourcemap.Map{}, false, errors.New("unused")
}
func (stubSourceMapRepository) Find(context.Context, int64, string, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}
func (stubSourceMapRepository) FindByDebugID(context.Context, int64, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}
func (stubSourceMapRepository) PutBundle(context.Context, int64, string, []sourcemap.BundleEntry) error {
	return errors.New("unused")
}

type stubProguardRepository struct{}

func (stubProguardRepository) Put(context.Context, int64, string, proguard.DecodedUpload) (bool, error) {
	return false, errors.New("unused")
}
func (stubProguardRepository) Find(context.Context, int64, string) (proguard.Stored, error) {
	return proguard.Stored{}, proguard.ErrNotFound
}
func (stubProguardRepository) MarkUsed(context.Context, int64, string) error { return nil }

type stubNativeDebugRepository struct{}

func (stubNativeDebugRepository) Put(context.Context, int64, nativedebug.Upload) error {
	return errors.New("unused")
}
func (stubNativeDebugRepository) Find(context.Context, int64, string) (nativedebug.Stored, error) {
	return nativedebug.Stored{}, nativedebug.ErrNotFound
}
func (stubNativeDebugRepository) MarkUsed(context.Context, int64, string) error { return nil }

type appDataScrubbingServiceStub struct{}

func (*appDataScrubbingServiceStub) Get(context.Context, int64) (scrub.Config, error) {
	return scrub.DefaultConfig(), nil
}

func (*appDataScrubbingServiceStub) Update(_ context.Context, _ int64, config scrub.Config) (scrub.Config, error) {
	return config, nil
}

type appGlobalDataScrubbingServiceStub struct{}

func (*appGlobalDataScrubbingServiceStub) GetGlobal(context.Context) ([]scrub.Rule, error) {
	return nil, nil
}

func (*appGlobalDataScrubbingServiceStub) UpdateGlobal(_ context.Context, rules []scrub.Rule) ([]scrub.Rule, error) {
	return rules, nil
}

type appGroupingRuleServiceStub struct{}

func (*appGroupingRuleServiceStub) ListRules(context.Context, string) ([]grouping.Rule, error) {
	return nil, nil
}
func (*appGroupingRuleServiceStub) CreateRule(context.Context, int64, string, string) (grouping.Rule, error) {
	return grouping.Rule{}, nil
}
func (*appGroupingRuleServiceStub) UpdateRule(context.Context, int64, int64, string, string) (grouping.Rule, error) {
	return grouping.Rule{}, nil
}
func (*appGroupingRuleServiceStub) MoveRule(context.Context, int64, int64, grouping.MoveDirection) (grouping.Rule, error) {
	return grouping.Rule{}, nil
}
func (*appGroupingRuleServiceStub) DeleteRule(context.Context, int64, int64) error { return nil }

// appGroupServiceStub backs Dependencies.GroupService so testDependencies
// registers the seven group-administration routes; without it, the
// `if dependencies.GroupService != nil` guard in app.go excludes those routes
// from routeTable entirely, and TestEveryRouteDeclaresAGate /
// TestMemberReachableRouteCount silently stop covering them.
type appGroupServiceStub struct{}

func (*appGroupServiceStub) List(context.Context) ([]group.Group, error) { return nil, nil }
func (*appGroupServiceStub) Find(context.Context, int64) (group.Detail, error) {
	return group.Detail{}, nil
}
func (*appGroupServiceStub) Create(context.Context, string) (group.Group, error) {
	return group.Group{}, nil
}
func (*appGroupServiceStub) Rename(context.Context, int64, string) (group.Group, error) {
	return group.Group{}, nil
}
func (*appGroupServiceStub) Delete(context.Context, int64) error { return nil }
func (*appGroupServiceStub) ReplaceMembers(context.Context, int64, []int64) ([]int64, error) {
	return nil, nil
}
func (*appGroupServiceStub) ReplaceProjects(context.Context, int64, []int64) ([]int64, error) {
	return nil, nil
}

type stubIssueService struct{}

func (*stubIssueService) ListIssues(context.Context, issue.ListFilters) (issue.Page, error) {
	return issue.Page{}, nil
}
func (*stubIssueService) IssueDetail(context.Context, access.Scope, int64) (issue.Detail, error) {
	return issue.Detail{}, nil
}
func (*stubIssueService) SetStatus(context.Context, access.Scope, int64, issue.Status) error {
	return nil
}
func (*stubIssueService) SetAssignee(context.Context, access.Scope, int64, *int64) error { return nil }
func (*stubIssueService) IssueEvents(context.Context, access.Scope, issue.EventQuery) (issue.EventPage, error) {
	return issue.EventPage{}, nil
}
func (*stubIssueService) LatestEvent(context.Context, access.Scope, int64) (issue.EventDetail, error) {
	return issue.EventDetail{}, nil
}
func (*stubIssueService) Event(context.Context, access.Scope, uuid.UUID) (issue.EventDetail, error) {
	return issue.EventDetail{}, nil
}
func (*stubIssueService) IssueStats(context.Context, access.Scope, int64, issue.Period) ([]issue.Bucket, error) {
	return nil, nil
}
func (*stubIssueService) Timeline(context.Context, access.Scope, uuid.UUID) ([]issue.Span, error) {
	return nil, nil
}
func (*stubIssueService) MergeIssue(context.Context, access.Scope, int64, int64) (issue.Detail, error) {
	return issue.Detail{}, nil
}
func (*stubIssueService) TagKeys(context.Context, access.Scope, string) ([]issue.TagKey, error) {
	return nil, nil
}
func (*stubIssueService) TagValues(context.Context, access.Scope, string, string, string, int) ([]issue.TagValue, error) {
	return nil, nil
}
func (*stubIssueService) IssueUsers(context.Context, access.Scope, int64, int) (issue.UserFacet, error) {
	return issue.UserFacet{}, nil
}

// appIngestKeyStoreStub backs TestRetiredEnvelopePathFailsClosedThroughTheRealHandler
// with a key that resolves successfully, to project 1, so the only thing that
// can reject the request is ProjectIDFromPath("v1"). A stub that returned
// ErrKeyNotFound would make the test pass for the wrong reason (401 from the
// unknown-key branch, masking whether the project-id gate ran at all).
type appIngestKeyStoreStub struct{}

func (appIngestKeyStoreStub) FindByPublicKey(context.Context, string) (ingest.KeyInfo, error) {
	return ingest.KeyInfo{ProjectID: 1, PublicKey: "retired-path-key", RateLimitRPS: 1000, RateLimitBurst: 1000}, nil
}

func authDependencies(service api.AuthService) Dependencies {
	return Dependencies{
		Database:      &healthChecker{},
		AuthService:   service,
		LoginLimiter:  auth.NewLoginLimiter(appTestClock{}),
		ScopeResolver: unrestrictedScopeResolverStub{},
		CookieSecure:  true,
	}
}

// testDependencies builds a fully-wired Dependencies: every optional service
// (ArtifactService, GroupingRuleService, GroupService, UploadChunks,
// EnvelopeHandler) is populated, so routeTable registers every route this
// package can produce, including all four conditional blocks. Tests that
// want to exercise the "absent" branches build a narrower Dependencies of
// their own, as the routing tests already do.
func testDependencies(t *testing.T) Dependencies {
	t.Helper()
	dependencies := authDependencies(&authServiceStub{})
	dependencies.UserAdminService = &appUserAdminServiceStub{}
	dependencies.ProjectService = &appProjectServiceStub{}
	dependencies.ArtifactService = &appArtifactServiceStub{}
	dependencies.ArtifactCatalog = &appArtifactCatalogStub{}
	dependencies.SourceMaps = &appSourceMapRepositoryStub{}
	dependencies.IssueService = &stubIssueService{}
	dependencies.GroupingRuleService = &appGroupingRuleServiceStub{}
	dependencies.DataScrubbingService = &appDataScrubbingServiceStub{}
	dependencies.GlobalDataScrubbingService = &appGlobalDataScrubbingServiceStub{}
	dependencies.NotificationService = &appNotificationServiceStub{}
	dependencies.GroupService = &appGroupServiceStub{}
	dependencies.UploadChunks = stubChunkStore{}
	dependencies.Releases = stubReleaseStore{}
	dependencies.EnvelopeHandler = http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	return dependencies
}

// testAuthHandler builds the *api.AuthHandler routeTable's signature demands,
// wired to the given Dependencies. Callers that also need a Dependencies
// value for routeTable itself should build one and pass it here, rather than
// each calling testDependencies separately — this keeps routeTable and the
// auth handler observing the same stub instances.
func testAuthHandler(t *testing.T, dependencies Dependencies) *api.AuthHandler {
	t.Helper()
	return api.NewAuthHandler(dependencies.AuthService, dependencies.LoginLimiter, dependencies.ScopeResolver, api.Options{
		CookieSecure: dependencies.CookieSecure,
		ReportError:  dependencies.ReportError,
	})
}

type appTestClock struct{}

func (appTestClock) Now() time.Time { return time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC) }

func assertStatusBody(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	var body map[string]string
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != want {
		t.Fatalf("status body = %q, want %q", body["status"], want)
	}
}
