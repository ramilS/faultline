package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ramilS/faultline/internal/notify"
	"github.com/ramilS/faultline/internal/project"
)

const notificationChannelColumns = `
	id, project_id, name, kind, target, secret <> '', enabled, notify_new, notify_regression,
	environments, min_level, last_success_at, COALESCE(last_error, ''), last_error_at, created_at`

type notificationChannelRepository struct {
	pool *pgxpool.Pool
}

var _ notify.ChannelRepository = (*notificationChannelRepository)(nil)

func NewNotificationChannelRepository(pool *pgxpool.Pool) notify.ChannelRepository {
	return &notificationChannelRepository{pool: pool}
}

func (repository *notificationChannelRepository) List(ctx context.Context, projectID int64) ([]notify.Channel, error) {
	rows, err := repository.pool.Query(ctx, `SELECT `+notificationChannelColumns+` FROM notification_channels WHERE project_id = $1 ORDER BY id`, projectID)
	if err != nil {
		return nil, fmt.Errorf("list notification channels: %w", err)
	}
	defer rows.Close()
	channels := make([]notify.Channel, 0)
	for rows.Next() {
		channel, err := scanNotificationChannel(rows)
		if err != nil {
			return nil, err
		}
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification channels: %w", err)
	}
	return channels, nil
}

func (repository *notificationChannelRepository) Get(ctx context.Context, projectID, channelID int64) (notify.Channel, notify.Destination, error) {
	var (
		channel notify.Channel
		secret  string
	)
	row := repository.pool.QueryRow(ctx, `SELECT `+notificationChannelColumns+`, secret FROM notification_channels WHERE project_id = $1 AND id = $2`, projectID, channelID)
	err := row.Scan(append(notificationChannelTargets(&channel), &secret)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return notify.Channel{}, notify.Destination{}, notify.ErrChannelNotFound
	}
	if err != nil {
		return notify.Channel{}, notify.Destination{}, fmt.Errorf("get notification channel: %w", err)
	}
	return channel, notify.Destination{Kind: channel.Kind, Target: channel.Target, Secret: secret}, nil
}

