package command

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/api"
	"github.com/ramilS/faultline/internal/artifactauth"
	"github.com/ramilS/faultline/internal/auth"
	"github.com/ramilS/faultline/internal/grouping"
	"github.com/ramilS/faultline/internal/ingest"
	"github.com/ramilS/faultline/internal/platform/config"
	migration "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/proguard"
	"github.com/ramilS/faultline/internal/project"
	"github.com/ramilS/faultline/internal/sentrycli"
	"github.com/ramilS/faultline/internal/sourcemap"
	"github.com/ramilS/faultline/internal/storage"
)

type fakePool struct{ closed bool }

func (*fakePool) Ping(context.Context) error { return nil }
func (p *fakePool) Close()                   { p.closed = true }

func TestRunDispatchesServerAndClosesPool(t *testing.T) {
	pool := &fakePool{}
	const (
		submittedEmail    = "unknown@example.com"
		submittedPassword = "do not log this"
	)
	var stderr bytes.Buffer
	dependencies := testDependencies(
		func() (config.Config, error) { return validConfig(), nil },
		func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return &runtimeDatabase{Pool: pool, Users: unknownUsers{}}, nil
		},
		func(_ context.Context, _ string, handler http.Handler) error {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "https://example.com/api/v1/auth/login", strings.NewReader(`{"email":"`+submittedEmail+`","password":"`+submittedPassword+`"}`))
			request.Header.Set("Origin", "https://example.com")
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("login status = %d, want 401: %s", recorder.Code, recorder.Body.String())
			}
			return nil
		},
		func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	)

	if code := run(t.Context(), nil, io.Discard, &stderr, dependencies); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !pool.closed {
		t.Fatal("database pool was not closed")
	}
	if strings.Contains(stderr.String(), submittedEmail) || strings.Contains(stderr.String(), submittedPassword) {
		t.Fatalf("stderr leaked submitted credentials: %q", stderr.String())
	}
}

func TestRunWiresArtifactServiceIntoHTTPApplication(t *testing.T) {
	pool := &fakePool{}
	artifacts := &artifactRepositoryStub{}
	database := &runtimeDatabase{
		Pool:           pool,
		Users:          unknownUsers{},
		Sessions:       authenticatedSessions{},
		Projects:       projectsForArtifactRoute{},
		Keys:           artifactRouteKeys{},
		ArtifactTokens: artifacts,
		Access:         accessRepositoryStub{},
	}
	dependencies := testDependencies(
		func() (config.Config, error) { return validConfig(), nil },
		func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return database, nil
		},
		func(_ context.Context, _ string, handler http.Handler) error {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/artifact-tokens", http.NoBody)
			request.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("artifact token list status = %d, want 200: %s", recorder.Code, recorder.Body.String())
			}
			return nil
		},
		func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	)

	if code := run(t.Context(), nil, io.Discard, io.Discard, dependencies); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !artifacts.listed {
		t.Fatal("artifact repository was not reached through assembled application")
	}
}

// groupingRuleRepositoryStub is a grouping.RuleRepository whose only expected
// call, through the real assembled application, is ListByProjectSlug (the
// dashboard's GET .../grouping-rules route). Every other method reports an
// unexpected call, exactly like artifactRepositoryStub above — this is the
// route-reachability proof for Hazard 1: if run() ever stopped setting
// app.Dependencies.GroupingRuleService, the routes app.go guards on it would
// 404 instead of reaching this stub, and listed would stay false.
type groupingRuleRepositoryStub struct{ listed bool }

func (r *groupingRuleRepositoryStub) ListByProjectID(context.Context, int64) ([]grouping.Rule, error) {
	return nil, nil
}
func (r *groupingRuleRepositoryStub) ListByProjectSlug(context.Context, string) ([]grouping.Rule, error) {
	r.listed = true
	return nil, nil
}
func (*groupingRuleRepositoryStub) Create(context.Context, int64, string, string) (grouping.Rule, error) {
	return grouping.Rule{}, errors.New("unexpected create")
}
func (*groupingRuleRepositoryStub) Update(context.Context, int64, int64, string, string) (grouping.Rule, error) {
	return grouping.Rule{}, errors.New("unexpected update")
}
func (*groupingRuleRepositoryStub) Move(context.Context, int64, int64, grouping.MoveDirection) (grouping.Rule, error) {
	return grouping.Rule{}, errors.New("unexpected move")
}
func (*groupingRuleRepositoryStub) Delete(context.Context, int64, int64) error {
	return errors.New("unexpected delete")
}

