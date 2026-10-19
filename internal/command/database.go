package command

import (
	"context"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/artifactcatalog"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/group"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	nativedebug "github.com/ramilS/faultline/internal/native"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/platform/config"
	platformpostgres "github.com/ramilS/faultline/internal/platform/postgres"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
)

type databasePool interface {
	storage.HealthChecker
	Close()
}

type runtimeDatabase struct {
	Pool            databasePool
	Users           auth.UserRepository
	Sessions        auth.SessionRepository
	Identities      auth.IdentityRepository
	Projects        project.ProjectRepository
	Keys            project.KeyRepository
	Issues          issue.Repository
	Access          access.Repository
	ArtifactTokens  artifactauth.Repository
	ArtifactCatalog artifactcatalog.Repository
	Groups          group.Repository
	SourceMaps      sourcemap.Repository
	Proguard        proguard.Repository
	NativeDebug     nativedebug.Repository
	UploadChunks    sentrycli.ChunkStore
	Releases        sentrycli.ReleaseStore
	IngestKeys      ingest.KeyStore
	EventSink       storage.EventSink
	// ProjectSettings resolves per-project ingestion settings, shared by the
	// ingest handler's non-durable dispatch and the durable feeder's replay so
	// the two paths cannot disagree about a project's setting (see
	// ingest.SettingsLookup). Use settingsLookup, not this field
	// directly, wherever it feeds HandlerOptions or durable.Config: a nil
	// *ProjectSettingsCache's bound Lookup method panics on first call
	// rather than degrading, and tests construct runtimeDatabase without it.
	ProjectSettings *ingest.ProjectSettingsCache
	// Scrubbing reads and writes a project's data-scrubbing configuration.
	Scrubbing scrub.Repository
	// NotificationChannels and NotificationQueue back issue notifications:
	// the dashboard's channel editor and the background delivery worker.
	// Either may be nil in tests, which then run without notifications.
	NotificationChannels notify.ChannelRepository
	NotificationQueue    notify.Queue
	// GroupingRules is the full CRUD repository backing the dashboard's
	// grouping-rule editor (grouping.NewRuleService). It is a separate handle
	// on the same underlying storage GroupingRuleCache reads through — not a
	// second store — since RuleService needs Create/Update/Move/Delete/
	// ListByProjectSlug, which RuleCache does not expose.
	GroupingRules grouping.RuleRepository
	// GroupingRuleCache is the one process-wide RuleCache instance: Issues'
	// merge invalidates it synchronously on merge commit (see
	// storagepostgres.NewIssueRepository), and it must be this exact instance
	// — not a second one — wherever ingest is wired to resolve grouping rules,
	// or invalidation silently invalidates a cache nothing reads from.
	GroupingRuleCache *grouping.RuleCache
}

func (database *runtimeDatabase) Close() {
	if database != nil && database.Pool != nil {
		database.Pool.Close()
	}
}

// settingsLookup adapts ProjectSettings to ingest.SettingsLookup, resolving
// to a nil lookup when the database has none configured. DispatchEnvelope
// already treats a nil lookup as "fail closed" (see resolveSettings); a
// bound method value taken from a nil *ProjectSettingsCache would instead
// panic the first time it is called, which a test building a partial
// runtimeDatabase would only discover by crashing an ingest request.
func (database *runtimeDatabase) settingsLookup() ingest.SettingsLookup {
	if database.ProjectSettings == nil {
		return nil
	}
	return database.ProjectSettings.Lookup
}

// assembleDatabase wires the Postgres-backed adapters. reportError observes
// failures from best-effort background/post-commit steps that must not fail
// the operation that triggered them — currently just the issue-merge
// sparkline refresh (see storagepostgres.IssueRepositoryOptions.ReportError).
func assembleDatabase(ctx context.Context, configuration config.DatabaseConfig, reportError func(error)) (*runtimeDatabase, error) {
	pool, err := platformpostgres.Open(ctx, configuration)
	if err != nil {
		return nil, err
	}
	authRepositories := storagepostgres.NewAuthRepository(pool)
	projectRepositories := storagepostgres.NewProjectRepository(pool)
	groupingRules := storagepostgres.NewGroupingRuleRepository(pool)
	ruleCache := grouping.NewRuleCache(groupingRules, time.Now)
	issues := storagepostgres.NewIssueRepositoryWithOptions(pool, storagepostgres.IssueRepositoryOptions{
		Invalidator: ruleCache,
		ReportError: reportError,
	})
	projectSettings := storagepostgres.NewProjectSettingsStore(pool)
	artifactLimits := []storagepostgres.ArtifactStoreOption{
		storagepostgres.WithArtifactQuota(configuration.ArtifactQuotaBytes),
		storagepostgres.WithDebugFileRetention(configuration.DebugFileRetention),
	}
	return &runtimeDatabase{
		Pool:                 pool,
		Users:                authRepositories.Users,
		Sessions:             authRepositories.Sessions,
		Identities:           authRepositories.Identities,
		Projects:             projectRepositories.Projects,
		Keys:                 projectRepositories.Keys,
		Issues:               issues,
		Access:               storagepostgres.NewAccessRepository(pool),
		ArtifactTokens:       storagepostgres.NewArtifactTokenRepository(pool),
		ArtifactCatalog:      storagepostgres.NewArtifactCatalogRepository(pool),
		Groups:               storagepostgres.NewGroupRepository(pool),
		SourceMaps:           storagepostgres.NewSourceMapRepository(pool, artifactLimits...),
		Proguard:             storagepostgres.NewProguardMappingRepository(pool, artifactLimits...),
		NativeDebug:          storagepostgres.NewNativeDebugFileRepository(pool, artifactLimits...),
		UploadChunks:         storagepostgres.NewUploadChunkStore(pool),
		Releases:             storagepostgres.NewReleaseStore(pool),
		GroupingRules:        groupingRules,
		IngestKeys:           storagepostgres.NewIngestKeyStore(pool),
		EventSink:            storagepostgres.NewEventSink(pool),
		GroupingRuleCache:    ruleCache,
		ProjectSettings:      ingest.NewProjectSettingsCache(projectSettings, time.Now),
		Scrubbing:            projectSettings,
		NotificationChannels: storagepostgres.NewNotificationChannelRepository(pool),
		NotificationQueue:    storagepostgres.NewNotificationQueue(pool),
	}, nil
}
