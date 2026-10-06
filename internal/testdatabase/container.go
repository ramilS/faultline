//go:build integration

package testdatabase

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// Image is the database every integration test runs against, including the
// dedicated containers a test starts for itself. It
// matches the image docker-compose.yml runs in production — integration tests
// that exercise version-specific behavior (e.g. columnstore/compression) must
// run against the same major version production does. The digest is pinned,
// not just the tag: "latest-pg18" is a floating tag upstream can move without
// a commit here, and the digest is what makes the two places agree on what
// they actually run against.
const Image = "timescale/timescaledb:latest-pg18@sha256:9508616d5b941ed931198504c5db3fb47e8f53f790732ea1e889591f1062057c"

// ContainerLabel marks every container the integration suite starts, shared
// one and dedicated alike, so a sweep can find leftovers without depending on
// any naming scheme: `docker rm -f $(docker ps -aq --filter label=faultline-itest)`.
const ContainerLabel = "faultline-itest"

const (
	postgresUser        = "faultline"
	postgresPassword    = "faultline-integration"
	maintenanceDatabase = "postgres"
)

// staleContainerAge is how old a labelled container must be before the sweep
// treats it as abandoned. It has to exceed the longest realistic single test
// run, because a container this old may still belong to a concurrent `go
// test` invocation the sweeping process cannot see.
const staleContainerAge = 2 * time.Hour

// containerName is unique per image digest so that two checkouts pinning
// different TimescaleDB versions (a worktree on another branch, say) never
// silently share one container and test against the wrong server.
var containerName = fmt.Sprintf("%s-%s", ContainerLabel, shortDigest(Image))

// endpoint is the reachable address of the shared TimescaleDB container.
type endpoint struct {
	host string
	port string
}

// databaseURL builds a connection string for one database on the shared server.
func (e endpoint) databaseURL(database string) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%s/%s?sslmode=disable",
		postgresUser, postgresPassword, e.host, e.port, database,
	)
}

var (
	sharedOnce     sync.Once
	sharedEndpoint endpoint
	sharedErr      error

	// attached records whether this process registered itself as an owner of
	// the shared container, and therefore owes it a release.
	attachedMu sync.Mutex
	attached   bool
)

// shared returns the endpoint of the process-wide TimescaleDB container,
// starting it on first use. Every caller in this process gets the same
// container; the container itself is shared with any other `go test` process
// running against the same image.
func shared() (endpoint, error) {
	sharedOnce.Do(func() {
		sharedEndpoint, sharedErr = attachShared()
		if sharedErr == nil {
			attachedMu.Lock()
			attached = true
			attachedMu.Unlock()
		}
	})
	return sharedEndpoint, sharedErr
}

// attachShared registers this process as an owner of the shared container and
// starts the container if nobody has yet. Ownership is tracked in a file next
// to the lock so the last process out — whichever package's test binary that
// turns out to be — is the one that removes the container.
func attachShared() (endpoint, error) {
	var resolved endpoint
	err := withStateLock(func() error {
		owners := livingOwners()
		// The shared container is only abandoned if no live process claims
		// it. Sweeping it while another test binary is mid-run would pull the
		// server out from under that run.
		sweepStaleContainers(len(owners) > 0)
		owners[os.Getpid()] = struct{}{}

		if err := ensureContainerRunning(); err != nil {
			return err
		}
		port, err := publishedPort()
		if err != nil {
			return err
		}
		resolved = endpoint{host: "127.0.0.1", port: port}

		return writeOwners(owners)
	})
	if err != nil {
		return endpoint{}, err
	}
	if err := waitReady(resolved); err != nil {
		return endpoint{}, err
	}
	return resolved, nil
}

