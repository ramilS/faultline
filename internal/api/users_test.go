package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
)

// userAdminServiceStub backs the test app's UserHandler. Each capture field
// records the arguments the handler actually passed, so a test can assert
// (for example) that the deactivation actor came from the session and not
// from the request body.
type userAdminServiceStub struct {
	list    []auth.User
	listErr error

	assignable      []auth.User
	assignableErr   error
	assignableSlug  string
	assignableScope auth.ProjectScope

	created   auth.User
	createErr error

	renamed   auth.User
	renameErr error

	roleUpdated auth.User
	setRoleErr  error

	deactivated       auth.User
	setDeactivatedErr error

	setPasswordErr error

	changeOwnPasswordErr error

	createCalls            []createUserCall
	renameCalls            []renameUserCall
	setRoleCalls           []setRoleUserCall
	setDeactivatedCalls    []setDeactivatedUserCall
	setPasswordCalls       []setPasswordUserCall
	changeOwnPasswordCalls []changeOwnPasswordCall
}

type createUserCall struct {
	email, name, password string
	role                  auth.Role
}

type renameUserCall struct {
	id   int64
	name string
}

type setRoleUserCall struct {
	id   int64
	role auth.Role
}

type setDeactivatedUserCall struct {
	actorID, id int64
	deactivated bool
}

type setPasswordUserCall struct {
	id       int64
	password string
}

type changeOwnPasswordCall struct {
	id            int64
	current, next string
	keepSession   [32]byte
}

func (s *userAdminServiceStub) List(context.Context) ([]auth.User, error) {
	return s.list, s.listErr
}

func (s *userAdminServiceStub) ListAssignable(_ context.Context, scope auth.ProjectScope, projectSlug string) ([]auth.User, error) {
	s.assignableScope = scope
	s.assignableSlug = projectSlug
	return s.assignable, s.assignableErr
}

func (s *userAdminServiceStub) Create(_ context.Context, email, name, password string, role auth.Role) (auth.User, error) {
	s.createCalls = append(s.createCalls, createUserCall{email, name, password, role})
	return s.created, s.createErr
}

func (s *userAdminServiceStub) Rename(_ context.Context, id int64, name string) (auth.User, error) {
	s.renameCalls = append(s.renameCalls, renameUserCall{id, name})
	return s.renamed, s.renameErr
}

func (s *userAdminServiceStub) SetRole(_ context.Context, id int64, role auth.Role) (auth.User, error) {
	s.setRoleCalls = append(s.setRoleCalls, setRoleUserCall{id, role})
	return s.roleUpdated, s.setRoleErr
}

func (s *userAdminServiceStub) SetDeactivated(_ context.Context, actorID, id int64, deactivated bool) (auth.User, error) {
	s.setDeactivatedCalls = append(s.setDeactivatedCalls, setDeactivatedUserCall{actorID, id, deactivated})
	return s.deactivated, s.setDeactivatedErr
}

func (s *userAdminServiceStub) SetPassword(_ context.Context, id int64, password string) error {
	s.setPasswordCalls = append(s.setPasswordCalls, setPasswordUserCall{id, password})
	return s.setPasswordErr
}

func (s *userAdminServiceStub) ChangeOwnPassword(_ context.Context, id int64, current, next string, keepSession [32]byte) error {
	s.changeOwnPasswordCalls = append(s.changeOwnPasswordCalls, changeOwnPasswordCall{id, current, next, keepSession})
	return s.changeOwnPasswordErr
}

// testApp configures a minimal app: an AuthHandler backed by a stub that
// authenticates any session cookie to authenticatedUser, and a UserHandler
// wired exactly as internal/app/app.go wires it (RequireSession composed
// with RequireOwner on every route except the assignable picker).
type testApp struct {
	authenticatedUser    auth.User
	authenticatedSession auth.Session
	authenticateErr      error

	users                []auth.User
	listErr              error
	assignable           []auth.User
	assignableErr        error
	created              auth.User
	createErr            error
	renamed              auth.User
	renameErr            error
	roleUpdated          auth.User
	setRoleErr           error
	deactivated          auth.User
	setDeactivatedErr    error
	setPasswordErr       error
	changeOwnPasswordErr error
}

