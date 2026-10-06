//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/access"
	"github.com/ramilS/faultline/internal/auth"
	storagepostgres "github.com/ramilS/faultline/internal/storage/postgres"
	"github.com/ramilS/faultline/internal/testdatabase"
)

func TestAuthRepository(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := t.Context()

	repository := storagepostgres.NewAuthRepository(pool)
	users := repository.Users
	sessions := repository.Sessions

	t.Run("creates user and finds normalized email", func(t *testing.T) {
		created, err := users.Create(ctx, auth.NewUser{
			Email:        "admin@example.com",
			Name:         "Administrator",
			PasswordHash: "password-hash",
			Role:         auth.RoleOwner,
		})
		if err != nil {
			t.Fatalf("create user: %v", err)
		}
		if created.ID == 0 || created.Email != "admin@example.com" || created.Name != "Administrator" {
			t.Fatalf("created user = %+v", created)
		}
		if created.PasswordHash != "" {
			t.Fatalf("created user password hash = %q; Create must not return one", created.PasswordHash)
		}
		if created.Role != auth.RoleOwner || !created.IsOwner() {
			t.Fatalf("created user role = %+v, want %q", created, auth.RoleOwner)
		}
		if created.DeactivatedAt != nil || !created.IsActive() {
			t.Fatalf("created user deactivated_at = %v, want nil", created.DeactivatedAt)
		}
		assertUTC(t, "created user timestamp", created.CreatedAt)

		found, err := users.FindByEmail(ctx, "  ADMIN@EXAMPLE.COM\t")
		if err != nil {
			t.Fatalf("find user: %v", err)
		}
		// FindByEmail is one of the two lookups that do carry the hash, so
		// the two results differ in exactly that field and nothing else.
		want := created
		want.PasswordHash = "password-hash"
		if found != want {
			t.Fatalf("found user = %+v, want %+v", found, want)
		}
	})

	t.Run("translates duplicate email only as conflict", func(t *testing.T) {
		createUser(t, ctx, users, "duplicate@example.com")
		_, err := users.Create(ctx, auth.NewUser{
			Email:        "duplicate@example.com",
			Name:         "Duplicate",
			PasswordHash: "password-hash",
			Role:         auth.RoleMember,
		})
		if !errors.Is(err, auth.ErrConflict) {
			t.Fatalf("duplicate create error = %v, want ErrConflict", err)
		}

		badSession := auth.NewSession{
			UserID:     -1,
			TokenHash:  tokenHash(0xf0),
			CreatedAt:  time.Now().UTC().Truncate(time.Microsecond),
			ExpiresAt:  time.Now().UTC().Add(time.Hour).Truncate(time.Microsecond),
			LastSeenAt: time.Now().UTC().Truncate(time.Microsecond),
		}
		err = sessions.Create(ctx, badSession)
		if err == nil || errors.Is(err, auth.ErrConflict) || errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("foreign-key error = %v, want unclassified diagnostic error", err)
		}
		postgresError, ok := errors.AsType[*pgconn.PgError](err)
		if !ok || postgresError.Code != "23503" {
			t.Fatalf("foreign-key error = %v, want wrapped SQLSTATE 23503", err)
		}
	})

	t.Run("returns not found for missing user", func(t *testing.T) {
		_, err := users.FindByEmail(ctx, "missing@example.com")
		if !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("missing user error = %v, want ErrNotFound", err)
		}
	})

	t.Run("creates finds touches and deletes session", func(t *testing.T) {
		user := createUser(t, ctx, users, "session@example.com")
		createdAt := time.Date(2026, time.July, 13, 8, 9, 10, 123456000, time.FixedZone("input", 3*60*60))
		session := auth.NewSession{
			UserID:     user.ID,
			TokenHash:  tokenHash(0xab),
			CreatedAt:  createdAt,
			ExpiresAt:  createdAt.Add(7 * 24 * time.Hour),
			LastSeenAt: createdAt.Add(time.Minute),
		}
		if err := sessions.Create(ctx, session); err != nil {
			t.Fatalf("create session: %v", err)
		}

		foundSession, foundUser, err := sessions.FindByTokenHash(ctx, session.TokenHash)
		if err != nil {
			t.Fatalf("find session: %v", err)
		}
		if foundSession.ID == 0 || foundSession.UserID != user.ID || foundSession.TokenHash != session.TokenHash {
			t.Fatalf("found session identity = %+v", foundSession)
		}
		assertSameInstant(t, "created_at", foundSession.CreatedAt, session.CreatedAt)
		assertSameInstant(t, "expires_at", foundSession.ExpiresAt, session.ExpiresAt)
		assertSameInstant(t, "last_seen_at", foundSession.LastSeenAt, session.LastSeenAt)
		assertUTC(t, "session created_at", foundSession.CreatedAt)
		assertUTC(t, "session expires_at", foundSession.ExpiresAt)
		assertUTC(t, "session last_seen_at", foundSession.LastSeenAt)
		assertUTC(t, "joined user created_at", foundUser.CreatedAt)
		if foundUser != user {
			t.Fatalf("found user = %+v, want %+v", foundUser, user)
		}

		touchedAt := createdAt.Add(10 * time.Minute)
		if err := sessions.Touch(ctx, session.TokenHash, touchedAt); err != nil {
			t.Fatalf("touch session: %v", err)
		}
		foundSession, _, err = sessions.FindByTokenHash(ctx, session.TokenHash)
		if err != nil {
			t.Fatalf("find touched session: %v", err)
		}
		assertSameInstant(t, "touched last_seen_at", foundSession.LastSeenAt, touchedAt)
		assertUTC(t, "touched last_seen_at", foundSession.LastSeenAt)

		if err := sessions.DeleteByTokenHash(ctx, session.TokenHash); err != nil {
			t.Fatalf("delete session: %v", err)
		}
		_, _, err = sessions.FindByTokenHash(ctx, session.TokenHash)
		if !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("deleted session lookup error = %v, want ErrNotFound", err)
		}
	})

	t.Run("finding session for deactivated user reports not found", func(t *testing.T) {
		user := createUser(t, ctx, users, "deactivated@example.com")
		session := newSession(user.ID, tokenHash(0xde), time.Now().UTC().Add(time.Hour), time.Now().UTC())
		if err := sessions.Create(ctx, session); err != nil {
			t.Fatalf("create session: %v", err)
		}
		if _, err := pool.Exec(ctx, `UPDATE users SET deactivated_at = now() WHERE id = $1`, user.ID); err != nil {
			t.Fatalf("deactivate user: %v", err)
		}

		found, err := users.FindByEmail(ctx, "deactivated@example.com")
		if err != nil {
			t.Fatalf("find deactivated user: %v", err)
		}
		if found.DeactivatedAt == nil || found.IsActive() {
			t.Fatalf("found user deactivated_at = %v, want set", found.DeactivatedAt)
		}

		// A session row could outlive deactivation if a future code path
		// forgets to delete it, so FindByTokenHash must guard independently
		// rather than trust that sessions are always cleaned up on deactivation.
		if _, _, err := sessions.FindByTokenHash(ctx, session.TokenHash); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("deactivated session lookup error = %v, want ErrNotFound", err)
		}
	})

	t.Run("reports missing touch and delete", func(t *testing.T) {
		missing := tokenHash(0xee)
		if err := sessions.Touch(ctx, missing, time.Now()); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("missing touch error = %v, want ErrNotFound", err)
		}
		if err := sessions.DeleteByTokenHash(ctx, missing); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("missing delete error = %v, want ErrNotFound", err)
		}
	})

	t.Run("deletes sessions expired at cutoff", func(t *testing.T) {
		user := createUser(t, ctx, users, "cleanup@example.com")
		cutoff := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)
		for _, session := range []auth.NewSession{
			newSession(user.ID, tokenHash(0x01), cutoff.Add(-time.Second), cutoff.Add(-time.Hour)),
			newSession(user.ID, tokenHash(0x02), cutoff, cutoff.Add(-time.Hour)),
			newSession(user.ID, tokenHash(0x03), cutoff.Add(time.Second), cutoff.Add(-time.Hour)),
		} {
			if err := sessions.Create(ctx, session); err != nil {
				t.Fatalf("create cleanup session: %v", err)
			}
		}

		if err := sessions.DeleteExpired(ctx, cutoff); err != nil {
			t.Fatalf("delete expired: %v", err)
		}
		for _, hash := range [][32]byte{tokenHash(0x01), tokenHash(0x02)} {
			if _, _, err := sessions.FindByTokenHash(ctx, hash); !errors.Is(err, auth.ErrNotFound) {
				t.Fatalf("expired session %x lookup error = %v, want ErrNotFound", hash[0], err)
			}
		}
		if _, _, err := sessions.FindByTokenHash(ctx, tokenHash(0x03)); err != nil {
			t.Fatalf("active session removed: %v", err)
		}
		if err := sessions.DeleteExpired(ctx, cutoff); err != nil {
			t.Fatalf("delete expired with no matches: %v", err)
		}
	})

	t.Run("user delete cascades sessions", func(t *testing.T) {
		user := createUser(t, ctx, users, "cascade@example.com")
		session := newSession(user.ID, tokenHash(0xca), time.Now().UTC().Add(time.Hour), time.Now().UTC())
		if err := sessions.Create(ctx, session); err != nil {
			t.Fatalf("create cascading session: %v", err)
		}
		if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, user.ID); err != nil {
			t.Fatalf("delete user: %v", err)
		}
		if _, _, err := sessions.FindByTokenHash(ctx, session.TokenHash); !errors.Is(err, auth.ErrNotFound) {
			t.Fatalf("cascaded session lookup error = %v, want ErrNotFound", err)
		}
	})

	t.Run("preserves context cancellation", func(t *testing.T) {
		canceled, cancel := context.WithCancel(t.Context())
		cancel()
		if _, err := users.FindByEmail(canceled, "admin@example.com"); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled user lookup error = %v, want context.Canceled", err)
		}
		if err := sessions.DeleteExpired(canceled, time.Now()); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled cleanup error = %v, want context.Canceled", err)
		}
	})
}