// releaseShared drops this process's claim on the shared container and removes
// the container once no live process still claims it. It is a no-op for a
// process that never started a database.
func releaseShared() error {
	attachedMu.Lock()
	claimed := attached
	attached = false
	attachedMu.Unlock()
	if !claimed {
		return nil
	}

	closeMaintenancePool()

	return withStateLock(func() error {
		owners := livingOwners()
		delete(owners, os.Getpid())
		if len(owners) > 0 {
			return writeOwners(owners)
		}
		if err := removeContainer(containerName); err != nil {
			return err
		}
		return os.Remove(ownersPath())
	})
}

// ensureContainerRunning starts the shared container, or leaves the already
// running one alone. `docker run` with a fixed --name is the create-or-fail
// primitive here: the daemon rejects a duplicate name atomically, so two test
// binaries racing to start the container cannot both win.
func ensureContainerRunning() error {
	state, err := containerState(containerName)
	if err != nil {
		return err
	}
	switch state {
	case "running":
		return nil
	case "":
		// Not there — start it below.
	default:
		// --rm means a container that exits is removed, so anything found in
		// another state is a wedged leftover. Drop it and start clean.
		if err := removeContainer(containerName); err != nil {
			return err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	output, err := exec.CommandContext(
		ctx,
		"docker", "run", "--detach", "--rm",
		"--name", containerName,
		"--label", ContainerLabel+"=1",
		"--env", "POSTGRES_DB="+postgresUser,
		"--env", "POSTGRES_USER="+postgresUser,
		"--env", "POSTGRES_PASSWORD="+postgresPassword,
		"--publish", "127.0.0.1::5432",
		Image,
		"postgres",
		// One server now backs every integration test in every package, and
		// `go test` runs those package binaries concurrently. The default 100
		// connections is not enough headroom for a pool per package.
		"-c", "max_connections=300",
		// Durability settings only matter to tests that kill the server, and
		// those run their own container (see startNamedTimescale). Turning
		// them off here cuts both test time and the disk churn that made the
		// old container-per-test scheme so expensive.
		"-c", "fsync=off",
		"-c", "synchronous_commit=off",
		"-c", "full_page_writes=off",
	).CombinedOutput()
	if err == nil {
		return nil
	}
	// Lost the create race against another test binary: its container is the
	// one we both use.
	if IsNameConflict(string(output)) {
		return nil
	}
	return fmt.Errorf("start TimescaleDB container: %w: %s", err, output)
}

// containerState reports the container's Docker state, or "" when no container
// by that name exists.
func containerState(name string) (string, error) {
	output, err := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", name).CombinedOutput()
	if err != nil {
		if IsMissingObject(string(output)) {
			return "", nil
		}
		return "", fmt.Errorf("inspect container %s: %w: %s", name, err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

// publishedPort resolves the host port Docker mapped the server's 5432 to.
// Docker reserves a container's name before it populates the port bindings,
// so a container this process did not create itself can briefly report no
// port at all; the retry covers that gap rather than failing the run.
func publishedPort() (string, error) {
	deadline := time.Now().Add(30 * time.Second)
	for {
		output, err := exec.Command(
			"docker", "inspect", "--format",
			`{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}`,
			containerName,
		).CombinedOutput()
		if err == nil {
			if port := strings.TrimSpace(string(output)); port != "" {
				return port, nil
			}
			err = errors.New("container published no host port")
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("resolve TimescaleDB port: %w: %s", err, output)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func removeContainer(name string) error {
	output, err := exec.Command("docker", "rm", "--force", "--volumes", name).CombinedOutput()
	if err != nil && !IsMissingObject(string(output)) {
		return fmt.Errorf("remove container %s: %w: %s", name, err, output)
	}
	return nil
}

// sweepStaleContainers removes labelled containers old enough that no running
// test can still own them, keeping the shared container when keepShared says
// another test binary is still using it. It is best-effort: a failure here is
// a housekeeping miss, not a reason to fail the run that found it.
func sweepStaleContainers(keepShared bool) {
	listed, err := exec.Command(
		"docker", "ps", "--all", "--quiet",
		"--filter", "label="+ContainerLabel,
	).CombinedOutput()
	if err != nil {
		return
	}
	identifiers := strings.Fields(string(listed))
	if len(identifiers) == 0 {
		return
	}

	// Age is computed here rather than pushed into a --filter: `docker ps`
	// has no `until` filter (only `docker container prune` does), and an
	// unsupported filter would fail the listing and silently sweep nothing.
	arguments := append([]string{"inspect", "--format", "{{.Id}} {{.Created}} {{.Name}}"}, identifiers...)
	inspected, err := exec.Command("docker", arguments...).CombinedOutput()
	if err != nil {
		return
	}

	for line := range strings.SplitSeq(strings.TrimSpace(string(inspected)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		identifier, created, name := fields[0], fields[1], strings.TrimPrefix(fields[2], "/")
		if keepShared && name == containerName {
			continue
		}
		createdAt, err := time.Parse(time.RFC3339Nano, created)
		if err != nil || time.Since(createdAt) < staleContainerAge {
			continue
		}
		_ = removeContainer(identifier)
	}
}

// --- owner registry -------------------------------------------------------
//
// Every test binary that starts a database writes its PID here and removes it
// on the way out. The registry is what lets one container outlive a single
// package's test binary while still being removed deterministically once the
// last one finishes. Crashed processes leave their PID behind, so entries are
// filtered by liveness on every read rather than trusted.

func stateDir() string {
	return filepath.Join(os.TempDir(), containerName)
}

func ownersPath() string { return filepath.Join(stateDir(), "owners") }

func lockPath() string { return filepath.Join(stateDir(), "lock") }

// withStateLock runs fn while holding an exclusive lock over the registry,
// serialising container start and removal across every test binary.
func withStateLock(fn func() error) error {
	if err := os.MkdirAll(stateDir(), 0o755); err != nil {
		return fmt.Errorf("create test database state directory: %w", err)
	}
	file, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open test database lock: %w", err)
	}
	defer func() { _ = file.Close() }()

	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("lock test database state: %w", err)
	}
	defer func() { _ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN) }()

	return fn()
}

// livingOwners reads the registry, dropping PIDs whose process is gone.
func livingOwners() map[int]struct{} {
	owners := make(map[int]struct{})
	contents, err := os.ReadFile(ownersPath())
	if err != nil {
		return owners
	}
	for line := range strings.FieldsSeq(string(contents)) {
		pid, err := strconv.Atoi(line)
		if err != nil || pid <= 0 {
			continue
		}
		if syscall.Kill(pid, 0) != nil {
			continue
		}
		owners[pid] = struct{}{}
	}
	return owners
}

func writeOwners(owners map[int]struct{}) error {
	lines := make([]string, 0, len(owners))
	for pid := range owners {
		lines = append(lines, strconv.Itoa(pid))
	}
	contents := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(ownersPath(), []byte(contents), 0o644); err != nil {
		return fmt.Errorf("record test database owner: %w", err)
	}
	return nil
}

// --- process lifecycle ----------------------------------------------------

// RunTests is the TestMain body every package with integration tests uses. It
// exists so the shared container is removed when the last test binary exits
// instead of surviving the run:
//
//	func TestMain(m *testing.M) { testdatabase.RunTests(m) }
//
// It also releases on SIGINT/SIGTERM, so interrupting `make test-integration`
// tears the container down rather than orphaning it.
func RunTests(m *testing.M) {
	interrupted := make(chan os.Signal, 1)
	signal.Notify(interrupted, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-interrupted
		if err := releaseShared(); err != nil {
			fmt.Fprintf(os.Stderr, "testdatabase: release after interrupt: %v\n", err)
		}
		os.Exit(130)
	}()

	code := m.Run()

	if err := releaseShared(); err != nil {
		fmt.Fprintf(os.Stderr, "testdatabase: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	os.Exit(code)
}

func shortDigest(image string) string {
	if _, digest, found := strings.Cut(image, "@sha256:"); found {
		return digest[:12]
	}
	sum := sha256.Sum256([]byte(image))
	return hex.EncodeToString(sum[:])[:12]
}
