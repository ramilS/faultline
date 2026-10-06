package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/scrub"
)

type stubGlobalScrubbingService struct {
	rules []scrub.Rule
	err   error
	puts  int
}

func (s *stubGlobalScrubbingService) GetGlobal(context.Context) ([]scrub.Rule, error) {
	return s.rules, s.err
}

func (s *stubGlobalScrubbingService) UpdateGlobal(_ context.Context, rules []scrub.Rule) ([]scrub.Rule, error) {
	s.puts++
	return rules, s.err
}

func globalRequest(method, body string) *http.Request {
	request := httptest.NewRequest(method, "/api/v1/settings/data-scrubbing", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	return request
}

func TestGetGlobalDataScrubbingReturnsRulesAsAnArray(t *testing.T) {
	handler := NewGlobalDataScrubbingHandler(&stubGlobalScrubbingService{}, Options{})
	recorder := httptest.NewRecorder()
	handler.Get(recorder, globalRequest(http.MethodGet, ""))
	if recorder.Code != http.StatusOK || strings.TrimSpace(recorder.Body.String()) != `{"rules":[]}` {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestUpdateGlobalDataScrubbingRefusesABodyWithoutRules(t *testing.T) {
	service := &stubGlobalScrubbingService{}
	recorder := httptest.NewRecorder()
	NewGlobalDataScrubbingHandler(service, Options{}).Update(recorder, globalRequest(http.MethodPut, `{}`))
	if recorder.Code != http.StatusBadRequest || service.puts != 0 {
		t.Fatalf("response = %d, puts = %d; want 400 and no write", recorder.Code, service.puts)
	}
}

// recordingScrubbingRepository lets the real scrub.Service validate while
// counting what would have reached storage.
type recordingScrubbingRepository struct {
	writes int
}

func (*recordingScrubbingRepository) Get(context.Context, int64) (scrub.Config, error) {
	return scrub.DefaultConfig(), nil
}

func (r *recordingScrubbingRepository) Update(context.Context, int64, scrub.Config) error {
	r.writes++
	return nil
}

func (*recordingScrubbingRepository) GlobalRules(context.Context) ([]scrub.Rule, error) {
	return []scrub.Rule{}, nil
}

func (r *recordingScrubbingRepository) UpdateGlobalRules(context.Context, []scrub.Rule) error {
	r.writes++
	return nil
}

const lookaheadRules = `"rules":[{"name":"x","pattern":"a(?=b)","replacement":""}]`

func TestUpdateGlobalDataScrubbingRefusesALookaheadThroughTheRealService(t *testing.T) {
	var reported error
	repository := &recordingScrubbingRepository{}
	handler := NewGlobalDataScrubbingHandler(scrub.NewService(repository), Options{ReportError: func(err error) { reported = err }})

	recorder := httptest.NewRecorder()
	handler.Update(recorder, globalRequest(http.MethodPut, `{`+lookaheadRules+`}`))

	if recorder.Code != http.StatusBadRequest || !strings.Contains(recorder.Body.String(), "RE2") {
		t.Fatalf("response = %d %s, want 400 naming RE2", recorder.Code, recorder.Body.String())
	}
	if reported != nil || repository.writes != 0 {
		t.Fatalf("reported = %v, writes = %d; want nothing reported and nothing written", reported, repository.writes)
	}
}

func TestUpdateGlobalDataScrubbingReportsStorageFailureAs500(t *testing.T) {
	var reported error
	service := &stubGlobalScrubbingService{err: errors.New("db down")}
	recorder := httptest.NewRecorder()
	NewGlobalDataScrubbingHandler(service, Options{ReportError: func(err error) { reported = err }}).
		Update(recorder, globalRequest(http.MethodPut, `{"rules":[]}`))
	if recorder.Code != http.StatusInternalServerError || reported == nil {
		t.Fatalf("response = %d, reported = %v; want 500 and a report", recorder.Code, reported)
	}
}
