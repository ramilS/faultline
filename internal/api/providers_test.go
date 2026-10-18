package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/api"
)

func TestProvidersReportsDisabledByDefault(t *testing.T) {
	recorder := httptest.NewRecorder()
	api.NewProvidersHandler(false).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", http.NoBody))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", recorder.Code)
	}
	var body struct {
		SSO struct {
			Enabled bool `json:"enabled"`
		} `json:"sso"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SSO.Enabled {
		t.Fatal(`{"sso":{"enabled":true}} was reported with no provider configured`)
	}
}

func TestProvidersReportsEnabled(t *testing.T) {
	recorder := httptest.NewRecorder()
	api.NewProvidersHandler(true).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/auth/providers", http.NoBody))

	if !strings.Contains(recorder.Body.String(), `"enabled":true`) {
		t.Fatalf("body = %q", recorder.Body.String())
	}
}
