//go:build integration

package command_test

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	"github.com/ramilS/faultline/internal/project"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

const ingestPublicKey = "ingest-public-key"

// TestSpillSurvivesKill is the completion-boundary test: every envelope
// acknowledged with 200 must survive an immediate SIGKILL and reappear in
// Postgres once the process restarts and replays its spill directory.
func TestSpillSurvivesKill(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	verifyPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer verifyPool.Close()

	projectID := seedProjectAndKey(t, ctx, verifyPool, ingestPublicKey)

	binPath := buildFaultlineBinary(t)
	spillDir := t.TempDir()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	env := buildEnv(databaseURL, addr, spillDir, envOptions{spillEnabled: true, segmentMaxAge: "200ms"})

	server := startServer(t, binPath, env)
	waitReady(t, addr)

	const eventCount = 50
	eventIDs := make([]string, eventCount)
	for i := range eventIDs {
		eventIDs[i] = uuid.NewString()
		resp := postEnvelope(t, addr, projectID, ingestPublicKey, eventIDs[i])
		if resp.StatusCode != http.StatusOK {
			body := readAndClose(t, resp)
			t.Fatalf("post %d: status = %d, want 200: %s", i, resp.StatusCode, body)
		}
		resp.Body.Close()
	}

	// Pre-kill assertion: the durable spill feeder only drains SEALED
	// segments, and the active segment holding the most-recent event(s) is
	// never sealed after this last POST (no subsequent Append, and the
	// process is killed before Close), so at least the last event cannot be
	// in Postgres yet. Asserting preKillCount < eventCount proves the
	// post-restart count of 50 required replay to recover the remainder,
	// rather than everything having drained before the kill.
	preKillCount := queryEventCount(t, ctx, databaseURL)
	if preKillCount >= eventCount {
		t.Fatalf("pre-kill count = %d, want < %d: replay would not be exercised by this test", preKillCount, eventCount)
	}

	// SIGKILL immediately after the last 202, before a full drain, is the
	// crash this test exists to survive.
	server.kill(t)

	server = startServer(t, binPath, env)
	waitReady(t, addr)

	deadline := time.Now().Add(30 * time.Second)
	var persistedIDs []string
	for {
		persistedIDs = queryEventIDs(t, ctx, verifyPool, projectID)
		if len(persistedIDs) >= eventCount {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("spill did not replay in time: got %d/%d events; server stderr:\n%s", len(persistedIDs), eventCount, server.stderrString())
		}
		time.Sleep(200 * time.Millisecond)
	}

	assertSameIDSet(t, eventIDs, persistedIDs)
	t.Logf("replay recovered %d events (pre-kill=%d)", eventCount-preKillCount, preKillCount)
}

// TestFlagOffNoSpillDir is the parity test: with the durability flag off, the
// spill machinery must not run at all, even when SPILL_DIR is configured.
func TestFlagOffNoSpillDir(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	verifyPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer verifyPool.Close()

	projectID := seedProjectAndKey(t, ctx, verifyPool, ingestPublicKey)

	binPath := buildFaultlineBinary(t)
	spillDir := t.TempDir()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	env := buildEnv(databaseURL, addr, spillDir, envOptions{spillEnabled: false})

	server := startServer(t, binPath, env)
	waitReady(t, addr)
	defer server.kill(t)

	for i := range 10 {
		resp := postEnvelope(t, addr, projectID, ingestPublicKey, uuid.NewString())
		if resp.StatusCode != http.StatusOK {
			body := readAndClose(t, resp)
			t.Fatalf("post %d: status = %d, want 200: %s", i, resp.StatusCode, body)
		}
		resp.Body.Close()
	}

	// Give any (absent) spill goroutine a chance to misbehave before asserting.
	time.Sleep(500 * time.Millisecond)

	segments, err := filepath.Glob(filepath.Join(spillDir, "segment-*.log"))
	if err != nil {
		t.Fatalf("glob spill dir: %v", err)
	}
	if len(segments) != 0 {
		t.Fatalf("spill files present with flag off: %v", segments)
	}
}

