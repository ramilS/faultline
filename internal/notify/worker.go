package notify

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// MaxAttempts is how many times a delivery is tried before it is dropped
	// and its error left on the channel for the dashboard to show.
	MaxAttempts = 8
	// claimLease is how long a claimed delivery stays invisible to other
	// workers. A worker that dies mid-send leaves it to be retried once the
	// lease runs out, so delivery is at least once.
	claimLease       = 2 * time.Minute
	claimBatch       = 20
	defaultPollEvery = 5 * time.Second
	baseRetryDelay   = 30 * time.Second
	maxRetryDelay    = time.Hour
)

// PendingDelivery is a claimed delivery with everything needed to send it.
// Attempts already counts the attempt being made.
type PendingDelivery struct {
	ID           int64
	ChannelID    int64
	Attempts     int
	Destination  Destination
	Notification Notification
}

// Queue is the delivery outbox. Rows enter it inside the transaction that
// created or reopened the issue (see storage/postgres eventSink).
type Queue interface {
	// Claim returns up to limit due deliveries, counting the attempt and
	// hiding each from other claimers until now+lease.
	Claim(ctx context.Context, now time.Time, limit int, lease time.Duration) ([]PendingDelivery, error)
	// Complete removes a delivered row and records success on its channel.
	Complete(ctx context.Context, delivery PendingDelivery, at time.Time) error
	// Reschedule records failure on the channel and makes the delivery due
	// again at next.
	Reschedule(ctx context.Context, delivery PendingDelivery, next time.Time, message string, at time.Time) error
	// Abandon removes a delivery that will not be retried and records
	// failure on its channel.
	Abandon(ctx context.Context, delivery PendingDelivery, message string, at time.Time) error
}

// Transport sends one notification; *Sender satisfies it.
type Transport interface {
	Send(ctx context.Context, destination Destination, n Notification, deliveryID int64) error
}

type WorkerOptions struct {
	// PublicURL is the dashboard's external base URL; without it messages
	// carry no link.
	PublicURL   string
	PollEvery   time.Duration
	Now         func() time.Time
	ReportError func(error)
}

// Worker drains the delivery queue in the background.
type Worker struct {
	queue       Queue
	transport   Transport
	publicURL   string
	pollEvery   time.Duration
	now         func() time.Time
	reportError func(error)
}

func NewWorker(queue Queue, transport Transport, options WorkerOptions) *Worker {
	worker := &Worker{queue: queue, transport: transport, publicURL: options.PublicURL, pollEvery: options.PollEvery, now: options.Now, reportError: options.ReportError}
	if worker.pollEvery <= 0 {
		worker.pollEvery = defaultPollEvery
	}
	if worker.now == nil {
		worker.now = time.Now
	}
	if worker.reportError == nil {
		worker.reportError = func(error) {}
	}
	return worker
}

// Run polls until ctx is cancelled. A full batch is followed immediately by
// another claim, so a backlog drains without waiting out the poll interval.
func (w *Worker) Run(ctx context.Context) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		claimed, err := w.RunOnce(ctx)
		if err != nil && ctx.Err() == nil {
			w.reportError(err)
		}
		if claimed == claimBatch {
			timer.Reset(0)
		} else {
			timer.Reset(w.pollEvery)
		}
	}
}

// RunOnce claims and processes one batch, returning how many it claimed.
func (w *Worker) RunOnce(ctx context.Context) (int, error) {
	deliveries, err := w.queue.Claim(ctx, w.now(), claimBatch, claimLease)
	if err != nil {
		return 0, fmt.Errorf("claim notification deliveries: %w", err)
	}
	var errs []error
	for _, delivery := range deliveries {
		if err := w.deliver(ctx, delivery); err != nil {
			errs = append(errs, err)
		}
	}
	return len(deliveries), errors.Join(errs...)
}

func (w *Worker) deliver(ctx context.Context, delivery PendingDelivery) error {
	notification := delivery.Notification
	notification.IssueURL = IssueURL(w.publicURL, notification.ProjectSlug, notification.IssueID)
	sendErr := w.transport.Send(ctx, delivery.Destination, notification, delivery.ID)
	at := w.now()
	if sendErr == nil {
		if err := w.queue.Complete(ctx, delivery, at); err != nil {
			return fmt.Errorf("complete notification delivery %d: %w", delivery.ID, err)
		}
		return nil
	}
	var failure *SendError
	if !errors.As(sendErr, &failure) {
		failure = &SendError{Message: sendErr.Error()}
	}
	if failure.Permanent || delivery.Attempts >= MaxAttempts {
		if err := w.queue.Abandon(ctx, delivery, failure.Message, at); err != nil {
			return fmt.Errorf("abandon notification delivery %d: %w", delivery.ID, err)
		}
		return nil
	}
	next := at.Add(RetryDelay(delivery.Attempts, failure.RetryAfter))
	if err := w.queue.Reschedule(ctx, delivery, next, failure.Message, at); err != nil {
		return fmt.Errorf("reschedule notification delivery %d: %w", delivery.ID, err)
	}
	return nil
}

// RetryDelay is the wait after the attempts-th failed attempt: 30s doubling
// to a one-hour cap, or the destination's own Retry-After when that is
// longer.
func RetryDelay(attempts int, retryAfter time.Duration) time.Duration {
	doublings := min(max(attempts-1, 0), MaxAttempts)
	return max(min(baseRetryDelay<<doublings, maxRetryDelay), retryAfter)
}
