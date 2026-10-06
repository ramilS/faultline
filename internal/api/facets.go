package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
)

// defaultFacetLimit bounds a value list. Autocomplete shows a handful; the cap
// exists so a high-cardinality key cannot return its whole 200-value retention
// on every keystroke.
const defaultFacetLimit = 100

// FacetService is the tag vocabulary behaviour the HTTP layer needs.
type FacetService interface {
	TagKeys(context.Context, access.Scope, string) ([]issue.TagKey, error)
	TagValues(context.Context, access.Scope, string, string, string, int) ([]issue.TagValue, error)
}

type FacetHandler struct {
	service     FacetService
	reportError func(error)
}

func NewFacetHandler(service FacetService, options Options) *FacetHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &FacetHandler{service: service, reportError: reportError}
}

type tagKeyResponse struct {
	Key        string `json:"key"`
	ValueCount int64  `json:"value_count"`
	TimesSeen  int64  `json:"times_seen"`
}

func (h *FacetHandler) TagKeys(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	keys, err := h.service.TagKeys(r.Context(), scope, r.PathValue("slug"))
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("tag keys: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]tagKeyResponse, 0, len(keys))
	for _, key := range keys {
		items = append(items, tagKeyResponse{Key: key.Key, ValueCount: key.ValueCount, TimesSeen: key.TimesSeen})
	}
	writeJSON(w, http.StatusOK, struct {
		Tags []tagKeyResponse `json:"tags"`
	}{Tags: items})
}

func (h *FacetHandler) TagValues(w http.ResponseWriter, r *http.Request) {
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	values, err := h.service.TagValues(r.Context(), scope, r.PathValue("slug"),
		r.PathValue("key"), r.URL.Query().Get("query"), defaultFacetLimit)
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "project not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("tag values: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]tagValueResponse, 0, len(values))
	for _, value := range values {
		items = append(items, tagValueResponse{
			Value:     value.Value,
			TimesSeen: value.TimesSeen,
			FirstSeen: value.FirstSeen.Format(time.RFC3339),
			LastSeen:  value.LastSeen.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, struct {
		Values []tagValueResponse `json:"values"`
	}{Values: items})
}
