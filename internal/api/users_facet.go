package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
)

// UsersFacetService is the affected-users read behavior the HTTP layer needs.
type UsersFacetService interface {
	IssueUsers(context.Context, access.Scope, int64, int) (issue.UserFacet, error)
}

type UsersFacetHandler struct {
	service     UsersFacetService
	reportError func(error)
}

func NewUsersFacetHandler(service UsersFacetService, options Options) *UsersFacetHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &UsersFacetHandler{service: service, reportError: reportError}
}

type userIdentityResponse struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	TimesSeen int64  `json:"times_seen"`
	LastSeen  string `json:"last_seen"`
}

// issueUsersResponse is the affected-users envelope. Saturated is derived
// from Total, never from len(Users): it tells the caller whether the issue's
// full distinct-identity count has hit ingest.MaxUsersPerIssue, a fact about
// the issue, not about how many rows this particular page happened to
// return. It exists so the frontend can render "10 000+" without importing
// the cap constant itself — the cap is a server-side decision.
type issueUsersResponse struct {
	Users     []userIdentityResponse `json:"users"`
	Total     int64                  `json:"total"`
	Saturated bool                   `json:"saturated"`
}

func (h *UsersFacetHandler) IssueUsers(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	limit, ok := parseFacetLimit(w, r)
	if !ok {
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	facet, err := h.service.IssueUsers(r.Context(), scope, id, limit)
	if errors.Is(err, issue.ErrNotFound) {
		writeError(w, http.StatusNotFound, "issue not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("issue users: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]userIdentityResponse, 0, len(facet.Users))
	for _, identity := range facet.Users {
		items = append(items, userIdentityResponse{
			Key:       identity.Key,
			Value:     identity.Value,
			TimesSeen: identity.TimesSeen,
			LastSeen:  identity.LastSeen.Format(time.RFC3339),
		})
	}
	writeJSON(w, http.StatusOK, issueUsersResponse{
		Users:     items,
		Total:     facet.Total,
		Saturated: facet.Total >= ingest.MaxUsersPerIssue,
	})
}

// parseFacetLimit reads the optional limit query parameter the way
// IssueEvents does: absent means "let the service apply its default",
// negative or non-numeric is a caller error, and anything else (including
// zero and values above the service's cap) is passed through for the service
// to normalize.
func parseFacetLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0, true
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		writeError(w, http.StatusBadRequest, "invalid limit")
		return 0, false
	}
	return limit, true
}