// TestSpillDrainsAfterDBRecovery is the DB-down recovery test: while Postgres
// is unreachable the spill WAL absorbs accepted envelopes (spill.Append is
// local disk I/O, independent of the database), and once Postgres comes back
// the feeder drains every backlogged segment without losing an accepted event.
//
// The store always keeps exactly one active segment file open on disk for the
// lifetime of the process (Store.Open creates it eagerly), so "SPILL_DIR
// drains" cannot mean "zero segment-*.log files" while the server keeps
// running — only a restart clears that last file. This test instead polls
// until at most one segment file remains (the harmless, still-open active
// segment) and every accepted event from the outage window is present in
// Postgres. Small "flush trigger" pings after recovery force the age-based
// roll that seals whatever the outage left in the active segment, since a
// segment only seals on a later Append, never spontaneously.
func TestSpillDrainsAfterDBRecovery(t *testing.T) {
	databaseURL, containerName := startNamedTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	verifyPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer verifyPool.Close()

	projectID := seedProjectAndKey(t, ctx, verifyPool, ingestPublicKey)

	binPath := buildFaultlineBinary(t)
	spillDir := t.TempDir()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	env := buildEnv(databaseURL, addr, spillDir, envOptions{
		spillEnabled:  true,
		segmentMaxAge: "150ms",
		segmentBytes:  "2048",
		maxBytes:      "8192",
	})

	server := startServer(t, binPath, env)
	waitReady(t, addr)
	defer server.kill(t)

	// Warm the ingest key cache (30s positive TTL) while the database is still
	// up: the first lookup for a key is always a DB round-trip, and that must
	// not happen after the outage starts, since spill.Append itself never
	// touches Postgres.
	warmupID := uuid.NewString()
	warmupResp := postEnvelope(t, addr, projectID, ingestPublicKey, warmupID)
	if warmupResp.StatusCode != http.StatusOK {
		body := readAndClose(t, warmupResp)
		t.Fatalf("warm-up post: status = %d, want 200: %s", warmupResp.StatusCode, body)
	}
	warmupResp.Body.Close()
	acceptedIDs := []string{warmupID}

	dockerStop(t, containerName)

	const maxAttempts = 200
	sawSpillFull := false
	for i := range maxAttempts {
		id := uuid.NewString()
		resp := postEnvelope(t, addr, projectID, ingestPublicKey, id)
		status := resp.StatusCode
		resp.Body.Close()
		if status == http.StatusOK {
			acceptedIDs = append(acceptedIDs, id)
			continue
		}
		if status == http.StatusTooManyRequests {
			sawSpillFull = true
			break
		}
		t.Fatalf("post %d during outage: unexpected status %d", i, status)
	}
	if len(acceptedIDs) == 0 {
		t.Fatalf("no envelopes were accepted during the outage")
	}
	t.Logf("accepted %d envelopes during outage (spill full observed: %v)", len(acceptedIDs), sawSpillFull)

	dockerStart(t, containerName)
	waitPostgresReady(t, ctx, verifyPool)

	deadline := time.Now().Add(60 * time.Second)
	trailerTicker := time.NewTicker(200 * time.Millisecond)
	defer trailerTicker.Stop()

	for {
		select {
		case <-trailerTicker.C:
			// A trailer ping forces the store to notice its age threshold on the
			// next Append, sealing whatever backlog is sitting in the active
			// segment so the feeder can drain it.
			resp := postEnvelope(t, addr, projectID, ingestPublicKey, uuid.NewString())
			resp.Body.Close()
		default:
		}

		persistedIDs := queryEventIDs(t, ctx, verifyPool, projectID)
		allPersisted := containsAll(persistedIDs, acceptedIDs)

		segments, err := filepath.Glob(filepath.Join(spillDir, "segment-*.log"))
		if err != nil {
			t.Fatalf("glob spill dir: %v", err)
		}
		drained := len(segments) <= 1

		if allPersisted && drained {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf(
				"spill did not drain after DB recovery: persisted %d/%d accepted events, %d segment file(s) remaining; server stderr:\n%s",
				len(intersect(persistedIDs, acceptedIDs)), len(acceptedIDs), len(segments), server.stderrString(),
			)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// TestSpillGracefulShutdownWhileDBDown proves that a graceful SIGTERM while the
// DB is down still leads to a bounded, panic-free process exit. With only a
// handful of spilled events, the feeder wedges inside the ctx-aware Flush
// barrier, and stopFeeder() alone (via ctx cancellation) unblocks it — this
// test exercises that fast path, not the CancelRetries grace-path fallback
// (feederDrainGrace elapsing with the feeder still stuck). That grace-path
// mechanism, where CancelRetries unwedges a shard stuck retrying PersistBatch
// so the feeder can exit, is covered deterministically by
// TestCancelRetriesUnblocksStuckDurableFlush in internal/pipeline. Since the
// feeder is fully gone before Stop closes the shard channels, no send can race
// it — the recover() band-aid this fix removes from run.go is never needed.
// stderr must contain no panic of any kind.
func TestSpillGracefulShutdownWhileDBDown(t *testing.T) {
	databaseURL, containerName := startNamedTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	verifyPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer verifyPool.Close()

	projectID := seedProjectAndKey(t, ctx, verifyPool, ingestPublicKey)

	binPath := buildFaultlineBinary(t)
	spillDir := t.TempDir()
	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	env := buildEnv(databaseURL, addr, spillDir, envOptions{
		spillEnabled:  true,
		segmentMaxAge: "150ms",
		segmentBytes:  "2048",
		maxBytes:      "65536",
	})

	cmd := exec.Command(binPath)
	cmd.Env = env
	stderrBuf := &bytes.Buffer{}
	cmd.Stderr = stderrBuf
	if err := cmd.Start(); err != nil {
		t.Fatalf("start faultline: %v", err)
	}
	exited := false
	t.Cleanup(func() {
		if !exited && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	})

	waitReady(t, addr)

	// Warm up the ingest key cache (30s positive TTL) while the DB is still up,
	// same rationale as TestSpillDrainsAfterDBRecovery: the first key lookup is
	// always a DB round-trip and must not happen after the outage starts.
	warmupResp := postEnvelope(t, addr, projectID, ingestPublicKey, uuid.NewString())
	if warmupResp.StatusCode != http.StatusOK {
		body := readAndClose(t, warmupResp)
		t.Fatalf("warm-up post: status = %d, want 200: %s", warmupResp.StatusCode, body)
	}
	warmupResp.Body.Close()

	dockerStop(t, containerName)

	// A handful of events during the outage: they append to spill and get 202,
	// and the feeder wedges trying to flush them against the down DB.
	for range 5 {
		resp := postEnvelope(t, addr, projectID, ingestPublicKey, uuid.NewString())
		resp.Body.Close()
	}

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	waitCh := make(chan error, 1)
	go func() { waitCh <- cmd.Wait() }()

	// persistenceShutdownTimeout (5s) + feederDrainGrace (2s) + margin: bounds
	// how long shutdown may legitimately take once CancelRetries unwedges the
	// shard, without hardcoding those unexported command-package constants here.
	const exitTimeout = 12 * time.Second
	select {
	case <-waitCh:
		exited = true
	case <-time.After(exitTimeout):
		t.Fatalf("process did not exit within %s after SIGTERM; stderr:\n%s", exitTimeout, stderrBuf.String())
	}

	output := stderrBuf.String()
	if strings.Contains(strings.ToLower(output), "panic") || strings.Contains(output, "send on closed channel") {
		t.Fatalf("shutdown produced a panic (recover() band-aid should no longer be needed): %s", output)
	}
	t.Logf("shutdown stderr:\n%s", output)
}

// --- helpers ---

func buildFaultlineBinary(t *testing.T) string {
	t.Helper()
	binPath := filepath.Join(t.TempDir(), "faultline")
	output, err := exec.Command("go", "build", "-o", binPath, "github.com/ramilS/faultline/cmd/faultline").CombinedOutput()
	if err != nil {
		t.Fatalf("build faultline binary: %v: %s", err, output)
	}
	return binPath
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve free port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release free port listener: %v", err)
	}
	return port
}

type envOptions struct {
	spillEnabled  bool
	segmentMaxAge string
	segmentBytes  string
	maxBytes      string
}

func buildEnv(databaseURL, addr, spillDir string, opts envOptions) []string {
	env := append(os.Environ(),
		"DATABASE_URL="+databaseURL,
		"FAULTLINE_ADDR="+addr,
		"SPILL_DIR="+spillDir,
		"FAULTLINE_SPILL_ENABLED="+fmt.Sprintf("%t", opts.spillEnabled),
	)
	if opts.segmentMaxAge != "" {
		env = append(env, "FAULTLINE_SPILL_SEGMENT_MAX_AGE="+opts.segmentMaxAge)
	}
	if opts.segmentBytes != "" {
		env = append(env, "FAULTLINE_SPILL_SEGMENT_BYTES="+opts.segmentBytes)
	}
	if opts.maxBytes != "" {
		env = append(env, "FAULTLINE_SPILL_MAX_BYTES="+opts.maxBytes)
	}
	return env
}

// testServer wraps a running faultline process, capturing stderr for
// debugging and guaranteeing the process is reaped exactly once even if a
// failed assertion skips the explicit kill in the test body.
type testServer struct {
	cmd    *exec.Cmd
	stderr *bytes.Buffer
	once   sync.Once
}

func startServer(t *testing.T, binPath string, env []string) *testServer {
	t.Helper()
	server := &testServer{stderr: &bytes.Buffer{}}
	server.cmd = exec.Command(binPath)
	server.cmd.Env = env
	server.cmd.Stderr = server.stderr
	if err := server.cmd.Start(); err != nil {
		t.Fatalf("start faultline: %v", err)
	}
	t.Cleanup(func() { server.kill(t) })
	return server
}

func (s *testServer) kill(t *testing.T) {
	t.Helper()
	s.once.Do(func() {
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.cmd.Wait()
	})
}

func (s *testServer) stderrString() string {
	return s.stderr.String()
}

// waitReady polls a fixed, unauthenticated Sentry envelope path: readiness
// only needs a live TCP+HTTP listener, not a valid project or key, so the
// path and credentials here are arbitrary.
func waitReady(t *testing.T, addr string) {
	t.Helper()
	client := http.Client{Timeout: 2 * time.Second}
	endpoint := "http://" + addr + "/api/1/envelope/"
	deadline := time.Now().Add(30 * time.Second)
	for {
		resp, err := client.Post(endpoint, "application/x-sentry-envelope", strings.NewReader("{}\n"))
		if err == nil {
			resp.Body.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("server at %s did not become ready: %v", addr, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// postEnvelope posts a single-event Sentry envelope for projectID, the shape
// ServeSentry (internal/ingest) requires: a newline-delimited envelope header
// followed by one "event" item.
func postEnvelope(t *testing.T, addr string, projectID int64, publicKey, eventID string) *http.Response {
	t.Helper()
	eventPayload := fmt.Sprintf(
		`{"event_id":%q,"timestamp":"2026-07-21T00:00:00Z","level":"error","message":"boom"}`,
		eventID,
	)
	body := "{}\n{\"type\":\"event\"}\n" + eventPayload + "\n"
	url := fmt.Sprintf("http://%s/api/%d/envelope/?sentry_key=%s", addr, projectID, publicKey)
	request, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-sentry-envelope")
	resp, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("post envelope: %v", err)
	}
	return resp
}

func readAndClose(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	buf := &bytes.Buffer{}
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read response body: %v", err)
	}
	return buf.String()
}

func seedProjectAndKey(t *testing.T, ctx context.Context, pool *pgxpool.Pool, publicKey string) int64 {
	t.Helper()
	repositories := storagepostgres.NewProjectRepository(pool)
	owner, err := repositories.Projects.Create(ctx, project.CreateProject{Slug: "svc", Name: "Svc", Platform: project.PlatformOther})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := repositories.Keys.Create(ctx, project.CreateKey{
		ProjectID:      owner.ID,
		PublicKey:      publicKey,
		RateLimitRPS:   1000,
		RateLimitBurst: 1000,
	}); err != nil {
		t.Fatalf("create ingest key: %v", err)
	}
	return owner.ID
}

func queryEventIDs(t *testing.T, ctx context.Context, pool *pgxpool.Pool, projectID int64) []string {
	t.Helper()
	rows, err := pool.Query(ctx, `SELECT event_id FROM events WHERE project_id = $1`, projectID)
	if err != nil {
		t.Fatalf("query event ids: %v", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan event id: %v", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate event ids: %v", err)
	}
	return ids
}

// queryEventCount opens a short-lived pool against databaseURL, runs
// SELECT COUNT(*) FROM events, and returns the result. It retries briefly on
// transient connection errors (e.g. a race with the verification pool) but
// fails the test outright if the count still can't be read.
func queryEventCount(t *testing.T, ctx context.Context, databaseURL string) int {
	t.Helper()
	const maxAttempts = 3
	var lastErr error
	for range maxAttempts {
		count, err := tryQueryEventCount(ctx, databaseURL)
		if err == nil {
			return count
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("query event count: %v", lastErr)
	return 0
}

func tryQueryEventCount(ctx context.Context, databaseURL string) (int, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return 0, fmt.Errorf("open event count pool: %w", err)
	}
	defer pool.Close()

	var count int
	if err := pool.QueryRow(ctx, `SELECT COUNT(*) FROM events`).Scan(&count); err != nil {
		return 0, fmt.Errorf("query event count: %w", err)
	}
	return count, nil
}

func assertSameIDSet(t *testing.T, want, got []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("persisted %d events, want %d", len(got), len(want))
	}
	wantSet := make(map[string]struct{}, len(want))
	for _, id := range want {
		wantSet[id] = struct{}{}
	}
	for _, id := range got {
		if _, ok := wantSet[id]; !ok {
			t.Fatalf("persisted unexpected event id %s", id)
		}
		delete(wantSet, id)
	}
	if len(wantSet) != 0 {
		t.Fatalf("missing %d expected event ids", len(wantSet))
	}
}

func containsAll(haystack, needles []string) bool {
	present := make(map[string]struct{}, len(haystack))
	for _, id := range haystack {
		present[id] = struct{}{}
	}
	for _, id := range needles {
		if _, ok := present[id]; !ok {
			return false
		}
	}
	return true
}

func intersect(a, b []string) []string {
	inA := make(map[string]struct{}, len(a))
	for _, id := range a {
		inA[id] = struct{}{}
	}
	var out []string
	for _, id := range b {
		if _, ok := inA[id]; ok {
			out = append(out, id)
		}
	}
	return out
}

// startNamedTimescale mirrors testdatabase.StartTimescale but also returns the
// container name, which TestSpillDrainsAfterDBRecovery needs to docker
// stop/start mid-test — a capability StartTimescale intentionally hides.
func startNamedTimescale(t *testing.T) (databaseURL, containerName string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	const password = "faultline-integration"
	name := fmt.Sprintf("faultline-integration-%d", time.Now().UnixNano())
	// A fixed host port, not `--publish 127.0.0.1::5432` (dynamic): Docker
	// reassigns a *new* random host port on every `docker start` of a
	// dynamically-published container, which would silently strand both this
	// helper's verification pool and the running faultline process (which has
	// the original DATABASE_URL baked into its env) on a now-dead port after
	// recovery. Binding a fixed port here keeps DATABASE_URL valid across the
	// stop/start cycle this test drives. Small TOCTOU race accepted.
	port := freePort(t)
	// No --rm here: this container is stopped and started mid-test to
	// simulate an outage, and --rm auto-removes a container the moment it
	// stops, which would make the later `docker start` fail with "no such
	// container". Teardown instead relies solely on the `docker rm --force`
	// cleanup registered below.
	output, err := exec.CommandContext(
		ctx,
		"docker", "run", "--detach", "--name", name,
		// Labelled like the shared container so one sweep finds every
		// integration leftover: docker ps -aq --filter label=faultline-itest.
		"--label", testdatabase.ContainerLabel+"=1",
		"--env", "POSTGRES_DB=faultline",
		"--env", "POSTGRES_USER=faultline",
		"--env", "POSTGRES_PASSWORD="+password,
		"--publish", fmt.Sprintf("127.0.0.1:%d:5432", port),
		// Same image the shared container and docker-compose pin, so a
		// recovery assertion cannot pass against a Postgres major
		// version the deployment never runs.
		testdatabase.Image,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("start TimescaleDB container: %v: %s", err, output)
	}
	t.Cleanup(func() {
		cleanupOutput, cleanupErr := exec.Command("docker", "rm", "--force", name).CombinedOutput()
		if cleanupErr != nil && !testdatabase.IsMissingObject(string(cleanupOutput)) {
			t.Errorf("remove TimescaleDB container: %v: %s", cleanupErr, cleanupOutput)
		}
	})

	deadline := time.Now().Add(time.Minute)
	for {
		output, err = exec.CommandContext(ctx, "docker", "exec", name, "pg_isready", "-U", "faultline", "-d", "faultline").CombinedOutput()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("TimescaleDB did not become ready: %v: %s", err, output)
		}
		time.Sleep(250 * time.Millisecond)
	}
	databaseURL = fmt.Sprintf("postgres://faultline:%s@127.0.0.1:%d/faultline?sslmode=disable", password, port)

	waitPostgresPortReady(t, ctx, databaseURL, deadline)
	return databaseURL, name
}

func waitPostgresPortReady(t *testing.T, ctx context.Context, databaseURL string, deadline time.Time) {
	t.Helper()
	for {
		pool, err := pgxpool.New(ctx, databaseURL)
		if err == nil {
			pingErr := pool.Ping(ctx)
			pool.Close()
			if pingErr == nil {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("connect to published TimescaleDB port: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func dockerStop(t *testing.T, containerName string) {
	t.Helper()
	if output, err := exec.Command("docker", "stop", containerName).CombinedOutput(); err != nil {
		t.Fatalf("docker stop %s: %v: %s", containerName, err, output)
	}
}

func dockerStart(t *testing.T, containerName string) {
	t.Helper()
	if output, err := exec.Command("docker", "start", containerName).CombinedOutput(); err != nil {
		t.Fatalf("docker start %s: %v: %s", containerName, err, output)
	}
}

func waitPostgresReady(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		if err := pool.Ping(ctx); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("Postgres did not become ready again after recovery")
		}
		time.Sleep(250 * time.Millisecond)
	}
}