// TestRunWiresGroupingRuleServiceIntoHTTPApplication is Hazard 1's proof: it
// runs the real run() assembly (not a hand-built app.Dependencies literal) and
// asserts the grouping-rules list route actually reaches the repository. If
// run() ever failed to set GroupingRuleService, app.go's nil guard would drop
// every grouping-rule route and this request would 404 instead of 200.
func TestRunWiresGroupingRuleServiceIntoHTTPApplication(t *testing.T) {
	pool := &fakePool{}
	rules := &groupingRuleRepositoryStub{}
	database := &runtimeDatabase{
		Pool:          pool,
		Users:         unknownUsers{},
		Sessions:      authenticatedSessions{},
		Projects:      projectsForArtifactRoute{},
		Keys:          artifactRouteKeys{},
		GroupingRules: rules,
		Access:        accessRepositoryStub{},
	}
	dependencies := testDependencies(
		func() (config.Config, error) { return validConfig(), nil },
		func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return database, nil
		},
		func(_ context.Context, _ string, handler http.Handler) error {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/web/grouping-rules", http.NoBody)
			request.AddCookie(&http.Cookie{Name: api.SessionCookieName, Value: base64.RawURLEncoding.EncodeToString(make([]byte, 32))})
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("grouping rules list status = %d, want 200: %s", recorder.Code, recorder.Body.String())
			}
			return nil
		},
		func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	)

	if code := run(t.Context(), nil, io.Discard, io.Discard, dependencies); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !rules.listed {
		t.Fatal("grouping rule repository was not reached through the assembled application")
	}
}

// groupingRuleStoreStub is a grouping.RuleStore that counts every
// ListByProjectID call, letting a test observe cache hits vs. misses.
type groupingRuleStoreStub struct {
	mu    sync.Mutex
	calls int
}

func (s *groupingRuleStoreStub) ListByProjectID(context.Context, int64) ([]grouping.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	return nil, nil
}

func (s *groupingRuleStoreStub) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

// ingestKeyStoreStub resolves every public key to the same project, exactly
// as much as this test needs from ingest.KeyStore.
type ingestKeyStoreStub struct{ projectID int64 }

func (s ingestKeyStoreStub) FindByPublicKey(context.Context, string) (ingest.KeyInfo, error) {
	return ingest.KeyInfo{ProjectID: s.projectID, PublicKey: "ingest-key", RateLimitRPS: 1000, RateLimitBurst: 1000}, nil
}

// noopEventSink discards persisted batches; this test only cares about the
// rule cache reached during the synchronous ingest request, not persistence.
type noopEventSink struct{}

func (noopEventSink) PersistBatch(context.Context, storage.Batch) error { return nil }

