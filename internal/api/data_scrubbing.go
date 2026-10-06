package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
)

const dataScrubbingBodyLimit = 64 << 10

// DataScrubbingService is the data-scrubbing configuration behavior required
// by the HTTP layer; *scrub.Service satisfies it.
type DataScrubbingService interface {
	Get(ctx context.Context, projectID int64) (scrub.Config, error)
	Update(ctx context.Context, projectID int64, config scrub.Config) (scrub.Config, error)
}

type DataScrubbingHandler struct {
	service     DataScrubbingService
	projects    ProjectResolver
	reportError func(error)
}

func NewDataScrubbingHandler(service DataScrubbingService, projects ProjectResolver, options Options) *DataScrubbingHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &DataScrubbingHandler{service: service, projects: projects, reportError: reportError}
}

type dataScrubbingResponse struct {
	Enabled     bool         `json:"enabled"`
	ExtraFields []string     `json:"extra_fields"`
	SafeFields  []string     `json:"safe_fields"`
	Rules       []scrub.Rule `json:"rules"`
}

// updateDataScrubbingRequest uses pointers and nil-able slices so an omitted
// field is distinguishable from a zero value: a body missing "enabled" must be
// refused, not stored as "scrubbing off".
type updateDataScrubbingRequest struct {
	Enabled     *bool        `json:"enabled"`
	ExtraFields []string     `json:"extra_fields"`
	SafeFields  []string     `json:"safe_fields"`
	Rules       []scrub.Rule `json:"rules"`
}

func (r updateDataScrubbingRequest) complete() bool {
	return r.Enabled != nil && r.ExtraFields != nil && r.SafeFields != nil && r.Rules != nil
}

func (h *DataScrubbingHandler) Get(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	resolved, ok := resolveProjectInScope(w, r, scope, r.PathValue("slug"), h.projects, h.reportError)
	if !ok {
		return
	}
	config, err := h.service.Get(r.Context(), resolved.ID)
	if err != nil {
		h.reportError(fmt.Errorf("get data scrubbing: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toDataScrubbingResponse(config))
}

// Update replaces the whole configuration; a body missing any of the four
// fields is refused, so a partial document cannot silently disable scrubbing.
func (h *DataScrubbingHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input updateDataScrubbingRequest
	if err := decodeSingleJSON(w, r, dataScrubbingBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if !input.complete() {
		writeError(w, http.StatusBadRequest, "enabled, extra_fields, safe_fields and rules are all required")
		return
	}
	resolved, ok := resolveProjectInScope(w, r, access.Unrestricted(), r.PathValue("slug"), h.projects, h.reportError)
	if !ok {
		return
	}
	stored, err := h.service.Update(r.Context(), resolved.ID, scrub.Config{Enabled: *input.Enabled, ExtraFields: input.ExtraFields, SafeFields: input.SafeFields, Rules: input.Rules})
	if errors.Is(err, scrub.ErrInvalidConfig) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if errors.Is(err, project.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("update data scrubbing: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toDataScrubbingResponse(stored))
}

func toDataScrubbingResponse(config scrub.Config) dataScrubbingResponse {
	response := dataScrubbingResponse{Enabled: config.Enabled, ExtraFields: config.ExtraFields, SafeFields: config.SafeFields, Rules: nonNilRules(config.Rules)}
	if response.ExtraFields == nil {
		response.ExtraFields = []string{}
	}
	if response.SafeFields == nil {
		response.SafeFields = []string{}
	}
	return response
}
