package notify

import (
	"context"
	"errors"
	"testing"
	"time"
)

type queueCall struct {
	op      string
	id      int64
	next    time.Time
	message string
}

type fakeQueue struct {
	pending []PendingDelivery
	calls   []queueCall
}

func (q *fakeQueue) Claim(context.Context, time.Time, int, time.Duration) ([]PendingDelivery, error) {
	claimed := q.pending
	q.pending = nil
	return claimed, nil
}

func (q *fakeQueue) Complete(_ context.Context, delivery PendingDelivery, _ time.Time) error {
	q.calls = append(q.calls, queueCall{op: "complete", id: delivery.ID})
	return nil
}

func (q *fakeQueue) Reschedule(_ context.Context, delivery PendingDelivery, next time.Time, message string, _ time.Time) error {
	q.calls = append(q.calls, queueCall{op: "reschedule", id: delivery.ID, next: next, message: message})
	return nil
}

func (q *fakeQueue) Abandon(_ context.Context, delivery PendingDelivery, message string, _ time.Time) error {
	q.calls = append(q.calls, queueCall{op: "abandon", id: delivery.ID, message: message})
	return nil
}

type fakeTransport struct {
	err  error
	sent []Notification
}

func (f *fakeTransport) Send(_ context.Context, _ Destination, n Notification, _ int64) error {
	f.sent = append(f.sent, n)
	return f.err
}

var workerNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func runOnce(t *testing.T, delivery PendingDelivery, sendErr error) ([]queueCall, []Notification) {
	t.Helper()
	queue := &fakeQueue{pending: []PendingDelivery{delivery}}
	transport := &fakeTransport{err: sendErr}
	worker := NewWorker(queue, transport, WorkerOptions{PublicURL: "https://faultline.example", Now: func() time.Time { return workerNow }})

	claimed, err := worker.RunOnce(t.Context())
	if err != nil || claimed != 1 {
		t.Fatalf("RunOnce = %d, %v", claimed, err)
	}
	return queue.calls, transport.sent
}

func TestWorkerCompletesADeliveredNotificationWithAnIssueLink(t *testing.T) {
	delivery := PendingDelivery{ID: 5, Attempts: 1, Notification: Notification{ProjectSlug: "shop", IssueID: 7}}

	calls, sent := runOnce(t, delivery, nil)

	if len(calls) != 1 || calls[0].op != "complete" || calls[0].id != 5 {
		t.Fatalf("queue calls = %+v, want one complete of 5", calls)
	}
	if sent[0].IssueURL != "https://faultline.example/shop/issues/7" {
		t.Fatalf("IssueURL = %q", sent[0].IssueURL)
	}
}

func TestWorkerFailureHandling(t *testing.T) {
	cases := map[string]struct {
		attempts int
		err      error
		want     queueCall
	}{
		"a transient failure is rescheduled with backoff": {
			1, &SendError{Message: "webhook: HTTP 503"},
			queueCall{op: "reschedule", id: 5, next: workerNow.Add(30 * time.Second), message: "webhook: HTTP 503"},
		},
		"the destination's Retry-After wins when longer": {
			1, &SendError{Message: "telegram: HTTP 429", RetryAfter: 5 * time.Minute},
			queueCall{op: "reschedule", id: 5, next: workerNow.Add(5 * time.Minute), message: "telegram: HTTP 429"},
		},
		"a permanent rejection is abandoned on the first attempt": {
			1, &SendError{Message: "telegram: HTTP 400: Bad Request: chat not found", Permanent: true},
			queueCall{op: "abandon", id: 5, message: "telegram: HTTP 400: Bad Request: chat not found"},
		},
		"the last allowed attempt is abandoned even when transient": {
			MaxAttempts, &SendError{Message: "webhook: HTTP 503"},
			queueCall{op: "abandon", id: 5, message: "webhook: HTTP 503"},
		},
		"a plain error is treated as transient": {
			2, errors.New("boom"),
			queueCall{op: "reschedule", id: 5, next: workerNow.Add(time.Minute), message: "boom"},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			calls, _ := runOnce(t, PendingDelivery{ID: 5, Attempts: tc.attempts}, tc.err)

			if len(calls) != 1 || calls[0] != tc.want {
				t.Fatalf("queue calls = %+v, want [%+v]", calls, tc.want)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	cases := []struct {
		attempts   int
		retryAfter time.Duration
		want       time.Duration
	}{
		{1, 0, 30 * time.Second},
		{2, 0, time.Minute},
		{3, 0, 2 * time.Minute},
		{7, 0, 32 * time.Minute},
		{8, 0, time.Hour},
		{50, 0, time.Hour},
		{0, 0, 30 * time.Second},
		{1, 10 * time.Second, 30 * time.Second},
		{1, 3 * time.Hour, 3 * time.Hour},
	}
	for _, tc := range cases {
		if got := RetryDelay(tc.attempts, tc.retryAfter); got != tc.want {
			t.Errorf("RetryDelay(%d, %v) = %v, want %v", tc.attempts, tc.retryAfter, got, tc.want)
		}
	}
}
