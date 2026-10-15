package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/project"
)

const projectBodyLimit = 16 << 10

// ProjectService is the project and ingest-key behavior required by the HTTP
// layer. Key operations are slug-scoped to match the public route contract.
type ProjectService interface {
	ListProjects(context.Context, access.Scope) ([]project.Project, error)
	CreateProject(context.Context, project.CreateProject) (project.Project, error)
	FindProject(context.Context, access.Scope, string) (project.Project, error)
	UpdateProject(context.Context, string, project.UpdateProject) (project.Project, error)
	ListKeys(context.Context, access.Scope, string) ([]project.Key, error)
	CreateKey(context.Context, int64, project.CreateKey) (project.Key, string, error)
	RevokeKey(context.Context, access.Scope, string, int64) error
}

type ProjectHandler struct {
	service     ProjectService
	reportError func(error)
}

func NewProjectHandler(service ProjectService, options Options) *ProjectHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &ProjectHandler{service: service, reportError: reportError}
}

type createProjectRequest struct {
	Slug     string `json:"slug"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type updateProjectRequest struct {
	Name     string `json:"name"`
	Platform string `json:"platform"`
}

type projectResponse struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Platform  string `json:"platform"`
	CreatedAt string `json:"created_at"`
}

type projectsResponse struct {
	Projects []projectResponse `json:"projects"`
}

type projectEnvelope struct {
	Project projectResponse `json:"project"`
}

type createKeyRequest struct {
	RateLimitRPS   int `json:"rate_limit_rps"`
	RateLimitBurst int `json:"rate_limit_burst"`
}

type keyResponse struct {
	ID             int64   `json:"id"`
	ProjectID      int64   `json:"project_id"`
	PublicKey      string  `json:"public_key"`
	RateLimitRPS   int     `json:"rate_limit_rps"`
	RateLimitBurst int     `json:"rate_limit_burst"`
	RevokedAt      *string `json:"revoked_at"`
}

type keysResponse struct {
	Keys []keyResponse `json:"keys"`
}

// createKeyResponse carries the raw key material exactly once, at creation.
type createKeyResponse struct {
	Key    keyResponse `json:"key"`
	RawKey string      `json:"raw_key"`
}

// scopeOrRefuse reads the scope the session middleware resolved. Its absence
// means the middleware did not run, which is a wiring bug rather than a
// caller error — answer 500 and never fall back to a permissive default.
func scopeOrRefuse(w http.ResponseWriter, r *http.Request) (access.Scope, bool) {
	scope, ok := access.ScopeFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusInternalServerError, "internal server error")
		return access.Scope{}, false
	}
	return scope, true
}

func (h *ProjectHandler) ListProjects(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	projects, err := h.service.ListProjects(r.Context(), scope)
	if err != nil {
		h.reportError(fmt.Errorf("list projects: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]projectResponse, 0, len(projects))
	for _, current := range projects {
		items = append(items, toProjectResponse(current))
	}
	writeJSON(w, http.StatusOK, projectsResponse{Projects: items})
}

func (h *ProjectHandler) CreateProject(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input createProjectRequest
	if err := decodeSingleJSON(w, r, projectBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	create := project.CreateProject{Slug: input.Slug, Name: input.Name, Platform: input.Platform}
	if err := project.ValidateCreateProject(create); err != nil {
		writeError(w, http.StatusBadRequest, "invalid project")
		return
	}
	created, err := h.service.CreateProject(r.Context(), create)
	if errors.Is(err, project.ErrConflict) {
		writeError(w, http.StatusConflict, "project already exists")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("create project: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, projectEnvelope{Project: toProjectResponse(created)})
}

func (h *ProjectHandler) UpdateProject(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input updateProjectRequest
	if err := decodeSingleJSON(w, r, projectBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	update := project.UpdateProject{Name: input.Name, Platform: input.Platform}
	if err := project.ValidateUpdateProject(update); err != nil {
		writeError(w, http.StatusBadRequest, "invalid project")
		return
	}
	updated, err := h.service.UpdateProject(r.Context(), r.PathValue("slug"), update)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("update project: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, projectEnvelope{Project: toProjectResponse(updated)})
}

func (h *ProjectHandler) ListKeys(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	if _, ok := h.resolveProject(w, r, scope, slug); !ok {
		return
	}
	keys, err := h.service.ListKeys(r.Context(), scope, slug)
	if err != nil {
		h.reportError(fmt.Errorf("list ingest keys: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// Ingest keys are plaintext, live credentials (see internal/project), not
	// one-time reveals — a member reading this list must never receive
	// material that could be used to send events. Masking is a presentation
	// decision about an audience, so it belongs here, not in the service.
	//
	// The role decides who sees a live credential, so it comes from a user
	// this handler actually loaded. A zero-value auth.User would mask too
	// (Role == "" is not owner), but relying on that leaves the next reader
	// unable to tell a safe default from an accident of the zero value.
	user, ok := UserFromContext(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}
	items := make([]keyResponse, 0, len(keys))
	for _, current := range keys {
		response := toKeyResponse(current)
		if !user.IsOwner() {
			response.PublicKey = maskPublicKey(response.PublicKey)
		}
		items = append(items, response)
	}
	writeJSON(w, http.StatusOK, keysResponse{Keys: items})
}

// maskPublicKey keeps the first and last four characters so a member can match a
// listed key against the one their application is configured with, without ever
// receiving material that could be used to send events.
func maskPublicKey(publicKey string) string {
	const kept = 4
	if len(publicKey) <= kept*2 {
		return strings.Repeat("•", len(publicKey))
	}
	return publicKey[:kept] + strings.Repeat("•", len(publicKey)-kept*2) + publicKey[len(publicKey)-kept:]
}

func (h *ProjectHandler) CreateKey(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input createKeyRequest
	if err := decodeSingleJSON(w, r, projectBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	create := project.CreateKey{RateLimitRPS: input.RateLimitRPS, RateLimitBurst: input.RateLimitBurst}
	if err := project.ValidateCreateKey(create); err != nil {
		writeError(w, http.StatusBadRequest, "invalid ingest key")
		return
	}
	resolved, ok := h.resolveProject(w, r, access.Unrestricted(), slug)
	if !ok {
		return
	}
	key, raw, err := h.service.CreateKey(r.Context(), resolved.ID, create)
	if errors.Is(err, project.ErrConflict) {
		writeError(w, http.StatusConflict, "ingest key already exists")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("create ingest key: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, createKeyResponse{Key: toKeyResponse(key), RawKey: raw})
}

func (h *ProjectHandler) RevokeKey(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	keyID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || keyID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid ingest key id")
		return
	}
	err = h.service.RevokeKey(r.Context(), access.Unrestricted(), slug, keyID)
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "ingest key not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("revoke ingest key: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *ProjectHandler) resolveProject(w http.ResponseWriter, r *http.Request, scope access.Scope, slug string) (project.Project, bool) {
	return resolveProjectInScope(w, r, scope, slug, h.service, h.reportError)
}

func toProjectResponse(source project.Project) projectResponse {
	return projectResponse{
		ID:        source.ID,
		Slug:      source.Slug,
		Name:      source.Name,
		Platform:  source.Platform,
		CreatedAt: source.CreatedAt.Format(time.RFC3339),
	}
}

func toKeyResponse(source project.Key) keyResponse {
	var revokedAt *string
	if source.RevokedAt != nil {
		formatted := source.RevokedAt.Format(time.RFC3339)
		revokedAt = &formatted
	}
	return keyResponse{
		ID:             source.ID,
		ProjectID:      source.ProjectID,
		PublicKey:      source.PublicKey,
		RateLimitRPS:   source.RateLimitRPS,
		RateLimitBurst: source.RateLimitBurst,
		RevokedAt:      revokedAt,
	}
}
