package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/issuedigest"
)

const digestContentType = "text/markdown; charset=utf-8"

var (
	errDigestEventNotFound = errors.New("digest event not found")
	errInvalidDigestEvent  = errors.New("invalid digest event id")
)

type mergedDigestError struct {
	mergedIntoID int64
}

func (e mergedDigestError) Error() string {
	return fmt.Sprintf("issue merged into %d", e.mergedIntoID)
}

func (h *IssueHandler) IssueDigest(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	digest, err := renderIssueDigest(r.Context(), h.service, scope, id, r.URL.Query().Get("event"))
	if merged, isMerged := errors.AsType[mergedDigestError](err); isMerged {
		writeJSON(w, http.StatusOK, mergedIssueResponse{MergedIntoID: merged.mergedIntoID})
		return
	}
	switch {
	case errors.Is(err, errInvalidDigestEvent):
		writeError(w, http.StatusBadRequest, "invalid event id")
	case errors.Is(err, issue.ErrNotFound):
		writeError(w, http.StatusNotFound, "issue not found")
	case errors.Is(err, errDigestEventNotFound):
		writeError(w, http.StatusNotFound, "event not found")
	case err != nil:
		h.reportError(fmt.Errorf("issue digest: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	default:
		writeDigest(w, digest)
	}
}

func renderIssueDigest(ctx context.Context, service IssueService, scope access.Scope, issueID int64, rawEventID string) (string, error) {
	var eventID *uuid.UUID
	if rawEventID != "" {
		parsed, err := uuid.Parse(rawEventID)
		if err != nil {
			return "", errInvalidDigestEvent
		}
		eventID = &parsed
	}
	detail, err := service.IssueDetail(ctx, scope, issueID)
	if err != nil {
		return "", err
	}
	if detail.Issue.MergedIntoID != nil {
		return "", mergedDigestError{mergedIntoID: *detail.Issue.MergedIntoID}
	}
	event, err := digestEvent(ctx, service, scope, issueID, eventID)
	if err != nil {
		return "", fmt.Errorf("digest event: %w", err)
	}
	if eventID != nil && event == nil {
		return "", errDigestEventNotFound
	}
	return issuedigest.Render(detail, event), nil
}

func digestEvent(ctx context.Context, service IssueService, scope access.Scope, issueID int64, eventID *uuid.UUID) (*issue.EventDetail, error) {
	var (
		event issue.EventDetail
		err   error
	)
	if eventID == nil {
		event, err = service.LatestEvent(ctx, scope, issueID)
	} else {
		event, err = service.Event(ctx, scope, *eventID)
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

func writeDigest(w http.ResponseWriter, digest string) {
	w.Header().Set("Content-Type", digestContentType)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	//nolint:gosec // G705: event text is served as text/markdown with nosniff, so a browser never renders it as HTML.
	_, _ = w.Write([]byte(digest))
}
