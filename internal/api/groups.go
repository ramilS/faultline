package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ramilS/faultline/internal/group"
)

const groupBodyLimit = 16 << 10

// GroupService is the group administration behavior required by the HTTP
// layer; *group.Service satisfies it.
type GroupService interface {
	List(ctx context.Context) ([]group.Group, error)
	Find(ctx context.Context, id int64) (group.Detail, error)
	Create(ctx context.Context, name string) (group.Group, error)
	Rename(ctx context.Context, id int64, name string) (group.Group, error)
	Delete(ctx context.Context, id int64) error
	ReplaceMembers(ctx context.Context, id int64, userIDs []int64) ([]int64, error)
	ReplaceProjects(ctx context.Context, id int64, projectIDs []int64) ([]int64, error)
}

type GroupHandler struct {
	service     GroupService
	reportError func(error)
}

func NewGroupHandler(service GroupService, options Options) *GroupHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &GroupHandler{service: service, reportError: reportError}
}

type groupResponse struct {
	ID           int64  `json:"id"`
	Name         string `json:"name"`
	MemberCount  int    `json:"member_count"`
	ProjectCount int    `json:"project_count"`
	CreatedAt    string `json:"created_at"`
}

type groupsResponse struct {
	Groups []groupResponse `json:"groups"`
}

// groupDetailResponse embeds groupResponse so a caller sees the group's
// metadata alongside the exact membership and grant sets, in one response.
type groupDetailResponse struct {
	groupResponse
	UserIDs    []int64 `json:"user_ids"`
	ProjectIDs []int64 `json:"project_ids"`
}

type createGroupRequest struct {
	Name string `json:"name"`
}

type patchGroupRequest struct {
	Name string `json:"name"`
}

type replaceMembersRequest struct {
	UserIDs []int64 `json:"user_ids"`
}

type replaceMembersResponse struct {
	UserIDs []int64 `json:"user_ids"`
}

type replaceProjectsRequest struct {
	ProjectIDs []int64 `json:"project_ids"`
}

type replaceProjectsResponse struct {
	ProjectIDs []int64 `json:"project_ids"`
}

func (h *GroupHandler) ListGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := h.service.List(r.Context())
	if err != nil {
		h.reportError(fmt.Errorf("list groups: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]groupResponse, 0, len(groups))
	for _, current := range groups {
		items = append(items, toGroupResponse(current))
	}
	writeJSON(w, http.StatusOK, groupsResponse{Groups: items})
}

func (h *GroupHandler) CreateGroup(w http.ResponseWriter, r *http.Request) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input createGroupRequest
	if err := decodeSingleJSON(w, r, groupBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	created, err := h.service.Create(r.Context(), input.Name)
	if !h.applyGroupError(w, "create group", err) {
		return
	}
	writeJSON(w, http.StatusCreated, toGroupResponse(created))
}

func (h *GroupHandler) GetGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := groupIDFromPath(w, r)
	if !ok {
		return
	}
	detail, err := h.service.Find(r.Context(), id)
	if !h.applyGroupError(w, "find group", err) {
		return
	}
	writeJSON(w, http.StatusOK, toGroupDetailResponse(detail))
}

func (h *GroupHandler) PatchGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := groupIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input patchGroupRequest
	if err := decodeSingleJSON(w, r, groupBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	renamed, err := h.service.Rename(r.Context(), id, input.Name)
	if !h.applyGroupError(w, "rename group", err) {
		return
	}
	writeJSON(w, http.StatusOK, toGroupResponse(renamed))
}

func (h *GroupHandler) DeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, ok := groupIDFromPath(w, r)
	if !ok {
		return
	}
	err := h.service.Delete(r.Context(), id)
	if !h.applyGroupError(w, "delete group", err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *GroupHandler) PutMembers(w http.ResponseWriter, r *http.Request) {
	id, ok := groupIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input replaceMembersRequest
	if err := decodeSingleJSON(w, r, groupBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	applied, err := h.service.ReplaceMembers(r.Context(), id, input.UserIDs)
	if !h.applyGroupError(w, "replace group members", err) {
		return
	}
	writeJSON(w, http.StatusOK, replaceMembersResponse{UserIDs: normalizeIDs(applied)})
}

func (h *GroupHandler) PutProjects(w http.ResponseWriter, r *http.Request) {
	id, ok := groupIDFromPath(w, r)
	if !ok {
		return
	}
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	var input replaceProjectsRequest
	if err := decodeSingleJSON(w, r, groupBodyLimit, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}
	applied, err := h.service.ReplaceProjects(r.Context(), id, input.ProjectIDs)
	if !h.applyGroupError(w, "replace group projects", err) {
		return
	}
	writeJSON(w, http.StatusOK, replaceProjectsResponse{ProjectIDs: normalizeIDs(applied)})
}

// applyGroupError maps a group-service error to a response and reports
// whether the caller may continue writing a success body (true) or a
// response was already written (false).
func (h *GroupHandler) applyGroupError(w http.ResponseWriter, operation string, err error) bool {
	switch {
	case err == nil:
		return true
	case errors.Is(err, group.ErrNotFound):
		writeError(w, http.StatusNotFound, "group not found")
	case errors.Is(err, group.ErrConflict):
		writeError(w, http.StatusConflict, "group already exists")
	case errors.Is(err, group.ErrInvalidName):
		writeError(w, http.StatusBadRequest, "invalid group name")
	case errors.Is(err, group.ErrUnknownReference):
		writeError(w, http.StatusBadRequest, "unknown user or project id")
	default:
		h.reportError(fmt.Errorf("%s: %w", operation, err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return false
}

func groupIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid group id")
		return 0, false
	}
	return id, true
}

func toGroupResponse(source group.Group) groupResponse {
	return groupResponse{
		ID:           source.ID,
		Name:         source.Name,
		MemberCount:  source.MemberCount,
		ProjectCount: source.ProjectCount,
		CreatedAt:    source.CreatedAt.Format(time.RFC3339),
	}
}

func toGroupDetailResponse(source group.Detail) groupDetailResponse {
	return groupDetailResponse{
		groupResponse: toGroupResponse(source.Group),
		UserIDs:       normalizeIDs(source.UserIDs),
		ProjectIDs:    normalizeIDs(source.ProjectIDs),
	}
}

// normalizeIDs reports a nil id slice as an empty one, so the wire shape for
// an empty set is always "[]", never "null".
func normalizeIDs(ids []int64) []int64 {
	if ids == nil {
		return []int64{}
	}
	return ids
}