func newTestApp(t *testing.T, config testApp) (http.Handler, *userAdminServiceStub) {
	t.Helper()
	authService := &authServiceStub{
		authUser:    config.authenticatedUser,
		authSession: config.authenticatedSession,
		authErr:     config.authenticateErr,
	}
	// One limiter shared between both handlers, mirroring internal/app/app.go:
	// AllowUser (keyed on user id) and Allow (keyed on email+IP) are two entry
	// points into the same mechanism, not two separate limiters.
	limiter := newTestLimiter()
	authHandler := NewAuthHandler(authService, limiter, &scopeResolverStub{scope: access.Unrestricted()}, Options{})
	service := &userAdminServiceStub{
		list:                 config.users,
		listErr:              config.listErr,
		assignable:           config.assignable,
		assignableErr:        config.assignableErr,
		created:              config.created,
		createErr:            config.createErr,
		renamed:              config.renamed,
		renameErr:            config.renameErr,
		roleUpdated:          config.roleUpdated,
		setRoleErr:           config.setRoleErr,
		deactivated:          config.deactivated,
		setDeactivatedErr:    config.setDeactivatedErr,
		setPasswordErr:       config.setPasswordErr,
		changeOwnPasswordErr: config.changeOwnPasswordErr,
	}
	userHandler := NewUserHandler(service, limiter, Options{})

	mux := http.NewServeMux()
	owner := func(next http.HandlerFunc) http.Handler {
		return authHandler.RequireSession(authHandler.RequireOwner(next))
	}
	mux.Handle("GET /api/v1/users", owner(userHandler.ListUsers))
	mux.Handle("POST /api/v1/users", owner(userHandler.CreateUser))
	mux.Handle("PATCH /api/v1/users/{id}", owner(userHandler.PatchUser))
	mux.Handle("POST /api/v1/users/{id}/password", owner(userHandler.SetPassword))
	mux.Handle("GET /api/v1/users/assignable", authHandler.RequireSession(http.HandlerFunc(userHandler.ListAssignable)))
	mux.Handle("POST /api/v1/auth/password", authHandler.RequireSession(http.HandlerFunc(userHandler.ChangeOwnPassword)))
	return mux, service
}

// request builds a request carrying a session cookie (so RequireSession
// resolves it through the stub authenticator) and, for unsafe methods, an
// Origin header matching the host — SameOrigin otherwise rejects them.
func request(t *testing.T, method, target string, body io.Reader) *http.Request {
	t.Helper()
	req := httptest.NewRequest(method, "https://example.com"+target, body)
	req.Header.Set("Cookie", SessionCookieName+"=session-token")
	req.Header.Set("Origin", "https://example.com")
	return req
}

func jsonRequest(t *testing.T, method, target, body string) *http.Request {
	t.Helper()
	req := request(t, method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func do(t *testing.T, handler http.Handler, req *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestListUsersRejectsAMember(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 2, Email: "member@example.com", Role: auth.RoleMember},
	})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/users", nil))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestListUsersRejectsUnauthenticated(t *testing.T) {
	handler, _ := newTestApp(t, testApp{authenticateErr: auth.ErrInvalidSession})

	req := httptest.NewRequest(http.MethodGet, "https://example.com/api/v1/users", http.NoBody)
	response := do(t, handler, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestListUsersReturnsManagedShapeForOwner(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner},
		users: []auth.User{
			{ID: 1, Email: "owner@example.com", Name: "Owner", Role: auth.RoleOwner},
		},
	})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/users", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	var body struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Users) != 1 {
		t.Fatalf("users = %v, want 1", body.Users)
	}
	assertExactKeys(t, body.Users[0], "id", "email", "name", "role", "deactivated_at", "created_at")
	if body.Users[0]["role"] != "owner" {
		t.Fatalf("role = %v, want owner", body.Users[0]["role"])
	}
}

