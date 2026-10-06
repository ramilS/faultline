// Package migration runs Faultline's embedded database migrations.
package migration

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	gomigrate "github.com/golang-migrate/migrate/v4"
	pgxmigrate "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"

	"github.com/ramilS/faultline/migrations"
)

// Action identifies a supported migration operation.
type Action string

const (
	Up      Action = "up"
	Down    Action = "down"
	Version Action = "version"

	// toVersionPrefix marks an Action produced by ToVersion: an absolute
	// target version rather than a relative direction.
	toVersionPrefix = "to-version:"
)

type engine interface {
	Up() error
	Steps(int) error
	Migrate(uint) error
	Version() (uint, bool, error)
}

// ToVersion returns an Action that migrates the database to the exact schema
// version, rather than stepping relative to wherever it currently sits. It is
// implemented with golang-migrate's Migrate(version uint), which is a no-op
// when the database is already at that version.
func ToVersion(version uint) Action {
	return Action(toVersionPrefix + strconv.FormatUint(uint64(version), 10))
}

// Run executes an action against databaseURL using migrations embedded in the binary.
func Run(databaseURL string, action Action, stdout io.Writer) error {
	sourceDriver, err := iofs.New(migrations.FS, ".")
	if err != nil {
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	database, err := sql.Open("pgx", databaseURL)
	if err != nil {
		_ = sourceDriver.Close()
		return fmt.Errorf("open migration database: %w", err)
	}
	databaseDriver, err := pgxmigrate.WithInstance(database, &pgxmigrate.Config{})
	if err != nil {
		_ = sourceDriver.Close()
		_ = database.Close()
		return fmt.Errorf("initialize migration database: %w", err)
	}

	runner, err := gomigrate.NewWithInstance("iofs", sourceDriver, "pgx5", databaseDriver)
	if err != nil {
		_ = sourceDriver.Close()
		_ = databaseDriver.Close()
		return fmt.Errorf("initialize migrations: %w", err)
	}
	defer func() {
		_, _ = runner.Close()
	}()

	return execute(runner, action, stdout)
}

func execute(runner engine, action Action, stdout io.Writer) error {
	switch action {
	case Up:
		if err := runner.Up(); err != nil && !errors.Is(err, gomigrate.ErrNoChange) {
			return fmt.Errorf("migrate up: %w", err)
		}
		return nil
	case Down:
		if err := runner.Steps(-1); err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		return nil
	case Version:
		version, dirty, err := runner.Version()
		if errors.Is(err, gomigrate.ErrNilVersion) {
			_, err = fmt.Fprintln(stdout, "0 clean")
			return err
		}
		if err != nil {
			return fmt.Errorf("migration version: %w", err)
		}
		state := "clean"
		if dirty {
			state = "dirty"
		}
		_, err = fmt.Fprintf(stdout, "%d %s\n", version, state)
		return err
	default:
		if raw, ok := strings.CutPrefix(string(action), toVersionPrefix); ok {
			target, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("parse migration target version %q: %w", raw, err)
			}
			if err := runner.Migrate(uint(target)); err != nil && !errors.Is(err, gomigrate.ErrNoChange) {
				return fmt.Errorf("migrate to version %d: %w", target, err)
			}
			return nil
		}
		return fmt.Errorf("unsupported migration action %q", action)
	}
}
