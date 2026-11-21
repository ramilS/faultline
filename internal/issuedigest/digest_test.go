package issuedigest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/sourcemap"
)

var (
	firstSeen = time.Date(2026, 11, 10, 8, 0, 0, 0, time.UTC)
	lastSeen  = time.Date(2026, 11, 21, 9, 30, 0, 0, time.UTC)
	eventID   = uuid.MustParse("6f1c2a52-3c0e-4a8a-9d39-4a5d2b9c1e11")
)

func sampleDetail() issue.Detail {
	detail := issue.Detail{
		Issue: issue.Issue{
			ID:             42,
			ProjectSlug:    "muslicast-ios",
			Title:          "Muslicast+0xfa6408",
			DisplayTitle:   "PlaybackFailure: NSURLErrorDomain -1001: The request timed out.",
			Culprit:        "Muslicast+0x1",
			DisplayCulprit: "PlaybackController.invoke",
			Level:          "error",
			Status:         issue.StatusUnresolved,
			TimesSeen:      200,
			UsersSeen:      37,
			FirstSeen:      firstSeen,
			LastSeen:       lastSeen,
		},
		Environments: []string{"production"},
		Tags: []issue.TagFacet{
			{Key: "release", Values: []issue.TagValue{{Value: "0.3.2", TimesSeen: 180}, {Value: "0.3.1", TimesSeen: 20}}},
		},
	}
	detail.Issue.Counts14d[13] = 12
	detail.Issue.Counts24h[23] = 3
	return detail
}

func sampleEvent(payload string) issue.EventDetail {
	release := "0.3.2"
	return issue.EventDetail{
		EventID:     eventID,
		Timestamp:   lastSeen,
		Level:       "error",
		Environment: "production",
		Release:     &release,
		Payload:     json.RawMessage(payload),
		SymbolicatedException: &sourcemap.Exception{
			Type:  "PlaybackFailure",
			Value: "NSURLErrorDomain -1001",
			Frames: []sourcemap.Frame{
				{Generated: sourcemap.Position{Function: "objc_msgSend", Filename: "libobjc.dylib"}},
				{
					Generated: sourcemap.Position{Function: "0xfa6408", Filename: "Muslicast"},
					Original:  &sourcemap.Position{Function: "invoke", Filename: "PlaybackController.kt", Line: 1042, InApp: true},
					Vars:      json.RawMessage(`{"token":"secret-frame-var"}`),
				},
			},
		},
	}
}

func TestRenderSummarisesTheIssue(t *testing.T) {
	got := Render(sampleDetail(), nil)

	for _, want := range []string{
		"# Issue 42 · muslicast-ios\n",
		"title: PlaybackFailure: NSURLErrorDomain -1001: The request timed out.\n",
		"culprit: PlaybackController.invoke\n",
		"level: error · status: unresolved\n",
		"events: 200 · users: 37\n",
		"first seen: 2026-11-10T08:00:00Z · last seen: 2026-11-21T09:30:00Z\n",
		"environments: production\n",
		"daily events, last 14 days, oldest first: 0 0 0 0 0 0 0 0 0 0 0 0 0 12\n",
		"release: 0.3.2 90%, 0.3.1 10%\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "## Event") {
		t.Errorf("digest without an event has an event section:\n%s", got)
	}
}

func TestRenderFallsBackToRawLabelsBeforeSymbolication(t *testing.T) {
	detail := sampleDetail()
	detail.Issue.DisplayTitle, detail.Issue.DisplayCulprit = "", ""

	got := Render(detail, nil)

	if !strings.Contains(got, "title: Muslicast+0xfa6408\n") || !strings.Contains(got, "culprit: Muslicast+0x1\n") {
		t.Errorf("digest does not fall back to raw labels:\n%s", got)
	}
}

func TestRenderShowsInAppFramesOfTheEvent(t *testing.T) {
	event := sampleEvent(`{}`)

	got := Render(sampleDetail(), &event)

	for _, want := range []string{
		"## Event 6f1c2a52-3c0e-4a8a-9d39-4a5d2b9c1e11\n",
		"time: 2026-11-21T09:30:00Z · environment: production · release: 0.3.2 · level: error\n",
		"PlaybackFailure: NSURLErrorDomain -1001\n",
		"  at invoke (PlaybackController.kt:1042)\n",
		"  … 1 other frames\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest lacks %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "objc_msgSend") {
		t.Errorf("digest lists a system frame although in-app frames exist:\n%s", got)
	}
}

func TestRenderShowsTopFramesWhenNoneIsInApp(t *testing.T) {
	event := sampleEvent(`{}`)
	event.SymbolicatedException.Frames = []sourcemap.Frame{
		{Generated: sourcemap.Position{Function: "objc_msgSend", Filename: "libobjc.dylib"}},
	}

	got := Render(sampleDetail(), &event)

	if !strings.Contains(got, "  at objc_msgSend (libobjc.dylib)\n") {
		t.Errorf("digest hides the only frames there are:\n%s", got)
	}
}

