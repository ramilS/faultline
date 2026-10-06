package ingest

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSentryKeyFromRequest(t *testing.T) {
	cases := []struct {
		name   string
		target string
		header string
		want   string
	}{
		{"query parameter", "/api/1/envelope/?sentry_version=7&sentry_key=abc123", "", "abc123"},
		{"auth header", "/api/1/envelope/", "Sentry sentry_key=abc123, sentry_version=7", "abc123"},
		{"header wins over query", "/api/1/envelope/?sentry_key=fromquery", "Sentry sentry_key=fromheader", "fromheader"},
		{"header with client and deprecated secret", "/api/1/envelope/", "Sentry sentry_version=7,sentry_client=sentry.python/2.1,sentry_key=abc123,sentry_secret=ignored", "abc123"},
		{"lowercase scheme", "/api/1/envelope/", "sentry sentry_key=abc123", "abc123"},
		{"padded pairs", "/api/1/envelope/", "Sentry  sentry_key = abc123 , sentry_version = 7 ", "abc123"},
		{"no credentials", "/api/1/envelope/", "", ""},
		{"header without the Sentry scheme", "/api/1/envelope/", "Bearer sentry_key=abc123", ""},
		{"header without sentry_key", "/api/1/envelope/", "Sentry sentry_version=7", ""},
		{"valid scheme with empty sentry_key falls through to the query parameter", "/api/1/envelope/?sentry_key=fromquery", "Sentry sentry_version=7", "fromquery"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, testCase.target, http.NoBody)
			if testCase.header != "" {
				request.Header.Set("X-Sentry-Auth", testCase.header)
			}

			if got := SentryKeyFromRequest(request); got != testCase.want {
				t.Fatalf("got %q, want %q", got, testCase.want)
			}
		})
	}
}

func TestProjectIDFromPath(t *testing.T) {
	cases := []struct {
		raw    string
		want   int64
		wantOK bool
	}{
		{"42", 42, true},
		{"1", 1, true},
		{"v1", 0, false},
		{"", 0, false},
		{"0", 0, false},
		{"-1", 0, false},
		{"+1", 0, false},
		{"1e3", 0, false},
		{" 1", 0, false},
		{"99999999999999999999999", 0, false},
	}

	for _, testCase := range cases {
		t.Run(testCase.raw, func(t *testing.T) {
			got, ok := ProjectIDFromPath(testCase.raw)

			if ok != testCase.wantOK || got != testCase.want {
				t.Fatalf("got (%d, %t), want (%d, %t)", got, ok, testCase.want, testCase.wantOK)
			}
		})
	}
}