func TestListAssignableAllowsAMemberAndOmitsRole(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 2, Role: auth.RoleMember},
		assignable:        []auth.User{{ID: 1, Email: "a@example.com", Name: "A", Role: auth.RoleOwner}},
	})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/users/assignable?project=web", nil))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.Code)
	}
	body := response.Body.String()
	// The picker needs id, name and email. Role and deactivated_at are
	// administrative facts and must not travel to every member's browser.
	for _, forbidden := range []string{`"role"`, `"deactivated_at"`, `"password"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("body contains %s: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `"a@example.com"`) {
		t.Fatalf("body missing the assignable user: %s", body)
	}

	// A field added later must fail this test loudly rather than silently
	// leaking to every signed-in user.
	var decoded struct {
		Users []map[string]any `json:"users"`
	}
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Users) != 1 {
		t.Fatalf("users = %v, want 1", decoded.Users)
	}
	assertExactKeys(t, decoded.Users[0], "id", "name", "email")
}

func TestListAssignablePassesTheProjectSlugToTheService(t *testing.T) {
	handler, service := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 2, Role: auth.RoleMember},
	})

	do(t, handler, request(t, http.MethodGet, "/api/v1/users/assignable?project=web", nil))

	if service.assignableSlug != "web" {
		t.Fatalf("assignableSlug = %q, want %q", service.assignableSlug, "web")
	}
}

func TestListAssignableRejectsAMissingProject(t *testing.T) {
	handler, service := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 2, Role: auth.RoleMember},
		assignable:        []auth.User{{ID: 1, Email: "a@example.com", Name: "A"}},
	})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/users/assignable", nil))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if service.assignableSlug != "" {
		t.Fatalf("service was called with slug %q; a missing project must not reach it", service.assignableSlug)
	}
}

// TestListAssignableMapsOutOfScopeToNotFound pins the fix for the finding
// that ListAssignable never bounded the roster query by the caller's own
// scope: a project the repository reports as out of reach (real slug the
// caller's groups don't grant, or one that names no project at all) must
// come back as a plain 404, indistinguishable from each other, never a 200
// with an empty or partial roster and never a 403 that would confirm the
// slug names a real project.
func TestListAssignableMapsOutOfScopeToNotFound(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 2, Role: auth.RoleMember},
		assignableErr:     auth.ErrNotFound,
	})

	response := do(t, handler, request(t, http.MethodGet, "/api/v1/users/assignable?project=secret", nil))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", response.Code, response.Body.String())
	}
}

// TestListAssignableWithoutScopeInContextIsInternalError exercises the
// handler directly, bypassing RequireSession the way internal/api's own
// handler-construction tests do, to prove scopeOrRefuse's contract holds
// here too: a missing scope means the session middleware did not run, which
// is a wiring bug, not a caller error, and must never be answered as if the
// caller were simply unrestricted.
func TestListAssignableWithoutScopeInContextIsInternalError(t *testing.T) {
	handler := NewUserHandler(&userAdminServiceStub{
		assignable: []auth.User{{ID: 1, Email: "a@example.com"}},
	}, newTestLimiter(), Options{})

	req := httptest.NewRequest(http.MethodGet, "https://example.com/api/v1/users/assignable?project=web", http.NoBody)
	recorder := httptest.NewRecorder()
	handler.ListAssignable(recorder, req)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", recorder.Code, recorder.Body.String())
	}
}

func TestListAssignableRejectsUnauthenticated(t *testing.T) {
	handler, _ := newTestApp(t, testApp{authenticateErr: auth.ErrInvalidSession})

	req := httptest.NewRequest(http.MethodGet, "https://example.com/api/v1/users/assignable", http.NoBody)
	response := do(t, handler, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
}

func TestCreateUserRejectsAMember(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 2, Role: auth.RoleMember}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users",
		`{"email":"a@example.com","name":"A","password":"correct horse battery","role":"member"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if len(service.createCalls) != 0 {
		t.Fatalf("createCalls = %d, want 0", len(service.createCalls))
	}
}

func TestCreateUserOwnerSuccess(t *testing.T) {
	handler, service := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner},
		created:           auth.User{ID: 5, Email: "new@example.com", Name: "New", Role: auth.RoleMember},
	})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users",
		`{"email":"new@example.com","name":"New","password":"correct horse battery","role":"member"}`))

	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", response.Code, response.Body.String())
	}
	if len(service.createCalls) != 1 {
		t.Fatalf("createCalls = %d, want 1", len(service.createCalls))
	}
	call := service.createCalls[0]
	if call.email != "new@example.com" || call.name != "New" || call.password != "correct horse battery" || call.role != auth.RoleMember {
		t.Fatalf("create call = %+v", call)
	}
}

