package app

import (
	"net/http"

	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/ingest"
)

// sentryCLIRoutes covers the Sentry chunk-upload and artifact-bundle protocol
// that sentry-cli and the @sentry/* bundler plugins speak. Every route is
// gatePublic: they authenticate on a project artifact bearer token, never on a
// session. The {org} segment is matched and discarded — Faultline has no
// organizations.
//
// These patterns must out-rank the "/api/" fallback in apiFallbackRoutes,
// which is a session-gated 501. They do, by ServeMux specificity, and
// TestSentryCLIRoutesOutrankTheAPIFallback pins it: if that ever inverts, the
// whole surface returns 501 (or, for an unsafe method with no Origin header,
// 403 from RequireSession's origin check — either way, not the sentry-cli
// handler's own response) with nothing else failing.
//
// It registers nothing when no artifact service or chunk store is wired, so
// narrow tests can assemble a minimal app (mirrors the ArtifactService guard
// on artifactRoutes).
func sentryCLIRoutes(dependencies Dependencies) []route {
	if dependencies.ArtifactService == nil || dependencies.UploadChunks == nil ||
		dependencies.ProjectService == nil || dependencies.SourceMaps == nil ||
		dependencies.Releases == nil {
		return nil
	}
	handler := api.NewSentryCLIHandler(
		dependencies.ArtifactService, dependencies.ProjectService, dependencies.UploadChunks,
		dependencies.SourceMaps, dependencies.Releases, dependencies.ProguardMappings,
		api.Options{ReportError: dependencies.ReportError},
	)
	routes := []route{
		{pattern: "GET /api/0/organizations/{org}/chunk-upload/", gate: gatePublic, handler: http.HandlerFunc(handler.ChunkUploadOptions)},
		{pattern: "POST /api/0/organizations/{org}/chunk-upload/", gate: gatePublic, handler: http.HandlerFunc(handler.UploadChunks)},
		{pattern: "POST /api/0/organizations/{org}/artifactbundle/assemble/", gate: gatePublic, handler: http.HandlerFunc(handler.AssembleBundle)},
		{pattern: "POST /api/0/organizations/{org}/releases/", gate: gatePublic, handler: http.HandlerFunc(handler.CreateRelease)},
		{pattern: "PUT /api/0/organizations/{org}/releases/{version}/", gate: gatePublic, handler: http.HandlerFunc(handler.UpdateRelease)},
		// The org-scoped pair above is NOT what the bundler plugin calls.
		// sentry-cli picks the path from whether it has a project: `releases
		// new` requires one and always POSTs the project-scoped path, and
		// `releases finalize` PUTs the project-scoped path when -p is set
		// (which the plugin always sets) and the org-scoped one otherwise.
		// Registering only the org-scoped pair 404s every default-configured
		// plugin build, so both shapes are registered. {project} is discarded
		// like {org} — the token decides.
		{pattern: "POST /api/0/projects/{org}/{project}/releases/", gate: gatePublic, handler: http.HandlerFunc(handler.CreateRelease)},
		{pattern: "PUT /api/0/projects/{org}/{project}/releases/{version}/", gate: gatePublic, handler: http.HandlerFunc(handler.UpdateRelease)},
		// `set-commits --auto` reads these two before it touches git. They are
		// not optional decoration: a 404 here is what the plugin's swallowed
		// error message is made of, and returning an object instead of an array
		// from /repos/ makes the CLI silently skip the rest of set-commits.
		// /repos/ MUST return a JSON array.
		{pattern: "GET /api/0/organizations/{org}/repos/", gate: gatePublic, handler: http.HandlerFunc(handler.ListRepos)},
		{pattern: "GET /api/0/organizations/{org}/releases/{version}/previous-with-commits/", gate: gatePublic, handler: http.HandlerFunc(handler.PreviousReleaseWithCommits)},
	}
	if dependencies.ProguardMappings == nil {
		return routes
	}
	// `upload-proguard` speaks one of two protocols depending on the CLI:
	// sentry-cli 3.x chunk-uploads and asks difs/assemble, 2.x posts one
	// multipart request to files/dsyms. Both are recorded under
	// internal/sentrycli/testdata/proguard. {org} is discarded like everywhere
	// else here; {project} is read and checked against the token's own project
	// by ownsEveryProject, same as the bundle assemble route above.
	return append(routes,
		route{pattern: "POST /api/0/projects/{org}/{project}/files/difs/assemble/", gate: gatePublic, handler: http.HandlerFunc(handler.AssembleDebugFiles)},
		route{pattern: "POST /api/0/projects/{org}/{project}/files/dsyms/", gate: gatePublic, handler: http.HandlerFunc(handler.UploadLegacyDebugFile)},
	)
}

