package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/ramilS/faultline/internal/auth"
)

const userBodyLimit = 16 << 10

// UserAdminService is the user administration behavior required by the HTTP
// layer. It is deliberately narrower than auth.Administration's concrete
// type so this package cannot reach Login or Authenticate.
type UserAdminService interface {
	List(context.Context) ([]auth.User, error)
	ListAssignable(ctx context.Context, scope auth.ProjectScope, projectSlug string) ([]auth.User, error)
	Create(ctx context.Context, email, name, password string, role auth.Role) (auth.User, error)
	Rename(ctx context.Context, id int64, name string) (auth.User, error)
	SetRole(ctx context.Context, id int64, role auth.Role) (auth.User, error)
	SetDeactivated(ctx context.Context, actorID, id int64, deactivated bool) (auth.User, error)
	SetPassword(ctx context.Context, id int64, password string) error
	ChangeOwnPassword(ctx context.Context, id int64, current, next string, keepSession [32]byte) error
}

type UserHandler struct {
	service     UserAdminService
	limiter     *auth.LoginLimiter
	reportError func(error)
}

// NewUserHandler wires service as the administration backend. limiter is
// used only by ChangeOwnPassword — the one route on this handler that
// verifies a caller-supplied password against a stored hash, the same
// operation Login throttles per email+IP. It may be the very same
// *auth.LoginLimiter passed to NewAuthHandler (internal/app/app.go does
// this): the two callers key into it differently (email+IP vs. user id), so
// sharing the instance is a deliberate reuse of one mechanism, not a
// collision.
func NewUserHandler(service UserAdminService, limiter *auth.LoginLimiter, options Options) *UserHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &UserHandler{service: service, limiter: limiter, reportError: reportError}
}

// managedUserResponse is the administrative view, owner-only.
type managedUserResponse struct {
	ID            int64   `json:"id"`
	Email         string  `json:"email"`
	Name          string  `json:"name"`
	Role          string  `json:"role"`
	DeactivatedAt *string `json:"deactivated_at"`
	CreatedAt     string  `json:"created_at"`
}

// assignableUserResponse is what any signed-in user may see: enough to
// render an assignee picker, and nothing administrative. Adding a field here
// leaks it to every member's browser, so its key set is pinned by
// TestListAssignableAllowsAMemberAndOmitsRole.
type assignableUserResponse struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

type usersResponse struct {
	Users []managedUserResponse `json:"users"`
}

type assignableUsersResponse struct {
	Users []assignableUserResponse `json:"users"`
}

type managedUserEnvelope struct {
	User managedUserResponse `json:"user"`
}

type createUserRequest struct {
	Email    string `json:"email"`
	Name     string `json:"name"`
	Password string `json:"password"`
	Role     string `json:"role"`
}

type patchUserRequest struct {
	Name        *string `json:"name"`
	Role        *string `json:"role"`
	Deactivated *bool   `json:"deactivated"`
}

type setPasswordRequest struct {
	Password string `json:"password"`
}