func TestCreateUserMapsConflictToConflictStatus(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner},
		createErr:         auth.ErrConflict,
	})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users",
		`{"email":"dup@example.com","name":"Dup","password":"correct horse battery","role":"member"}`))

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", response.Code, response.Body.String())
	}
}

// TestCreateUserMapsValidationSentinelsToStatusCodes pins applyUserError's
// mapping so a wording change to an internal/auth validation error cannot
// silently demote a 400 into a 500: every case matches by errors.Is against
// an exported sentinel, never by message text.
func TestCreateUserMapsValidationSentinelsToStatusCodes(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{"email required", auth.ErrEmailRequired, http.StatusBadRequest},
		{"email invalid", auth.ErrEmailInvalid, http.StatusBadRequest},
		{"name required", auth.ErrNameRequired, http.StatusBadRequest},
		{"invalid role", auth.ErrInvalidRole, http.StatusBadRequest},
		{"password too short", auth.ErrPasswordTooShort, http.StatusBadRequest},
		{"password invalid utf8", auth.ErrPasswordInvalidUTF8, http.StatusBadRequest},
		{"unexpected error", errors.New("boom"), http.StatusInternalServerError},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler, _ := newTestApp(t, testApp{
				authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner},
				createErr:         test.err,
			})

			response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users",
				`{"email":"a@example.com","name":"A","password":"correct horse battery","role":"member"}`))

			if response.Code != test.want {
				t.Fatalf("status = %d, want %d: %s", response.Code, test.want, response.Body.String())
			}
		})
	}
}

func TestPatchUserRejectsAMember(t *testing.T) {
	handler, _ := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 2, Role: auth.RoleMember}})

	response := do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/users/1", `{"name":"New Name"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestPatchUserRejectsAnEmptyBody(t *testing.T) {
	handler, _ := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner}})

	response := do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/users/1", `{}`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
}

func TestPatchUserReturnsConflictForTheLastOwner(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner},
		setRoleErr:        auth.ErrLastOwner,
	})

	response := do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/users/1", `{"role":"member"}`))

	if response.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", response.Code)
	}
	if !strings.Contains(response.Body.String(), "last owner") {
		t.Fatalf("body should explain the refusal: %s", response.Body.String())
	}
}

func TestPatchUserMapsSelfDeactivationToBadRequest(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner},
		setDeactivatedErr: auth.ErrSelfDeactivation,
	})

	response := do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/users/1", `{"deactivated":true}`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "own account") {
		t.Fatalf("body should explain the refusal: %s", response.Body.String())
	}
}

func TestPatchUserMapsNotFoundToNotFoundStatus(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner},
		renameErr:         auth.ErrNotFound,
	})

	response := do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/users/99", `{"name":"New Name"}`))

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", response.Code, response.Body.String())
	}
}