// authRoutes covers session bootstrap: login, logout and the "who am I" probe.
func authRoutes(authHandler *api.AuthHandler) []route {
	return []route{
		{pattern: "POST /api/v1/auth/login", gate: gatePublic, handler: http.HandlerFunc(authHandler.Login)},
		{pattern: "POST /api/v1/auth/logout", gate: gatePublic, handler: http.HandlerFunc(authHandler.Logout)},
		{pattern: "GET /api/v1/auth/me", gate: gateSession, handler: http.HandlerFunc(authHandler.Me)},
	}
}

// ssoRoutes covers the OIDC sign-in flow. Every route here is gatePublic:
// the whole point of the flow is that the caller has no session yet, and
// /auth/providers is read by the login page before anyone has signed in. The
// flow's own protection is the state cookie and the ID-token nonce, not
// session middleware. The two flow routes exist only when a provider is
// configured, so an unconfigured deployment 404s instead of redirecting to a
// provider that is not there.
func ssoRoutes(dependencies Dependencies) []route {
	routes := []route{{
		pattern: "GET /api/v1/auth/providers",
		gate:    gatePublic,
		handler: api.NewProvidersHandler(dependencies.SSOHandler != nil),
	}}
	if dependencies.SSOHandler == nil {
		return routes
	}
	return append(routes,
		route{pattern: "GET /api/v1/auth/sso/start", gate: gatePublic, handler: http.HandlerFunc(dependencies.SSOHandler.Start)},
		route{pattern: "GET /api/v1/auth/sso/callback", gate: gatePublic, handler: http.HandlerFunc(dependencies.SSOHandler.Callback)},
	)
}

// userRoutes covers self-service and owner-administered user management.
func userRoutes(dependencies Dependencies) []route {
	// The same LoginLimiter instance as authHandler's: ChangeOwnPassword
	// verifies a password (the one thing on this handler that needs
	// throttling), keyed per user id rather than per email+IP — see
	// api.NewUserHandler's doc comment.
	userHandler := api.NewUserHandler(dependencies.UserAdminService, dependencies.LoginLimiter, api.Options{ReportError: dependencies.ReportError})
	return []route{
		// Self-service password change: any signed-in user, acting on themselves.
		// Deliberately not under RequireOwner — see UserHandler.ChangeOwnPassword.
		{pattern: "POST /api/v1/auth/password", gate: gateSession, handler: http.HandlerFunc(userHandler.ChangeOwnPassword)},
		{pattern: "GET /api/v1/users", gate: gateOwner, handler: http.HandlerFunc(userHandler.ListUsers)},
		{pattern: "POST /api/v1/users", gate: gateOwner, handler: http.HandlerFunc(userHandler.CreateUser)},
		{pattern: "PATCH /api/v1/users/{id}", gate: gateOwner, handler: http.HandlerFunc(userHandler.PatchUser)},
		{pattern: "POST /api/v1/users/{id}/password", gate: gateOwner, handler: http.HandlerFunc(userHandler.SetPassword)},
		{pattern: "GET /api/v1/users/assignable", gate: gateMember, handler: http.HandlerFunc(userHandler.ListAssignable)},
	}
}

// projectRoutes covers project and ingest-key management.
func projectRoutes(dependencies Dependencies) []route {
	projectHandler := api.NewProjectHandler(dependencies.ProjectService, api.Options{ReportError: dependencies.ReportError})
	return []route{
		{pattern: "GET /api/v1/projects", gate: gateMember, handler: http.HandlerFunc(projectHandler.ListProjects)},
		{pattern: "POST /api/v1/projects", gate: gateOwner, handler: http.HandlerFunc(projectHandler.CreateProject)},
		{pattern: "GET /api/v1/projects/{slug}/keys", gate: gateMember, handler: http.HandlerFunc(projectHandler.ListKeys)},
		{pattern: "POST /api/v1/projects/{slug}/keys", gate: gateOwner, handler: http.HandlerFunc(projectHandler.CreateKey)},
		{pattern: "POST /api/v1/projects/{slug}/keys/{id}/revoke", gate: gateOwner, handler: http.HandlerFunc(projectHandler.RevokeKey)},
	}
}

// artifactRoutes covers artifact-token management and source-map upload. It
// registers nothing when no artifact service is wired, so narrow tests can
// assemble a minimal app.
func artifactRoutes(dependencies Dependencies) []route {
	if dependencies.ArtifactService == nil {
		return nil
	}
	artifactHandler := api.NewArtifactHandler(dependencies.ArtifactService, dependencies.ProjectService, dependencies.SourceMaps, api.Options{ReportError: dependencies.ReportError})
	return []route{
		{pattern: "GET /api/v1/projects/{slug}/artifact-tokens", gate: gateMember, handler: http.HandlerFunc(artifactHandler.ListTokens)},
		{pattern: "POST /api/v1/projects/{slug}/artifact-tokens", gate: gateOwner, handler: http.HandlerFunc(artifactHandler.CreateToken)},
		{pattern: "POST /api/v1/projects/{slug}/artifact-tokens/{id}/revoke", gate: gateOwner, handler: http.HandlerFunc(artifactHandler.RevokeToken)},
		{pattern: "POST /api/v1/projects/{slug}/sourcemaps", gate: gatePublic, handler: http.HandlerFunc(artifactHandler.UploadSourceMap)},
	}
}

