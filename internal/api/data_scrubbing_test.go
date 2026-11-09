package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
)

type stubDataScrubbingService struct {
	config      scrub.Config
	hasEvents   bool
	err         error
	updateCalls int
	lastUpdate  struct {
		projectID int64
		config    scrub.Config
	}
}

func (s *stubDataScrubbingService) Get(context.Context, int64) (scrub.Config, error) {
	return s.config, s.err
}

func (s *stubDataScrubbingService) HasEvents(context.Context, int64) (bool, error) {
	return s.hasEvents, nil
}

func (s *stubDataScrubbingService) PreviewProject(context.Context, int64, scrub.ProjectPreviewInput) (scrub.Preview, error) {
	return scrub.Preview{}, s.err
}

func (s *stubDataScrubbingService) Update(_ context.Context, projectID int64, config scrub.Config) (scrub.Config, error) {
	s.updateCalls++
	s.lastUpdate.projectID, s.lastUpdate.config = projectID, config
	return config, s.err
}

// errorRecorder keeps the last error a handler reported.
type errorRecorder struct{ err error }

func newDataScrubbingHandler(service DataScrubbingService, projects ProjectResolver, reported *errorRecorder) *DataScrubbingHandler {
	return NewDataScrubbingHandler(service, projects, Options{ReportError: func(err error) { reported.err = err }})
}

