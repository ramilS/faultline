// Package api implements Faultline's authenticated dashboard HTTP API.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/platform/httpjson"
)

const (
	SessionCookieName = "faultline_session"
	loginBodyLimit    = 16 << 10
)

// AuthService is the authentication behavior required by the HTTP layer.
type AuthService interface {
	Login(context.Context, string, string) (auth.User, string, error)
	Authenticate(context.Context, string) (auth.Session, auth.User, error)
	Logout(context.Context, string) error
}

// ScopeResolver resolves the set of projects an authenticated caller may read.
// It is consulted once per request by RequireSession.
type ScopeResolver interface {
	ScopeFor(context.Context, auth.User) (access.Scope, error)
}

type Options struct {
	CookieSecure bool
	ReportError  func(error)
}

type AuthHandler struct {
	service      AuthService
	limiter      *auth.LoginLimiter
	scopes       ScopeResolver
	cookieSecure bool
	reportError  func(error)
}

func NewAuthHandler(service AuthService, limiter *auth.LoginLimiter, scopes ScopeResolver, options Options) *AuthHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &AuthHandler{service: service, limiter: limiter, scopes: scopes, cookieSecure: options.CookieSecure, reportError: reportError}
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type userResponse struct {
	ID    int64  `json:"id"`
	Email string `json:"email"`
	Name  string `json:"name"`
	Role  string `json:"role"`
}

type authResponse struct {
	User userResponse `json:"user"`
}

type errorResponse struct {
	Error string `json:"error"`
}

func (h *AuthHandler) Login(w http.ResponseWriter, r *http.Request) {
	setAuthCacheControl(w)
	if !SameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	var input loginRequest
	if err := decodeSingleJSON(w, r, loginBodyLimit, &input); err != nil || input.Email == "" || input.Password == "" {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if h.limiter != nil {
		if allowed, retry := h.limiter.Allow(input.Email, r.RemoteAddr); !allowed {
			writeRetryAfter(w, retry)
			writeError(w, http.StatusTooManyRequests, "too many login attempts")
			return
		}
	}

	user, token, err := h.service.Login(r.Context(), input.Email, input.Password)
	if errors.Is(err, auth.ErrAuthenticationBusy) {
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusTooManyRequests, "authentication temporarily unavailable")
		return
	}
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("login: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	setSessionCookie(w, token, sessionCookieMaxAge(auth.SessionLifetime), h.cookieSecure)
	writeUser(w, user)
}

func (h *AuthHandler) Logout(w http.ResponseWriter, r *http.Request) {
	setAuthCacheControl(w)
	if !SameOrigin(r) {
		writeError(w, http.StatusForbidden, "origin not allowed")
		return
	}
	token, err := sessionToken(r)
	if err != nil {
		token = ""
	}
	err = h.service.Logout(r.Context(), token)
	h.clearSessionCookie(w)
	if err != nil {
		h.reportError(fmt.Errorf("logout: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AuthHandler) Me(w http.ResponseWriter, r *http.Request) {
	setAuthCacheControl(w)
	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	writeUser(w, user)
}

func setAuthCacheControl(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store")
}

// declaredJSONBody reports whether the request declares exactly
// "application/json", with no media-type parameters. Fifteen handlers gate
// their body on this; one definition is what keeps them from drifting apart.
//
// The exactness is pinned by TestLoginValidatesJSONRequest, which asserts
// that "application/json; charset=utf-8" is rejected. That is stricter than
// the media-type grammar requires, and stricter than the source-map upload,
// which parses the parameters off with mime.ParseMediaType (artifacts.go).
// The two are left as they are: loosening a request guard is a behaviour
// change, not a refactor, and each shape has its own test.
func declaredJSONBody(r *http.Request) bool {
	return r.Header.Get("Content-Type") == "application/json"
}

// limit is per-endpoint policy, not a shared constant: every caller names its
// own budget (loginBodyLimit, groupBodyLimit, issueBodyLimit, ...) so that
// raising one endpoint's ceiling raises only that endpoint's. That they all
// currently choose 16KiB is each endpoint's own answer, not a default — the
// lenient sibling below takes 1MiB and 64KiB from its two callers.
//
// Unknown fields are rejected: this is Faultline's own API, and a key the
// server does not understand means the client and the server disagree about
// what was requested.
//
//nolint:unparam // see the limit paragraph above: uniform today, per-endpoint by design
func decodeSingleJSON(w http.ResponseWriter, r *http.Request, limit int64, output any) error {
	return decodeJSONBody(w, r, limit, output, true)
}

// decodeSingleJSONIgnoringUnknownFields decodes a body whose schema Faultline
// does not own — the sentry-cli protocol, whose fields are whatever the
// version of the CLI on the caller's machine decided to send. Rejecting an
// unrecognized key there pins the server to one CLI release: a single
// additive field in a patch version would turn every upload into a 400, for
// a feature whose whole premise is that it works with no configuration.
// Declared fields are validated exactly as on the strict path, and a body
// carrying more than one JSON value is still rejected.
func decodeSingleJSONIgnoringUnknownFields(w http.ResponseWriter, r *http.Request, limit int64, output any) error {
	return decodeJSONBody(w, r, limit, output, false)
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, limit int64, output any, rejectUnknownFields bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	decoder := json.NewDecoder(r.Body)
	if rejectUnknownFields {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(output); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

// setSessionCookie writes the session cookie. Every path that establishes or
// ends a session goes through here — password sign-in, sign-out, and the SSO
// callback — so the cookie's attributes have exactly one definition. Only
// maxAge differs between them, and each caller derives it from the auth
// package's own lifetime constant rather than restating the number.
func setSessionCookie(w http.ResponseWriter, value string, maxAge int, secure bool) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// sessionCookieMaxAge converts a session lifetime to the cookie MaxAge that
// expires with it.
func sessionCookieMaxAge(lifetime time.Duration) int {
	return int(lifetime / time.Second)
}

func (h *AuthHandler) clearSessionCookie(w http.ResponseWriter) {
	setSessionCookie(w, "", -1, h.cookieSecure)
}

func writeUser(w http.ResponseWriter, user auth.User) {
	writeJSON(w, http.StatusOK, authResponse{User: userResponse{ID: user.ID, Email: user.Email, Name: user.Name, Role: string(user.Role)}})
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, errorResponse{Error: message})
}

// writeJSON and writeError are this package's vocabulary for the shared
// helper; the 190-odd call sites keep the shorter local names.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	httpjson.Write(w, status, payload)
}
