// Package command assembles and dispatches Faultline's single-binary commands.
package command

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"golang.org/x/term"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/app"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/durable"
	"github.com/ramilS/faultline/internal/group"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/issue"
	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/pipeline"
	"github.com/ramilS/faultline/internal/platform/config"
	"github.com/ramilS/faultline/internal/platform/httpserver"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/scrub"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/spill"
)

// persistenceShutdownTimeout bounds the final pipeline drain during shutdown.
const persistenceShutdownTimeout = 5 * time.Second

// parsedMapCacheBudget caps the resident uncompressed source-map bytes held by
// the read-time symbolication cache.
const parsedMapCacheBudget = 128 << 20

// reportingResolver wraps a source-map resolver so a symbolication failure is
// reported once here and then degrades to an unsymbolicated event, keeping the
// issue read path unaware of transient source-map errors. The exception tree
// itself is still returned on failure: the underlying resolver keeps every
// frame it did manage to map, and a bad map for one frame should not throw
// away symbolication that already succeeded for the rest of the stack.
type reportingResolver struct {
	resolver sourcemap.Resolver
	report   func(error)
}

func (r reportingResolver) Resolve(ctx context.Context, input sourcemap.EventInput) (*sourcemap.Exception, error) {
	exception, err := r.resolver.Resolve(ctx, input)
	if err != nil {
		r.report(fmt.Errorf("symbolicate event: %w", err))
	}
	return exception, nil
}

// feederDrainGrace bounds how long shutdown waits for the feeder to exit on
// its own (DB healthy) before cancelling durable retries to unwedge it.
const feederDrainGrace = 2 * time.Second

type passwordReader func(int) ([]byte, error)

type userCreator interface {
	CreateUser(context.Context, string, string, string, auth.Role) (auth.User, error)
}

// userAdmin is the subset of auth.Administration the CLI's list, deactivate,
// and password commands need. It is deliberately narrower than the concrete
// type for the same reason internal/api/users.go's UserAdminService is: it
// keeps this package from accidentally reaching Create, Rename, or SetRole.
type userAdmin interface {
	List(context.Context) ([]auth.User, error)
	SetDeactivated(ctx context.Context, actorID, id int64, deactivated bool) (auth.User, error)
	SetPassword(ctx context.Context, id int64, password string) error
}

type commandDependencies struct {
	loadConfiguration func() (config.Config, error)
	openDatabase      func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error)
	serveHTTP         func(context.Context, string, http.Handler) error
	runMigration      func(string, migrate.Action, io.Writer) error
	newUserCreator    func(auth.UserRepository, auth.SessionRepository) userCreator
	newUserAdmin      func(auth.UserRepository, auth.SessionRepository) userAdmin
	readPassword      passwordReader
	passwordFD        int
}

func defaultDependencies(passwordFD int) commandDependencies {
	return commandDependencies{
		loadConfiguration: config.Load,
		openDatabase:      assembleDatabase,
		serveHTTP:         httpserver.Run,
		runMigration:      migrate.Run,
		newUserCreator: func(users auth.UserRepository, sessions auth.SessionRepository) userCreator {
			return auth.NewService(users, sessions, auth.ServiceOptions{})
		},
		newUserAdmin: func(users auth.UserRepository, sessions auth.SessionRepository) userAdmin {
			return auth.NewAdministration(users, sessions, auth.ServiceOptions{})
		},
		readPassword: term.ReadPassword,
		passwordFD:   passwordFD,
	}
}

