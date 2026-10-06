//go:build integration

package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/group"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// accessCase, foreignProjectCases and TestEveryMemberRouteHasAForeignProjectCase
// live in access_case_table_test.go, an untagged file: that check needs no
// database, and the case table it defines is what the tests below replay
// against a real server.

// Every member-reachable route must answer 404 for a project the caller cannot
// reach — not 403, which would confirm the project exists.
func TestForeignProjectIsNotFound(t *testing.T) {
	server, fixture := startServerForMemberOfWebOnly(t)

	for _, testCase := range foreignProjectCases {
		if testCase.pattern == "GET /api/v1/projects" {
			continue // covered by TestProjectListOmitsForeignProjects
		}
		t.Run(testCase.pattern, func(t *testing.T) {
			response := fixture.do(t, server, testCase)
			if response.StatusCode != http.StatusNotFound {
				t.Errorf("status = %d, want 404 (body %s)", response.StatusCode, response.body)
			}
		})
	}
}

// The list route cannot answer 404 — it has no slug to be wrong about — so its
// assertion is that the foreign project is absent from the body. The body is
// decoded and compared by slug rather than matched as a raw substring: a
// substring match on `"secret"` would also fire on a field named secret, or
// on any longer slug that merely contains it.
func TestProjectListOmitsForeignProjects(t *testing.T) {
	server, fixture := startServerForMemberOfWebOnly(t)

	body := fixture.getJSON(t, server, "/api/v1/projects")
	var decoded struct {
		Projects []struct {
			Slug string `json:"slug"`
		} `json:"projects"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode project list: %v (body %s)", err, body)
	}
	slugs := make(map[string]bool, len(decoded.Projects))
	for _, current := range decoded.Projects {
		slugs[current.Slug] = true
	}
	if slugs["secret"] {
		t.Errorf("project list contains the out-of-scope project: %s", body)
	}
	if !slugs["web"] {
		t.Errorf("project list is missing the in-scope project: %s", body)
	}
}

// memberFixture carries what a request needs to arrive as the seeded member:
// the session cookie the login route issued, and the ids of the rows the case
// table refers to by placeholder.
type memberFixture struct {
	cookie             *http.Cookie
	foreignIssue       int64
	secondForeignIssue int64
	foreignEvent       uuid.UUID
}

// recordedResponse is the status plus the already-drained body, so a failing
// subtest can name what the server actually said.
type recordedResponse struct {
	StatusCode int
	body       string
}

// do issues one case's request as the member. The session arrives in a real
// cookie rather than an injected context value: injecting a user would bypass
// RequireSession, and with it the scope resolution this whole file exists to
// test.
func (f *memberFixture) do(t *testing.T, server *httptest.Server, testCase accessCase) recordedResponse {
	t.Helper()
	target := f.substitute(testCase.target)
	var payload io.Reader
	if testCase.body != "" {
		payload = strings.NewReader(f.substitute(testCase.body))
	}
	request, err := http.NewRequest(testCase.method, server.URL+target, payload)
	if err != nil {
		t.Fatalf("build %s %s: %v", testCase.method, target, err)
	}
	request.AddCookie(f.cookie)
	if testCase.body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	// SameOrigin refuses an unsafe method without an exact scheme-and-host
	// Origin match, and a 403 from that guard would be indistinguishable from
	// the 403 this test exists to rule out.
	request.Header.Set("Origin", server.URL)
	return send(t, server, request)
}

// substitute replaces the case table's id placeholders with the seeded rows.
func (f *memberFixture) substitute(text string) string {
	return strings.NewReplacer(
		"{foreignIssue}", strconv.FormatInt(f.foreignIssue, 10),
		"{secondForeignIssue}", strconv.FormatInt(f.secondForeignIssue, 10),
		"{foreignEvent}", f.foreignEvent.String(),
	).Replace(text)
}

// getJSON performs a GET as the member and returns the body, failing the test
// unless the request succeeded.
func (f *memberFixture) getJSON(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, server.URL+path, http.NoBody)
	if err != nil {
		t.Fatalf("build GET %s: %v", path, err)
	}
	request.AddCookie(f.cookie)
	response := send(t, server, request)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200 (body %s)", path, response.StatusCode, response.body)
	}
	return response.body
}

func send(t *testing.T, server *httptest.Server, request *http.Request) recordedResponse {
	t.Helper()
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", request.Method, request.URL.Path, err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Errorf("close response body: %v", closeErr)
		}
	}()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return recordedResponse{StatusCode: response.StatusCode, body: strings.TrimSpace(string(body))}
}

const memberPassword = "correct-horse-battery"

// startServerForMemberOfWebOnly brings up a migrated database, seeds the
// reachable "web" project and an out-of-scope "secret" project carrying two
// issues (so the merge case can point both a source and a target at a
// project the member cannot reach), seeds a member who reaches only "web"
// through a group, assembles the real app.New handler over the real Postgres
// adapters, and signs the member in through the real login route.
//
// Every adapter is the production one on purpose: the boundary is enforced in
// SQL, so a stubbed repository would assert nothing about it.
func startServerForMemberOfWebOnly(t *testing.T) (*httptest.Server, *memberFixture) {
	t.Helper()
	pool := testdatabase.Migrated(t)
	ctx := t.Context()

	localProject := seedProject(t, pool, "web", "Web", "javascript")
	foreignProject := seedProject(t, pool, "secret", "Secret", "other")
	foreignIssue, foreignEvent := seedIssueWithEvent(t, pool, foreignProject, "foreign")
	secondForeignIssue, _ := seedIssueWithEvent(t, pool, foreignProject, "foreign-second")

	authRepositories := storagepostgres.NewAuthRepository(pool)
	projectRepositories := storagepostgres.NewProjectRepository(pool)
	authService := auth.NewService(authRepositories.Users, authRepositories.Sessions, auth.ServiceOptions{})

	member, err := authService.CreateUser(ctx, "member@example.com", "Member", memberPassword, auth.RoleMember)
	if err != nil {
		t.Fatalf("create member: %v", err)
	}
	grantThroughGroup(t, pool, member.ID, localProject)

	handler := New(Dependencies{
		Database:             pool,
		AuthService:          authService,
		UserAdminService:     auth.NewAdministration(authRepositories.Users, authRepositories.Sessions, auth.ServiceOptions{}),
		ProjectService:       project.NewService(projectRepositories.Projects, projectRepositories.Keys),
		ArtifactService:      artifactauth.NewService(storagepostgres.NewArtifactTokenRepository(pool), artifactauth.Options{}),
		SourceMaps:           storagepostgres.NewSourceMapRepository(pool),
		IssueService:         issue.NewService(storagepostgres.NewIssueRepository(pool)),
		GroupingRuleService:  grouping.NewRuleService(storagepostgres.NewGroupingRuleRepository(pool)),
		DataScrubbingService: scrub.NewService(storagepostgres.NewProjectSettingsStore(pool)),
		GroupService:         group.NewService(storagepostgres.NewGroupRepository(pool)),
		LoginLimiter:         auth.NewLoginLimiter(nil),
		ScopeResolver:        access.NewResolver(storagepostgres.NewAccessRepository(pool)),
		ReportError:          func(err error) { t.Errorf("server reported: %v", err) },
	})
	// The server is the fixture's own resource, so it closes it: a t.Fatalf
	// in signIn below happens before any caller could defer a Close of its own.
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)

	return server, &memberFixture{
		cookie:             signIn(t, server, "member@example.com", memberPassword),
		foreignIssue:       foreignIssue,
		secondForeignIssue: secondForeignIssue,
		foreignEvent:       foreignEvent,
	}
}

// signIn authenticates through POST /api/v1/auth/login and returns the session
// cookie it set, so every later request carries a session the middleware
// resolves for itself.
func signIn(t *testing.T, server *httptest.Server, email, password string) *http.Cookie {
	t.Helper()
	body := strings.NewReader(`{"email":"` + email + `","password":"` + password + `"}`)
	request, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/auth/login", body)
	if err != nil {
		t.Fatalf("build login request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", server.URL)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() {
		if closeErr := response.Body.Close(); closeErr != nil {
			t.Errorf("close login body: %v", closeErr)
		}
	}()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", response.StatusCode)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == api.SessionCookieName {
			return cookie
		}
	}
	t.Fatalf("login set no %s cookie", api.SessionCookieName)
	return nil
}

func seedProject(t *testing.T, pool *pgxpool.Pool, slug, name, platform string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(t.Context(),
		`INSERT INTO projects (slug, name, platform) VALUES ($1, $2, $3) RETURNING id`,
		slug, name, platform).Scan(&id); err != nil {
		t.Fatalf("seed project %s: %v", slug, err)
	}
	return id
}

// seedIssueWithEvent persists one issue and its event through the production
// batch writer, tagged with a "browser" value so the tag-facet cases have
// something they would leak if the scope predicate were missing.
func seedIssueWithEvent(t *testing.T, pool *pgxpool.Pool, projectID int64, label string) (int64, uuid.UUID) {
	t.Helper()
	ctx := t.Context()
	fingerprint := paddedFingerprint(label)
	eventID := uuid.New()
	now := time.Now().UTC()
	if err := storagepostgres.NewEventSink(pool).PersistBatch(ctx, storage.Batch{
		ProjectID: projectID,
		Issues: []storage.IssueDelta{{
			Fingerprint: fingerprint, Title: label, Level: "error", Count: 1, FirstSeen: now, LastSeen: now,
			Tags: []storage.TagDelta{{Key: "browser", Value: "firefox", Count: 1, FirstSeen: now, LastSeen: now}},
		}},
		Events: []storage.EventRow{{
			ProjectID: projectID, EventID: eventID, Timestamp: now, ReceivedAt: now,
			Level: "error", Message: label, Fingerprint: fingerprint,
			Payload: json.RawMessage(`{"message":"` + label + `"}`),
		}},
	}); err != nil {
		t.Fatalf("seed issue %s: %v", label, err)
	}
	var issueID int64
	if err := pool.QueryRow(ctx, `SELECT id FROM issues WHERE fingerprint = $1`, fingerprint).Scan(&issueID); err != nil {
		t.Fatalf("read back issue %s: %v", label, err)
	}
	return issueID, eventID
}

// grantThroughGroup is the only path a member has to a project: no role, flag,
// or seed row grants access on its own.
func grantThroughGroup(t *testing.T, pool *pgxpool.Pool, userID, projectID int64) {
	t.Helper()
	ctx := t.Context()
	var groupID int64
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name) VALUES ('web-only') RETURNING id`).Scan(&groupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, groupID, userID); err != nil {
		t.Fatalf("seed group member: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO group_projects (group_id, project_id) VALUES ($1, $2)`, groupID, projectID); err != nil {
		t.Fatalf("seed group project: %v", err)
	}
}

// paddedFingerprint pads a label out to the 64 characters the fingerprint
// column expects.
func paddedFingerprint(label string) string {
	out := []byte(strings.Repeat("0", 64))
	copy(out, label)
	return string(out)
}
