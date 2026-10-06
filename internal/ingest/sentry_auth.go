package ingest

import (
	"net/http"
	"strconv"
	"strings"
)

// SentryKeyFromRequest reads the ingest public key. The header form is checked
// first because server SDKs send it; browser SDKs use the query form instead,
// deliberately, so that posting an envelope stays a CORS-simple request.
// sentry_version, sentry_client, and the deprecated sentry_secret are accepted
// and ignored — rejecting them would break clients for no gain.
func SentryKeyFromRequest(r *http.Request) string {
	if key := sentryKeyFromAuthHeader(r.Header.Get("X-Sentry-Auth")); key != "" {
		return key
	}
	return strings.TrimSpace(r.URL.Query().Get("sentry_key"))
}

func sentryKeyFromAuthHeader(header string) string {
	trimmed := strings.TrimSpace(header)
	if len(trimmed) < len("sentry ") || !strings.EqualFold(trimmed[:len("sentry ")], "sentry ") {
		return ""
	}
	for pair := range strings.SplitSeq(trimmed[len("sentry "):], ",") {
		name, value, found := strings.Cut(pair, "=")
		if !found {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(name), "sentry_key") {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// ProjectIDFromPath parses the project id segment of an ingest path. Every DSN
// carries a numeric project id, and requiring digits here is what makes the
// retired /api/v1/envelope path fail closed instead of resolving to a project.
func ProjectIDFromPath(raw string) (int64, bool) {
	if raw == "" {
		return 0, false
	}
	for index := range len(raw) {
		if raw[index] < '0' || raw[index] > '9' {
			return 0, false
		}
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}