// Run dispatches server, migration, and administrator bootstrap modes and
// returns a process exit code. passwordFD is the terminal descriptor used for
// hidden password input in administrator bootstrap mode.
func Run(ctx context.Context, args []string, passwordFD int, stdout, stderr io.Writer) int {
	return run(ctx, args, stdout, stderr, defaultDependencies(passwordFD))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, dependencies commandDependencies) int {
	mode, action, userOptions, groupOptions, valid := parseArguments(args)
	if !valid {
		printUsage(stderr)
		return 2
	}

	configuration, err := dependencies.loadConfiguration()
	if err != nil {
		fmt.Fprintf(stderr, "faultline: configuration: %v\n", err)
		return 1
	}

	if mode == "migrate" {
		if err := dependencies.runMigration(configuration.Database.URL, action, stdout); err != nil {
			fmt.Fprintf(stderr, "faultline: %v\n", err)
			return 1
		}
		return 0
	}

	reportPersistenceError := func(err error) {
		fmt.Fprintf(stderr, "faultline: persistence: %v\n", err)
	}

	database, err := dependencies.openDatabase(ctx, configuration.Database, reportPersistenceError)
	if err != nil {
		fmt.Fprintf(stderr, "faultline: database: %v\n", err)
		return 1
	}
	defer database.Close()

	switch mode {
	case "user-create":
		return runUserCreate(ctx, userOptions, stdout, stderr, database, dependencies)
	case "user-list":
		return runUserList(ctx, stdout, stderr, database, dependencies)
	case "user-deactivate":
		return runUserDeactivate(ctx, userOptions, stdout, stderr, database, dependencies)
	case "user-password":
		return runUserPassword(ctx, userOptions, stdout, stderr, database, dependencies)
	case "group-list":
		return runGroupList(ctx, stdout, stderr, database)
	case "group-create":
		return runGroupCreate(ctx, groupOptions, stdout, stderr, database)
	case "group-delete":
		return runGroupDelete(ctx, groupOptions, stdout, stderr, database)
	case "group-members":
		return runGroupMembers(ctx, groupOptions, stdout, stderr, database)
	case "group-projects":
		return runGroupProjects(ctx, groupOptions, stdout, stderr, database)
	}

	reportAuthError := func(err error) {
		fmt.Fprintf(stderr, "faultline: authentication: %v\n", err)
	}

	authService := auth.NewService(database.Users, database.Sessions, auth.ServiceOptions{
		Identities:  database.Identities,
		ReportError: reportAuthError,
	})
	userAdminService := auth.NewAdministration(database.Users, database.Sessions, auth.ServiceOptions{})
	scopeResolver := access.NewResolver(database.Access)
	projectService := project.NewService(database.Projects, database.Keys)
	artifactService := artifactauth.NewService(database.ArtifactTokens, artifactauth.Options{})
	groupingRuleService := grouping.NewRuleService(database.GroupingRules)
	groupService := group.NewService(database.Groups)

	symbolicationResolver := reportingResolver{
		resolver: proguard.NewResolver(
			database.Proguard,
			sourcemap.NewResolver(database.SourceMaps, sourcemap.NewParsedMapCache(parsedMapCacheBudget)),
		),
		report: reportPersistenceError,
	}
	issueService := issue.NewService(database.Issues, symbolicationResolver)
	reportIngestError := func(err error) {
		fmt.Fprintf(stderr, "faultline: ingestion: %v\n", err)
	}

	var persistence *pipeline.Pipeline
	var envelopeHandler *ingest.Handler

	if configuration.Durability.SpillEnabled {
		cfg := configuration.Durability
		dedup := ingest.NewDeduplicator()
		store, err := spill.Open(spill.Options{
			Dir:           cfg.SpillDir,
			SegmentBytes:  cfg.SpillSegmentBytes,
			SegmentMaxAge: cfg.SpillSegmentMaxAge,
			FsyncInterval: cfg.SpillFsyncInterval,
			MaxBytes:      cfg.SpillMaxBytes,
		})
		if err != nil {
			fmt.Fprintf(stderr, "faultline: spill: %v\n", err)
			return 1
		}

		persistence = pipeline.NewWithOptions(database.EventSink, pipeline.Options{Now: time.Now, ReportError: reportPersistenceError, Durable: true})
		persistence.Start()

		feeder := durable.New(durable.Config{
			Store:         store,
			Dedup:         dedup,
			Dispatch:      persistence.Dispatch,
			DispatchSpans: persistence.DispatchSpans,
			Flush:         persistence.Flush,
			ReportError:   reportPersistenceError,
			Rules:         database.GroupingRuleCache.Get,
			Settings:      database.settingsLookup(),
		})
		feederCtx, stopFeeder := context.WithCancel(context.Background())
		feederDone := make(chan struct{})
		go func() {
			defer close(feederDone)
			feeder.Run(feederCtx)
		}()

		defer func() {
			stopFeeder()
			// Let the feeder drain and exit on its own (healthy DB). If it can't within the
			// grace window, the shard workers are wedged retrying a failed DB — cancel those
			// retries so the wedged shard resumes draining, unblocking the feeder's in-flight
			// dispatch so it can exit. The feeder is the sole producer in durable mode, so once
			// it has returned no send can race Stop closing the shard channels.
			select {
			case <-feederDone:
			case <-time.After(feederDrainGrace):
				persistence.CancelRetries()
				<-feederDone // unbounded, but CancelRetries → lifeCtx cancel guarantees the wedged
				// shard's PersistBatch returns (pgx honors ctx cancellation), so the feeder exits
			}
			stopCtx, cancel := context.WithTimeout(context.Background(), persistenceShutdownTimeout)
			defer cancel()
			if err := persistence.Stop(stopCtx); err != nil {
				fmt.Fprintf(stderr, "faultline: persistence shutdown: %v\n", err)
			}
			if err := store.Close(); err != nil {
				fmt.Fprintf(stderr, "faultline: spill close: %v\n", err)
			}
		}()

		// No Rules or Settings here: Durable: true makes ServeSentry take its
		// spill branch, which never calls DispatchEnvelope — so a lookup set on
		// this handler would never be read. The feeder wired above (with its
		// own Rules and Settings) is what applies grouping rules, the
		// ip-identity gate and data scrubbing in durable mode, once envelopes are replayed out of
		// the spill log.
		envelopeHandler = ingest.NewHandlerWithOptions(ingest.HandlerOptions{
			Store:         database.IngestKeys,
			Now:           time.Now,
			ReportError:   reportIngestError,
			Dispatch:      persistence.Dispatch,
			DispatchSpans: persistence.DispatchSpans,
			Dedup:         dedup,
			Spiller:       storeSpiller{store},
			Durable:       true,
		})
	} else {
		persistence = pipeline.New(database.EventSink, 0, time.Now, reportPersistenceError)
		persistence.Start()
		defer func() {
			stopCtx, cancel := context.WithTimeout(context.Background(), persistenceShutdownTimeout)
			defer cancel()
			if err := persistence.Stop(stopCtx); err != nil {
				fmt.Fprintf(stderr, "faultline: persistence shutdown: %v\n", err)
			}
		}()

		envelopeHandler = ingest.NewHandlerWithOptions(ingest.HandlerOptions{
			Store:         database.IngestKeys,
			Now:           time.Now,
			ReportError:   reportIngestError,
			Dispatch:      persistence.Dispatch,
			DispatchSpans: persistence.DispatchSpans,
			Rules:         database.GroupingRuleCache.Get,
			Settings:      database.settingsLookup(),
		})
	}

	// One limiter instance for both sign-in paths: the SSO callback and the
	// password login share the leaky bucket rather than each getting its own
	// allowance per address.
	loginLimiter := auth.NewLoginLimiter(nil)

	var ssoHandler *api.SSOHandler
	if configuration.SSO.Enabled() {
		provider, err := auth.NewOIDCProvider(ctx, auth.OIDCOptions{
			Issuer:       configuration.SSO.Issuer,
			ClientID:     configuration.SSO.ClientID,
			ClientSecret: configuration.SSO.ClientSecret,
			RedirectURL:  configuration.SSO.RedirectURL,
		})
		if err != nil {
			fmt.Fprintf(stderr, "faultline: sso: %v\n", err)
			return 1
		}
		ssoHandler = api.NewSSOHandler(provider, authService, loginLimiter, auth.AllowedDomains(configuration.SSO.AllowedDomains), api.Options{
			CookieSecure: configuration.CookieSecure,
			ReportError:  reportAuthError,
		})
	}

	scrubService := scrub.NewService(database.Scrubbing)

	notificationSender := notify.NewSender(notify.SenderOptions{AllowPrivateNetworks: configuration.WebhookAllowPrivate})
	var notificationService api.NotificationService
	if database.NotificationChannels != nil {
		notificationService = notify.NewService(database.NotificationChannels, notificationSender, notify.ServiceOptions{
			PublicURL:            configuration.PublicURL,
			AllowPrivateNetworks: configuration.WebhookAllowPrivate,
		})
	}
	if database.NotificationQueue != nil {
		worker := notify.NewWorker(database.NotificationQueue, notificationSender, notify.WorkerOptions{
			PublicURL: configuration.PublicURL,
			ReportError: func(err error) {
				fmt.Fprintf(stderr, "faultline: notifications: %v\n", err)
			},
		})
		workerCtx, stopWorker := context.WithCancel(context.Background())
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			worker.Run(workerCtx)
		}()
		// A send cut off here is retried by whichever instance claims it once
		// its lease expires, so shutdown does not wait for in-flight sends.
		defer func() {
			stopWorker()
			<-workerDone
		}()
	}

	handler := app.New(app.Dependencies{
		Database:                   database.Pool,
		AuthService:                authService,
		UserAdminService:           userAdminService,
		ProjectService:             projectService,
		ArtifactService:            artifactService,
		SourceMaps:                 database.SourceMaps,
		ProguardMappings:           database.Proguard,
		UploadChunks:               database.UploadChunks,
		Releases:                   database.Releases,
		IssueService:               issueService,
		GroupingRuleService:        groupingRuleService,
		DataScrubbingService:       scrubService,
		GlobalDataScrubbingService: scrubService,
		NotificationService:        notificationService,
		GroupService:               groupService,
		EnvelopeHandler:            http.HandlerFunc(envelopeHandler.ServeSentry),
		LoginLimiter:               loginLimiter,
		ScopeResolver:              scopeResolver,
		SSOHandler:                 ssoHandler,
		CookieSecure:               configuration.CookieSecure,
		ReportError:                reportAuthError,
	})
	if err := dependencies.serveHTTP(ctx, configuration.Address, handler); err != nil {
		fmt.Fprintf(stderr, "faultline: server: %v\n", err)
		return 1
	}
	return 0
}