// TestRunSharesGroupingRuleCacheBetweenIngestAndMergeInvalidator is Hazard 2's
// proof. ingest.DispatchEnvelope resolves grouping rules synchronously inside
// the HTTP request (see internal/ingest/dispatch.go), so this test can submit
// envelopes through the real run() assembly and observe the underlying
// RuleStore's call count directly, with no goroutine timing involved.
//
// It seeds runtimeDatabase.GroupingRuleCache with a cache built on a counting
// store, submits two envelopes for the same project (first: cold-cache
// fetch; second: still within the 30s TTL, no re-fetch), then calls
// Invalidate on that exact cache instance — the same method
// storagepostgres.issueRepository's merge invalidator calls after a merge
// transaction commits — and submits a third envelope. If run() wired ingest
// to this cache instance (rather than constructing a second, unrelated
// RuleCache), the third request must miss the cache and re-hit the store: a
// second RuleCache instance would still show its own warm entry and the call
// count would stay at 1, silently reproducing the exact bug the invalidation
// exists to prevent.
func TestRunSharesGroupingRuleCacheBetweenIngestAndMergeInvalidator(t *testing.T) {
	const projectID = int64(7)
	store := &groupingRuleStoreStub{}
	ruleCache := grouping.NewRuleCache(store, time.Now)
	database := &runtimeDatabase{
		Pool:              &fakePool{},
		Users:             unknownUsers{},
		Sessions:          authenticatedSessions{},
		Projects:          projectsForArtifactRoute{},
		Keys:              artifactRouteKeys{},
		IngestKeys:        ingestKeyStoreStub{projectID: projectID},
		EventSink:         noopEventSink{},
		GroupingRuleCache: ruleCache,
	}
	dependencies := testDependencies(
		func() (config.Config, error) { return validConfig(), nil },
		func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return database, nil
		},
		func(_ context.Context, _ string, handler http.Handler) error {
			postEnvelope := func(eventID string) {
				path := fmt.Sprintf("/api/%d/envelope/?sentry_key=ingest-key", projectID)
				payload := `{"event_id":"` + eventID + `","timestamp":"2026-07-21T00:00:00Z","level":"error","message":"boom"}`
				body := `{}` + "\n" + `{"type":"event"}` + "\n" + payload + "\n"
				recorder := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				handler.ServeHTTP(recorder, request)
				if recorder.Code != http.StatusOK {
					t.Fatalf("envelope %s status = %d, want 200: %s", eventID, recorder.Code, recorder.Body.String())
				}
			}

			postEnvelope("11111111-1111-1111-1111-111111111111")
			if got := store.callCount(); got != 1 {
				t.Fatalf("rule store calls after first ingest = %d, want 1 (cold cache load)", got)
			}

			postEnvelope("22222222-2222-2222-2222-222222222222")
			if got := store.callCount(); got != 1 {
				t.Fatalf("rule store calls after second ingest = %d, want 1 (warm cache must not re-fetch)", got)
			}

			ruleCache.Invalidate(projectID)

			postEnvelope("33333333-3333-3333-3333-333333333333")
			if got := store.callCount(); got != 2 {
				t.Fatalf("rule store calls after invalidate + third ingest = %d, want 2 (ingest must read the cache merge invalidates)", got)
			}
			return nil
		},
		func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	)

	if code := run(t.Context(), nil, io.Discard, io.Discard, dependencies); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
}

type unknownUsers struct{}

func (unknownUsers) Create(context.Context, auth.NewUser) (auth.User, error) {
	return auth.User{}, errors.New("unexpected create")
}
func (unknownUsers) FindByEmail(context.Context, string) (auth.User, error) {
	return auth.User{}, auth.ErrNotFound
}
func (unknownUsers) FindByID(context.Context, int64) (auth.User, error) {
	return auth.User{}, auth.ErrNotFound
}
func (unknownUsers) List(context.Context) ([]auth.User, error)       { return nil, nil }
func (unknownUsers) ListActive(context.Context) ([]auth.User, error) { return nil, nil }
func (unknownUsers) ListAssignableForProject(context.Context, auth.ProjectScope, string) ([]auth.User, error) {
	return nil, nil
}
func (unknownUsers) Update(context.Context, int64, auth.UserChanges) (auth.User, error) {
	return auth.User{}, auth.ErrNotFound
}
func (unknownUsers) UpdatePasswordHash(context.Context, int64, string) error { return nil }

type authenticatedSessions struct{}

func (authenticatedSessions) Create(context.Context, auth.NewSession) error { return nil }
func (authenticatedSessions) FindByTokenHash(context.Context, [32]byte) (auth.Session, auth.User, error) {
	now := time.Now()
	return auth.Session{ExpiresAt: now.Add(time.Hour), LastSeenAt: now}, auth.User{ID: 1}, nil
}
func (authenticatedSessions) Touch(context.Context, [32]byte, time.Time) error  { return nil }
func (authenticatedSessions) DeleteByTokenHash(context.Context, [32]byte) error { return nil }
func (authenticatedSessions) DeleteExpired(context.Context, time.Time) error    { return nil }
func (authenticatedSessions) DeleteByUserID(context.Context, int64) error       { return nil }