// groupingRuleRoutes covers grouping-rule management. It registers nothing
// when no grouping-rule service is wired (mirrors the ArtifactService guard),
// so narrow tests can assemble a minimal app.
func groupingRuleRoutes(dependencies Dependencies) []route {
	if dependencies.GroupingRuleService == nil {
		return nil
	}
	groupingRuleHandler := api.NewGroupingRuleHandler(dependencies.GroupingRuleService, dependencies.ProjectService, api.Options{ReportError: dependencies.ReportError})
	return []route{
		{pattern: "GET /api/v1/projects/{slug}/grouping-rules", gate: gateMember, handler: http.HandlerFunc(groupingRuleHandler.ListRules)},
		{pattern: "POST /api/v1/projects/{slug}/grouping-rules", gate: gateOwner, handler: http.HandlerFunc(groupingRuleHandler.CreateRule)},
		{pattern: "PATCH /api/v1/projects/{slug}/grouping-rules/{id}", gate: gateOwner, handler: http.HandlerFunc(groupingRuleHandler.UpdateRule)},
		{pattern: "POST /api/v1/projects/{slug}/grouping-rules/{id}/move", gate: gateOwner, handler: http.HandlerFunc(groupingRuleHandler.MoveRule)},
		{pattern: "DELETE /api/v1/projects/{slug}/grouping-rules/{id}", gate: gateOwner, handler: http.HandlerFunc(groupingRuleHandler.DeleteRule)},
	}
}

// dataScrubbingRoutes covers a project's scrubbing configuration. Like
// groupingRuleRoutes it registers nothing when no service is wired.
func dataScrubbingRoutes(dependencies Dependencies) []route {
	if dependencies.DataScrubbingService == nil {
		return nil
	}
	handler := api.NewDataScrubbingHandler(dependencies.DataScrubbingService, dependencies.ProjectService, api.Options{ReportError: dependencies.ReportError})
	return []route{
		{pattern: "GET /api/v1/projects/{slug}/data-scrubbing", gate: gateMember, handler: http.HandlerFunc(handler.Get)},
		{pattern: "PUT /api/v1/projects/{slug}/data-scrubbing", gate: gateOwner, handler: http.HandlerFunc(handler.Update)},
	}
}

// globalDataScrubbingRoutes covers the instance-wide scrubbing rules, which
// only an owner may read or change. It registers nothing when no service is
// wired, independently of the per-project routes.
func globalDataScrubbingRoutes(dependencies Dependencies) []route {
	if dependencies.GlobalDataScrubbingService == nil {
		return nil
	}
	handler := api.NewGlobalDataScrubbingHandler(dependencies.GlobalDataScrubbingService, api.Options{ReportError: dependencies.ReportError})
	return []route{
		{pattern: "GET /api/v1/settings/data-scrubbing", gate: gateOwner, handler: http.HandlerFunc(handler.Get)},
		{pattern: "PUT /api/v1/settings/data-scrubbing", gate: gateOwner, handler: http.HandlerFunc(handler.Update)},
	}
}

// issueRoutes covers issue and event reads, issue mutation, and the tag and
// user facets derived from the same issue service.
func issueRoutes(dependencies Dependencies) []route {
	issueHandler := api.NewIssueHandler(dependencies.IssueService, api.Options{ReportError: dependencies.ReportError})
	facetHandler := api.NewFacetHandler(dependencies.IssueService, api.Options{ReportError: dependencies.ReportError})
	usersFacetHandler := api.NewUsersFacetHandler(dependencies.IssueService, api.Options{ReportError: dependencies.ReportError})
	return []route{
		{pattern: "GET /api/v1/projects/{slug}/issues", gate: gateMember, handler: http.HandlerFunc(issueHandler.ListIssues)},
		{pattern: "GET /api/v1/issues/{id}", gate: gateMember, handler: http.HandlerFunc(issueHandler.IssueDetail)},
		{pattern: "PATCH /api/v1/issues/{id}", gate: gateMember, handler: http.HandlerFunc(issueHandler.PatchIssue)},
		{pattern: "POST /api/v1/issues/{id}/merge", gate: gateMember, handler: http.HandlerFunc(issueHandler.MergeIssue)},
		{pattern: "GET /api/v1/issues/{id}/events/latest", gate: gateMember, handler: http.HandlerFunc(issueHandler.LatestEvent)},
		{pattern: "GET /api/v1/issues/{id}/events", gate: gateMember, handler: http.HandlerFunc(issueHandler.IssueEvents)},
		{pattern: "GET /api/v1/issues/{id}/stats", gate: gateMember, handler: http.HandlerFunc(issueHandler.IssueStats)},
		{pattern: "GET /api/v1/events/{event_id}", gate: gateMember, handler: http.HandlerFunc(issueHandler.EventByID)},
		{pattern: "GET /api/v1/events/{event_id}/timeline", gate: gateMember, handler: http.HandlerFunc(issueHandler.Timeline)},
		{pattern: "GET /api/v1/projects/{slug}/tags", gate: gateMember, handler: http.HandlerFunc(facetHandler.TagKeys)},
		{pattern: "GET /api/v1/projects/{slug}/tags/{key}/values", gate: gateMember, handler: http.HandlerFunc(facetHandler.TagValues)},
		{pattern: "GET /api/v1/issues/{id}/users", gate: gateMember, handler: http.HandlerFunc(usersFacetHandler.IssueUsers)},
	}
}

