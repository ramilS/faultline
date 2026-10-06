package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
)

const artifactTokenBodyLimit = 16 << 10

// ArtifactService is the artifact-token lifecycle behavior required by the HTTP layer.
type ArtifactService interface {
	Create(context.Context, int64, string) (artifactauth.Token, string, error)
	List(context.Context, string) ([]artifactauth.Token, error)
	Revoke(context.Context, string, int64) error
	Authenticate(context.Context, string) (artifactauth.Token, error)
}

// ProjectResolver resolves a route slug to its project, scoped by the
// caller's access, without coupling its handlers to unrelated project and
// ingest-key operations.
type ProjectResolver interface {
	FindProject(context.Context, access.Scope, string) (project.Project, error)
}

type ArtifactHandler struct {
	service     ArtifactService
	projects    ProjectResolver
	sourceMaps  sourcemap.Repository
	reportError func(error)
}

// artifactQuotaMessage is what a client is told when an upload would take the
// project past its stored source-map and mapping quota.
const artifactQuotaMessage = "project artifact storage quota exceeded"

func NewArtifactHandler(service ArtifactService, projects ProjectResolver, sourceMaps sourcemap.Repository, options Options) *ArtifactHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &ArtifactHandler{service: service, projects: projects, sourceMaps: sourceMaps, reportError: reportError}
}

type createArtifactTokenRequest struct {
	Name string `json:"name"`
}

type artifactTokenResponse struct {
	ID          int64   `json:"id"`
	ProjectID   int64   `json:"project_id"`
	Name        string  `json:"name"`
	TokenPrefix string  `json:"token_prefix"`
	CreatedAt   string  `json:"created_at"`
	LastUsedAt  *string `json:"last_used_at"`
	RevokedAt   *string `json:"revoked_at"`
}

type artifactTokensResponse struct {
	Tokens []artifactTokenResponse `json:"tokens"`
}

type sourceMapResponse struct {
	ID                int64  `json:"id"`
	ReleaseID         int64  `json:"release_id"`
	GeneratedPath     string `json:"generated_path"`
	ContentSHA256     string `json:"content_sha256"`
	UncompressedBytes int64  `json:"uncompressed_bytes"`
	UploadedAt        string `json:"uploaded_at"`
}

// createArtifactTokenResponse is the only API response that contains raw token material.
type createArtifactTokenResponse struct {
	Token    artifactTokenResponse `json:"token"`
	RawToken string                `json:"raw_token"`
}