// storeSpiller adapts *spill.Store to ingest.Spiller, keeping internal/spill
// free of an ingest import.
type storeSpiller struct{ store *spill.Store }

func (s storeSpiller) Append(projectID int64, receivedAt time.Time, envelope []byte) error {
	return s.store.Append(spill.Record{ProjectID: projectID, ReceivedAt: receivedAt, Payload: envelope})
}

func parseArguments(args []string) (string, migrate.Action, userOptions, groupOptions, bool) {
	if len(args) == 0 {
		return "server", "", userOptions{}, groupOptions{}, true
	}
	if args[0] == "user" {
		mode, options, valid := ParseUserArguments(args[1:])
		return mode, "", options, groupOptions{}, valid
	}
	if args[0] == "group" {
		mode, options, valid := ParseGroupArguments(args[1:])
		return mode, "", userOptions{}, options, valid
	}
	if len(args) != 2 || args[0] != "migrate" {
		return "", "", userOptions{}, groupOptions{}, false
	}
	action := migrate.Action(args[1])
	switch action {
	case migrate.Up, migrate.Down, migrate.Version:
		return "migrate", action, userOptions{}, groupOptions{}, true
	default:
		return "", "", userOptions{}, groupOptions{}, false
	}
}

func printUsage(output io.Writer) {
	fmt.Fprintln(output, "usage: faultline [migrate up|down|version]")
	fmt.Fprintln(output, "usage: faultline user create --email <email> --name <name>")
	fmt.Fprintln(output, "       faultline user list")
	fmt.Fprintln(output, "       faultline user deactivate --email <email>")
	fmt.Fprintln(output, "       faultline user password --email <email>")
	fmt.Fprintln(output, "       faultline group create --name <name>")
	fmt.Fprintln(output, "       faultline group list")
	fmt.Fprintln(output, "       faultline group delete --name <name>")
	fmt.Fprintln(output, "       faultline group members --name <name> (--add|--remove) <email>")
	fmt.Fprintln(output, "       faultline group projects --name <name> (--add|--remove) <project slug>")
}