func (authenticatedSessions) DeleteByUserIDExcept(context.Context, int64, [32]byte) error {
	return nil
}

// accessRepositoryStub grants every authenticatedSessions caller access to no
// projects. These route-reachability tests exercise RequireSession with a
// non-owner user (ID 1), which now resolves a scope on every request; a nil
// Access field would panic through access.Resolver rather than degrade, so
// route-reachability tests that authenticate with a session cookie wire this
// in. No route reads the resolved scope yet, so an empty scope cannot mask
// what these tests actually assert: that the request reached the stub
// repository behind the route.
type accessRepositoryStub struct{}

func (accessRepositoryStub) ProjectIDsForUser(context.Context, int64) ([]int64, error) {
	return nil, nil
}

type projectsForArtifactRoute struct{}

func (projectsForArtifactRoute) Create(context.Context, project.CreateProject) (project.Project, error) {
	return project.Project{}, errors.New("unexpected create")
}
func (projectsForArtifactRoute) List(context.Context, access.Scope) ([]project.Project, error) {
	return nil, errors.New("unexpected list")
}
func (projectsForArtifactRoute) FindBySlug(context.Context, access.Scope, string) (project.Project, error) {
	return project.Project{ID: 1, Slug: "web"}, nil
}
func (projectsForArtifactRoute) Update(context.Context, string, project.UpdateProject) (project.Project, error) {
	return project.Project{}, errors.New("unexpected update")
}

type artifactRouteKeys struct{}

func (artifactRouteKeys) Create(context.Context, project.CreateKey) (project.Key, error) {
	return project.Key{}, errors.New("unexpected create")
}
func (artifactRouteKeys) ListByProjectSlug(context.Context, access.Scope, string) ([]project.Key, error) {
	return nil, errors.New("unexpected list")
}
func (artifactRouteKeys) Revoke(context.Context, access.Scope, string, int64) error {
	return errors.New("unexpected revoke")
}

type artifactRepositoryStub struct{ listed bool }

func (*artifactRepositoryStub) Create(context.Context, artifactauth.Token) (artifactauth.Token, error) {
	return artifactauth.Token{}, errors.New("unexpected create")
}
func (r *artifactRepositoryStub) ListByProjectSlug(context.Context, string) ([]artifactauth.Token, error) {
	r.listed = true
	return nil, nil
}
func (*artifactRepositoryStub) Revoke(context.Context, string, int64) error {
	return errors.New("unexpected revoke")
}
func (*artifactRepositoryStub) FindByPrefix(context.Context, string) (artifactauth.Token, error) {
	return artifactauth.Token{}, errors.New("unexpected find")
}
func (*artifactRepositoryStub) Touch(context.Context, int64) error {
	return errors.New("unexpected touch")
}

// authenticatingArtifactRepo accepts exactly one raw token whose prefix/hash it
// was seeded with, scoped to the given project.
type authenticatingArtifactRepo struct {
	prefix    string
	hash      [32]byte
	projectID int64
	touched   bool
}

func (*authenticatingArtifactRepo) Create(context.Context, artifactauth.Token) (artifactauth.Token, error) {
	return artifactauth.Token{}, errors.New("unexpected create")
}
func (*authenticatingArtifactRepo) ListByProjectSlug(context.Context, string) ([]artifactauth.Token, error) {
	return nil, errors.New("unexpected list")
}
func (*authenticatingArtifactRepo) Revoke(context.Context, string, int64) error {
	return errors.New("unexpected revoke")
}
func (r *authenticatingArtifactRepo) FindByPrefix(_ context.Context, prefix string) (artifactauth.Token, error) {
	if prefix != r.prefix {
		return artifactauth.Token{}, artifactauth.ErrNotFound
	}
	return artifactauth.Token{ID: 5, ProjectID: r.projectID, TokenPrefix: r.prefix, TokenHash: r.hash}, nil
}
func (r *authenticatingArtifactRepo) Touch(context.Context, int64) error {
	r.touched = true
	return nil
}

type fakeSourceMapResolver struct {
	exception *sourcemap.Exception
	err       error
}

