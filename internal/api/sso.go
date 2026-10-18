package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/ramilS/faultline/internal/auth"
)

const (
	ssoStateCookieName = "faultline_sso_state"
	ssoStatePath       = "/api/v1/auth/sso"
	ssoStateMaxAge     = 600
	ssoRandomBytes     = 32

	// ssoExchangeTimeout bounds the callback's outbound token exchange.
	// Callback is the only unauthenticated route that blocks on egress, and
	// the request context carries no deadline of its own — httpserver sets
	// only ReadHeaderTimeout — so without this a blackholed route to the
	// provider pins one goroutine and one socket per callback for as long as
	// the network takes to give up.
	ssoExchangeTimeout = 10 * time.Second
)

// SSOService performs the OIDC authorization-code flow: building the
// provider's authorization URL and exchanging a returned code for verified
// identity claims.
type SSOService interface {
	AuthCodeURL(state, nonce, challenge string) string
	Exchange(ctx context.Context, code, verifier, nonce string) (auth.IdentityClaims, error)
}

// IdentityLoginService signs in the account behind a verified identity,
// provisioning or linking one as needed.
type IdentityLoginService interface {
	LoginWithIdentity(ctx context.Context, provider string, claims auth.IdentityClaims) (auth.User, string, error)
}

// SSOHandler serves the corporate single sign-on flow.
type SSOHandler struct {
	provider     SSOService
	logins       IdentityLoginService
	limiter      *auth.LoginLimiter
	domains      auth.AllowedDomains
	cookieSecure bool
	reportError  func(error)
}

// NewSSOHandler builds an SSOHandler. limiter bounds how often one client
// address can make Callback's outbound token exchange; pass the same
// instance the password login path uses, or nil to leave the callback
// unlimited. A nil options.ReportError defaults to a no-op, matching
// NewAuthHandler.
func NewSSOHandler(provider SSOService, logins IdentityLoginService, limiter *auth.LoginLimiter, domains auth.AllowedDomains, options Options) *SSOHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &SSOHandler{
		provider:     provider,
		logins:       logins,
		limiter:      limiter,
		domains:      domains,
		cookieSecure: options.CookieSecure,
		reportError:  reportError,
	}
}

// flowState is the anti-forgery material for one sign-in attempt. It is
// serialized as one cookie value: state guards against CSRF on the OIDC
// callback, nonce binds the returned ID token to this flow, and verifier is
// the PKCE secret whose SHA-256 (the challenge) is what the provider is
// shown — the cookie carries the verifier, never the challenge, since the
// callback needs the value the challenge cannot be reversed into.
type flowState struct {
	State    string `json:"state"`
	Nonce    string `json:"nonce"`
	Verifier string `json:"verifier"`
}