// Create locks the project row before counting so two concurrent creates
// cannot both see room for one more channel. NO KEY UPDATE leaves the row's
// key share free, so ingest inserting issues for the project is not blocked.
func (repository *notificationChannelRepository) Create(ctx context.Context, projectID int64, input notify.ChannelInput) (notify.Channel, error) {
	tx, err := repository.pool.Begin(ctx)
	if err != nil {
		return notify.Channel{}, fmt.Errorf("begin create notification channel: %w", err)
	}
	defer tx.Rollback(ctx)

	var count int
	err = tx.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM notification_channels WHERE project_id = p.id)
		FROM projects p WHERE p.id = $1 FOR NO KEY UPDATE
	`, projectID).Scan(&count)
	if errors.Is(err, pgx.ErrNoRows) {
		return notify.Channel{}, project.ErrNotFound
	}
	if err != nil {
		return notify.Channel{}, fmt.Errorf("lock project for notification channel: %w", err)
	}
	if count >= notify.MaxChannelsPerProject {
		return notify.Channel{}, notify.ErrTooManyChannels
	}

	secret := ""
	if input.Secret != nil {
		secret = *input.Secret
	}
	var channel notify.Channel
	err = tx.QueryRow(ctx, `
		INSERT INTO notification_channels (project_id, name, kind, target, secret, enabled, notify_new, notify_regression, environments, min_level)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		RETURNING `+notificationChannelColumns,
		projectID, input.Name, string(input.Kind), input.Target, secret, input.Enabled, input.NotifyNew, input.NotifyRegression, input.Environments, input.MinLevel,
	).Scan(notificationChannelTargets(&channel)...)
	if err != nil {
		return notify.Channel{}, fmt.Errorf("insert notification channel: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return notify.Channel{}, fmt.Errorf("commit notification channel: %w", err)
	}
	return channel, nil
}

// Update also clears the channel's last error: it described the previous
// configuration, and leaving it up would read as a verdict on the new one.
func (repository *notificationChannelRepository) Update(ctx context.Context, projectID, channelID int64, input notify.ChannelInput) (notify.Channel, error) {
	var channel notify.Channel
	err := repository.pool.QueryRow(ctx, `
		UPDATE notification_channels SET
			name = $3, target = $4, secret = COALESCE($5, secret), enabled = $6,
			notify_new = $7, notify_regression = $8, environments = $9, min_level = $10,
			last_error = NULL, last_error_at = NULL
		WHERE project_id = $1 AND id = $2
		RETURNING `+notificationChannelColumns,
		projectID, channelID, input.Name, input.Target, input.Secret, input.Enabled, input.NotifyNew, input.NotifyRegression, input.Environments, input.MinLevel,
	).Scan(notificationChannelTargets(&channel)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return notify.Channel{}, notify.ErrChannelNotFound
	}
	if err != nil {
		return notify.Channel{}, fmt.Errorf("update notification channel: %w", err)
	}
	return channel, nil
}

func (repository *notificationChannelRepository) Delete(ctx context.Context, projectID, channelID int64) error {
	tag, err := repository.pool.Exec(ctx, `DELETE FROM notification_channels WHERE project_id = $1 AND id = $2`, projectID, channelID)
	if err != nil {
		return fmt.Errorf("delete notification channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return notify.ErrChannelNotFound
	}
	return nil
}

func notificationChannelTargets(channel *notify.Channel) []any {
	return []any{
		&channel.ID, &channel.ProjectID, &channel.Name, &channel.Kind, &channel.Target, &channel.HasSecret,
		&channel.Enabled, &channel.NotifyNew, &channel.NotifyRegression, &channel.Environments, &channel.MinLevel,
		&channel.LastSuccessAt, &channel.LastError, &channel.LastErrorAt, &channel.CreatedAt,
	}
}

func scanNotificationChannel(rows pgx.Rows) (notify.Channel, error) {
	var channel notify.Channel
	if err := rows.Scan(notificationChannelTargets(&channel)...); err != nil {
		return notify.Channel{}, fmt.Errorf("scan notification channel: %w", err)
	}
	return channel, nil
}

type notificationQueue struct {
	pool *pgxpool.Pool
}

var _ notify.Queue = (*notificationQueue)(nil)

func NewNotificationQueue(pool *pgxpool.Pool) notify.Queue {
	return &notificationQueue{pool: pool}
}

// Claim first drops deliveries whose channel was disabled after they were
// queued, then leases the due ones. SKIP LOCKED lets several Faultline
// instances drain the same queue without claiming a row twice.
func (queue *notificationQueue) Claim(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]notify.PendingDelivery, error) {
	tx, err := queue.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin claim notification deliveries: %w", err)
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		DELETE FROM notification_deliveries d USING notification_channels c
		WHERE d.channel_id = c.id AND NOT c.enabled
	`); err != nil {
		return nil, fmt.Errorf("drop deliveries of disabled channels: %w", err)
	}
	rows, err := tx.Query(ctx, `
		WITH due AS (
			SELECT id FROM notification_deliveries
			WHERE next_attempt_at <= $1
			ORDER BY next_attempt_at
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		), claimed AS (
			UPDATE notification_deliveries d SET attempts = d.attempts + 1, next_attempt_at = $3
			FROM due WHERE d.id = due.id
			RETURNING d.id, d.channel_id, d.issue_id, d.trigger, d.environment, d.attempts
		)
		SELECT claimed.id, claimed.channel_id, claimed.attempts, claimed.trigger, claimed.environment,
		       c.kind, c.target, c.secret, p.slug, p.name, i.id, i.title, i.culprit, i.level
		FROM claimed
		JOIN notification_channels c ON c.id = claimed.channel_id
		JOIN projects p ON p.id = c.project_id
		JOIN issues i ON i.id = claimed.issue_id
		ORDER BY claimed.id
	`, now, limit, now.Add(lease))
	if err != nil {
		return nil, fmt.Errorf("claim notification deliveries: %w", err)
	}
	defer rows.Close()

	deliveries := make([]notify.PendingDelivery, 0, limit)
	for rows.Next() {
		var delivery notify.PendingDelivery
		n := &delivery.Notification
		d := &delivery.Destination
		if err := rows.Scan(&delivery.ID, &delivery.ChannelID, &delivery.Attempts, &n.Trigger, &n.Environment,
			&d.Kind, &d.Target, &d.Secret, &n.ProjectSlug, &n.ProjectName, &n.IssueID, &n.Title, &n.Culprit, &n.Level); err != nil {
			return nil, fmt.Errorf("scan notification delivery: %w", err)
		}
		deliveries = append(deliveries, delivery)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notification deliveries: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("commit claim notification deliveries: %w", err)
	}
	return deliveries, nil
}