func TestUserRepositoryUpdateAppliesOnlySuppliedFields(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	created, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "member@example.com", Name: "Member", PasswordHash: "x", Role: auth.RoleMember,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	promoted := auth.RoleOwner
	updated, err := repository.Users.Update(ctx, created.ID, auth.UserChanges{Role: &promoted})
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	if updated.Role != auth.RoleOwner {
		t.Fatalf("Role = %q, want owner", updated.Role)
	}
	// The omitted field must survive. A naive UPDATE that always writes every
	// column would blank the name here.
	if updated.Name != "Member" {
		t.Fatalf("Name = %q, want %q", updated.Name, "Member")
	}
	if !updated.IsActive() {
		t.Fatal("IsActive() = false, want true for an untouched deactivated_at")
	}
}

func TestUserRepositoryUpdateDeactivationIsIdempotentAndReversible(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	created, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "deactivate@example.com", Name: "Deactivate Me", PasswordHash: "x", Role: auth.RoleMember,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	deactivate := true
	firstDeactivation, err := repository.Users.Update(ctx, created.ID, auth.UserChanges{Deactivated: &deactivate})
	if err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	if firstDeactivation.DeactivatedAt == nil {
		t.Fatal("DeactivatedAt = nil, want set after deactivation")
	}
	firstTimestamp := *firstDeactivation.DeactivatedAt

	// A naive UPDATE that always wrote deactivated_at = $5 on every deactivate
	// call, instead of COALESCE(deactivated_at, $5), would move this
	// timestamp forward on a second, redundant deactivation.
	time.Sleep(5 * time.Millisecond)
	secondDeactivation, err := repository.Users.Update(ctx, created.ID, auth.UserChanges{Deactivated: &deactivate})
	if err != nil {
		t.Fatalf("re-deactivate: %v", err)
	}
	if secondDeactivation.DeactivatedAt == nil {
		t.Fatal("DeactivatedAt = nil, want still set after redundant deactivation")
	}
	assertSameInstant(t, "deactivated_at after redundant deactivation", *secondDeactivation.DeactivatedAt, firstTimestamp)

	reactivate := false
	reactivated, err := repository.Users.Update(ctx, created.ID, auth.UserChanges{Deactivated: &reactivate})
	if err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if reactivated.DeactivatedAt != nil {
		t.Fatalf("DeactivatedAt = %v, want nil after reactivation", reactivated.DeactivatedAt)
	}
	if !reactivated.IsActive() {
		t.Fatal("IsActive() = false, want true after reactivation")
	}
}

