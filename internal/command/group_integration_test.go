//go:build integration

package command

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/platform/config"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

// TestGroupCommandsGrantAccessEndToEnd is the bootstrap scenario the CLI
// exists for: with no grandfathering group and no seed data, granting a
// member access to a project on a fresh deployment has no route but
// `faultline group`. This test drives that exact sequence — create ->
// members --add -> projects --add -> list — against a migrated database and
// confirms the member's resolved project access matches what was granted.
func TestGroupCommandsGrantAccessEndToEnd(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx := t.Context()
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	verifyPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer verifyPool.Close()

	memberID := seedGroupCommandUser(t, ctx, verifyPool, "member@example.com", "Member")
	projectID := seedGroupCommandProject(t, ctx, verifyPool, "web")

	dependencies := defaultDependencies(0)
	dependencies.loadConfiguration = func() (config.Config, error) {
		return config.Config{Database: testDatabaseConfig(databaseURL)}, nil
	}

	if stdout, stderr, code := runGroupCommand(ctx, dependencies, "group", "create", "--name", "backend"); code != 0 {
		t.Fatalf("group create: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout, stderr, code := runGroupCommand(ctx, dependencies, "group", "members", "--name", "backend", "--add", "member@example.com"); code != 0 {
		t.Fatalf("group members --add: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
	if stdout, stderr, code := runGroupCommand(ctx, dependencies, "group", "projects", "--name", "backend", "--add", "web"); code != 0 {
		t.Fatalf("group projects --add: exit = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}

	stdout, stderr, code := runGroupCommand(ctx, dependencies, "group", "list")
	if code != 0 {
		t.Fatalf("group list: exit = %d, stderr = %q", code, stderr)
	}
	if !strings.Contains(stdout, "backend") {
		t.Fatalf("group list stdout = %q, want it to mention backend", stdout)
	}

	accessRepository := storagepostgres.NewAccessRepository(verifyPool)
	projectIDs, err := accessRepository.ProjectIDsForUser(ctx, memberID)
	if err != nil {
		t.Fatalf("ProjectIDsForUser: %v", err)
	}
	if len(projectIDs) != 1 || projectIDs[0] != projectID {
		t.Fatalf("ProjectIDsForUser(%d) = %v, want just [%d]", memberID, projectIDs, projectID)
	}
}

// TestGroupMembersRemoveRejectsNonMemberAgainstRealDatabase confirms
// correctness point 4 end to end: removing a user who was never added must
// fail rather than silently succeed, even once a real ReplaceMembers
// transaction and group-existence check are in play.
func TestGroupMembersRemoveRejectsNonMemberAgainstRealDatabase(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx := t.Context()
	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	verifyPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open verification pool: %v", err)
	}
	defer verifyPool.Close()

	seedGroupCommandUser(t, ctx, verifyPool, "outsider@example.com", "Outsider")

	dependencies := defaultDependencies(0)
	dependencies.loadConfiguration = func() (config.Config, error) {
		return config.Config{Database: testDatabaseConfig(databaseURL)}, nil
	}

	if _, stderr, code := runGroupCommand(ctx, dependencies, "group", "create", "--name", "backend"); code != 0 {
		t.Fatalf("group create: exit = %d, stderr = %q", code, stderr)
	}

	_, stderr, code := runGroupCommand(ctx, dependencies, "group", "members", "--name", "backend", "--remove", "outsider@example.com")
	if code == 0 {
		t.Fatal("group members --remove on a non-member: exit = 0, want non-zero")
	}
	if !strings.Contains(stderr, "not a member") {
		t.Fatalf("stderr = %q, want it to report the user is not a member", stderr)
	}
}

// testDatabaseConfig fills in the pool settings config.Load would normally
// default from the environment (see internal/platform/config), since these
// tests build a config.Config by hand rather than through Load.
func testDatabaseConfig(databaseURL string) config.DatabaseConfig {
	return config.DatabaseConfig{
		URL:               databaseURL,
		MaxConns:          5,
		MaxConnLifetime:   time.Hour,
		MaxConnIdleTime:   30 * time.Minute,
		HealthCheckPeriod: time.Minute,
	}
}

func runGroupCommand(ctx context.Context, dependencies commandDependencies, args ...string) (stdout, stderr string, code int) {
	var outBuffer, errBuffer bytes.Buffer
	code = run(ctx, args, &outBuffer, &errBuffer, dependencies)
	return outBuffer.String(), errBuffer.String(), code
}

func seedGroupCommandUser(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, name string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash) VALUES ($1, $2, 'x') RETURNING id
	`, email, name).Scan(&id); err != nil {
		t.Fatalf("seed user %s: %v", email, err)
	}
	return id
}

func seedGroupCommandProject(t *testing.T, ctx context.Context, pool *pgxpool.Pool, slug string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO projects (slug, name, platform) VALUES ($1, $1, 'javascript') RETURNING id
	`, slug).Scan(&id); err != nil {
		t.Fatalf("seed project %s: %v", slug, err)
	}
	return id
}
