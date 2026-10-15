package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/project"
)

type stubNotificationService struct {
	channels  []notify.Channel
	err       error
	created   *notify.ChannelInput
	updated   *notify.ChannelInput
	testedFor notify.ProjectRef
}

func (s *stubNotificationService) List(context.Context, int64) ([]notify.Channel, error) {
	return s.channels, s.err
}

func (s *stubNotificationService) Create(_ context.Context, _ int64, input notify.ChannelInput) (notify.Channel, error) {
	s.created = &input
	if s.err != nil {
		return notify.Channel{}, s.err
	}
	return notify.Channel{ID: 1, Name: input.Name, Kind: input.Kind, Target: input.Target, Environments: input.Environments}, nil
}

func (s *stubNotificationService) Update(_ context.Context, _, channelID int64, input notify.ChannelInput) (notify.Channel, error) {
	s.updated = &input
	return notify.Channel{ID: channelID, Name: input.Name}, s.err
}

func (s *stubNotificationService) Delete(context.Context, int64, int64) error { return s.err }

func (s *stubNotificationService) SendTest(_ context.Context, _, _ int64, ref notify.ProjectRef) error {
	s.testedFor = ref
	return s.err
}

func notificationRequest(method, path, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "shop")
	request.SetPathValue("id", "3")
	return request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
}

// reportedError records the last error a handler reported as a server fault.
type reportedError struct {
	last error
}

func newNotificationHandler(service NotificationService) (*NotificationHandler, *reportedError) {
	projects := &stubGroupingRuleProjects{project: project.Project{ID: 5, Slug: "shop", Name: "Shop"}}
	reported := &reportedError{}
	return NewNotificationHandler(service, projects, Options{ReportError: func(err error) { reported.last = err }}), reported
}

const completeChannelBody = `{"name":"Ops","kind":"telegram","target":"-100","secret":"123:abc","enabled":true,"notify_new":true,"notify_regression":false,"environments":["production"],"min_level":"error"}`

func TestListNotificationChannelsWireShape(t *testing.T) {
	successAt := time.Date(2026, 10, 15, 9, 0, 0, 0, time.UTC)
	errorAt := successAt.Add(time.Hour)
	service := &stubNotificationService{channels: []notify.Channel{{
		ID: 3, Name: "Ops", Kind: notify.KindTelegram, Target: "-100", HasSecret: true, Enabled: true,
		NotifyNew: true, NotifyRegression: false, MinLevel: "error",
		LastSuccessAt: &successAt, LastError: "telegram: HTTP 403: Forbidden", LastErrorAt: &errorAt, CreatedAt: successAt,
	}}}
	recorder := httptest.NewRecorder()

	handler, _ := newNotificationHandler(service)
	handler.List(recorder, notificationRequest(http.MethodGet, "/", ""))

	want := `{"channels":[{"id":3,"name":"Ops","kind":"telegram","target":"-100","has_secret":true,"enabled":true,` +
		`"notify_new":true,"notify_regression":false,"environments":[],"min_level":"error",` +
		`"last_success_at":"2026-10-15T09:00:00Z","last_error":"telegram: HTTP 403: Forbidden","last_error_at":"2026-10-15T10:00:00Z",` +
		`"created_at":"2026-10-15T09:00:00Z"}]}`
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != want {
		t.Fatalf("response = %d %s\nwant %s", recorder.Code, recorder.Body.String(), want)
	}
}

func TestListNotificationChannelsSendsNullForNoError(t *testing.T) {
	service := &stubNotificationService{channels: []notify.Channel{{ID: 3, Kind: notify.KindWebhook}}}
	recorder := httptest.NewRecorder()

	handler, _ := newNotificationHandler(service)
	handler.List(recorder, notificationRequest(http.MethodGet, "/", ""))

	if body := recorder.Body.String(); !strings.Contains(body, `"last_error":null`) || !strings.Contains(body, `"last_success_at":null`) {
		t.Fatalf("body = %s, want null last_error and last_success_at", body)
	}
}

func TestCreateNotificationChannelPassesEveryField(t *testing.T) {
	service := &stubNotificationService{}
	recorder := httptest.NewRecorder()

	handler, _ := newNotificationHandler(service)
	handler.Create(recorder, notificationRequest(http.MethodPost, "/", completeChannelBody))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d %s", recorder.Code, recorder.Body.String())
	}
	got := service.created
	if got.Name != "Ops" || got.Kind != notify.KindTelegram || got.Target != "-100" || got.Secret == nil || *got.Secret != "123:abc" ||
		!got.Enabled || !got.NotifyNew || got.NotifyRegression || got.MinLevel != "error" || len(got.Environments) != 1 {
		t.Fatalf("service got %+v", got)
	}
	if strings.Contains(recorder.Body.String(), "123:abc") {
		t.Fatalf("response echoes the secret: %s", recorder.Body.String())
	}
}

