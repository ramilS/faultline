package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/notify"
)

type appNotificationServiceStub struct{}

func (*appNotificationServiceStub) List(context.Context, int64) ([]notify.Channel, error) {
	return nil, nil
}

func (*appNotificationServiceStub) Create(context.Context, int64, notify.ChannelInput) (notify.Channel, error) {
	return notify.Channel{}, nil
}

func (*appNotificationServiceStub) Update(context.Context, int64, int64, notify.ChannelInput) (notify.Channel, error) {
	return notify.Channel{}, nil
}

func (*appNotificationServiceStub) Delete(context.Context, int64, int64) error { return nil }

func (*appNotificationServiceStub) SendTest(context.Context, int64, int64, notify.ProjectRef) error {
	return nil
}

// A webhook URL can itself be a credential, so every notification route —
// listing included — is owner-only, unlike most project settings.
func TestNotificationRoutesAreAllOwnerGated(t *testing.T) {
	routes := notificationRoutes(testDependencies(t))
	if len(routes) != 5 {
		t.Fatalf("notification routes = %d, want 5", len(routes))
	}
	for _, current := range routes {
		if current.gate != gateOwner {
			t.Errorf("route %q gate = %v, want gateOwner", current.pattern, current.gate)
		}
	}
}

func TestNotificationRoutesRejectASignedInMember(t *testing.T) {
	for _, target := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/projects/shop/notification-channels"},
		{http.MethodPost, "/api/v1/projects/shop/notification-channels"},
		{http.MethodPut, "/api/v1/projects/shop/notification-channels/1"},
		{http.MethodDelete, "/api/v1/projects/shop/notification-channels/1"},
		{http.MethodPost, "/api/v1/projects/shop/notification-channels/1/test"},
	} {
		dependencies := testDependencies(t)
		dependencies.AuthService = &authServiceStub{authUser: auth.User{ID: 2, Role: auth.RoleMember}}
		request := httptest.NewRequest(target.method, "https://example.com"+target.path, strings.NewReader("{}"))
		request.Header.Set("Cookie", api.SessionCookieName+"=token")
		request.Header.Set("Origin", "https://example.com")
		request.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()

		New(dependencies).ServeHTTP(recorder, request)

		if recorder.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", target.method, target.path, recorder.Code)
		}
	}
}

func TestNotificationRoutesAbsentWithoutAService(t *testing.T) {
	if routes := notificationRoutes(Dependencies{}); routes != nil {
		t.Fatalf("routes = %v, want none", routes)
	}
}
