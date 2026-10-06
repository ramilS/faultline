package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/project"
)

// resolveProjectInScope looks up the project for slug within scope through
// resolver, writing the response and returning ok=false when the lookup did
// not yield a usable project: 404 when the project does not exist or falls
// outside scope, 500 (reported via reportError) for any other failure.
//
// Shared by ArtifactHandler, GroupingRuleHandler and ProjectHandler, whose
// own resolveProject methods were identical but for the field holding the
// resolver.
func resolveProjectInScope(w http.ResponseWriter, r *http.Request, scope access.Scope, slug string, resolver ProjectResolver, reportError func(error)) (project.Project, bool) {
	resolved, err := resolver.FindProject(r.Context(), scope, slug)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return project.Project{}, false
	}
	if err != nil {
		reportError(fmt.Errorf("find project: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return project.Project{}, false
	}
	return resolved, true
}
