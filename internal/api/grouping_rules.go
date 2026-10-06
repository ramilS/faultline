package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/project"
)

const groupingRuleBodyLimit = 16 << 10

// GroupingRuleService is the grouping-rule CRUD behavior required by the
// HTTP layer; *grouping.RuleService satisfies it.
type GroupingRuleService interface {
	ListRules(ctx context.Context, projectSlug string) ([]grouping.Rule, error)
	CreateRule(ctx context.Context, projectID int64, matcher, template string) (grouping.Rule, error)
	UpdateRule(ctx context.Context, projectID, ruleID int64, matcher, template string) (grouping.Rule, error)
	MoveRule(ctx context.Context, projectID, ruleID int64, direction grouping.MoveDirection) (grouping.Rule, error)
	DeleteRule(ctx context.Context, projectID, ruleID int64) error
}

type GroupingRuleHandler struct {
	service     GroupingRuleService
	projects    ProjectResolver
	reportError func(error)
}

func NewGroupingRuleHandler(service GroupingRuleService, projects ProjectResolver, options Options) *GroupingRuleHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &GroupingRuleHandler{service: service, projects: projects, reportError: reportError}
}

type groupingRuleResponse struct {
	ID        int64  `json:"id"`
	ProjectID int64  `json:"project_id"`
	Position  int    `json:"position"`
	Matcher   string `json:"matcher"`
	Template  string `json:"template"`
}

type groupingRulesResponse struct {
	Rules []groupingRuleResponse `json:"rules"`
}

type createGroupingRuleRequest struct {
	Matcher  string `json:"matcher"`
	Template string `json:"template"`
}

type updateGroupingRuleRequest struct {
	Matcher  string `json:"matcher"`
	Template string `json:"template"`
}

type moveGroupingRuleRequest struct {
	Direction string `json:"direction"`
}

func (h *GroupingRuleHandler) ListRules(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	scope, ok := scopeOrRefuse(w, r)
	if !ok {
		return
	}
	if _, ok := h.resolveProject(w, r, scope, slug); !ok {
		return
	}
	rules, err := h.service.ListRules(r.Context(), slug)
	if err != nil {
		h.reportError(fmt.Errorf("list grouping rules: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	writeJSON(w, http.StatusOK, toGroupingRulesResponse(rules))
}

func (h *GroupingRuleHandler) CreateRule(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input createGroupingRuleRequest
	if err := decodeSingleJSON(w, r, groupingRuleBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	resolved, ok := h.resolveProject(w, r, access.Unrestricted(), r.PathValue("slug"))
	if !ok {
		return
	}
	created, err := h.service.CreateRule(r.Context(), resolved.ID, input.Matcher, input.Template)
	if !h.applyRuleError(w, "create grouping rule", err) {
		return
	}
	writeJSON(w, http.StatusCreated, toGroupingRuleResponse(created))
}

func (h *GroupingRuleHandler) UpdateRule(w http.ResponseWriter, r *http.Request) {
	ruleID, ok := groupingRuleIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input updateGroupingRuleRequest
	if err := decodeSingleJSON(w, r, groupingRuleBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	resolved, ok := h.resolveProject(w, r, access.Unrestricted(), r.PathValue("slug"))
	if !ok {
		return
	}
	updated, err := h.service.UpdateRule(r.Context(), resolved.ID, ruleID, input.Matcher, input.Template)
	if !h.applyRuleError(w, "update grouping rule", err) {
		return
	}
	writeJSON(w, http.StatusOK, toGroupingRuleResponse(updated))
}

func (h *GroupingRuleHandler) MoveRule(w http.ResponseWriter, r *http.Request) {
	ruleID, ok := groupingRuleIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input moveGroupingRuleRequest
	if err := decodeSingleJSON(w, r, groupingRuleBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	direction := grouping.MoveDirection(input.Direction)
	if direction != grouping.MoveUp && direction != grouping.MoveDown {
		writeError(w, http.StatusBadRequest, "invalid move direction")
		return
	}
	resolved, ok := h.resolveProject(w, r, access.Unrestricted(), r.PathValue("slug"))
	if !ok {
		return
	}
	moved, err := h.service.MoveRule(r.Context(), resolved.ID, ruleID, direction)
	if !h.applyRuleError(w, "move grouping rule", err) {
		return
	}
	writeJSON(w, http.StatusOK, toGroupingRuleResponse(moved))
}

func (h *GroupingRuleHandler) DeleteRule(w http.ResponseWriter, r *http.Request) {
	ruleID, ok := groupingRuleIDFromPath(w, r)
	if !ok {
		return
	}
	resolved, ok := h.resolveProject(w, r, access.Unrestricted(), r.PathValue("slug"))
	if !ok {
		return
	}
	err := h.service.DeleteRule(r.Context(), resolved.ID, ruleID)
	if !h.applyRuleError(w, "delete grouping rule", err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// applyRuleError maps a rule-service error to a response and reports whether
// the caller may continue writing a success body (true) or a response was
// already written (false).
func (h *GroupingRuleHandler) applyRuleError(w http.ResponseWriter, operation string, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, grouping.ErrRuleNotFound):
		writeError(w, http.StatusNotFound, "grouping rule not found")
	case errors.Is(err, grouping.ErrInvalidMatcher), errors.Is(err, grouping.ErrInvalidTemplate), errors.Is(err, grouping.ErrTooManyRules):
		writeError(w, http.StatusBadRequest, "invalid grouping rule")
	default:
		h.reportError(fmt.Errorf("%s: %w", operation, err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return false
}

func groupingRuleIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid grouping rule id")
		return 0, false
	}
	return id, true
}

func (h *GroupingRuleHandler) resolveProject(w http.ResponseWriter, r *http.Request, scope access.Scope, slug string) (project.Project, bool) {
	return resolveProjectInScope(w, r, scope, slug, h.projects, h.reportError)
}

func toGroupingRulesResponse(rules []grouping.Rule) groupingRulesResponse {
	items := make([]groupingRuleResponse, 0, len(rules))
	for _, rule := range rules {
		items = append(items, toGroupingRuleResponse(rule))
	}
	return groupingRulesResponse{Rules: items}
}

func toGroupingRuleResponse(rule grouping.Rule) groupingRuleResponse {
	return groupingRuleResponse{ID: rule.ID, ProjectID: rule.ProjectID, Position: rule.Position, Matcher: rule.Matcher, Template: rule.Template}
}