func (f *fakeSourceMapResolver) Resolve(context.Context, sourcemap.EventInput) (*sourcemap.Exception, error) {
	return f.exception, f.err
}

// TestReportingResolverReportsFailureButKeepsPartialSymbolication proves the
// production wiring's failure path: a resolver error is reported exactly
// once through the injected hook, the error itself is swallowed rather than
// bubbling into the issue read path, and any exception the underlying
// resolver did manage to build is still returned rather than thrown away.
func TestReportingResolverReportsFailureButKeepsPartialSymbolication(t *testing.T) {
	partial := &sourcemap.Exception{Type: "TypeError", Value: "cart is undefined"}
	underlying := &fakeSourceMapResolver{exception: partial, err: errors.New("parse source map: unexpected EOF")}
	var reported []error
	resolver := reportingResolver{resolver: underlying, report: func(err error) { reported = append(reported, err) }}

	exception, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: 1, Release: "web@1"})
	if err != nil {
		t.Fatalf("Resolve() error = %v, want nil (reported, not returned)", err)
	}
	if exception != partial {
		t.Fatalf("Resolve() exception = %#v, want the partially-symbolicated exception preserved", exception)
	}
	if len(reported) != 1 {
		t.Fatalf("report called %d times, want exactly once", len(reported))
	}
	if !strings.Contains(reported[0].Error(), "parse source map: unexpected EOF") {
		t.Fatalf("reported error = %v, want it to wrap the underlying resolver error", reported[0])
	}
}

func TestReportingResolverPassesThroughOnSuccess(t *testing.T) {
	exception := &sourcemap.Exception{Type: "TypeError"}
	underlying := &fakeSourceMapResolver{exception: exception}
	reported := false
	resolver := reportingResolver{resolver: underlying, report: func(error) { reported = true }}

	got, err := resolver.Resolve(t.Context(), sourcemap.EventInput{ProjectID: 1, Release: "web@1"})
	if err != nil || got != exception {
		t.Fatalf("Resolve() = %#v, %v, want (%#v, nil)", got, err, exception)
	}
	if reported {
		t.Fatal("report must not be called on success")
	}
}

type sourceMapRepositoryStub struct{ put bool }

func (r *sourceMapRepositoryStub) Put(context.Context, int64, string, string, sourcemap.DecodedUpload) (sourcemap.Map, bool, error) {
	r.put = true
	return sourcemap.Map{ID: 1}, true, nil
}
func (*sourceMapRepositoryStub) Find(context.Context, int64, string, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}

func (*sourceMapRepositoryStub) FindByDebugID(context.Context, int64, string) (sourcemap.Map, error) {
	return sourcemap.Map{}, sourcemap.ErrNotFound
}

func (*sourceMapRepositoryStub) PutBundle(context.Context, int64, string, []sourcemap.BundleEntry) error {
	return errors.New("unused")
}

type unusedChunkStore struct{}

func (unusedChunkStore) Save(context.Context, int64, map[sentrycli.Checksum][]byte) error {
	return errors.New("unused")
}
func (unusedChunkStore) Load(context.Context, int64, []sentrycli.Checksum) (map[sentrycli.Checksum][]byte, error) {
	return nil, errors.New("unused")
}
func (unusedChunkStore) Delete(context.Context, int64, []sentrycli.Checksum) error { return nil }
func (unusedChunkStore) DeleteExpired(context.Context, time.Time) (int64, error)   { return 0, nil }

type unusedReleaseStore struct{}

func (unusedReleaseStore) Upsert(context.Context, int64, string) error { return errors.New("unused") }
func (unusedReleaseStore) Finalize(context.Context, int64, string, time.Time) error {
	return errors.New("unused")
}

type proguardMappingRepositoryStub struct{ put bool }

func (r *proguardMappingRepositoryStub) Put(context.Context, int64, string, proguard.DecodedUpload) (bool, error) {
	r.put = true
	return true, nil
}

func (*proguardMappingRepositoryStub) Find(context.Context, int64, string) (proguard.Stored, error) {
	return proguard.Stored{}, proguard.ErrNotFound
}