func (queue *notificationQueue) Complete(ctx context.Context, delivery notify.PendingDelivery, at time.Time) error {
	return queue.exec(ctx, "complete notification delivery", `
		WITH removed AS (DELETE FROM notification_deliveries WHERE id = $1)
		UPDATE notification_channels SET last_success_at = $3 WHERE id = $2
	`, delivery.ID, delivery.ChannelID, at)
}

func (queue *notificationQueue) Reschedule(ctx context.Context, delivery notify.PendingDelivery, next time.Time, message string, at time.Time) error {
	return queue.exec(ctx, "reschedule notification delivery", `
		WITH rescheduled AS (UPDATE notification_deliveries SET next_attempt_at = $3 WHERE id = $1)
		UPDATE notification_channels SET last_error = $4, last_error_at = $5 WHERE id = $2
	`, delivery.ID, delivery.ChannelID, next, message, at)
}

func (queue *notificationQueue) Abandon(ctx context.Context, delivery notify.PendingDelivery, message string, at time.Time) error {
	return queue.exec(ctx, "abandon notification delivery", `
		WITH removed AS (DELETE FROM notification_deliveries WHERE id = $1)
		UPDATE notification_channels SET last_error = $3, last_error_at = $4 WHERE id = $2
	`, delivery.ID, delivery.ChannelID, message, at)
}

func (queue *notificationQueue) exec(ctx context.Context, operation, sql string, arguments ...any) error {
	if _, err := queue.pool.Exec(ctx, sql, arguments...); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	return nil
}

// enqueueNotifications writes, inside the batch's own transaction, one
// delivery per (change, matching enabled channel). Most flushes create and
// reopen nothing and return before touching the database.
func enqueueNotifications(ctx context.Context, tx pgx.Tx, projectID int64, changes []notify.IssueChange) error {
	if len(changes) == 0 {
		return nil
	}
	rows, err := tx.Query(ctx, `
		SELECT id, notify_new, notify_regression, environments, min_level
		FROM notification_channels WHERE project_id = $1 AND enabled ORDER BY id
	`, projectID)
	if err != nil {
		return fmt.Errorf("load notification channels: %w", err)
	}
	channels, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (notify.ChannelFilter, error) {
		var channel notify.ChannelFilter
		err := row.Scan(&channel.ID, &channel.NotifyNew, &channel.NotifyRegression, &channel.Environments, &channel.MinLevel)
		return channel, err
	})
	if err != nil {
		return fmt.Errorf("scan notification channels: %w", err)
	}
	deliveries := notify.Match(channels, changes)
	if len(deliveries) == 0 {
		return nil
	}
	channelIDs := make([]int64, len(deliveries))
	issueIDs := make([]int64, len(deliveries))
	triggers := make([]string, len(deliveries))
	environments := make([]string, len(deliveries))
	for index, delivery := range deliveries {
		channelIDs[index] = delivery.ChannelID
		issueIDs[index] = delivery.IssueID
		triggers[index] = string(delivery.Trigger)
		environments[index] = delivery.Environment
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO notification_deliveries (channel_id, issue_id, trigger, environment)
		SELECT * FROM unnest($1::bigint[], $2::bigint[], $3::text[], $4::text[])
	`, channelIDs, issueIDs, triggers, environments)
	if err != nil {
		return fmt.Errorf("enqueue notification deliveries: %w", err)
	}
	return nil
}
