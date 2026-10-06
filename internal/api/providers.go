package api

import "net/http"

// NewProvidersHandler reports which sign-in methods the deployment offers. It
// is unauthenticated by necessity — the login page reads it before anyone has
// a session — and therefore says nothing beyond whether SSO is switched on.
func NewProvidersHandler(ssoEnabled bool) http.Handler {
	type ssoStatus struct {
		Enabled bool `json:"enabled"`
	}
	type response struct {
		SSO ssoStatus `json:"sso"`
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		setAuthCacheControl(w)
		writeJSON(w, http.StatusOK, response{SSO: ssoStatus{Enabled: ssoEnabled}})
	})
}