func TestSessionRepositoryDeleteByUserIDRemovesEverySession(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	targetUser, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "many@example.com", Name: "Many", PasswordHash: "x", Role: auth.RoleMember,
	})
	if err != nil {
		t.Fatalf("create target user: %v", err)
	}
	// A second user's sessions must survive: this is what distinguishes
	// DeleteByUserID from an unscoped "delete all sessions".
	otherUser, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "other@example.com", Name: "Other", PasswordHash: "x", Role: auth.RoleMember,
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	now := time.Now().UTC()
	for index := range byte(3) {
		var hash [32]byte
		hash[0] = index
		if err := repository.Sessions.Create(ctx, auth.NewSession{
			UserID: targetUser.ID, TokenHash: hash,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
		}); err != nil {
			t.Fatalf("create target session %d: %v", index, err)
		}
	}
	otherHash := [32]byte{0xaa}
	if err := repository.Sessions.Create(ctx, auth.NewSession{
		UserID: otherUser.ID, TokenHash: otherHash,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
	}); err != nil {
		t.Fatalf("create other user session: %v", err)
	}

	if err := repository.Sessions.DeleteByUserID(ctx, targetUser.ID); err != nil {
		t.Fatalf("delete by user id: %v", err)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id = $1`, targetUser.ID).Scan(&remaining); err != nil {
		t.Fatalf("count target: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("remaining sessions for target user = %d, want 0", remaining)
	}

	if _, _, err := repository.Sessions.FindByTokenHash(ctx, otherHash); err != nil {
		t.Fatalf("other user's session lookup error = %v, want nil (session must survive)", err)
	}
}

// TestSessionRepositoryDeleteByUserIDExceptKeepsOnlyTheActingSession is the
// database-level proof behind self-service password change: three sessions
// for the acting user plus one for somebody else, and afterwards only the
// acting session and the other user's session are left. Both halves matter —
// revoking too little leaves a stolen cookie alive, revoking too much signs
// the user out of the tab they are changing their password in.
func TestSessionRepositoryDeleteByUserIDExceptKeepsOnlyTheActingSession(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	targetUser, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "keeper@example.com", Name: "Keeper", PasswordHash: "x", Role: auth.RoleMember,
	})
	if err != nil {
		t.Fatalf("create target user: %v", err)
	}
	otherUser, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "bystander@example.com", Name: "Bystander", PasswordHash: "x", Role: auth.RoleMember,
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}

	now := time.Now().UTC()
	actingHash := [32]byte{0x01}
	for _, hash := range [][32]byte{actingHash, {0x02}, {0x03}} {
		if err := repository.Sessions.Create(ctx, auth.NewSession{
			UserID: targetUser.ID, TokenHash: hash,
			CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
		}); err != nil {
			t.Fatalf("create target session %x: %v", hash[0], err)
		}
	}
	otherHash := [32]byte{0xbb}
	if err := repository.Sessions.Create(ctx, auth.NewSession{
		UserID: otherUser.ID, TokenHash: otherHash,
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
	}); err != nil {
		t.Fatalf("create other user session: %v", err)
	}

	if err := repository.Sessions.DeleteByUserIDExcept(ctx, targetUser.ID, actingHash); err != nil {
		t.Fatalf("delete by user id except: %v", err)
	}

	var remaining [][32]byte
	rows, err := pool.Query(ctx, `SELECT token_hash FROM sessions ORDER BY token_hash`)
	if err != nil {
		t.Fatalf("list remaining sessions: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatalf("scan remaining session: %v", err)
		}
		var hash [32]byte
		copy(hash[:], raw)
		remaining = append(remaining, hash)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate remaining sessions: %v", err)
	}

	want := [][32]byte{actingHash, otherHash}
	if !reflect.DeepEqual(remaining, want) {
		t.Fatalf("remaining token hashes = %x, want %x (the acting session and the other user's)", remaining, want)
	}
}

// TestUserRepositoryUpdateGuardsConcurrentDemotionOfBothLastOwners closes the
// TOCTOU race a count-then-update sequence cannot: with exactly two active
// owners, two goroutines each demote a *different* one of them at the same
// instant. Both start from the same "2 active owners" world; only one may
// finish that world with an active owner remaining.
//
// Against a naive implementation (an active-owner count read outside the
// writing transaction, then an unconditional Update) both goroutines would
// read count == 2 before either commits, both would conclude "another owner
// exists", and both updates would succeed — this test would then observe
// succeeded == 2, refused == 0, and zero active owners left, failing every
// assertion below.
func TestUserRepositoryUpdateGuardsConcurrentDemotionOfBothLastOwners(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	ownerA, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "owner-a@example.com", Name: "Owner A", PasswordHash: "x", Role: auth.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner a: %v", err)
	}
	ownerB, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "owner-b@example.com", Name: "Owner B", PasswordHash: "x", Role: auth.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner b: %v", err)
	}

	member := auth.RoleMember
	errs := raceUserUpdates(ctx, repository.Users, ownerA.ID, auth.UserChanges{Role: &member}, ownerB.ID, auth.UserChanges{Role: &member})
	assertExactlyOneRefusedAsLastOwner(t, errs)
	assertAtLeastOneActiveOwnerRemains(t, ctx, repository.Users)
}

// TestUserRepositoryUpdateGuardsConcurrentDeactivationOfBothLastOwners is the
// deactivation counterpart: two owners, each concurrently deactivating the
// other one of the last two active owners.
func TestUserRepositoryUpdateGuardsConcurrentDeactivationOfBothLastOwners(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	ownerA, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "deactivate-a@example.com", Name: "Owner A", PasswordHash: "x", Role: auth.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner a: %v", err)
	}
	ownerB, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "deactivate-b@example.com", Name: "Owner B", PasswordHash: "x", Role: auth.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner b: %v", err)
	}

	deactivate := true
	errs := raceUserUpdates(ctx, repository.Users, ownerA.ID, auth.UserChanges{Deactivated: &deactivate}, ownerB.ID, auth.UserChanges{Deactivated: &deactivate})
	assertExactlyOneRefusedAsLastOwner(t, errs)
	assertAtLeastOneActiveOwnerRemains(t, ctx, repository.Users)
}

// TestUserRepositoryUpdateGuardsConcurrentDemotionRacingDeactivation covers
// the mixed case: one request demotes an owner while another concurrently
// deactivates the other last owner.
func TestUserRepositoryUpdateGuardsConcurrentDemotionRacingDeactivation(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	ownerA, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "mixed-a@example.com", Name: "Owner A", PasswordHash: "x", Role: auth.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner a: %v", err)
	}
	ownerB, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "mixed-b@example.com", Name: "Owner B", PasswordHash: "x", Role: auth.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner b: %v", err)
	}

	member := auth.RoleMember
	deactivate := true
	errs := raceUserUpdates(ctx, repository.Users, ownerA.ID, auth.UserChanges{Role: &member}, ownerB.ID, auth.UserChanges{Deactivated: &deactivate})
	assertExactlyOneRefusedAsLastOwner(t, errs)
	assertAtLeastOneActiveOwnerRemains(t, ctx, repository.Users)
}

// TestUserRepositoryUpdateGuardingLastOwnerWaitsOnTheSharedAdvisoryLock is
// the deterministic counterpart to the three scheduling-dependent races
// above. Those prove the observable outcome (exactly one success, one
// active owner survives) but, as a mutation test against a naive
// implementation confirmed, they do not reliably fail on a fast, idle local
// database — both racers can complete before their windows ever overlap.
//
// This test does not depend on two operations racing at all. It takes the
// guard's advisory lock itself, from its own held transaction, then proves
// two separate things about UpdateGuardingLastOwner while that lock is
// held:
//
//  1. A session-level pg_try_advisory_lock on the same key reports it held
//     (non-blocking, no timing involved at all) — proving the key this test
//     acquired is exactly the key production code uses, not merely "a"
//     lock.
//  2. A concurrent guarded Update call blocks rather than completing. This
//     assertion is bounded by a wait, but unlike the scheduling races above
//     it cannot pass by luck: if the guard is broken (e.g. reverted to an
//     unlocked read-then-write), Update returns in well under a
//     millisecond regardless of anything this test holds, so failing to
//     observe completion within hundreds of milliseconds is only possible
//     when the same lock this test holds is genuinely blocking it — a
//     multiple-orders-of-magnitude gap, not a coin flip between two
//     similarly-fast operations.
//
// Releasing the held lock and then requiring Update to complete promptly
// closes the loop: the block was this test's lock and nothing else.
func TestUserRepositoryUpdateGuardingLastOwnerWaitsOnTheSharedAdvisoryLock(t *testing.T) {
	pool := testdatabase.Migrated(t)
	repository := storagepostgres.NewAuthRepository(pool)
	ctx := t.Context()

	owner, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "lock-owner@example.com", Name: "Owner", PasswordHash: "x", Role: auth.RoleOwner,
	})
	if err != nil {
		t.Fatalf("create owner: %v", err)
	}
	// A second active owner, so the guarded Update below — once it does get
	// the lock — is not itself refused by the last-owner check. This test
	// is only about whether Update contends on the lock at all.
	if _, err := repository.Users.Create(ctx, auth.NewUser{
		Email: "lock-other-owner@example.com", Name: "Other Owner", PasswordHash: "x", Role: auth.RoleOwner,
	}); err != nil {
		t.Fatalf("create other owner: %v", err)
	}

	holder, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin lock-holding transaction: %v", err)
	}
	defer holder.Rollback(ctx)
	if _, err := holder.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, storagepostgres.LastOwnerGuardLockKeyForTest); err != nil {
		t.Fatalf("acquire test-held guard lock: %v", err)
	}

	var lockHeldByThisTest bool
	if err := pool.QueryRow(ctx, `SELECT NOT pg_try_advisory_lock($1)`, storagepostgres.LastOwnerGuardLockKeyForTest).Scan(&lockHeldByThisTest); err != nil {
		t.Fatalf("probe guard lock: %v", err)
	}
	if !lockHeldByThisTest {
		t.Fatal("pg_try_advisory_lock() succeeded while this test holds the guard lock — LastOwnerGuardLockKeyForTest does not match the key Update actually locks on")
	}

	member := auth.RoleMember
	updateDone := make(chan error, 1)
	go func() {
		_, err := repository.Users.Update(ctx, owner.ID, auth.UserChanges{Role: &member})
		updateDone <- err
	}()

	select {
	case err := <-updateDone:
		t.Fatalf("Update() returned (err=%v) while this test still holds the guard lock — it did not wait for the lock at all", err)
	case <-time.After(300 * time.Millisecond):
		// Expected: Update() is genuinely blocked on the lock this test holds.
	}

	if err := holder.Rollback(ctx); err != nil {
		t.Fatalf("release test-held guard lock: %v", err)
	}

	select {
	case err := <-updateDone:
		if err != nil {
			t.Fatalf("Update() after the guard lock was released: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Update() did not complete after the guard lock was released — it is stuck on something other than this test's lock")
	}
}

// raceUserUpdates starts both updates from goroutines released at the same
// instant (via a closed channel, not a sleep) to maximize the window in
// which both could observe the pre-write world, and returns their errors in
// a fixed order.
func raceUserUpdates(ctx context.Context, users auth.UserRepository, firstID int64, firstChanges auth.UserChanges, secondID int64, secondChanges auth.UserChanges) [2]error {
	var errs [2]error
	var wg sync.WaitGroup
	start := make(chan struct{})
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, errs[0] = users.Update(ctx, firstID, firstChanges)
	}()
	go func() {
		defer wg.Done()
		<-start
		_, errs[1] = users.Update(ctx, secondID, secondChanges)
	}()
	close(start)
	wg.Wait()
	return errs
}

func assertExactlyOneRefusedAsLastOwner(t *testing.T, errs [2]error) {
	t.Helper()
	succeeded, refused := 0, 0
	for _, err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, auth.ErrLastOwner):
			refused++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("succeeded = %d, refused = %d (errors = %v); want exactly one of each", succeeded, refused, errs)
	}
}

func assertAtLeastOneActiveOwnerRemains(t *testing.T, ctx context.Context, users auth.UserRepository) {
	t.Helper()
	// Counted from ListActive rather than a dedicated repository count:
	// auth.UserRepository exposes no standalone active-owner count, precisely
	// so that no caller can build the check-then-write sequence this test
	// exists to rule out.
	active, err := users.ListActive(ctx)
	if err != nil {
		t.Fatalf("list active users: %v", err)
	}
	activeOwners := 0
	for _, user := range active {
		if user.IsOwner() {
			activeOwners++
		}
	}
	if activeOwners < 1 {
		t.Fatalf("active owners = %d, want at least 1 — the deployment would be unadministerable", activeOwners)
	}
}

func createUser(t *testing.T, ctx context.Context, repository auth.UserRepository, email string) auth.User {
	t.Helper()
	user, err := repository.Create(ctx, auth.NewUser{Email: email, Name: email, PasswordHash: "password-hash", Role: auth.RoleMember})
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return user
}

func newSession(userID int64, hash [32]byte, expiresAt, createdAt time.Time) auth.NewSession {
	return auth.NewSession{
		UserID:     userID,
		TokenHash:  hash,
		CreatedAt:  createdAt,
		ExpiresAt:  expiresAt,
		LastSeenAt: createdAt,
	}
}

func tokenHash(value byte) [32]byte {
	var hash [32]byte
	for index := range hash {
		hash[index] = value
	}
	return hash
}

func assertSameInstant(t *testing.T, name string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s = %s, want instant %s", name, got, want)
	}
}

func assertUTC(t *testing.T, name string, timestamp time.Time) {
	t.Helper()
	if timestamp.Location() != time.UTC {
		t.Fatalf("%s location = %s, want UTC", name, timestamp.Location())
	}
}

// TestNullPasswordHashSurvivesEveryUserReadPath drives every read path in
// this package with an SSO-provisioned account, whose users.password_hash is
// SQL NULL. Its absence is what let a projection reintroduce a bare
// `u.password_hash` — pgx cannot scan NULL into a string, so
// ListAssignableForProject returned an error for every caller once such an
// account reached a group holding the project. The two lookups the
// UserRepository contract says carry the hash must read it back as the empty
// string; every other path must return it empty because it never selected
// the column at all.
func TestNullPasswordHashSurvivesEveryUserReadPath(t *testing.T) {
	pool := testdatabase.Migrated(t)
	ctx := context.Background()
	repository := storagepostgres.NewAuthRepository(pool)
	users := repository.Users
	sessions := repository.Sessions

	// A second owner exists so the last-owner guard cannot refuse the
	// deactivation below for an unrelated reason.
	web := seedProject(t, ctx, pool, "web")
	seedOwner(t, ctx, pool, "olivia@example.com")
	var frontend int64
	if err := pool.QueryRow(ctx, `INSERT INTO groups (name) VALUES ('frontend') RETURNING id`).Scan(&frontend); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	mustExec(t, pool, `INSERT INTO group_projects (group_id, project_id) VALUES ($1, $2)`, frontend, web)

	var ssoUserID int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role)
		VALUES ($1, $2, NULL, 'member')
		RETURNING id
	`, "sso@example.com", "SSO User").Scan(&ssoUserID); err != nil {
		t.Fatalf("insert passwordless user: %v", err)
	}
	mustExec(t, pool, `INSERT INTO group_members (group_id, user_id) VALUES ($1, $2)`, frontend, ssoUserID)

	byEmail, err := users.FindByEmail(ctx, "sso@example.com")
	if err != nil {
		t.Fatalf("FindByEmail: %v", err)
	}
	if byEmail.ID != ssoUserID || byEmail.PasswordHash != "" {
		t.Fatalf("FindByEmail = %+v, want the account with an empty hash", byEmail)
	}

	byID, err := users.FindByID(ctx, ssoUserID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	if byID.ID != ssoUserID || byID.PasswordHash != "" {
		t.Fatalf("FindByID = %+v, want the account with an empty hash", byID)
	}

	all, err := users.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	assertListedWithoutHash(t, "List", all, ssoUserID, true)

	active, err := users.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive: %v", err)
	}
	assertListedWithoutHash(t, "ListActive", active, ssoUserID, true)

	// The failure path the missed projection produced: an SSO account in a
	// group that holds the project made this query error for every caller.
	assignable, err := users.ListAssignableForProject(ctx, access.Unrestricted(), "web")
	if err != nil {
		t.Fatalf("ListAssignableForProject: %v", err)
	}
	assertListedWithoutHash(t, "ListAssignableForProject", assignable, ssoUserID, true)

	now := time.Now().UTC().Truncate(time.Microsecond)
	if err := sessions.Create(ctx, auth.NewSession{
		UserID:     ssoUserID,
		TokenHash:  tokenHash(0x5a),
		CreatedAt:  now,
		ExpiresAt:  now.Add(auth.SSOSessionLifetime),
		LastSeenAt: now,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, sessionUser, err := sessions.FindByTokenHash(ctx, tokenHash(0x5a))
	if err != nil {
		t.Fatalf("FindByTokenHash: %v", err)
	}
	if sessionUser.ID != ssoUserID || sessionUser.PasswordHash != "" {
		t.Fatalf("session user = %+v, want the account with an empty hash", sessionUser)
	}

	// A role change to owner takes the unguarded update path.
	owner := auth.RoleOwner
	promoted, err := users.Update(ctx, ssoUserID, auth.UserChanges{Role: &owner})
	if err != nil {
		t.Fatalf("Update role: %v", err)
	}
	if promoted.Role != auth.RoleOwner || promoted.PasswordHash != "" {
		t.Fatalf("promoted user = %+v, want an owner with an empty hash", promoted)
	}

	// A deactivation takes the guarded path, which reads the subject row in
	// its own projection before writing.
	deactivate := true
	deactivated, err := users.Update(ctx, ssoUserID, auth.UserChanges{Deactivated: &deactivate})
	if err != nil {
		t.Fatalf("Update deactivation: %v", err)
	}
	if deactivated.IsActive() || deactivated.PasswordHash != "" {
		t.Fatalf("deactivated user = %+v, want inactive with an empty hash", deactivated)
	}

	active, err = users.ListActive(ctx)
	if err != nil {
		t.Fatalf("ListActive after deactivation: %v", err)
	}
	assertListedWithoutHash(t, "ListActive after deactivation", active, ssoUserID, false)
}

// assertListedWithoutHash checks that userID is present (or absent) in users
// and that no listed account carries a password hash — the projection-level
// guarantee that a list handler never loads credential material.
func assertListedWithoutHash(t *testing.T, name string, users []auth.User, userID int64, wantPresent bool) {
	t.Helper()
	present := false
	for _, user := range users {
		if user.ID == userID {
			present = true
		}
		if user.PasswordHash != "" {
			t.Fatalf("%s returned a password hash for user %d", name, user.ID)
		}
	}
	if present != wantPresent {
		t.Fatalf("%s: user %d present = %v, want %v", name, userID, present, wantPresent)
	}
}

// seedOwner inserts an active owner with a password, so tests that need the
// last-owner guard to pass have a second owner to count.
func seedOwner(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role)
		VALUES ($1, $1, 'password-hash', 'owner')
		RETURNING id
	`, email).Scan(&id); err != nil {
		t.Fatalf("seed owner %s: %v", email, err)
	}
	return id
}