// TestPatchUserDeactivationUsesTheSessionActorNotTheBody is the load-bearing
// test for the self-deactivation guard: PatchUser's request struct has no
// actor field at all (an unknown field would be rejected outright by
// decodeSingleJSON's DisallowUnknownFields), and the actor id the service
// receives must be the authenticated caller's, never something the caller
// could inject.
func TestPatchUserDeactivationUsesTheSessionActorNotTheBody(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner}})

	response := do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/users/2", `{"deactivated":true,"actor_id":999}`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for the unknown actor_id field: %s", response.Code, response.Body.String())
	}
	if len(service.setDeactivatedCalls) != 0 {
		t.Fatalf("setDeactivatedCalls = %d, want 0 — the malformed body must not reach the service", len(service.setDeactivatedCalls))
	}

	response = do(t, handler, jsonRequest(t, http.MethodPatch, "/api/v1/users/2", `{"deactivated":true}`))

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", response.Code, response.Body.String())
	}
	if len(service.setDeactivatedCalls) != 1 {
		t.Fatalf("setDeactivatedCalls = %d, want 1", len(service.setDeactivatedCalls))
	}
	call := service.setDeactivatedCalls[0]
	if call.actorID != 1 || call.id != 2 || !call.deactivated {
		t.Fatalf("setDeactivated call = %+v, want actorID=1 id=2 deactivated=true", call)
	}
}

func TestSetPasswordRejectsAMemberEvenForSelf(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 2, Role: auth.RoleMember}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users/2/password", `{"password":"correct horse battery"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
	if len(service.setPasswordCalls) != 0 {
		t.Fatalf("setPasswordCalls = %d, want 0", len(service.setPasswordCalls))
	}
}

// TestSetPasswordRejectsAnOwnerTargetingSelf pins that an owner cannot reset
// their own password through the admin endpoint: routing self-service through
// this unverified reset would let a hijacked session change its own password
// without the current one, bypassing the check the self-service password
// change exists to enforce.
func TestSetPasswordRejectsAnOwnerTargetingSelf(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users/1/password", `{"password":"correct horse battery"}`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "/api/v1/auth/password") {
		t.Fatalf("body should point at the self-service endpoint: %s", response.Body.String())
	}
	if len(service.setPasswordCalls) != 0 {
		t.Fatalf("setPasswordCalls = %d, want 0", len(service.setPasswordCalls))
	}
}

func TestSetPasswordAllowsAnOwnerTargetingAnotherUser(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users/2/password", `{"password":"correct horse battery"}`))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", response.Code, response.Body.String())
	}
	if len(service.setPasswordCalls) != 1 {
		t.Fatalf("setPasswordCalls = %d, want 1", len(service.setPasswordCalls))
	}
	if call := service.setPasswordCalls[0]; call.id != 2 || call.password != "correct horse battery" {
		t.Fatalf("setPassword call = %+v, want id=2", call)
	}
}

func TestSetPasswordBodySuppliedIdentityFieldsAreRejectedNotSilentlyIgnored(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 1, Role: auth.RoleOwner}})

	// A caller cannot smuggle an alternate actor/target through the body: the
	// request type has no such field, and unknown-field rejection means the
	// attempt is refused outright rather than quietly ignored.
	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/users/2/password",
		`{"password":"correct horse battery","user_id":1}`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
	if len(service.setPasswordCalls) != 0 {
		t.Fatalf("setPasswordCalls = %d, want 0", len(service.setPasswordCalls))
	}
}

