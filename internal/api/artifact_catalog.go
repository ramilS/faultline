package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ramilS/faultline/internal/artifactcatalog"
)

const (
	defaultArtifactCatalogLimit = 50
	maxArtifactCatalogLimit     = 100
)

type ArtifactCatalogHandler struct {
	catalog     artifactcatalog.Repository
	projects    ProjectResolver
	reportError func(error)
}

func NewArtifactCatalogHandler(catalog artifactcatalog.Repository, projects ProjectResolver, options Options) *ArtifactCatalogHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &ArtifactCatalogHandler{catalog: catalog, projects: projects, reportError: reportError}
}

type artifactCatalogResponse struct {
	Artifacts  []artifactCatalogItemResponse `json:"artifacts"`
	NextCursor *string                       `json:"next_cursor"`
}

type artifactCatalogItemResponse struct {
	Type          artifactcatalog.Type               `json:"type"`
	ContentSHA256 string                             `json:"content_sha256,omitempty"`
	MappingUUID   string                             `json:"mapping_uuid,omitempty"`
	SizeBytes     int64                              `json:"size_bytes"`
	UploadedAt    string                             `json:"uploaded_at"`
	LastUsedAt    *string                            `json:"last_used_at"`
	References    []artifactCatalogReferenceResponse `json:"references,omitempty"`
}

type artifactCatalogReferenceResponse struct {
	Release       string `json:"release,omitempty"`
	GeneratedPath string `json:"generated_path,omitempty"`
	DebugID       string `json:"debug_id,omitempty"`
	CPUName       string `json:"cpu_name,omitempty"`
}

func (h *ArtifactCatalogHandler) List(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	resolved, ok := resolveProjectInScope(w, r, scope, r.PathValue("slug"), h.projects, h.reportError)
	if !ok {
		return
	}
	options, ok := parseArtifactCatalogOptions(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "invalid artifact inventory query")
		return
	}
	page, err := h.catalog.List(r.Context(), resolved.ID, options)
	if errors.Is(err, artifactcatalog.ErrInvalidCursor) {
		writeError(w, http.StatusBadRequest, "invalid artifact inventory cursor")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("list artifact inventory: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]artifactCatalogItemResponse, 0, len(page.Artifacts))
	for _, artifact := range page.Artifacts {
		items = append(items, toArtifactCatalogResponse(artifact))
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	writeJSON(w, http.StatusOK, artifactCatalogResponse{Artifacts: items, NextCursor: next})
}

func parseArtifactCatalogOptions(r *http.Request) (artifactcatalog.ListOptions, bool) {
	query := r.URL.Query()
	options := artifactcatalog.ListOptions{Limit: defaultArtifactCatalogLimit}
	if values, exists := query["cursor"]; exists {
		if len(values) != 1 || values[0] == "" {
			return artifactcatalog.ListOptions{}, false
		}
		options.Cursor = values[0]
	}
	if values, exists := query["type"]; exists {
		if len(values) != 1 || values[0] == "" {
			return artifactcatalog.ListOptions{}, false
		}
		options.Type = artifactcatalog.Type(values[0])
		if !options.Type.Valid() {
			return artifactcatalog.ListOptions{}, false
		}
	}
	if values, exists := query["limit"]; exists {
		if len(values) != 1 {
			return artifactcatalog.ListOptions{}, false
		}
		limit, err := strconv.Atoi(values[0])
		if err != nil || limit <= 0 || limit > maxArtifactCatalogLimit {
			return artifactcatalog.ListOptions{}, false
		}
		options.Limit = limit
	}
	return options, true
}

func toArtifactCatalogResponse(artifact artifactcatalog.Artifact) artifactCatalogItemResponse {
	response := artifactCatalogItemResponse{
		Type: artifact.Type, ContentSHA256: artifact.ContentSHA256, MappingUUID: artifact.MappingUUID,
		SizeBytes: artifact.SizeBytes, UploadedAt: artifact.UploadedAt.UTC().Format(time.RFC3339Nano),
	}
	if artifact.LastUsedAt != nil {
		value := artifact.LastUsedAt.UTC().Format(time.RFC3339Nano)
		response.LastUsedAt = &value
	}
	for _, ref := range artifact.SourceMapRefs {
		response.References = append(response.References, artifactCatalogReferenceResponse{Release: ref.Release, GeneratedPath: ref.GeneratedPath, DebugID: ref.DebugID})
	}
	for _, ref := range artifact.NativeDebugIDRefs {
		response.References = append(response.References, artifactCatalogReferenceResponse{DebugID: ref.DebugID, CPUName: ref.CPUName})
	}
	return response
}