// groupRoutes covers group administration. It registers nothing when no group
// service is wired (mirrors the ArtifactService guard). Every route is
// gateOwner: owners bypass groups entirely, and only an owner may create or
// edit the groups that gate everyone else.
func groupRoutes(dependencies Dependencies) []route {
	if dependencies.GroupService == nil {
		return nil
	}
	groupHandler := api.NewGroupHandler(dependencies.GroupService, api.Options{ReportError: dependencies.ReportError})
	return []route{
		{pattern: "GET /api/v1/groups", gate: gateOwner, handler: http.HandlerFunc(groupHandler.ListGroups)},
		{pattern: "POST /api/v1/groups", gate: gateOwner, handler: http.HandlerFunc(groupHandler.CreateGroup)},
		{pattern: "GET /api/v1/groups/{id}", gate: gateOwner, handler: http.HandlerFunc(groupHandler.GetGroup)},
		{pattern: "PATCH /api/v1/groups/{id}", gate: gateOwner, handler: http.HandlerFunc(groupHandler.PatchGroup)},
		{pattern: "DELETE /api/v1/groups/{id}", gate: gateOwner, handler: http.HandlerFunc(groupHandler.DeleteGroup)},
		{pattern: "PUT /api/v1/groups/{id}/members", gate: gateOwner, handler: http.HandlerFunc(groupHandler.PutMembers)},
		{pattern: "PUT /api/v1/groups/{id}/projects", gate: gateOwner, handler: http.HandlerFunc(groupHandler.PutProjects)},
	}
}

// ingestRoutes covers envelope ingestion. Public ingestion authenticates on
// the Sentry key alone, so it is mounted outside RequireSession. The {$}
// anchor matters for the trailing-slash pattern: without it, ServeMux treats
// "/api/{project_id}/envelope/" as a subtree match that would swallow
// /api/{id}/envelope/anything below it. It is not what keeps the retired
// /api/v1/envelope path out — that path matches the plain no-slash pattern
// below like any other numeric-looking segment; ProjectIDFromPath rejecting
// "v1" as non-numeric is what fails it closed (see
// TestRetiredEnvelopePathFailsClosedThroughTheRealHandler). The no-slash
// variants spare clients a redirect on a POST body. It registers nothing when
// no envelope handler is wired.
func ingestRoutes(dependencies Dependencies) []route {
	if dependencies.EnvelopeHandler == nil {
		return nil
	}
	envelopeHandler := ingest.WithCORS(dependencies.EnvelopeHandler)
	return []route{
		{pattern: "POST /api/{project_id}/envelope/{$}", gate: gatePublic, handler: envelopeHandler},
		{pattern: "POST /api/{project_id}/envelope", gate: gatePublic, handler: envelopeHandler},
		{pattern: "OPTIONS /api/{project_id}/envelope/{$}", gate: gatePublic, handler: envelopeHandler},
		{pattern: "OPTIONS /api/{project_id}/envelope", gate: gatePublic, handler: envelopeHandler},
	}
}

// apiFallbackRoutes keeps an unknown API path on the API contract — 501, not
// a 200 of dashboard HTML. The literal "/api/" subtree pattern cannot coexist
// with the ingest pattern (ServeMux panics on the ambiguous overlap), but a
// broader "/api/" subtree is unambiguously less specific than every wildcard
// route above and coexists fine, and keeps mux.Handler's pattern lookup
// working for guardAPIBoundary's bypass check.
func apiFallbackRoutes() []route {
	return []route{
		{pattern: "/api/", gate: gateSession, handler: http.HandlerFunc(notImplemented)},
	}
}
