//go:build integration

package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/grouping"
	migrate "github.com/ramilS/faultline/internal/platform/migrate"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestGroupingRuleRepository(t *testing.T) {
	databaseURL := testdatabase.StartTimescale(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()

	if err := migrate.Run(databaseURL, migrate.Up, &bytes.Buffer{}); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	defer pool.Close()

	projects := storagepostgres.NewProjectRepository(pool).Projects
	repository := storagepostgres.NewGroupingRuleRepository(pool)

	t.Run("creates rules appended at successive positions and lists by slug and id", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "grouping-rules")
		first, err := repository.Create(ctx, owner.ID, "logger:billing", "billing")
		if err != nil || first.Position != 1 {
			t.Fatalf("create first rule = %+v, %v", first, err)
		}
		second, err := repository.Create(ctx, owner.ID, "logger:payments", "payments")
		if err != nil || second.Position != 2 {
			t.Fatalf("create second rule = %+v, %v", second, err)
		}

		bySlug, err := repository.ListByProjectSlug(ctx, "grouping-rules")
		if err != nil || len(bySlug) != 2 || bySlug[0] != first || bySlug[1] != second {
			t.Fatalf("ListByProjectSlug = %+v, %v", bySlug, err)
		}
		byID, err := repository.ListByProjectID(ctx, owner.ID)
		if err != nil || len(byID) != 2 || byID[0] != first || byID[1] != second {
			t.Fatalf("ListByProjectID = %+v, %v", byID, err)
		}
	})

	t.Run("unknown project slug lists as empty, not an error", func(t *testing.T) {
		bySlug, err := repository.ListByProjectSlug(ctx, "grouping-rules-does-not-exist")
		if err != nil || len(bySlug) != 0 {
			t.Fatalf("ListByProjectSlug for unknown slug = %+v, %v, want empty slice and nil error", bySlug, err)
		}
	})

	t.Run("updates matcher and template without changing position", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "grouping-rules-update")
		created, err := repository.Create(ctx, owner.ID, "logger:billing", "billing")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		updated, err := repository.Update(ctx, owner.ID, created.ID, "logger:payments", "payments-renamed")
		if err != nil || updated.Position != created.Position || updated.Matcher != "logger:payments" || updated.Template != "payments-renamed" {
			t.Fatalf("update = %+v, %v", updated, err)
		}
		if _, err := repository.Update(ctx, owner.ID, 9_999_999, "logger:x", "x"); !errors.Is(err, grouping.ErrRuleNotFound) {
			t.Fatalf("update missing rule error = %v, want ErrRuleNotFound", err)
		}
		if _, err := repository.Update(ctx, owner.ID+999_999, created.ID, "logger:x", "x"); !errors.Is(err, grouping.ErrRuleNotFound) {
			t.Fatalf("update under wrong project error = %v, want ErrRuleNotFound", err)
		}
	})

	t.Run("moves swap adjacent positions and no-op at the ends", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "grouping-rules-move")
		foreign := createProject(t, ctx, projects, "grouping-rules-move-foreign")
		first, err := repository.Create(ctx, owner.ID, "logger:a", "a")
		if err != nil {
			t.Fatalf("create first: %v", err)
		}
		second, err := repository.Create(ctx, owner.ID, "logger:b", "b")
		if err != nil {
			t.Fatalf("create second: %v", err)
		}
		third, err := repository.Create(ctx, owner.ID, "logger:c", "c")
		if err != nil {
			t.Fatalf("create third: %v", err)
		}

		// first (pos1) is at the start: MoveUp must no-op.
		noopUp, err := repository.Move(ctx, owner.ID, first.ID, grouping.MoveUp)
		if err != nil || noopUp.Position != 1 || noopUp.ID != first.ID {
			t.Fatalf("move up at start must no-op, got %+v, %v", noopUp, err)
		}
		listedAfterNoop, err := repository.ListByProjectID(ctx, owner.ID)
		if err != nil || listedAfterNoop[0].ID != first.ID || listedAfterNoop[1].ID != second.ID || listedAfterNoop[2].ID != third.ID {
			t.Fatalf("order after no-op move up = %+v, %v", listedAfterNoop, err)
		}

		movedDown, err := repository.Move(ctx, owner.ID, first.ID, grouping.MoveDown)
		if err != nil || movedDown.Position != 2 {
			t.Fatalf("move down = %+v, %v", movedDown, err)
		}
		listed, err := repository.ListByProjectID(ctx, owner.ID)
		if err != nil || listed[0].ID != second.ID || listed[1].ID != first.ID || listed[2].ID != third.ID {
			t.Fatalf("after swap listed = %+v, %v", listed, err)
		}
		// Positions must be a contiguous, unique set — a broken swap could
		// leave two rows sharing a position or an int stuck at the sentinel.
		if listed[0].Position != 1 || listed[1].Position != 2 || listed[2].Position != 3 {
			t.Fatalf("positions after swap = %d,%d,%d, want 1,2,3", listed[0].Position, listed[1].Position, listed[2].Position)
		}

		// third (now pos3) is at the end: MoveDown must no-op.
		noopDown, err := repository.Move(ctx, owner.ID, third.ID, grouping.MoveDown)
		if err != nil || noopDown.Position != 3 || noopDown.ID != third.ID {
			t.Fatalf("move down at end must no-op, got %+v, %v", noopDown, err)
		}

		if _, err := repository.Move(ctx, owner.ID, 9_999_999, grouping.MoveUp); !errors.Is(err, grouping.ErrRuleNotFound) {
			t.Fatalf("move missing rule error = %v, want ErrRuleNotFound", err)
		}

		// second exists but belongs to owner, not foreign: Move under the
		// wrong project must report ErrRuleNotFound, matching the authorization
		// boundary Update and Delete already enforce, and must not move the
		// rule under its real project.
		if _, err := repository.Move(ctx, foreign.ID, second.ID, grouping.MoveUp); !errors.Is(err, grouping.ErrRuleNotFound) {
			t.Fatalf("move under wrong project error = %v, want ErrRuleNotFound", err)
		}
		listedAfterForeignAttempt, err := repository.ListByProjectID(ctx, owner.ID)
		if err != nil || listedAfterForeignAttempt[0].ID != second.ID || listedAfterForeignAttempt[1].ID != first.ID || listedAfterForeignAttempt[2].ID != third.ID {
			t.Fatalf("order after rejected foreign move = %+v, %v", listedAfterForeignAttempt, err)
		}
	})

	t.Run("deletes and enforces project scoping", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "grouping-rules-delete")
		foreign := createProject(t, ctx, projects, "grouping-rules-foreign")
		created, err := repository.Create(ctx, owner.ID, "logger:a", "a")
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if err := repository.Delete(ctx, foreign.ID, created.ID); !errors.Is(err, grouping.ErrRuleNotFound) {
			t.Fatalf("delete under foreign project error = %v, want ErrRuleNotFound", err)
		}
		if err := repository.Delete(ctx, owner.ID, created.ID); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if err := repository.Delete(ctx, owner.ID, created.ID); !errors.Is(err, grouping.ErrRuleNotFound) {
			t.Fatalf("re-delete error = %v, want ErrRuleNotFound", err)
		}
		remaining, err := repository.ListByProjectID(ctx, owner.ID)
		if err != nil || len(remaining) != 0 {
			t.Fatalf("remaining after delete = %+v, %v", remaining, err)
		}
	})

	// This test deliberately does not rely on goroutine-scheduling luck to
	// produce the race — a razor-thin, single-statement window like that is
	// exactly the trap that let an earlier version of the cap-race test above
	// pass even against a broken implementation. Instead it forces a
	// deterministic interleaving using Postgres's own row-lock queue:
	//
	//  1. blockerTx holds `first`'s row locked (SELECT ... FOR UPDATE),
	//     uncommitted.
	//  2. Delete(first) is started. Its DELETE needs that same row's write
	//     lock, so it queues up waiting for blockerTx — deterministically,
	//     not a race.
	//  3. Move(first, MoveDown) is started next. Move's own SELECTs are
	//     plain reads and are not blocked by another transaction's row lock,
	//     so they complete immediately and read `first` as still present.
	//     Move's swap then reaches its own write on `first`'s row, which
	//     queues up behind Delete's (already-waiting) request.
	//  4. blockerTx releases. Postgres grants the row lock to Delete first
	//     (it queued first), so Delete's DELETE runs to completion before
	//     Move's write is ever granted the lock. Move's write therefore lands
	//     on a row Delete has already removed.
	//
	// An unfixed Move (no project lock on Delete, no RowsAffected check)
	// updates zero rows in that situation, notices nothing, and commits a
	// fabricated success describing a row that no longer exists. A fixed
	// Move either can't reach this interleaving at all (Delete now takes the
	// same per-project advisory lock Move does, so Delete's transaction
	// fully finishes — or fully waits — around Move's, never landing
	// mid-swap) or, as defence in depth, notices RowsAffected == 0 on the
	// swap UPDATE and returns ErrRuleNotFound instead of fabricating success.
	t.Run("Move never reports success for a row a concurrent Delete removed mid-swap", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "grouping-rules-move-delete-race")
		first, err := repository.Create(ctx, owner.ID, "logger:a", "a")
		if err != nil {
			t.Fatalf("create first: %v", err)
		}
		if _, err := repository.Create(ctx, owner.ID, "logger:b", "b"); err != nil {
			t.Fatalf("create second: %v", err)
		}

		blockerTx, err := pool.Begin(ctx)
		if err != nil {
			t.Fatalf("begin blocker tx: %v", err)
		}
		if _, err := blockerTx.Exec(ctx, `SELECT id FROM grouping_rules WHERE id = $1 FOR UPDATE`, first.ID); err != nil {
			t.Fatalf("blocker lock first row: %v", err)
		}

		deleteDone := make(chan error, 1)
		go func() { deleteDone <- repository.Delete(ctx, owner.ID, first.ID) }()
		// Generous margin, not a race window: just long enough for Delete's
		// DELETE to reach the row-lock wait queue ahead of Move's.
		time.Sleep(200 * time.Millisecond)

		type moveOutcome struct {
			rule grouping.Rule
			err  error
		}
		moveDone := make(chan moveOutcome, 1)
		go func() {
			rule, moveErr := repository.Move(ctx, owner.ID, first.ID, grouping.MoveDown)
			moveDone <- moveOutcome{rule: rule, err: moveErr}
		}()
		// Generous margin for Move's own (unblocked) reads to complete and
		// its write to queue up behind Delete's, before releasing the
		// blocker and letting both proceed.
		time.Sleep(200 * time.Millisecond)

		if err := blockerTx.Rollback(ctx); err != nil {
			t.Fatalf("release blocker tx: %v", err)
		}

		select {
		case err := <-deleteDone:
			if err != nil {
				t.Fatalf("delete = %v, want nil (first must still exist when the blocker releases)", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("delete did not complete after blocker released")
		}

		select {
		case outcome := <-moveDone:
			if outcome.err == nil {
				t.Fatalf("move reported success (rule=%+v) for id=%d, but a concurrent delete removed that row mid-swap — fabricated success", outcome.rule, first.ID)
			}
			if !errors.Is(outcome.err, grouping.ErrRuleNotFound) {
				t.Fatalf("move error = %v, want ErrRuleNotFound", outcome.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("move did not complete after blocker released")
		}
	})

	// This is the load-bearing test for the cap race: MaxRulesPerProject-20
	// rules already exist (20 free slots), and 60 goroutines race Create
	// concurrently for those slots — deliberately more contenders than free
	// slots, and more free slots than 1, so genuinely overlapping
	// transactions are likely rather than a coin-flip edge case. If Create
	// only recounted in Go (the TOCTOU the service-level check has) instead
	// of serializing per project at the database level, this reliably
	// reproduces duplicate-key errors and/or a final count above the cap —
	// verified by temporarily removing the lock and rerunning this test.
	t.Run("concurrent creates never exceed the cap", func(t *testing.T) {
		owner := createProject(t, ctx, projects, "grouping-rules-cap-race")
		const nearCap = grouping.MaxRulesPerProject - 20
		for i := range nearCap {
			if _, err := repository.Create(ctx, owner.ID, "logger:seed", "seed"); err != nil {
				t.Fatalf("seed create %d: %v", i, err)
			}
		}

		const attempts = 60
		var (
			wg        sync.WaitGroup
			mu        sync.Mutex
			succeeded int
			tooMany   int
			otherErrs []error
		)
		// start is a barrier: every goroutine blocks on it so all attempts'
		// Create calls fire as close to simultaneously as possible. Without
		// this, goroutine scheduling and connection-pool acquisition spread
		// the calls out in time and the race window never lines up — a
		// staggered version of this test passed even against a naive
		// count-then-insert implementation with no DB-level guard.
		start := make(chan struct{})
		wg.Add(attempts)
		for range attempts {
			go func() {
				defer wg.Done()
				<-start
				_, err := repository.Create(ctx, owner.ID, "logger:race", "race")
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					succeeded++
				case errors.Is(err, grouping.ErrTooManyRules):
					tooMany++
				default:
					otherErrs = append(otherErrs, err)
				}
			}()
		}
		close(start)
		wg.Wait()

		if len(otherErrs) != 0 {
			t.Fatalf("unexpected errors from concurrent create: %v", otherErrs)
		}
		const freeSlots = grouping.MaxRulesPerProject - nearCap
		if succeeded != freeSlots {
			t.Fatalf("succeeded = %d, want exactly %d (cap has room for exactly %d more)", succeeded, freeSlots, freeSlots)
		}
		if tooMany != attempts-freeSlots {
			t.Fatalf("tooMany = %d, want %d", tooMany, attempts-freeSlots)
		}

		final, err := repository.ListByProjectID(ctx, owner.ID)
		if err != nil {
			t.Fatalf("list after race: %v", err)
		}
		if len(final) != grouping.MaxRulesPerProject {
			t.Fatalf("final rule count = %d, want exactly %d (cap must never be exceeded)", len(final), grouping.MaxRulesPerProject)
		}
	})
}