func TestRenderWalksTheCauseChain(t *testing.T) {
	event := sampleEvent(`{}`)
	event.SymbolicatedException.Cause = &sourcemap.Exception{Type: "IOException", Value: "socket closed"}

	got := Render(sampleDetail(), &event)

	if !strings.Contains(got, "caused by: IOException: socket closed\n") {
		t.Errorf("digest lacks the cause:\n%s", got)
	}
}

func TestRenderReadsTagsContextsAndBreadcrumbs(t *testing.T) {
	event := sampleEvent(`{
		"tags": [["os", "iOS 26.1"], ["device", "iPhone17,1"]],
		"contexts": {"os": {"type": "os", "name": "iOS", "version": "26.1"}, "app": {"app_version": "0.3.2", "nested": {"x": 1}}},
		"breadcrumbs": {"values": [
			{"timestamp": 1763717400.5, "level": "info", "category": "http", "data": {"method": "GET", "status_code": 200}},
			{"timestamp": "2026-11-21T09:29:59.123Z", "category": "playback", "message": "lesson a"}
		]}
	}`)

	got := Render(sampleDetail(), &event)

	for _, want := range []string{
		"os=iOS 26.1 · device=iPhone17,1\n",
		"app: app_version=0.3.2\n",
		"os: name=iOS · version=26.1\n",
		"### Breadcrumbs (last 2, oldest first)\n",
		"2025-11-21T09:30:00Z info [http] method=GET status_code=200\n",
		"2026-11-21T09:29:59Z [playback] lesson a\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("digest lacks %q:\n%s", want, got)
		}
	}
}

func TestRenderKeepsOnlyTheLatestBreadcrumbs(t *testing.T) {
	crumbs := make([]string, 0, maxBreadcrumbs+5)
	for index := range maxBreadcrumbs + 5 {
		crumbs = append(crumbs, `{"message":"crumb-`+string(rune('a'+index))+`"}`)
	}
	event := sampleEvent(`{"breadcrumbs": [` + strings.Join(crumbs, ",") + `]}`)

	got := Render(sampleDetail(), &event)

	if strings.Contains(got, "crumb-a") || !strings.Contains(got, "crumb-y") {
		t.Errorf("digest does not keep the latest %d breadcrumbs:\n%s", maxBreadcrumbs, got)
	}
}

func TestRenderLeavesOutWhatIdentifiesEndUsers(t *testing.T) {
	detail := sampleDetail()
	detail.Tags = append(detail.Tags, issue.TagFacet{Key: "user", Values: []issue.TagValue{{Value: "id:secret-user-facet", TimesSeen: 1}}})
	event := sampleEvent(`{
		"tags": {"user.id": "secret-user-tag", "os": "iOS"},
		"contexts": {
			"device": {"name": "secret-device-name", "id": "secret-device-id", "model": "iPhone17,1"},
			"app": {"device_app_hash": "secret-app-hash", "app_version": "0.3.2"}
		},
		"user": {"email": "reader@example.com", "ip_address": "203.0.113.9"},
		"request": {"headers": {"Authorization": "Bearer secret-header"}, "cookies": "session=secret-cookie"},
		"extra": {"note": "secret-extra"}
	}`)

	got := Render(detail, &event)

	if !strings.Contains(got, "device: model=iPhone17,1\n") || !strings.Contains(got, "os=iOS") {
		t.Errorf("digest dropped non-identifying fields along with identifying ones:\n%s", got)
	}
	for _, leaked := range []string{
		"reader@example.com", "203.0.113.9", "secret-header", "secret-cookie", "secret-extra", "secret-frame-var",
		"secret-user-facet", "secret-user-tag", "secret-device-name", "secret-device-id", "secret-app-hash",
	} {
		if strings.Contains(got, leaked) {
			t.Errorf("digest leaks %q:\n%s", leaked, got)
		}
	}
}

func TestRenderSurvivesAMalformedPayloadField(t *testing.T) {
	event := sampleEvent(`{"tags": 7, "contexts": "nope", "breadcrumbs": [{"message": "kept"}]}`)

	got := Render(sampleDetail(), &event)

	if !strings.Contains(got, "kept") || strings.Contains(got, "### Event tags") || strings.Contains(got, "### Contexts") {
		t.Errorf("a malformed field broke other sections:\n%s", got)
	}
}

func TestRenderClipsLongText(t *testing.T) {
	event := sampleEvent(`{}`)
	event.Message = strings.Repeat("x", maxTextRunes+50) + "\nsecond line"

	got := Render(sampleDetail(), &event)

	if !strings.Contains(got, "message: "+strings.Repeat("x", maxTextRunes)+"…\n") {
		t.Errorf("message not clipped to %d runes:\n%s", maxTextRunes, got)
	}
}
