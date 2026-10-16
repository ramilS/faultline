package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/project"
)

const notificationChannelBodyLimit = 16 << 10

// NotificationService is the channel behavior required by the HTTP layer;
// *notify.Service satisfies it.
type NotificationService interface {
	List(ctx context.Context, projectID int64) ([]notify.Channel, error)
	Create(ctx context.Context, projectID int64, input notify.ChannelInput) (notify.Channel, error)
	Update(ctx context.Context, projectID, channelID int64, input notify.ChannelInput) (notify.Channel, error)
	Delete(ctx context.Context, projectID, channelID int64) error
	SendTest(ctx context.Context, projectID, channelID int64, project notify.ProjectRef) error
}

// NotificationHandler serves a project's notification channels. Every route
// is owner-only: a webhook URL can itself be a credential, so even listing
// channels is not for members.
type NotificationHandler struct {
	service     NotificationService
	projects    ProjectResolver
	reportError func(error)
}

func NewNotificationHandler(service NotificationService, projects ProjectResolver, options Options) *NotificationHandler {
	reportError := options.ReportError
	if reportError == nil {
		reportError = func(error) {}
	}
	return &NotificationHandler{service: service, projects: projects, reportError: reportError}
}

type notificationChannelResponse struct {
	ID               int64      `json:"id"`
	Name             string     `json:"name"`
	Kind             string     `json:"kind"`
	Target           string     `json:"target"`
	HasSecret        bool       `json:"has_secret"`
	Enabled          bool       `json:"enabled"`
	NotifyNew        bool       `json:"notify_new"`
	NotifyRegression bool       `json:"notify_regression"`
	Environments     []string   `json:"environments"`
	MinLevel         string     `json:"min_level"`
	LastSuccessAt    *time.Time `json:"last_success_at"`
	LastError        *string    `json:"last_error"`
	LastErrorAt      *time.Time `json:"last_error_at"`
	CreatedAt        time.Time  `json:"created_at"`
}

type notificationChannelsResponse struct {
	Channels []notificationChannelResponse `json:"channels"`
}

// notificationChannelRequest serves create and update. Pointers and a
// nil-able slice make an omitted field distinguishable from false or empty,
// so a partial body is refused instead of switching a filter off. Secret
// stays optional: on update, omitting it keeps the stored one.
type notificationChannelRequest struct {
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Target           string   `json:"target"`
	Secret           *string  `json:"secret"`
	Enabled          *bool    `json:"enabled"`
	NotifyNew        *bool    `json:"notify_new"`
	NotifyRegression *bool    `json:"notify_regression"`
	Environments     []string `json:"environments"`
	MinLevel         string   `json:"min_level"`
}

func (r notificationChannelRequest) complete() bool {
	return r.Enabled != nil && r.NotifyNew != nil && r.NotifyRegression != nil && r.Environments != nil
}

func (r notificationChannelRequest) input() notify.ChannelInput {
	return notify.ChannelInput{
		Name:             r.Name,
		Kind:             notify.Kind(r.Kind),
		Target:           r.Target,
		Secret:           r.Secret,
		Enabled:          *r.Enabled,
		NotifyNew:        *r.NotifyNew,
		NotifyRegression: *r.NotifyRegression,
		Environments:     r.Environments,
		MinLevel:         r.MinLevel,
	}
}

func (h *NotificationHandler) List(w http.ResponseWriter, r *http.Request) {
	resolved, ok := h.resolveProject(w, r)
	if !ok {
		return
	}
	channels, err := h.service.List(r.Context(), resolved.ID)
	if err != nil {
		h.reportError(fmt.Errorf("list notification channels: %w", err))
		writeError(w, http.StatusInternalServerError, "internal server error")
		return
	}
	items := make([]notificationChannelResponse, 0, len(channels))
	for _, channel := range channels {
		items = append(items, toNotificationChannelResponse(channel))
	}
	writeJSON(w, http.StatusOK, notificationChannelsResponse{Channels: items})
}

func (h *NotificationHandler) Create(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeNotificationChannel(w, r)
	if !ok {
		return
	}
	resolved, ok := h.resolveProject(w, r)
	if !ok {
		return
	}
	created, err := h.service.Create(r.Context(), resolved.ID, input)
	if !h.applyError(w, "create notification channel", err) {
		return
	}
	writeJSON(w, http.StatusCreated, toNotificationChannelResponse(created))
}

