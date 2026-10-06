package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/auth"
)

type identityRepository struct {
	pool *pgxpool.Pool
}

var _ auth.IdentityRepository = (*identityRepository)(nil)

func (repository *identityRepository) FindUserBySubject(ctx context.Context, provider, subject string) (auth.User, error) {
	var user auth.User
	err := repository.pool.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.role, u.deactivated_at, u.created_at
		FROM user_identities i
		JOIN users u ON u.id = i.user_id
		WHERE i.provider = $1 AND i.subject = $2
	`, provider, subject).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.Role,
		&user.DeactivatedAt,
		&user.CreatedAt,
	)
	if err != nil {
		return auth.User{}, translateError("find user by subject", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}

func (repository *identityRepository) Link(ctx context.Context, provider, subject string, userID int64) error {
	_, err := repository.pool.Exec(ctx, `
		INSERT INTO user_identities (provider, subject, user_id)
		VALUES ($1, $2, $3)
	`, provider, subject, userID)
	if err != nil {
		return translateError("link identity", err)
	}
	return nil
}

func (repository *identityRepository) CreateWithIdentity(ctx context.Context, provider, subject string, input auth.NewIdentityUser) (auth.User, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return auth.User{}, translateError("begin create with identity", err)
	}
	defer tx.Rollback(ctx)

	var user auth.User
	err = tx.QueryRow(ctx, `
		INSERT INTO users (email, name, password_hash, role)
		VALUES ($1, $2, NULL, $3)
		RETURNING id, email, name, role, deactivated_at, created_at
	`, input.Email, input.Name, string(input.Role)).Scan(
		&user.ID,
		&user.Email,
		&user.Name,
		&user.Role,
		&user.DeactivatedAt,
		&user.CreatedAt,
	)
	if err != nil {
		return auth.User{}, translateError("create user with identity", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_identities (provider, subject, user_id)
		VALUES ($1, $2, $3)
	`, provider, subject, user.ID); err != nil {
		return auth.User{}, translateError("create user with identity", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return auth.User{}, translateError("commit create with identity", err)
	}
	user.CreatedAt = user.CreatedAt.UTC()
	return user, nil
}