type changeOwnPasswordRequest struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ListUsers returns every account, active or not. Owner-only.
func (h *UserHandler) ListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.service.List(r.Context())
	if err != nil {
		h.reportError(fmt.Errorf("list users: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]managedUserResponse, 0, len(users))
	for _, user := range users {
		items = append(items, toManagedUserResponse(user))
	}
	writeJSON(w, http.StatusOK, usersResponse{Users: items})
}

// ListAssignable returns active users in the narrow shape a picker needs,
// scoped to the project named by the required ?project= slug. Any signed-in
// user may call it; it must never expose Role or DeactivatedAt, which are
// administrative facts.
//
// The caller must themselves reach the named project: an out-of-scope slug
// answers 404, identically to a slug naming no project at all, so this
// route cannot be used to probe which project slugs exist.
func (h *UserHandler) ListAssignable(w http.ResponseWriter, r *http.Request) {
	slug := r.URL.Query().Get("project")
	if slug == "" {
		writeError(w, http.StatusBadRequest, "project is required")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	users, err := h.service.ListAssignable(r.Context(), scope, slug)
	if errors.Is(err, auth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("list assignable users: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]assignableUserResponse, 0, len(users))
	for _, user := range users {
		items = append(items, assignableUserResponse{ID: user.ID, Name: user.Name, Email: user.Email})
	}
	writeJSON(w, http.StatusOK, assignableUsersResponse{Users: items})
}

// CreateUser adds an account. Owner-only.
func (h *UserHandler) CreateUser(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input createUserRequest
	if err := decodeSingleJSON(w, r, userBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	created, err := h.service.Create(r.Context(), input.Email, input.Name, input.Password, auth.Role(input.Role))
	if !h.applyUserError(w, "create user", err) {
		return
	}
	writeJSON(w, http.StatusCreated, managedUserEnvelope{User: toManagedUserResponse(created)})
}

// PatchUser applies a partial update: rename, role change, or
// deactivate/reactivate. Owner-only. Deactivation reads the acting user from
// the session context — never from the request body — because a
// caller-supplied actor would make the self-deactivation guard a formality.
func (h *UserHandler) PatchUser(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input patchUserRequest
	if err := decodeSingleJSON(w, r, userBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if input.Name == nil && input.Role == nil && input.Deactivated == nil {
		writeError(w, http.StatusBadRequest, "no user fields to update")
		return
	}

	var (
		updated auth.User
		err     error
	)
	if input.Name != nil {
		updated, err = h.service.Rename(r.Context(), id, *input.Name)
		if !h.applyUserError(w, "rename user", err) {
			return
		}
	}
	if input.Role != nil {
		updated, err = h.service.SetRole(r.Context(), id, auth.Role(*input.Role))
		if !h.applyUserError(w, "set user role", err) {
			return
		}
	}
	if input.Deactivated != nil {
		actor, ok := UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		updated, err = h.service.SetDeactivated(r.Context(), actor.ID, id, *input.Deactivated)
		if !h.applyUserError(w, "set user deactivation", err) {
			return
		}
	}
	writeJSON(w, http.StatusOK, managedUserEnvelope{User: toManagedUserResponse(updated)})
}

// SetPassword resets another user's password and ends every session that
// account had open. Owner-only, and restricted to owners acting on someone
// else's account: an owner's own password change belongs to
// POST /api/v1/auth/password (self-verified, and it keeps the acting
// session), not this administrative reset, which does not verify the current
// password.
func (h *UserHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	id, ok := userIDFromPath(w, r)
	if !ok {
		return
	}
	actor, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	if actor.ID == id {
		writeError(w, http.StatusBadRequest, "use POST /api/v1/auth/password to change your own password")
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input setPasswordRequest
	if err := decodeSingleJSON(w, r, userBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	err := h.service.SetPassword(r.Context(), id, input.Password)
	if !h.applyUserError(w, "set user password", err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ChangeOwnPassword lets the signed-in user change their own password. It is
// the self-service counterpart to SetPassword above, reached at
// POST /api/v1/auth/password behind RequireSession alone — not RequireOwner
// — and acting on the user the session belongs to, never a path parameter.
// Unlike SetPassword it verifies the current password first (see
// auth.Administration.ChangeOwnPassword), which is why a mismatch maps to
// 403 rather than 401: the session itself is valid, only the supplied
// password is wrong.
//
// This route verifies a password with Argon2id — the single most expensive
// operation in the system — behind nothing but a valid session, so it is
// rate limited the same way Login is: h.limiter throttles the request rate
// per user id (AllowUser, distinct from Login's per-email+IP Allow, since
// the caller is already authenticated), and the service-layer
// passwordVerificationGate — shared with Login, see
// auth.Administration.ChangeOwnPassword's doc comment — bounds concurrency
// underneath that. auth.ErrAuthenticationBusy from the gate is reported the
// same way Login reports it: 429 with Retry-After, not folded into the
// generic error mapping below.
//
// The acting session's token hash comes from the context RequireSession
// populated, so the service can revoke the account's other sessions while
// keeping this one alive. Its absence means the route was mounted outside
// RequireSession, which is a wiring mistake rather than a caller error:
// refuse rather than guess, since guessing here would sign the caller out of
// the session they are using.
func (h *UserHandler) ChangeOwnPassword(w http.ResponseWriter, r *http.Request) {
	actor, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	keepSession, ok := SessionTokenHashFromContext(r.Context())
	if !ok {
		h.reportError(errors.New("change own password: request carries no session token hash"))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if h.limiter != nil {
		if allowed, retry := h.limiter.AllowUser(actor.ID); !allowed {
			writeRetryAfter(w, retry)
			writeError(w, http.StatusTooManyRequests, "too many password change attempts")
			return
		}
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input changeOwnPasswordRequest
	if err := decodeSingleJSON(w, r, userBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	err := h.service.ChangeOwnPassword(r.Context(), actor.ID, input.CurrentPassword, input.NewPassword, keepSession)
	if errors.Is(err, auth.ErrAuthenticationBusy) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "authentication temporarily unavailable")
		return
	}
	if !h.applyUserError(w, "change own password", err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// writeRetryAfter sets the Retry-After header from a limiter's suggested
// wait, rounded up to a whole positive second the way AuthHandler.Login
// does for the same header.
func writeRetryAfter(w http.ResponseWriter, retry time.Duration) {
	seconds := max(int(math.Ceil(retry.Seconds())), 1)
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
}

// applyUserError maps a user-administration error to a response and reports
// whether the caller may continue (true) or a response was already written
// (false).
func (h *UserHandler) applyUserError(w http.ResponseWriter, operation string, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, auth.ErrLastOwner):
		writeError(w, http.StatusConflict, "cannot remove the last owner")
	case errors.Is(err, auth.ErrSelfDeactivation):
		writeError(w, http.StatusBadRequest, "an owner cannot deactivate their own account")
	case errors.Is(err, auth.ErrCurrentPasswordMismatch):
		writeError(w, http.StatusForbidden, "current password is incorrect")
	case errors.Is(err, auth.ErrNotFound):
		writeError(w, http.StatusNotFound, "user not found")
	case errors.Is(err, auth.ErrConflict):
		writeError(w, http.StatusConflict, "email already in use")
	case isUserValidationError(err):
		writeError(w, http.StatusBadRequest, "invalid user")
	default:
		h.reportError(fmt.Errorf("%s: %w", operation, err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return false
}

// isUserValidationError matches the auth package's user-input validation
// sentinels by identity, not by message text: a wording change in
// internal/auth must not silently turn a 400 into a 500.
func isUserValidationError(err error) bool {
	for _, sentinel := range []error{
		auth.ErrEmailRequired,
		auth.ErrEmailInvalid,
		auth.ErrNameRequired,
		auth.ErrInvalidRole,
		auth.ErrPasswordTooShort,
		auth.ErrPasswordInvalidUTF8,
	} {
		if errors.Is(err, sentinel) {
			return true
		}
	}
	return false
}

func userIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid user id")
		return 0, false
	}
	return id, true
}

func toManagedUserResponse(user auth.User) managedUserResponse {
	var deactivatedAt *string
	if user.DeactivatedAt != nil {
		formatted := user.DeactivatedAt.UTC().Format(time.RFC3339)
		deactivatedAt = &formatted
	}
	return managedUserResponse{
		ID:            user.ID,
		Email:         user.Email,
		Name:          user.Name,
		Role:          string(user.Role),
		DeactivatedAt: deactivatedAt,
		CreatedAt:     user.CreatedAt.UTC().Format(time.RFC3339),
	}
}
