package api

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/issuedigest"
)

const digestContentType = "text/markdown; charset=utf-8"

func (h *IssueHandler) IssueDigest(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	var eventID *uuid.UUID
	if raw := r.URL.Query().Get("event"); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid event id")
			return
		}
		eventID = &parsed
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	detail, ok := h.loadDetail(w, r, scope, id, "issue digest")
	if !ok {
		return
	}
	if detail.Issue.MergedIntoID != nil {
		writeJSON(w, http.StatusOK, mergedIssueResponse{MergedIntoID: *detail.Issue.MergedIntoID})
		return
	}
	event, err := h.digestEvent(r, scope, id, eventID)
	if err != nil {
		h.reportError(fmt.Errorf("issue digest event: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	if eventID != nil && event == nil {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	w.Header().Set("Content-Type", digestContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: event text is served as text/markdown with nosniff, so a browser never renders it as HTML.
	_, _ = w.Write([]byte(issuedigest.Render(detail, event)))
}

func (h *IssueHandler) digestEvent(r *http.Request, scope access.Scope, issueID int64, eventID *uuid.UUID) (*issue.EventDetail, error) {
	var (
		event issue.EventDetail
		err   error
	)
	if eventID == nil {
		event, err = h.service.LatestEvent(r.Context(), scope, issueID)
	} else {
		event, err = h.service.Event(r.Context(), scope, *eventID)
	}
	if errors.Is(err, issue.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if event.IssueID != issueID {
		return nil, nil
	}
	return &event, nil
}
