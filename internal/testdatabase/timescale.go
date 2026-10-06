//go:build integration

// Package testdatabase provides disposable integration-test databases.
//
// Every test gets its own database, but all of them live in a single
// TimescaleDB container shared by every test binary in the repository. The
// container is started on first use and removed when the last test binary
// exits, which is why each package with integration tests needs:
//
//	func TestMain(m *testing.M) { testdatabase.RunTests(m) }
//
// A database is far cheaper than a container — no image start, no initdb, no
// writable layer and no anonymous volume per test — and dropping it returns
// the space immediately.
package testdatabase

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	migrate "github.com/ramilS/faultline/internal/platform/migrate"
)

// readyTimeout bounds the first connection to a freshly started container,
// which has to complete initdb before it listens on TCP.
const readyTimeout = time.Minute

// StartTimescale creates an empty database on the shared TimescaleDB server
// and returns its URL. The database holds no schema — callers that need one
// run the migrations themselves, or use Migrated. It is dropped when the test
// ends.
func StartTimescale(t *testing.T) string {
	t.Helper()

	server, err := shared()
	if err != nil {
		t.Fatalf("start shared TimescaleDB: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := maintenancePool(server)
	if err != nil {
		t.Fatalf("connect to shared TimescaleDB: %v", err)
	}

	name := fmt.Sprintf("itest_%d_%d", os.Getpid(), nextDatabase.Add(1))
	quoted := pgx.Identifier{name}.Sanitize()
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+quoted); err != nil {
		t.Fatalf("create test database %s: %v", name, err)
	}
	t.Cleanup(func() {
		dropCtx, dropCancel := context.WithTimeout(context.Background(), time.Minute)
		defer dropCancel()
		// WITH (FORCE) terminates connections a test left open instead of
		// failing the drop and leaking the database for the rest of the run.
		if _, err := pool.Exec(dropCtx, "DROP DATABASE IF EXISTS "+quoted+" WITH (FORCE)"); err != nil {
			t.Errorf("drop test database %s: %v", name, err)
		}
	})

	return server.databaseURL(name)
}

// Migrated creates a test database, applies every migration, and returns a
// connection pool to it. The pool is closed automatically when the test ends.
func Migrated(t *testing.T) *pgxpool.Pool {
	t.Helper()
	databaseURL := StartTimescale(t)

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// nextDatabase makes database names unique within this process; the PID in the
// name keeps them unique across the test binaries sharing the server.
var nextDatabase atomic.Int64

var (
	maintenanceOnce sync.Once
	maintenance     *pgxpool.Pool
	maintenanceErr  error
)

// maintenancePool is the connection to the server's own `postgres` database,
// used to create and drop the per-test databases. CREATE DATABASE cannot run
// from inside the database being created, so this connection has to live
// outside all of them.
func maintenancePool(server endpoint) (*pgxpool.Pool, error) {
	maintenanceOnce.Do(func() {
		config, err := pgxpool.ParseConfig(server.databaseURL(maintenanceDatabase))
		if err != nil {
			maintenanceErr = fmt.Errorf("parse maintenance URL: %w", err)
			return
		}
		// Creating and dropping databases is brief and rare relative to the
		// tests themselves; a couple of connections is plenty and keeps the
		// server's connection budget for the tests.
		config.MaxConns = 4

		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		maintenance, maintenanceErr = pgxpool.NewWithConfig(ctx, config)
	})
	return maintenance, maintenanceErr
}

// closeMaintenancePool releases the admin connections on the way out. The
// pool is left in place rather than nilled: release also runs from the signal
// handler, concurrently with tests, and a closed pool fails their next call
// with an error instead of a nil dereference.
func closeMaintenancePool() {
	if maintenance != nil {
		maintenance.Close()
	}
}

// waitReady blocks until the server accepts connections. A container running
// initdb is not listening on TCP yet, so a successful connect — not a
// pg_isready over `docker exec` — is what actually proves the server is up.
func waitReady(server endpoint) error {
	ctx, cancel := context.WithTimeout(context.Background(), readyTimeout)
	defer cancel()

	url := server.databaseURL(maintenanceDatabase)
	var lastErr error
	for {
		connection, err := pgx.Connect(ctx, url)
		if err == nil {
			closeErr := connection.Close(ctx)
			if closeErr != nil {
				return fmt.Errorf("close readiness connection: %w", closeErr)
			}
			return nil
		}
		lastErr = err

		select {
		case <-ctx.Done():
			return fmt.Errorf("TimescaleDB did not become ready: %w", lastErr)
		case <-time.After(250 * time.Millisecond):
		}
	}
}
