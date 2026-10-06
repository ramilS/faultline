package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/ramilS/faultline/internal/scrub"
)

// GlobalDataScrubbingService is the instance-wide scrubbing-rule behavior the
// HTTP layer needs; *scrub.Service satisfies it.
type GlobalDataScrubbingService interface {
	GetGlobal(ctx context.Context) ([]scrub.Rule, error)
	UpdateGlobal(ctx context.Context, rules []scrub.Rule) ([]scrub.Rule, error)
}

type GlobalDataScrubbingHandler struct {
	service     GlobalDataScrubbingService
	reportError func(error)
}

func NewGlobalDataScrubbingHandler(service GlobalDataScrubbingService, options Options) *GlobalDataScrubbingHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &GlobalDataScrubbingHandler{service: service, reportError: reportError}
}

type globalDataScrubbingDocument struct {
	Rules []scrub.Rule `json:"rules"`
}

func (h *GlobalDataScrubbingHandler) Get(w http.ResponseWriter, r *http.Request) {
	rules, err := h.service.GetGlobal(r.Context())
	if err != nil {
		h.reportError(fmt.Errorf("get global data scrubbing: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, globalDataScrubbingDocument{Rules: nonNilRules(rules)})
}

// Update replaces the whole rule list; a body without "rules" is refused so an
// empty document cannot silently clear the instance's rules.
func (h *GlobalDataScrubbingHandler) Update(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input globalDataScrubbingDocument
	if err := decodeSingleJSON(w, r, dataScrubbingBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	if input.Rules == nil {
		writeError(w, http.StatusBadRequest, "rules is required")
		return
	}
	stored, err := h.service.UpdateGlobal(r.Context(), input.Rules)
	if errors.Is(err, scrub.ErrInvalidConfig) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		h.reportError(fmt.Errorf("update global data scrubbing: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, globalDataScrubbingDocument{Rules: nonNilRules(stored)})
}

func nonNilRules(rules []scrub.Rule) []scrub.Rule {
	if rules == nil {
		return []scrub.Rule{}
	}
	return rules
}