// TestChangeOwnPasswordAllowsAMemberActingOnSelf covers the same boundary from
// the other side: POST /api/v1/auth/password must work for a member, not
// just an owner, and must act on the session's own user id, never a
// caller-supplied one (the request type carries no id field at all).
func TestChangeOwnPasswordAllowsAMemberActingOnSelf(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 7, Role: auth.RoleMember}})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"the-real-one-12","new_password":"a-new-password-12"}`))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", response.Code, response.Body.String())
	}
	if len(service.changeOwnPasswordCalls) != 1 {
		t.Fatalf("changeOwnPasswordCalls = %d, want 1", len(service.changeOwnPasswordCalls))
	}
	call := service.changeOwnPasswordCalls[0]
	if call.id != 7 || call.current != "the-real-one-12" || call.next != "a-new-password-12" {
		t.Fatalf("changeOwnPassword call = %+v, want id=7", call)
	}
}

// TestChangeOwnPasswordSparesTheActingSession pins the wiring that makes
// "revoke my other sessions, but not this one" possible: the token hash the
// handler hands the service is the one RequireSession resolved from the
// cookie, so no request body can influence which session survives.
func TestChangeOwnPasswordSparesTheActingSession(t *testing.T) {
	acting := [32]byte{9, 8, 7}
	handler, service := newTestApp(t, testApp{
		authenticatedUser:    auth.User{ID: 7, Role: auth.RoleMember},
		authenticatedSession: auth.Session{UserID: 7, TokenHash: acting},
	})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"the-real-one-12","new_password":"a-new-password-12"}`))

	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", response.Code, response.Body.String())
	}
	if len(service.changeOwnPasswordCalls) != 1 {
		t.Fatalf("changeOwnPasswordCalls = %d, want 1", len(service.changeOwnPasswordCalls))
	}
	if call := service.changeOwnPasswordCalls[0]; call.keepSession != acting {
		t.Fatalf("keepSession = %x, want %x — the acting session must be the one spared", call.keepSession, acting)
	}
}

func TestChangeOwnPasswordRejectsUnauthenticated(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticateErr: auth.ErrInvalidSession})

	req := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/auth/password",
		strings.NewReader(`{"current_password":"the-real-one-12","new_password":"a-new-password-12"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "https://example.com")
	response := do(t, handler, req)

	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", response.Code)
	}
	if len(service.changeOwnPasswordCalls) != 0 {
		t.Fatalf("changeOwnPasswordCalls = %d, want 0", len(service.changeOwnPasswordCalls))
	}
}

// TestChangeOwnPasswordMapsMismatchToForbidden pins the status for a wrong
// current password: 403, not 401 — the session is valid, only the supplied current
// password is wrong.
func TestChangeOwnPasswordMapsMismatchToForbidden(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser:    auth.User{ID: 7, Role: auth.RoleMember},
		changeOwnPasswordErr: auth.ErrCurrentPasswordMismatch,
	})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"wrong","new_password":"a-new-password-12"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", response.Code, response.Body.String())
	}
	if response.Code == http.StatusUnauthorized {
		t.Fatal("a wrong current password must not be reported as 401")
	}
}

func TestChangeOwnPasswordMapsValidationErrorToBadRequest(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser:    auth.User{ID: 7, Role: auth.RoleMember},
		changeOwnPasswordErr: auth.ErrPasswordTooShort,
	})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"the-real-one-12","new_password":"short"}`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
}

