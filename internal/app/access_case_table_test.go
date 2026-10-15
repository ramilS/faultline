package app

import "testing"

// accessCase is one member-reachable route, paired with a concrete request
// aimed at a project the caller cannot reach. Every gateMember route in
// routeTable must appear here — TestEveryMemberRouteHasAForeignProjectCase
// enforces it, so the security surface and its tests cannot drift apart.
type accessCase struct {
	pattern string // must match the routeTable entry verbatim
	method  string
	target  string // a concrete URL against the out-of-scope project
	body    string
}

var foreignProjectCases = []accessCase{
	{pattern: "GET /api/v1/projects/{slug}/keys", method: "GET", target: "/api/v1/projects/secret/keys"},
	{pattern: "GET /api/v1/projects/{slug}/artifact-tokens", method: "GET", target: "/api/v1/projects/secret/artifact-tokens"},
	{pattern: "GET /api/v1/projects/{slug}/grouping-rules", method: "GET", target: "/api/v1/projects/secret/grouping-rules"},
	{pattern: "GET /api/v1/projects/{slug}/data-scrubbing", method: "GET", target: "/api/v1/projects/secret/data-scrubbing"},
	{pattern: "GET /api/v1/projects/{slug}/issues", method: "GET", target: "/api/v1/projects/secret/issues"},
	{pattern: "GET /api/v1/projects/{slug}/tags", method: "GET", target: "/api/v1/projects/secret/tags"},
	{pattern: "GET /api/v1/projects/{slug}/tags/{key}/values", method: "GET", target: "/api/v1/projects/secret/tags/browser/values"},
	{pattern: "GET /api/v1/issues/{id}", method: "GET", target: "/api/v1/issues/{foreignIssue}"},
	{pattern: "PATCH /api/v1/issues/{id}", method: "PATCH", target: "/api/v1/issues/{foreignIssue}", body: `{"status":"resolved"}`},
	// Both source and target are foreign here on purpose: a foreign source
	// paired with a local target would 404 via the cross-project rule in
	// MergeIssue (issues.go:501-505) regardless of scope, which would leave
	// this row vacuous as a boundary test. Pointing both ends at the
	// "secret" project means lockMergeParticipants's scopePredicate is what
	// excludes them — see issue-keyed scope test in
	// issues_access_integration_test.go for the repository-level version of
	// this same distinction.
	{pattern: "POST /api/v1/issues/{id}/merge", method: "POST", target: "/api/v1/issues/{foreignIssue}/merge", body: `{"target_id":{secondForeignIssue}}`},
	{pattern: "GET /api/v1/issues/{id}/events", method: "GET", target: "/api/v1/issues/{foreignIssue}/events"},
	{pattern: "GET /api/v1/issues/{id}/events/latest", method: "GET", target: "/api/v1/issues/{foreignIssue}/events/latest"},
	{pattern: "GET /api/v1/issues/{id}/stats", method: "GET", target: "/api/v1/issues/{foreignIssue}/stats"},
	{pattern: "GET /api/v1/issues/{id}/users", method: "GET", target: "/api/v1/issues/{foreignIssue}/users"},
	{pattern: "GET /api/v1/events/{event_id}", method: "GET", target: "/api/v1/events/{foreignEvent}"},
	{pattern: "GET /api/v1/events/{event_id}/timeline", method: "GET", target: "/api/v1/events/{foreignEvent}/timeline"},
	{pattern: "GET /api/v1/users/assignable", method: "GET", target: "/api/v1/users/assignable?project=secret"},
	{pattern: "GET /api/v1/projects", method: "GET", target: "/api/v1/projects"}, // asserted by absence, see below
	{pattern: "GET /api/v1/issues", method: "GET", target: "/api/v1/issues"},     // asserted by absence, see below
}

// A gateMember route with no case here is an unverified hole in the boundary.
//
// This check needs no database — routeTable runs over stub dependencies — so
// it lives in an untagged file: route drift must be caught by the cheap
// `go test ./...` path, not only by the container-backed integration suite.
func TestEveryMemberRouteHasAForeignProjectCase(t *testing.T) {
	covered := make(map[string]bool, len(foreignProjectCases))
	for _, testCase := range foreignProjectCases {
		covered[testCase.pattern] = true
	}
	dependencies := testDependencies(t)
	for _, current := range routeTable(dependencies, testAuthHandler(t, dependencies)) {
		if current.gate == gateMember && !covered[current.pattern] {
			t.Errorf("gateMember route %q has no foreign-project case", current.pattern)
		}
	}
}