// Start begins a sign-in attempt: it mints fresh state, nonce and PKCE
// verifier, stores them in a short-lived cookie scoped to the flow's own
// path, and redirects the caller to the identity provider.
func (h *SSOHandler) Start(w http.ResponseWriter, r *http.Request) {
	setAuthCacheControl(w)
	flow, err := newFlowState()
	if err != nil {
		h.reportError(fmt.Errorf("sso start: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	encoded, err := json.Marshal(flow)
	if err != nil {
		h.reportError(fmt.Errorf("sso start: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	//nolint:gosec // G124: Secure follows FAULTLINE_COOKIE_SECURE, true unless a plain-http deployment turns it off explicitly.
	http.SetCookie(w, &http.Cookie{
		Name:     ssoStateCookieName,
		Value:    base64.RawURLEncoding.EncodeToString(encoded),
		Path:     ssoStatePath,
		MaxAge:   ssoStateMaxAge,
		Secure:   h.cookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	challenge := sha256.Sum256([]byte(flow.Verifier))
	http.Redirect(w, r, h.provider.AuthCodeURL(flow.State, flow.Nonce, base64.RawURLEncoding.EncodeToString(challenge[:])), http.StatusFound)
}

// Callback completes a sign-in attempt. It clears the flow cookie before any
// validation so a rejected attempt leaves nothing replayable, then checks the
// state, exchanges the code for verified claims, applies the hosted-domain
// policy, and only then signs the caller in. A flow-level rejection (bad or
// missing cookie, mismatched state, empty code, failed exchange) returns
// "sign-in could not be verified"; an account-level rejection (foreign
// hosted domain, unverified email, deactivated account, or an identity
// refused because it would link into an owner account) returns "this
// account is not allowed to sign in". Those account-level reasons are
// indistinguishable from each other in both the response and its status
// code, which is what prevents a caller from enumerating who belongs to the
// organization — reaching that message at all already requires a real
// Google grant for the caller's own account.
//
// The exchange is rate-limited per client address and given its own
// deadline, because it is the only outbound call this codebase makes from an
// unauthenticated route; a caller over the limit gets 429 with Retry-After,
// exactly as the password path does.
func (h *SSOHandler) Callback(w http.ResponseWriter, r *http.Request) {
	setAuthCacheControl(w)
	flow, ok := h.readFlowState(r)
	h.clearFlowCookie(w)
	if !ok {
		writeError(w, http.StatusForbidden, "sign-in could not be verified")
		return
	}
	if subtle.ConstantTimeCompare([]byte(flow.State), []byte(r.URL.Query().Get("state"))) != 1 {
		writeError(w, http.StatusForbidden, "sign-in could not be verified")
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusForbidden, "sign-in could not be verified")
		return
	}
	// Checked here, after the cheap local checks and before the exchange: the
	// exchange is the only part of this route that leaves the process, and
	// the client address is the only actor signal a callback carries.
	if h.limiter != nil {
		if allowed, retry := h.limiter.AllowAddress(r.RemoteAddr); !allowed {
			writeRetryAfter(w, retry)
			writeError(w, http.StatusTooManyRequests, "too many sign-in attempts")
			return
		}
	}
	exchangeCtx, cancel := context.WithTimeout(r.Context(), ssoExchangeTimeout)
	defer cancel()
	claims, err := h.provider.Exchange(exchangeCtx, code, flow.Verifier, flow.Nonce)
	if err != nil {
		h.reportError(fmt.Errorf("sso exchange: %w", err))
		writeError(w, http.StatusForbidden, "sign-in could not be verified")
		return
	}
	if err := h.domains.Permit(claims); err != nil {
		h.reportError(fmt.Errorf("sso permit: %w", err))
		writeError(w, http.StatusForbidden, "this account is not allowed to sign in")
		return
	}
	_, token, err := h.logins.LoginWithIdentity(r.Context(), auth.ProviderGoogle, claims)
	if errors.Is(err, auth.ErrInvalidCredentials) {
		writeError(w, http.StatusForbidden, "this account is not allowed to sign in")
		return
	}
	// A deployment that serves this route with no identity repository behind
	// it is misconfigured, not a caller error. Recognized by identity so the
	// operator gets that said, instead of a bare "sso login" wrap that reads
	// like a database failure.
	if errors.Is(err, auth.ErrSSONotConfigured) {
		h.reportError(fmt.Errorf("sso callback reached a login service with no identity repository: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("sso login: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	setSessionCookie(w, token, sessionCookieMaxAge(auth.SSOSessionLifetime), h.cookieSecure)
	http.Redirect(w, r, "/", http.StatusFound)
}

// readFlowState reads and decodes the flow cookie set by Start. It reports
// ok=false for a missing cookie, malformed encoding, or any empty field —
// each treated as an unverifiable sign-in attempt.
func (h *SSOHandler) readFlowState(r *http.Request) (flowState, bool) {
	cookie, err := r.Cookie(ssoStateCookieName)
	if err != nil {
		return flowState{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return flowState{}, false
	}
	var flow flowState
	if err := json.Unmarshal(raw, &flow); err != nil {
		return flowState{}, false
	}
	if flow.State == "" || flow.Nonce == "" || flow.Verifier == "" {
		return flowState{}, false
	}
	return flow, true
}

// clearFlowCookie expires the flow cookie so a failed or completed attempt
// cannot be replayed.
func (h *SSOHandler) clearFlowCookie(w http.ResponseWriter) {
	//nolint:gosec // G124: Secure follows FAULTLINE_COOKIE_SECURE, true unless a plain-http deployment turns it off explicitly.
	http.SetCookie(w, &http.Cookie{
		Name:     ssoStateCookieName,
		Value:    "",
		Path:     ssoStatePath,
		MaxAge:   -1,
		Secure:   h.cookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func newFlowState() (flowState, error) {
	values := make([]string, 3)
	for index := range values {
		raw := make([]byte, ssoRandomBytes)
		if _, err := io.ReadFull(rand.Reader, raw); err != nil {
			return flowState{}, fmt.Errorf("generate flow state: %w", err)
		}
		values[index] = base64.RawURLEncoding.EncodeToString(raw)
	}
	return flowState{State: values[0], Nonce: values[1], Verifier: values[2]}, nil
}
