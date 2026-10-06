// Package app assembles the Faultline HTTP application.
package app

import (
	"context"
	"io/fs"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/dashboard"
	"github.com/ramilS/faultline/internal/platform/httpjson"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
)

// Dependencies contains infrastructure required by the HTTP application.
type Dependencies struct {
	Database                   storage.HealthChecker
	AuthService                api.AuthService
	UserAdminService           api.UserAdminService
	ProjectService             api.ProjectService
	ArtifactService            api.ArtifactService
	SourceMaps                 sourcemap.Repository
	ProguardMappings           proguard.Repository
	UploadChunks               sentrycli.ChunkStore
	Releases                   sentrycli.ReleaseStore
	IssueService               api.IssueService
	GroupingRuleService        api.GroupingRuleService
	DataScrubbingService       api.DataScrubbingService
	GlobalDataScrubbingService api.GlobalDataScrubbingService
	GroupService               api.GroupService
	EnvelopeHandler            http.Handler
	LoginLimiter               *auth.LoginLimiter
	ScopeResolver              api.ScopeResolver
	SSOHandler                 *api.SSOHandler
	CookieSecure               bool
	ReportError                func(error)
}

// gate is the authorization posture a route runs under. It is declared per
// route rather than applied by hand so that the set of member-reachable routes
// — the security surface of project access — is data a test can enumerate.
type gate int

const (
	// gateUnset is the zero value. A route carrying it refuses to register:
	// a posture nobody declared must stop the process, not default to one.
	gateUnset gate = iota
	// gatePublic authenticates on something other than a session (an ingest
	// key, an artifact token) or needs no authentication at all.
	gatePublic
	// gateSession requires a session. The route acts on the caller or on
	// nothing project-shaped, so there is no scope to apply.
	gateSession
	// gateMember requires a session and carries a project whose scope is
	// enforced downstream, in SQL. The gate does not perform that check — it
	// declares that the route owes one, which is what makes the assertion in
	// internal/app/app_access_integration_test.go enumerable.
	gateMember
	// gateOwner requires the owner role. Owners bypass groups entirely, so
	// an owner-gated route needs no scope.
	gateOwner
)

type route struct {
	pattern string
	gate    gate
	handler http.Handler
}

// register installs each route under the middleware its gate demands.
func register(mux *http.ServeMux, authHandler *api.AuthHandler, routes []route) {
	for _, current := range routes {
		switch current.gate {
		case gatePublic:
			mux.Handle(current.pattern, current.handler)
		case gateSession, gateMember:
			mux.Handle(current.pattern, authHandler.RequireSession(current.handler))
		case gateOwner:
			mux.Handle(current.pattern, authHandler.RequireSession(authHandler.RequireOwner(current.handler)))
		default:
			panic("app: route " + current.pattern + " declares no authorization gate")
		}
	}
}

// routeTable builds every route this application can serve, declaring the
// authorization posture each one runs under, by composing the per-domain
// route builders below. New registers them verbatim; the route tests enumerate
// the same table to assert every gateMember route refuses a foreign project.
func routeTable(dependencies Dependencies, authHandler *api.AuthHandler) []route {
	var routes []route
	routes = append(routes, authRoutes(authHandler)...)
	routes = append(routes, ssoRoutes(dependencies)...)
	routes = append(routes, userRoutes(dependencies)...)
	routes = append(routes, projectRoutes(dependencies)...)
	routes = append(routes, artifactRoutes(dependencies)...)
	routes = append(routes, groupingRuleRoutes(dependencies)...)
	routes = append(routes, dataScrubbingRoutes(dependencies)...)
	routes = append(routes, globalDataScrubbingRoutes(dependencies)...)
	routes = append(routes, issueRoutes(dependencies)...)
	routes = append(routes, groupRoutes(dependencies)...)
	routes = append(routes, sentryCLIRoutes(dependencies)...)
	routes = append(routes, ingestRoutes(dependencies)...)
	routes = append(routes, apiFallbackRoutes()...)
	return routes
}

// New assembles the root HTTP handler. Product routes are intentionally reserved.
func New(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", health)
	mux.HandleFunc("GET /readyz", ready(dependencies.Database))
	authHandler := api.NewAuthHandler(dependencies.AuthService, dependencies.LoginLimiter, dependencies.ScopeResolver, api.Options{
		CookieSecure: dependencies.CookieSecure,
		ReportError:  dependencies.ReportError,
	})

	register(mux, authHandler, routeTable(dependencies, authHandler))

	assets := dashboard.Files()
	assetServer := http.FileServer(http.FS(assets))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		assetPath := strings.TrimPrefix(r.URL.Path, "/")
		if assetPath != "" {
			if _, err := fs.Stat(assets, assetPath); err == nil {
				assetServer.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(dashboard.Index())
	})
	return guardAPIBoundary(mux)
}

// statusResponse and apiErrorResponse pin the wire shape of the health,
// readiness, and boundary-rejection payloads. They are structs rather than
// map[string]string so the JSON encoder cannot fail on them, which is what
// makes the discard inside httpjson.Write sound for these call sites.
type statusResponse struct {
	Status string `json:"status"`
}

type apiErrorResponse struct {
	Error string `json:"error"`
}

func guardAPIBoundary(mux *http.ServeMux) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if escapedStructuralPathTargetsAPI(r.URL.EscapedPath()) && !muxMatchesAPI(mux, r) {
			httpjson.Write(w, http.StatusBadRequest, apiErrorResponse{Error: "invalid api path"})
			return
		}
		mux.ServeHTTP(w, r)
	})
}

func escapedStructuralPathTargetsAPI(escapedPath string) bool {
	lower := strings.ToLower(escapedPath)
	if !strings.Contains(lower, "%2e") && !strings.Contains(lower, "%2f") && !strings.Contains(lower, "%5c") && !strings.Contains(escapedPath, "\\") {
		return false
	}
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return false
	}
	normalizedPath := path.Clean(strings.ReplaceAll(decodedPath, "\\", "/"))
	return normalizedPath == "/api" || strings.HasPrefix(normalizedPath, "/api/")
}

func muxMatchesAPI(mux *http.ServeMux, r *http.Request) bool {
	_, pattern := mux.Handler(r)
	if methodSeparator := strings.IndexByte(pattern, ' '); methodSeparator >= 0 {
		pattern = pattern[methodSeparator+1:]
	}
	return pattern == "/api" || strings.HasPrefix(pattern, "/api/")
}

func ready(database storage.HealthChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if database == nil || database.Ping(ctx) != nil {
			httpjson.Write(w, http.StatusServiceUnavailable, statusResponse{Status: "unavailable"})
			return
		}
		httpjson.Write(w, http.StatusOK, statusResponse{Status: "ready"})
	}
}

func health(w http.ResponseWriter, _ *http.Request) {
	httpjson.Write(w, http.StatusOK, statusResponse{Status: "ok"})
}

func notImplemented(w http.ResponseWriter, _ *http.Request) {
	httpjson.Write(w, http.StatusNotImplemented, apiErrorResponse{Error: "api route not implemented"})
}