// TestRunWiresProguardUploadIntoHTTPApplication proves database.Proguard, set by
// assembleDatabase in production, actually reaches the assembled HTTP
// application. Every other test in this file injects assembleDatabase and
// bypasses that function entirely, so nothing else exercises the line that
// wires the real Postgres-backed repository into the resolver and into route
// registration — a dropped wire there would panic on the first proguard-tagged
// event and no test would catch it.
func TestRunWiresProguardUploadIntoHTTPApplication(t *testing.T) {
	prefix := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32))
	raw := "fl_art_" + prefix + "_" + secret
	artifacts := &authenticatingArtifactRepo{prefix: prefix, hash: sha256.Sum256([]byte(raw)), projectID: 1}
	mappings := &proguardMappingRepositoryStub{}
	database := &runtimeDatabase{
		Pool:           &fakePool{},
		Users:          unknownUsers{},
		Sessions:       authenticatedSessions{},
		Projects:       projectsForArtifactRoute{},
		Keys:           artifactRouteKeys{},
		ArtifactTokens: artifacts,
		SourceMaps:     &sourceMapRepositoryStub{},
		Proguard:       mappings,
		UploadChunks:   unusedChunkStore{},
		Releases:       unusedReleaseStore{},
	}
	dependencies := testDependencies(
		func() (config.Config, error) { return validConfig(), nil },
		func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return database, nil
		},
		func(_ context.Context, _ string, handler http.Handler) error {
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			entry, err := writer.Create("proguard/cd395950-361a-563d-9df3-49aea740c6f6.txt")
			if err != nil {
				t.Fatalf("zip create: %v", err)
			}
			if _, err := entry.Write([]byte("com.acme.Cart -> a.a:\n    1:1:void run():3:3 -> a\n")); err != nil {
				t.Fatalf("zip write: %v", err)
			}
			if err := writer.Close(); err != nil {
				t.Fatalf("zip close: %v", err)
			}
			var body bytes.Buffer
			multipartWriter := multipart.NewWriter(&body)
			part, err := multipartWriter.CreateFormFile("file", "upload")
			if err != nil {
				t.Fatalf("multipart create: %v", err)
			}
			if _, err := part.Write(archive.Bytes()); err != nil {
				t.Fatalf("multipart write: %v", err)
			}
			if err := multipartWriter.Close(); err != nil {
				t.Fatalf("multipart close: %v", err)
			}

			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/0/projects/demo/demo/files/dsyms/", &body)
			request.Header.Set("Authorization", "Bearer "+raw)
			request.Header.Set("Content-Type", multipartWriter.FormDataContentType())
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("proguard mapping upload status = %d, want 201: %s", recorder.Code, recorder.Body.String())
			}
			return nil
		},
		func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	)

	if code := run(t.Context(), nil, io.Discard, io.Discard, dependencies); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !mappings.put {
		t.Fatal("proguard mapping repository was not reached through assembled application")
	}
}

func TestRunWiresSourceMapUploadIntoHTTPApplication(t *testing.T) {
	prefix := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{2}, 32))
	raw := "fl_art_" + prefix + "_" + secret
	artifacts := &authenticatingArtifactRepo{prefix: prefix, hash: sha256.Sum256([]byte(raw)), projectID: 1}
	sourceMaps := &sourceMapRepositoryStub{}
	database := &runtimeDatabase{
		Pool:           &fakePool{},
		Users:          unknownUsers{},
		Sessions:       authenticatedSessions{},
		Projects:       projectsForArtifactRoute{},
		Keys:           artifactRouteKeys{},
		ArtifactTokens: artifacts,
		SourceMaps:     sourceMaps,
	}
	dependencies := testDependencies(
		func() (config.Config, error) { return validConfig(), nil },
		func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return database, nil
		},
		func(_ context.Context, _ string, handler http.Handler) error {
			recorder := httptest.NewRecorder()
			body := `{"version":3,"file":"app.js","sources":["app.ts"],"sourcesContent":["console.log(1);"],"names":[],"mappings":"AAAA"}`
			request := httptest.NewRequest(http.MethodPost, "/api/v1/projects/web/sourcemaps?release=web%401&path=%2Fassets%2Fapp.js", strings.NewReader(body))
			request.Header.Set("Authorization", "Bearer "+raw)
			request.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusCreated {
				t.Fatalf("source map upload status = %d, want 201: %s", recorder.Code, recorder.Body.String())
			}
			return nil
		},
		func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	)

	if code := run(t.Context(), nil, io.Discard, io.Discard, dependencies); code != 0 {
		t.Fatalf("run() = %d, want 0", code)
	}
	if !sourceMaps.put {
		t.Fatal("source map repository was not reached through assembled application")
	}
	if !artifacts.touched {
		t.Fatal("artifact token was not authenticated through assembled application")
	}
}

