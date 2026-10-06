package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
)

type userContextKey struct{}

type sessionTokenHashContextKey struct{}

func ContextWithUser(ctx context.Context, user auth.User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}

func UserFromContext(ctx context.Context) (auth.User, bool) {
	user, ok := ctx.Value(userContextKey{}).(auth.User)
	return user, ok
}

// ContextWithSessionTokenHash carries the identifier of the session a request
// authenticated with — the value the sessions table stores in token_hash,
// read back from the row by AuthService.Authenticate rather than re-derived
// from the cookie here. A handler that revokes a user's sessions while
// sparing the acting one (UserHandler.ChangeOwnPassword) needs to name that
// session; nothing else does. It is a one-way hash of the token, never the
// bearer token itself, so it cannot be replayed as a credential.
func ContextWithSessionTokenHash(ctx context.Context, hash [32]byte) context.Context {
	return context.WithValue(ctx, sessionTokenHashContextKey{}, hash)
}

func SessionTokenHashFromContext(ctx context.Context) ([32]byte, bool) {
	hash, ok := ctx.Value(sessionTokenHashContextKey{}).([32]byte)
	return hash, ok
}

func (h *AuthHandler) RequireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/auth/me" {
			setAuthCacheControl(w)
		}
		if !SameOrigin(r) {
			writeError(w, http.StatusForbidden, "origin not allowed")
			return
		}
		token, err := sessionToken(r)
		if err != nil || token == "" {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		session, user, err := h.service.Authenticate(r.Context(), token)
		if errors.Is(err, auth.ErrInvalidSession) {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if err != nil {
			h.reportError(fmt.Errorf("authenticate session: %w", err))
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		scope, err := h.scopes.ScopeFor(r.Context(), user)
		if err != nil {
			h.reportError(fmt.Errorf("resolve project scope: %w", err))
			writeError(w, http.StatusInternalServerError, "internal server error")
			return
		}
		ctx := access.ContextWithScope(
			ContextWithSessionTokenHash(ContextWithUser(r.Context(), user), session.TokenHash),
			scope,
		)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequireOwner gates a handler on the owner role. It must be composed inside
// RequireSession, which is what puts the user in the context.
func (h *AuthHandler) RequireOwner(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, ok := UserFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		if !user.IsOwner() {
			writeError(w, http.StatusForbidden, "owner role required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// SameOrigin permits safe requests and requires unsafe requests to provide an
// exact scheme-and-host Origin match.
//
// net/http.CrossOriginProtection (Go 1.25) is deliberately not used here: its
// Check fails open on cases this function blocks, so adopting it would weaken
// CSRF protection rather than strengthen it.
//
//   - An unsafe request carrying neither Sec-Fetch-Site nor Origin is allowed.
//     From its documentation: "Requests without Sec-Fetch-Site or Origin
//     headers are currently assumed to be either same-origin or non-browser
//     requests, and are allowed." This function blocks such a request.
//   - Check never compares the scheme at all: it matches only
//     url.Parse(Origin).Host against r.Host, so it fails open exactly where
//     this function's X-Forwarded-Proto-derived scheme check (requestScheme,
//     below) fails closed. An https Origin therefore satisfies a cleartext
//     request, an Origin carrying a path satisfies the comparison, and Check
//     has no option for supplying the external scheme faultline's
//     TLS-terminating proxy strips.
//   - Check also honours a client-supplied Sec-Fetch-Site: same-origin and
//     skips the Origin comparison entirely, so a forged header defeats it
//     for any non-browser client. TestUnsafeMethodsIgnoreFetchMetadata pins
//     this.
//
// TestSameOriginMatrix and TestUnsafeMethodsIgnoreFetchMetadata pin these
// differences; re-evaluate only if Check stops failing open.
func SameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return true
	}
	origins := r.Header.Values("Origin")
	if len(origins) != 1 || origins[0] == "" {
		return false
	}
	origin, err := url.Parse(origins[0])
	if err != nil || origin.Scheme == "" || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" {
		return false
	}
	scheme, ok := requestScheme(r)
	return ok && origin.Scheme == scheme && origin.Host == r.Host
}

func requestScheme(r *http.Request) (string, bool) {
	if values := r.Header.Values("X-Forwarded-Proto"); len(values) > 0 {
		first := strings.TrimSpace(strings.Split(values[0], ",")[0])
		return first, first == "http" || first == "https"
	}
	if r.TLS != nil {
		return "https", true
	}
	return "http", true
}

func sessionToken(r *http.Request) (string, error) {
	var token string
	found := false
	for _, header := range r.Header.Values("Cookie") {
		for part := range strings.SplitSeq(header, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				return "", errors.New("invalid cookie fragment")
			}
			name, value, ok := strings.Cut(part, "=")
			if !ok || !validCookieName(name) || !validCookieValue(value) {
				return "", errors.New("invalid cookie pair")
			}
			if name != SessionCookieName {
				continue
			}
			if found || value == "" || value[0] == '"' {
				return "", errors.New("invalid session cookie")
			}
			token = value
			found = true
		}
	}
	if !found {
		return "", http.ErrNoCookie
	}
	return token, nil
}

func validCookieName(name string) bool {
	if name == "" {
		return false
	}
	for index := range len(name) {
		character := name[index]
		if character <= 0x20 || character >= 0x7f || strings.ContainsRune("()<>@,;:\\\"/[]?={}", rune(character)) {
			return false
		}
	}
	return true
}

func validCookieValue(value string) bool {
	if value != "" && value[0] == '"' {
		if len(value) < 2 || value[len(value)-1] != '"' {
			return false
		}
		return validCookieOctets(value[1 : len(value)-1])
	}
	return validCookieOctets(value)
}

func validCookieOctets(value string) bool {
	for index := range len(value) {
		if value[index] < 0x21 || value[index] > 0x7e || value[index] == '"' || value[index] == ',' || value[index] == ';' || value[index] == '\\' {
			return false
		}
	}
	return true
}