func TestCreateNotificationChannelRefusesAPartialBody(t *testing.T) {
	for _, field := range []string{"enabled", "notify_new", "notify_regression", "environments"} {
		var body map[string]any
		_ = json.Unmarshal([]byte(completeChannelBody), &body)
		delete(body, field)
		encoded, _ := json.Marshal(body)
		service := &stubNotificationService{}
		recorder := httptest.NewRecorder()

		handler, _ := newNotificationHandler(service)
		handler.Create(recorder, notificationRequest(http.MethodPost, "/", string(encoded)))

		if recorder.Code != http.StatusBadRequest || service.created != nil {
			t.Errorf("without %s: status = %d, service called = %v", field, recorder.Code, service.created != nil)
		}
	}
}

func TestUpdateNotificationChannelWithoutSecretPassesNil(t *testing.T) {
	service := &stubNotificationService{}
	body := strings.Replace(completeChannelBody, `"secret":"123:abc",`, "", 1)
	recorder := httptest.NewRecorder()

	handler, _ := newNotificationHandler(service)
	handler.Update(recorder, notificationRequest(http.MethodPut, "/", body))

	if recorder.Code != http.StatusOK || service.updated == nil || service.updated.Secret != nil {
		t.Fatalf("status = %d, update input = %+v, want a nil secret", recorder.Code, service.updated)
	}
}

func TestNotificationChannelErrorMapping(t *testing.T) {
	cases := map[string]struct {
		err        error
		wantStatus int
		wantBody   string
		reported   bool
	}{
		"validation":     {&notify.ValidationError{Message: "bot token is required"}, http.StatusBadRequest, `{"error":"bot token is required"}`, false},
		"cap":            {notify.ErrTooManyChannels, http.StatusBadRequest, `{"error":"a project can have at most 20 notification channels"}`, false},
		"not found":      {notify.ErrChannelNotFound, http.StatusNotFound, `{"error":"notification channel not found"}`, false},
		"project gone":   {project.ErrNotFound, http.StatusNotFound, `{"error":"project not found"}`, false},
		"storage failed": {context.DeadlineExceeded, http.StatusInternalServerError, `{"error":"internal server error"}`, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()

			handler, reported := newNotificationHandler(&stubNotificationService{err: tc.err})
			handler.Create(recorder, notificationRequest(http.MethodPost, "/", completeChannelBody))

			if recorder.Code != tc.wantStatus || strings.TrimSpace(recorder.Body.String()) != tc.wantBody || (reported.last != nil) != tc.reported {
				t.Fatalf("response = %d %s reported=%v", recorder.Code, recorder.Body.String(), reported.last)
			}
		})
	}
}

func TestSendTestNotificationReturnsTheDestinationsRefusalAs502(t *testing.T) {
	service := &stubNotificationService{err: &notify.SendError{Message: "telegram: HTTP 400: Bad Request: chat not found", Permanent: true}}
	recorder := httptest.NewRecorder()

	handler, reported := newNotificationHandler(service)
	handler.SendTest(recorder, notificationRequest(http.MethodPost, "/", ""))

	if recorder.Code != http.StatusBadGateway || strings.TrimSpace(recorder.Body.String()) != `{"error":"telegram: HTTP 400: Bad Request: chat not found"}` {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	if reported.last != nil {
		t.Fatalf("a destination's refusal was reported as a server error: %v", reported.last)
	}
}

func TestSendTestNotificationNamesTheProject(t *testing.T) {
	service := &stubNotificationService{}
	recorder := httptest.NewRecorder()

	handler, _ := newNotificationHandler(service)
	handler.SendTest(recorder, notificationRequest(http.MethodPost, "/", ""))

	if recorder.Code != http.StatusNoContent || service.testedFor != (notify.ProjectRef{Slug: "shop", Name: "Shop"}) {
		t.Fatalf("status = %d, tested for %+v", recorder.Code, service.testedFor)
	}
}

func TestNotificationChannelRejectsABadID(t *testing.T) {
	request := notificationRequest(http.MethodDelete, "/", "")
	request.SetPathValue("id", "abc")
	recorder := httptest.NewRecorder()

	handler, _ := newNotificationHandler(&stubNotificationService{err: errors.New("must not be called")})
	handler.Delete(recorder, request)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}