func TestRunReportsServerFailureAndClosesPool(t *testing.T) {
	pool := &fakePool{}
	dependencies := testDependencies(
		func() (config.Config, error) { return validConfig(), nil },
		func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return &runtimeDatabase{Pool: pool}, nil
		},
		func(context.Context, string, http.Handler) error { return errors.New("listener failed") },
		func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	)

	var stderr bytes.Buffer
	if code := run(t.Context(), nil, io.Discard, &stderr, dependencies); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "faultline: server: listener failed") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !pool.closed {
		t.Fatal("database pool was not closed")
	}
}

func TestRunDispatchesMigrationActions(t *testing.T) {
	for _, action := range []migration.Action{migration.Up, migration.Down, migration.Version} {
		t.Run(string(action), func(t *testing.T) {
			var got migration.Action
			dependencies := testDependencies(
				func() (config.Config, error) { return validConfig(), nil },
				func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
					return nil, errors.New("unexpected pool")
				},
				func(context.Context, string, http.Handler) error { return errors.New("unexpected server") },
				func(_ string, action migration.Action, _ io.Writer) error { got = action; return nil },
			)
			if code := run(t.Context(), []string{"migrate", string(action)}, io.Discard, io.Discard, dependencies); code != 0 {
				t.Fatalf("run() = %d, want 0", code)
			}
			if got != action {
				t.Fatalf("migration action = %q, want %q", got, action)
			}
		})
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{{"migrate"}, {"migrate", "force"}, {"migrate", "up", "extra"}, {"serve"}} {
		var stderr bytes.Buffer
		if code := run(t.Context(), args, io.Discard, &stderr, commandDependencies{}); code != 2 {
			t.Fatalf("run(%v) = %d, want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "usage:") {
			t.Fatalf("Run(%v) stderr = %q", args, stderr.String())
		}
	}
}

func TestRunReportsConfigurationFailure(t *testing.T) {
	dependencies := testDependencies(
		func() (config.Config, error) { return config.Config{}, errors.New("bad config") }, nil, nil, nil,
	)
	var stderr bytes.Buffer
	if code := run(t.Context(), nil, io.Discard, &stderr, dependencies); code != 1 {
		t.Fatalf("run() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "bad config") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func validConfig() config.Config {
	return config.Config{Address: ":8080", Database: config.DatabaseConfig{URL: "postgres://db/faultline", MaxConns: 1}}
}

func testDependencies(
	load func() (config.Config, error),
	open func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error),
	serve func(context.Context, string, http.Handler) error,
	migrate func(string, migration.Action, io.Writer) error,
) commandDependencies {
	dependencies := commandDependencies{
		loadConfiguration: func() (config.Config, error) { return validConfig(), nil },
		openDatabase: func(context.Context, config.DatabaseConfig, func(error)) (*runtimeDatabase, error) {
			return nil, errors.New("unexpected database")
		},
		serveHTTP:    func(context.Context, string, http.Handler) error { return errors.New("unexpected server") },
		runMigration: func(string, migration.Action, io.Writer) error { return errors.New("unexpected migration") },
	}
	if load != nil {
		dependencies.loadConfiguration = load
	}
	if open != nil {
		dependencies.openDatabase = open
	}
	if serve != nil {
		dependencies.serveHTTP = serve
	}
	if migrate != nil {
		dependencies.runMigration = migrate
	}
	return dependencies
}
