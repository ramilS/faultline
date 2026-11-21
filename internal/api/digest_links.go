package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/digestlink"
	"github.com/ramilS/faultline/internal/issue"
)

const (
	digestLinkBodyLimit = 1 << 10
	digestLinkPath      = "/api/v1/digest-links/"
)

type DigestLinkService interface {
	Create(ctx context.Context, scope access.Scope, issueID int64, createdBy *int64, ttl string) (string, time.Time, error)
	Resolve(ctx context.Context, raw string) (digestlink.Target, error)
	RevokeAll(ctx context.Context, scope access.Scope, issueID int64) error
}

type DigestLinkHandler struct {
	links       DigestLinkService
	issues      IssueService
	publicURL   string
	reportError func(error)
}

func NewDigestLinkHandler(links DigestLinkService, issues IssueService, publicURL string, options Options) *DigestLinkHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &DigestLinkHandler{links: links, issues: issues, publicURL: publicURL, reportError: reportError}
}

type createDigestLinkRequest struct {
	TTL string `json:"ttl"`
}

type digestLinkResponse struct {
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"`
}

func (h *DigestLinkHandler) Create(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input createDigestLinkRequest
	if err := decodeSingleJSON(w, r, digestLinkBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	var createdBy *int64
	if user, hasSession := UserFromContext(r.Context()); hasSession {
		createdBy = &user.ID
	}
	raw, expiresAt, err := h.links.Create(r.Context(), scope, id, createdBy, input.TTL)
	switch {
	case errors.Is(err, digestlink.ErrInvalidTTL):
		writeError(w, http.StatusBadRequest, "ttl must be one of 1h, 24h, 7d")
	case errors.Is(err, digestlink.ErrNotFound):
		writeError(w, http.StatusNotFound, "issue not found")
	case err != nil:
		h.reportError(fmt.Errorf("create digest link: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	default:
		writeJSON(w, http.StatusCreated, digestLinkResponse{
			URL:       h.baseURL(r) + digestLinkPath + url.PathEscape(raw),
			ExpiresAt: expiresAt.UTC().Format(time.RFC3339),
		})
	}
}

func (h *DigestLinkHandler) RevokeAll(w http.ResponseWriter, r *http.Request) {
	id, ok := issueIDFromPath(w, r)
	if !ok {
		return
	}
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	err := h.links.RevokeAll(r.Context(), scope, id)
	switch {
	case errors.Is(err, digestlink.ErrNotFound):
		writeError(w, http.StatusNotFound, "issue not found")
	case err != nil:
		h.reportError(fmt.Errorf("revoke digest links: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *DigestLinkHandler) Digest(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	target, err := h.links.Resolve(r.Context(), r.PathValue("token"))
	if errors.Is(err, digestlink.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("resolve digest link: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	scope := access.Limited([]int64{target.ProjectID})
	digest, err := renderIssueDigest(r.Context(), h.issues, scope, target.IssueID, r.URL.Query().Get("event"))
	if _, isMerged := errors.AsType[mergedDigestError](err); isMerged ||
		errors.Is(err, issue.ErrNotFound) || errors.Is(err, errDigestEventNotFound) || errors.Is(err, errInvalidDigestEvent) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("digest link: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeDigest(w, digest)
}

func (h *DigestLinkHandler) baseURL(r *http.Request) string {
	if h.publicURL != "" {
		return strings.TrimRight(h.publicURL, "/")
	}
	if scheme, ok := requestScheme(r); ok {
		return scheme + "://" + r.Host
	}
	return "//" + r.Host
}