func (h *NotificationHandler) Update(w http.ResponseWriter, r *http.Request) {
	channelID, ok := notificationChannelIDFromPath(w, r)
	if !ok {
		return
	}
	input, ok := decodeNotificationChannel(w, r)
	if !ok {
		return
	}
	resolved, ok := h.resolveProject(w, r)
	if !ok {
		return
	}
	updated, err := h.service.Update(r.Context(), resolved.ID, channelID, input)
	if !h.applyError(w, "update notification channel", err) {
		return
	}
	writeJSON(w, http.StatusOK, toNotificationChannelResponse(updated))
}

func (h *NotificationHandler) Delete(w http.ResponseWriter, r *http.Request) {
	channelID, ok := notificationChannelIDFromPath(w, r)
	if !ok {
		return
	}
	resolved, ok := h.resolveProject(w, r)
	if !ok {
		return
	}
	if !h.applyError(w, "delete notification channel", h.service.Delete(r.Context(), resolved.ID, channelID)) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// SendTest answers 502 with the destination's own error when the test
// message is not accepted: the request itself was fine, the upstream refused
// it, and the owner needs that reason to fix the configuration.
func (h *NotificationHandler) SendTest(w http.ResponseWriter, r *http.Request) {
	channelID, ok := notificationChannelIDFromPath(w, r)
	if !ok {
		return
	}
	resolved, ok := h.resolveProject(w, r)
	if !ok {
		return
	}
	err := h.service.SendTest(r.Context(), resolved.ID, channelID, notify.ProjectRef{Slug: resolved.Slug, Name: resolved.Name, IconEmoji: resolved.IconEmoji})
	if failure, ok := errors.AsType[*notify.SendError](err); ok {
		writeError(w, http.StatusBadGateway, failure.Message)
		return
	}
	if !h.applyError(w, "send test notification", err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// applyError maps a service error to a response and reports whether the
// caller may still write its success body.
func (h *NotificationHandler) applyError(w http.ResponseWriter, operation string, err error) bool {
	var invalid *notify.ValidationError
	switch {
	case err == nil:
		return true
	case errors.As(err, &invalid):
		writeError(w, http.StatusBadRequest, invalid.Message)
	case errors.Is(err, notify.ErrTooManyChannels):
		writeError(w, http.StatusBadRequest, fmt.Sprintf("a project can have at most %d notification channels", notify.MaxChannelsPerProject))
	case errors.Is(err, notify.ErrChannelNotFound):
		writeError(w, http.StatusNotFound, "notification channel not found")
	case errors.Is(err, project.ErrNotFound):
		writeError(w, http.StatusNotFound, "project not found")
	default:
		h.reportError(fmt.Errorf("%s: %w", operation, err))
		writeError(w, http.StatusInternalServerError, "internal server error")
	}
	return false
}

func (h *NotificationHandler) resolveProject(w http.ResponseWriter, r *http.Request) (project.Project, bool) {
	return resolveProjectInScope(w, r, access.Unrestricted(), r.PathValue("slug"), h.projects, h.reportError)
}

func decodeNotificationChannel(w http.ResponseWriter, r *http.Request) (notify.ChannelInput, bool) {
	if !declaredJSONBody(r) {
		writeError(w, http.StatusBadRequest, "invalid request")
		return notify.ChannelInput{}, false
	}
	var request notificationChannelRequest
	if err := decodeSingleJSON(w, r, notificationChannelBodyLimit, &request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return notify.ChannelInput{}, false
	}
	if !request.complete() {
		writeError(w, http.StatusBadRequest, "enabled, notify_new, notify_regression and environments are all required")
		return notify.ChannelInput{}, false
	}
	return request.input(), true
}

func notificationChannelIDFromPath(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid notification channel id")
		return 0, false
	}
	return id, true
}

func toNotificationChannelResponse(channel notify.Channel) notificationChannelResponse {
	response := notificationChannelResponse{
		ID:               channel.ID,
		Name:             channel.Name,
		Kind:             string(channel.Kind),
		Target:           channel.Target,
		HasSecret:        channel.HasSecret,
		Enabled:          channel.Enabled,
		NotifyNew:        channel.NotifyNew,
		NotifyRegression: channel.NotifyRegression,
		Environments:     channel.Environments,
		MinLevel:         channel.MinLevel,
		LastSuccessAt:    channel.LastSuccessAt,
		LastErrorAt:      channel.LastErrorAt,
		CreatedAt:        channel.CreatedAt,
	}
	if channel.LastError != "" {
		response.LastError = &channel.LastError
	}
	if response.Environments == nil {
		response.Environments = []string{}
	}
	return response
}