func (h *ArtifactHandler) ListTokens(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	if _, ok := h.resolveProject(w, r, scope, slug); !ok {
		return
	}
	tokens, err := h.service.List(r.Context(), slug)
	if err != nil {
		h.reportError(fmt.Errorf("list artifact tokens: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]artifactTokenResponse, 0, len(tokens))
	for _, token := range tokens {
		items = append(items, toArtifactTokenResponse(token))
	}
	writeJSON(w, http.StatusOK, artifactTokensResponse{Tokens: items})
}

func (h *ArtifactHandler) CreateToken(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input createArtifactTokenRequest
	if err := decodeSingleJSON(w, r, artifactTokenBodyLimit, &input); err != nil || !validArtifactTokenName(input.Name) {
		writeError(w, http.StatusBadRequest, "invalid artifact token")
		return
	}
	resolved, ok := h.resolveProject(w, r, access.Unrestricted(), r.PathValue("slug"))
	if !ok {
		return
	}
	token, raw, err := h.service.Create(r.Context(), resolved.ID, input.Name)
	if err != nil {
		h.reportError(fmt.Errorf("create artifact token: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusCreated, createArtifactTokenResponse{Token: toArtifactTokenResponse(token), RawToken: raw})
}

func (h *ArtifactHandler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	tokenID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || tokenID <= 0 {
		writeError(w, http.StatusBadRequest, "invalid artifact token id")
		return
	}
	if _, ok := h.resolveProject(w, r, access.Unrestricted(), r.PathValue("slug")); !ok {
		return
	}
	if err := h.service.Revoke(r.Context(), r.PathValue("slug"), tokenID); errors.Is(err, artifactauth.ErrNotFound) {
		writeError(w, http.StatusNotFound, "artifact token not found")
		return
	} else if err != nil {
		h.reportError(fmt.Errorf("revoke artifact token: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// UploadSourceMap accepts one immutable release-scoped map authenticated with a
// project artifact bearer token. It is deliberately public to browser upload
// clients and must never be wrapped in session middleware.
func (h *ArtifactHandler) UploadSourceMap(w http.ResponseWriter, r *http.Request) {
	rawToken, ok := bearerToken(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid artifact token")
		return
	}
	if h.service == nil {
		h.reportError(errors.New("artifact service is not configured"))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	token, err := h.service.Authenticate(r.Context(), rawToken)
	if errors.Is(err, artifactauth.ErrUnauthorized) {
		writeError(w, http.StatusUnauthorized, "invalid artifact token")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("authenticate artifact token: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	// This route is gatePublic: it authenticates on the artifact bearer token
	// above, never on a session, so RequireSession never ran and there is no
	// group scope in the request context to read. The token-to-project check
	// below is the actual authorization for this route.
	resolved, ok := h.resolveProject(w, r, access.Unrestricted(), r.PathValue("slug"))
	if !ok {
		return
	}
	if token.ProjectID != resolved.ID {
		writeError(w, http.StatusForbidden, "artifact token does not belong to project")
		return
	}
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		writeError(w, http.StatusBadRequest, "invalid source map upload")
		return
	}
	release, validRelease := validSourceMapRelease(r.URL.Query().Get("release"))
	rawPath := r.URL.Query().Get("path")
	if !validRelease || utf8.RuneCountInString(rawPath) == 0 || utf8.RuneCountInString(rawPath) > 2048 {
		writeError(w, http.StatusBadRequest, "invalid source map upload")
		return
	}
	generatedPath, err := sourcemap.NormalizePath(rawPath)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid source map upload")
		return
	}
	upload, err := sourcemap.DecodeUpload(r.Body, r.Header.Get("Content-Encoding"))
	if errors.Is(err, sourcemap.ErrTooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "source map too large")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid source map upload")
		return
	}
	if h.sourceMaps == nil {
		h.reportError(errors.New("source map repository is not configured"))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	stored, inserted, err := h.sourceMaps.Put(r.Context(), resolved.ID, release, generatedPath, upload)
	if errors.Is(err, sourcemap.ErrConflict) {
		writeError(w, http.StatusConflict, "source map already exists with different content")
		return
	}
	if errors.Is(err, storage.ErrArtifactQuotaExceeded) {
		writeError(w, http.StatusRequestEntityTooLarge, artifactQuotaMessage)
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("put source map: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if inserted {
		writeJSON(w, http.StatusCreated, toSourceMapResponse(stored))
		return
	}
	writeJSON(w, http.StatusOK, toSourceMapResponse(stored))
}

func validSourceMapRelease(value string) (string, bool) {
	value = strings.TrimSpace(value)
	return value, value != "" && utf8.RuneCountInString(value) <= 200
}

func isJSONContentType(value string) bool {
	mediaType, _, err := mime.ParseMediaType(value)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func bearerToken(r *http.Request) (string, bool) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "Bearer ") {
		return "", false
	}
	raw := strings.TrimPrefix(values[0], "Bearer ")
	if raw == "" || strings.ContainsAny(raw, " \t\r\n") {
		return "", false
	}
	return raw, true
}

func toSourceMapResponse(source sourcemap.Map) sourceMapResponse {
	return sourceMapResponse{
		ID:                source.ID,
		ReleaseID:         source.ReleaseID,
		GeneratedPath:     source.GeneratedPath,
		ContentSHA256:     hex.EncodeToString(source.ContentSHA256[:]),
		UncompressedBytes: source.UncompressedBytes,
		UploadedAt:        source.UploadedAt.Format(time.RFC3339),
	}
}

func (h *ArtifactHandler) resolveProject(w http.ResponseWriter, r *http.Request, scope access.Scope, slug string) (project.Project, bool) {
	return resolveProjectInScope(w, r, scope, slug, h.projects, h.reportError)
}

func validArtifactTokenName(name string) bool {
	name = strings.TrimSpace(name)
	return name != "" && utf8.RuneCountInString(name) <= 100
}

func toArtifactTokenResponse(source artifactauth.Token) artifactTokenResponse {
	return artifactTokenResponse{
		ID:          source.ID,
		ProjectID:   source.ProjectID,
		Name:        source.Name,
		TokenPrefix: source.TokenPrefix,
		CreatedAt:   source.CreatedAt.Format(time.RFC3339),
		LastUsedAt:  formatArtifactTokenTime(source.LastUsedAt),
		RevokedAt:   formatArtifactTokenTime(source.RevokedAt),
	}
}

func formatArtifactTokenTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := value.Format(time.RFC3339)
	return &formatted
}
