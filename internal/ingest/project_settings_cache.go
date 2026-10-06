package ingest

import (
	"context"
	"sync"
	"time"

	"github.com/ramilS/faultline/internal/scrub"
)

// ProjectSettings are a project's ingestion-time settings.
type ProjectSettings struct {
	// StoreUserIP reports whether the project has opted into aggregating an
	// end user's IP address as an identity (projects.store_user_ip).
	StoreUserIP bool
	// Scrub is the project's data-scrubbing configuration.
	Scrub scrub.Config
	// GlobalRules are the instance-wide scrubbing rules, which apply to every
	// project even when its own scrubbing is off.
	GlobalRules []scrub.Rule

	scrubber *scrub.Scrubber
}

// withScrubber returns the settings with a Scrubber built from Scrub, so the
// name lists are prepared once per snapshot rather than once per event. A
// stored rule that does not compile is an error, never skipped.
func (s ProjectSettings) withScrubber() (ProjectSettings, error) {
	if s.scrubber != nil {
		return s, nil
	}
	scrubber, err := scrub.NewWithGlobal(s.Scrub, s.GlobalRules)
	if err != nil {
		return ProjectSettings{}, err
	}
	s.scrubber = scrubber
	return s, nil
}

// closedSettings are the fail-closed defaults: not opted in to store_user_ip,
// default scrubbing on, no custom rules.
func closedSettings() ProjectSettings {
	return ProjectSettings{Scrub: scrub.DefaultConfig(), scrubber: scrub.NewDefault()}
}

// ProjectSettingsStore resolves a project's ingestion-time settings from
// durable storage.
type ProjectSettingsStore interface {
	Settings(ctx context.Context, projectID int64) (ProjectSettings, error)
}

// ProjectSettingsCache resolves project settings through a short TTL cache
// backed by a ProjectSettingsStore, mirroring KeyLookup's cache shape:
// positive results are cached for positiveTTL, so flipping the project
// setting takes effect within one TTL rather than instantly, and a transient
// store error is never cached. It exists because DispatchEnvelope runs on
// both the synchronous ingest path and the durable feeder's replay path, and
// the feeder can process many records for the same project in one pass — an
// uncached lookup there would be a database round trip per event, not per
// request.
type ProjectSettingsCache struct {
	store ProjectSettingsStore
	now   Clock

	mu      sync.Mutex
	entries map[int64]projectSettingsEntry
}

type projectSettingsEntry struct {
	settings  ProjectSettings
	expiresAt time.Time
}

func NewProjectSettingsCache(store ProjectSettingsStore, now Clock) *ProjectSettingsCache {
	return &ProjectSettingsCache{store: store, now: now, entries: make(map[int64]projectSettingsEntry)}
}

// Lookup satisfies SettingsLookup (see dispatch.go).
func (c *ProjectSettingsCache) Lookup(ctx context.Context, projectID int64) (ProjectSettings, error) {
	if entry, ok := c.cached(projectID); ok {
		return entry.settings, nil
	}
	settings, err := c.store.Settings(ctx, projectID)
	if err != nil {
		return ProjectSettings{}, err
	}
	settings, err = settings.withScrubber()
	if err != nil {
		return ProjectSettings{}, err
	}
	c.put(projectID, projectSettingsEntry{settings: settings, expiresAt: c.now().Add(positiveTTL)})
	return settings, nil
}

func (c *ProjectSettingsCache) cached(projectID int64) (projectSettingsEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[projectID]
	if !ok || !entry.expiresAt.After(c.now()) {
		return projectSettingsEntry{}, false
	}
	return entry, true
}

func (c *ProjectSettingsCache) put(projectID int64, entry projectSettingsEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[projectID] = entry
}