func TestChangeOwnPasswordBodySuppliedIdentityFieldsAreRejectedNotSilentlyIgnored(t *testing.T) {
	handler, service := newTestApp(t, testApp{authenticatedUser: auth.User{ID: 7, Role: auth.RoleMember}})

	// The request type has no id/user_id field at all — the acting user comes
	// only from the session context — so an attempt to supply one is refused
	// outright by unknown-field rejection, not silently ignored.
	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"the-real-one-12","new_password":"a-new-password-12","user_id":1}`))

	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
	}
	if len(service.changeOwnPasswordCalls) != 0 {
		t.Fatalf("changeOwnPasswordCalls = %d, want 0", len(service.changeOwnPasswordCalls))
	}
}

// TestChangeOwnPasswordThrottlesABurstOfAttempts proves the route is rate
// limited the same way Login is: this endpoint verifies a password with
// Argon2id behind nothing but a valid session, so a burst of attempts
// (wrong current password, here — the throttle must trip before the
// service even gets a chance to say so) is throttled exactly like a burst
// of login attempts, and the throttled request must never reach the
// service.
func TestChangeOwnPasswordThrottlesABurstOfAttempts(t *testing.T) {
	handler, service := newTestApp(t, testApp{
		authenticatedUser:    auth.User{ID: 9, Role: auth.RoleMember},
		changeOwnPasswordErr: auth.ErrCurrentPasswordMismatch,
	})
	attempt := func() *httptest.ResponseRecorder {
		return do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
			`{"current_password":"wrong","new_password":"a-new-password-12"}`))
	}

	for range 5 {
		attempt()
	}
	if len(service.changeOwnPasswordCalls) != 5 {
		t.Fatalf("changeOwnPasswordCalls after 5 attempts = %d, want 5", len(service.changeOwnPasswordCalls))
	}

	response := attempt()

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Retry-After"); got == "" || got == "0" {
		t.Fatalf("Retry-After = %q, want positive seconds", got)
	}
	if len(service.changeOwnPasswordCalls) != 5 {
		t.Fatalf("changeOwnPasswordCalls after the throttled attempt = %d, want still 5 — a throttled request must not reach the service", len(service.changeOwnPasswordCalls))
	}
}

// TestChangeOwnPasswordThrottlesPerUserNotGlobally confirms AllowUser keys
// on the acting user, not a shared bucket: a different user's burst must
// not be affected by another user's throttling.
func TestChangeOwnPasswordThrottlesPerUserNotGlobally(t *testing.T) {
	handler, service := newTestApp(t, testApp{
		authenticatedUser:    auth.User{ID: 9, Role: auth.RoleMember},
		changeOwnPasswordErr: auth.ErrCurrentPasswordMismatch,
	})
	for range 6 {
		do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
			`{"current_password":"wrong","new_password":"a-new-password-12"}`))
	}
	if len(service.changeOwnPasswordCalls) != 5 {
		t.Fatalf("changeOwnPasswordCalls for user 9 = %d, want 5 (throttled after that)", len(service.changeOwnPasswordCalls))
	}

	// A distinct actor gets a fresh bucket on the very same handler/limiter.
	otherHandler, otherService := newTestApp(t, testApp{
		authenticatedUser:    auth.User{ID: 10, Role: auth.RoleMember},
		changeOwnPasswordErr: auth.ErrCurrentPasswordMismatch,
	})
	response := do(t, otherHandler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"wrong","new_password":"a-new-password-12"}`))

	if response.Code != http.StatusForbidden {
		t.Fatalf("a different user's first attempt status = %d, want 403 (not throttled): %s", response.Code, response.Body.String())
	}
	if len(otherService.changeOwnPasswordCalls) != 1 {
		t.Fatalf("changeOwnPasswordCalls for user 10 = %d, want 1", len(otherService.changeOwnPasswordCalls))
	}
}

// TestChangeOwnPasswordMapsAuthenticationBusyToTooManyRequests covers the
// second, independent throttle: even once the request-rate limiter admits
// a request, the shared Argon2id concurrency gate
// (auth.Administration.ChangeOwnPassword) can itself report
// ErrAuthenticationBusy, and that must reach the client as 429 with
// Retry-After, not be folded into the generic error mapping (which would
// otherwise report a saturated server as "current password is incorrect").
func TestChangeOwnPasswordMapsAuthenticationBusyToTooManyRequests(t *testing.T) {
	handler, _ := newTestApp(t, testApp{
		authenticatedUser:    auth.User{ID: 9, Role: auth.RoleMember},
		changeOwnPasswordErr: auth.ErrAuthenticationBusy,
	})

	response := do(t, handler, jsonRequest(t, http.MethodPost, "/api/v1/auth/password",
		`{"current_password":"the-real-one-12","new_password":"a-new-password-12"}`))

	if response.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429: %s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Retry-After"); got != "1" {
		t.Fatalf("Retry-After = %q, want 1", got)
	}
}

func assertExactKeys(t *testing.T, object map[string]any, want ...string) {
	t.Helper()
	got := make([]string, 0, len(object))
	for key := range object {
		got = append(got, key)
	}
	sort.Strings(got)
	wantSorted := append([]string(nil), want...)
	sort.Strings(wantSorted)
	if len(got) != len(wantSorted) {
		t.Fatalf("keys = %v, want exactly %v", got, wantSorted)
	}
	for i := range got {
		if got[i] != wantSorted[i] {
			t.Fatalf("keys = %v, want exactly %v", got, wantSorted)
		}
	}
}
