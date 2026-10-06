package ingest

import "net/http"

// CORS values for the public ingestion edge. The origin is an unconditional
// wildcard: the endpoint authenticates on the ingest key alone, reads no
// cookie, and that key is meant to ship inside a browser bundle, so an origin
// allowlist would restrict nothing a non-browser caller cannot already do. The
// key's rate limit is what bounds abuse.
const (
	corsAllowOrigin   = "*"
	corsAllowMethods  = "POST, OPTIONS"
	corsAllowHeaders  = "Content-Type, Content-Encoding, X-Sentry-Auth"
	corsExposeHeaders = "Retry-After, X-Sentry-Rate-Limits"
	corsMaxAge        = "86400"
)

// WithCORS lets a browser page on any origin post envelopes to next, answering
// the preflight itself. Apply it only to the ingestion endpoint: every other
// route authenticates with an ambient session cookie, for which a wildcard
// origin would be an exfiltration hole.
func WithCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Access-Control-Allow-Origin", corsAllowOrigin)

		if r.Method == http.MethodOptions {
			header.Set("Access-Control-Allow-Methods", corsAllowMethods)
			header.Set("Access-Control-Allow-Headers", corsAllowHeaders)
			header.Set("Access-Control-Max-Age", corsMaxAge)
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Neither rate-limit header is on the CORS response-header safelist, so a
		// cross-origin caller reads null for both unless they are exposed — which
		// silently degrades the SDK's backoff to a blind fixed interval, and only
		// under load.
		header.Set("Access-Control-Expose-Headers", corsExposeHeaders)
		next.ServeHTTP(w, r)
	})
}