func dataScrubbingRequest(method, body string) *http.Request {
	request := httptest.NewRequest(method, "/api/v1/projects/web/data-scrubbing", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.SetPathValue("slug", "web")
	return request.WithContext(access.ContextWithScope(request.Context(), access.Unrestricted()))
}

const defaultFieldsJSON = `"default_fields":["password","secret","passwd","api_key","apikey","auth","credentials","mysql_pwd","privatekey","private_key","token","bearer","cookie","api-key"]`

func TestGetDataScrubbingReturnsTheConfigWithEmptyListsAsArrays(t *testing.T) {
	var reported errorRecorder
	handler := newDataScrubbingHandler(&stubDataScrubbingService{config: scrub.Config{Enabled: true}, hasEvents: true}, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Get(recorder, dataScrubbingRequest(http.MethodGet, ""))

	want := `{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[],` + defaultFieldsJSON + `,"has_events":true}`
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != want {
		t.Fatalf("response = %d %s, want %s", recorder.Code, recorder.Body.String(), want)
	}
}

func TestGetDataScrubbingProjectNotFound(t *testing.T) {
	var reported errorRecorder
	handler := newDataScrubbingHandler(&stubDataScrubbingService{}, &stubGroupingRuleProjects{err: project.ErrNotFound}, &reported)

	recorder := httptest.NewRecorder()
	handler.Get(recorder, dataScrubbingRequest(http.MethodGet, ""))

	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestGetDataScrubbingReportsServiceError(t *testing.T) {
	var reported errorRecorder
	handler := newDataScrubbingHandler(&stubDataScrubbingService{err: context.DeadlineExceeded}, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Get(recorder, dataScrubbingRequest(http.MethodGet, ""))

	if recorder.Code != http.StatusInternalServerError || !errors.Is(reported.err, context.DeadlineExceeded) {
		t.Fatalf("status = %d, reported = %v", recorder.Code, reported.err)
	}
}

func TestUpdateDataScrubbingStoresTheConfig(t *testing.T) {
	var reported errorRecorder
	service := &stubDataScrubbingService{}
	handler := newDataScrubbingHandler(service, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Update(recorder, dataScrubbingRequest(http.MethodPut, `{"enabled":false,"extra_fields":["ssn"],"safe_fields":[],"rules":[]}`))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", recorder.Code, recorder.Body.String())
	}
	want := scrub.Config{Enabled: false, ExtraFields: []string{"ssn"}, SafeFields: []string{}, Rules: []scrub.Rule{}}
	if service.lastUpdate.projectID != 5 || !reflect.DeepEqual(service.lastUpdate.config, want) {
		t.Fatalf("update = %+v, want project 5 and %+v", service.lastUpdate, want)
	}
	if got := strings.TrimSpace(recorder.Body.String()); got != `{"enabled":false,"extra_fields":["ssn"],"safe_fields":[],"rules":[]}` {
		t.Fatalf("body = %s", got)
	}
}

func TestUpdateDataScrubbingRejectsBadRequests(t *testing.T) {
	for name, body := range map[string]string{
		"unknown field":   `{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[],"regex":"x"}`,
		"not json":        `nope`,
		"empty object":    `{}`,
		"missing enabled": `{"extra_fields":[],"safe_fields":[],"rules":[]}`,
		"missing extra":   `{"enabled":true,"safe_fields":[],"rules":[]}`,
		"null safe":       `{"enabled":true,"extra_fields":[],"safe_fields":null,"rules":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			var reported errorRecorder
			service := &stubDataScrubbingService{}
			handler := newDataScrubbingHandler(service, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

			recorder := httptest.NewRecorder()
			handler.Update(recorder, dataScrubbingRequest(http.MethodPut, body))

			if recorder.Code != http.StatusBadRequest || service.updateCalls != 0 {
				t.Fatalf("status = %d, update calls = %d, want 400 and none", recorder.Code, service.updateCalls)
			}
		})
	}
}

func TestUpdateDataScrubbingMapsValidationFailureTo400(t *testing.T) {
	var reported errorRecorder
	service := &stubDataScrubbingService{err: scrub.ErrInvalidConfig}
	handler := newDataScrubbingHandler(service, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Update(recorder, dataScrubbingRequest(http.MethodPut, `{"enabled":true,"extra_fields":[" "],"safe_fields":[],"rules":[]}`))

	if recorder.Code != http.StatusBadRequest || reported.err != nil {
		t.Fatalf("status = %d, reported = %v, want 400 and nothing reported", recorder.Code, reported.err)
	}
}

func TestUpdateDataScrubbingRefusesALookaheadThroughTheRealService(t *testing.T) {
	var reported errorRecorder
	repository := &recordingScrubbingRepository{}
	handler := newDataScrubbingHandler(scrub.NewService(repository), &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Update(recorder, dataScrubbingRequest(http.MethodPut, `{"enabled":true,"extra_fields":[],"safe_fields":[],`+lookaheadRules+`}`))

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "RE2") {
		t.Fatalf("response = %d %s, want 400 naming RE2", recorder.Code, recorder.Body.String())
	}
	if reported.err != nil || repository.writes != 0 {
		t.Fatalf("reported = %v, writes = %d; want nothing reported and nothing written", reported.err, repository.writes)
	}
}

func TestUpdateDataScrubbingReportsStorageError(t *testing.T) {
	var reported errorRecorder
	handler := newDataScrubbingHandler(&stubDataScrubbingService{err: context.DeadlineExceeded}, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Update(recorder, dataScrubbingRequest(http.MethodPut, `{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[]}`))

	if recorder.Code != http.StatusInternalServerError || !errors.Is(reported.err, context.DeadlineExceeded) {
		t.Fatalf("status = %d, reported = %v", recorder.Code, reported.err)
	}
}

func TestUpdateDataScrubbingMapsAProjectDeletedAfterResolveTo404(t *testing.T) {
	var reported errorRecorder
	service := &stubDataScrubbingService{err: project.ErrNotFound}
	handler := newDataScrubbingHandler(service, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Update(recorder, dataScrubbingRequest(http.MethodPut, `{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[]}`))

	if recorder.Code != http.StatusNotFound || reported.err != nil {
		t.Fatalf("status = %d, reported = %v, want 404 and nothing reported", recorder.Code, reported.err)
	}
}

func TestUpdateDataScrubbingRefusesABodyWithoutRules(t *testing.T) {
	var reported errorRecorder
	service := &stubDataScrubbingService{}
	handler := newDataScrubbingHandler(service, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	recorder := httptest.NewRecorder()
	handler.Update(recorder, dataScrubbingRequest(http.MethodPut, `{"enabled":true,"extra_fields":[],"safe_fields":[]}`))

	if recorder.Code != http.StatusBadRequest || service.updateCalls != 0 {
		t.Fatalf("response = %d, updates = %d; want 400 and no update", recorder.Code, service.updateCalls)
	}
}

func TestUpdateDataScrubbingPassesRulesToTheService(t *testing.T) {
	var reported errorRecorder
	service := &stubDataScrubbingService{}
	handler := newDataScrubbingHandler(service, &stubGroupingRuleProjects{project: project.Project{ID: 5}}, &reported)

	body := `{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[{"name":"Telegram","pattern":"bot\\d+","replacement":"bot[Filtered]"}]}`
	recorder := httptest.NewRecorder()
	handler.Update(recorder, dataScrubbingRequest(http.MethodPut, body))

	want := []scrub.Rule{{Name: "Telegram", Pattern: `bot\d+`, Replacement: "bot[Filtered]"}}
	if recorder.Code != http.StatusOK || !reflect.DeepEqual(service.lastUpdate.config.Rules, want) {
		t.Fatalf("response = %d, rules = %v; want 200 and %v", recorder.Code, service.lastUpdate.config.Rules, want)
	}
	if !strings.Contains(recorder.Body.String(), `"rules":[{"name":"Telegram"`) {
		t.Fatalf("body = %s, want the rules echoed", recorder.Body.String())
	}
}

func previewHandler(repository *recordingScrubbingRepository, reported *errorRecorder) *DataScrubbingHandler {
	return newDataScrubbingHandler(scrub.NewService(repository), &stubGroupingRuleProjects{project: project.Project{ID: 5}}, reported)
}

func TestPreviewDataScrubbingHidesAFieldAddedByNameWithoutStoringIt(t *testing.T) {
	var reported errorRecorder
	repository := &recordingScrubbingRepository{}

	recorder := httptest.NewRecorder()
	previewHandler(repository, &reported).Preview(recorder, dataScrubbingRequest(http.MethodPost,
		`{"config":{"enabled":true,"extra_fields":["phone"],"safe_fields":[],"rules":[]},"sample":"{\"phone\":\"+1 555 0100\"}"}`))

	want := `{"scrubbed":"{\n  \"phone\": \"[Filtered]\"\n}","segments":[{"text":"{\n  \"phone\": \"","redacted":false},{"text":"[Filtered]","redacted":true},{"text":"\"\n}","redacted":false}]}`
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != want {
		t.Fatalf("response = %d %s, want 200 %s", recorder.Code, recorder.Body.String(), want)
	}
	if repository.writes != 0 || reported.err != nil {
		t.Fatalf("writes = %d, reported = %v; want neither", repository.writes, reported.err)
	}
}

func TestPreviewDataScrubbingHighlightsAPresetRule(t *testing.T) {
	var reported errorRecorder
	body := `{"config":{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[{"name":"Slack token","description":"Bot tokens.","pattern":"\\bxox[abeoprs]-[0-9A-Za-z-]{10,}","replacement":""}]},"sample":"posted with xoxb-EXAMPLE-TOKEN"}`

	recorder := httptest.NewRecorder()
	previewHandler(&recordingScrubbingRepository{}, &reported).Preview(recorder, dataScrubbingRequest(http.MethodPost, body))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `{"text":"[Filtered]","redacted":true}`) {
		t.Fatalf("response = %d %s, want the token highlighted", recorder.Code, recorder.Body.String())
	}
}

func TestPreviewDataScrubbingReturnsTheRE2ErrorAndStoresNothing(t *testing.T) {
	var reported errorRecorder
	repository := &recordingScrubbingRepository{}

	recorder := httptest.NewRecorder()
	previewHandler(repository, &reported).Preview(recorder, dataScrubbingRequest(http.MethodPost,
		`{"config":{"enabled":true,"extra_fields":[],"safe_fields":[],`+lookaheadRules+`},"sample":"ab"}`))

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "RE2") {
		t.Fatalf("response = %d %s, want 400 naming RE2", recorder.Code, recorder.Body.String())
	}
	if repository.writes != 0 || reported.err != nil {
		t.Fatalf("writes = %d, reported = %v; want neither", repository.writes, reported.err)
	}
}

func TestPreviewDataScrubbingRunsOnTheLatestEvent(t *testing.T) {
	var reported errorRecorder
	repository := &recordingScrubbingRepository{latestEvent: json.RawMessage(`{"message":"pin-1234","level":"error"}`)}
	body := `{"config":{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[{"name":"pin","pattern":"pin-\\d+","replacement":""}]},"latest_event":true}`

	recorder := httptest.NewRecorder()
	previewHandler(repository, &reported).Preview(recorder, dataScrubbingRequest(http.MethodPost, body))

	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), `"scrubbed":"{\n  \"level\": \"error\",\n  \"message\": \"[Filtered]\"\n}"`) {
		t.Fatalf("response = %d %s, want the latest event scrubbed", recorder.Code, recorder.Body.String())
	}
}

func TestPreviewDataScrubbingSaysWhenNoEventIsStored(t *testing.T) {
	var reported errorRecorder

	recorder := httptest.NewRecorder()
	previewHandler(&recordingScrubbingRepository{}, &reported).Preview(recorder, dataScrubbingRequest(http.MethodPost,
		`{"config":{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[]},"latest_event":true}`))

	if recorder.Code != http.StatusNotFound || strings.TrimSpace(recorder.Body.String()) != `{"error":"No events yet"}` || reported.err != nil {
		t.Fatalf("response = %d %s, reported = %v; want 404 No events yet", recorder.Code, recorder.Body.String(), reported.err)
	}
}

func TestPreviewDataScrubbingRejectsBadRequests(t *testing.T) {
	for name, body := range map[string]string{
		"no config":         `{"sample":"x"}`,
		"incomplete config": `{"config":{"enabled":true,"extra_fields":[],"safe_fields":[]},"sample":"x"}`,
		"unknown field":     `{"config":{"enabled":true,"extra_fields":[],"safe_fields":[],"rules":[]},"text":"x"}`,
		"not json":          `nope`,
	} {
		t.Run(name, func(t *testing.T) {
			var reported errorRecorder

			recorder := httptest.NewRecorder()
			previewHandler(&recordingScrubbingRepository{}, &reported).Preview(recorder, dataScrubbingRequest(http.MethodPost, body))

			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("response = %d %s, want 400", recorder.Code, recorder.Body.String())
			}
		})
	}
}
