package store

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestInsertWebhookEventDedup covers TODO.md Phase 8's "test duplicate and
// out-of-order webhooks": the same (provider, event_id) delivered twice
// (a provider retry, or two workers racing on the same delivery) must
// insert exactly once.
func TestInsertWebhookEventDedup(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	id1, inserted1, err := st.InsertWebhookEvent(ctx, "stripe", "evt_dedup_1", "checkout.session.completed", "cs_dedup_1", []byte(`{"id":"evt_dedup_1"}`), "hash1")
	if err != nil {
		t.Fatalf("first insert: %v", err)
	}
	if !inserted1 {
		t.Fatalf("first insert should report inserted=true")
	}

	id2, inserted2, err := st.InsertWebhookEvent(ctx, "stripe", "evt_dedup_1", "checkout.session.completed", "cs_dedup_1", []byte(`{"id":"evt_dedup_1"}`), "hash1")
	if err != nil {
		t.Fatalf("duplicate insert: %v", err)
	}
	if inserted2 {
		t.Fatalf("duplicate insert should report inserted=false")
	}
	if id2 != 0 {
		t.Errorf("duplicate insert id = %d, want 0 (no new row)", id2)
	}
	_ = id1

	events, err := st.PendingWebhookEvents(ctx, 100)
	if err != nil {
		t.Fatalf("PendingWebhookEvents: %v", err)
	}
	count := 0
	for _, e := range events {
		if e.Provider == "stripe" && e.EventID == "evt_dedup_1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("found %d rows for the deduped event, want 1", count)
	}
}

// TestRetryJobRequeuesDeadLetter covers the "safe webhook retry tool"
// (admin console, closed out in this pass): a job that exhausted its
// retries and dead-lettered can be explicitly re-queued by an admin, with a
// fresh attempts budget, and only from a retryable state.
func TestRetryJobRequeuesDeadLetter(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	id, err := st.EnqueueJob(ctx, "payment.webhook_process", map[string]any{"provider": "stripe", "event_id": "evt_retry_1"}, time.Time{}, 1)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	job, err := st.ClaimJob(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	status, err := st.FailJob(ctx, job.ID, errors.New("boom"), time.Millisecond)
	if err != nil {
		t.Fatalf("FailJob: %v", err)
	}
	if status != JobStatusDead {
		t.Fatalf("status after exhausting max_attempts=1 = %s, want dead", status)
	}

	if err := st.RetryJob(ctx, id); err != nil {
		t.Fatalf("RetryJob: %v", err)
	}
	requeued, err := st.ClaimJob(ctx, "worker-2", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob after retry: %v", err)
	}
	if requeued.ID != id {
		t.Fatalf("claimed job %d, want %d", requeued.ID, id)
	}

	// Retrying a job that is not dead/failed (e.g. already queued) is a
	// no-op reported as ErrNotFound, not a silent success.
	if err := st.RetryJob(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("RetryJob on a claimed job = %v, want ErrNotFound", err)
	}
}

// TestConcurrentClaimJobOnlyOneWinner covers "provider downtime and retry
// queues": when multiple worker goroutines race to claim the same pending
// job (simulating several workers polling after a provider outage clears),
// FOR UPDATE SKIP LOCKED must hand it to exactly one of them.
func TestConcurrentClaimJobOnlyOneWinner(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()

	id, err := st.EnqueueJob(ctx, "payment.webhook_process", map[string]any{"provider": "stripe", "event_id": "evt_race_1"}, time.Time{}, 3)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	winners := 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			job, err := st.ClaimJob(ctx, "worker-race", time.Minute)
			if errors.Is(err, ErrNoJob) {
				return
			}
			if err != nil {
				t.Errorf("ClaimJob: %v", err)
				return
			}
			if job.ID == id {
				mu.Lock()
				winners++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if winners != 1 {
		t.Fatalf("job claimed by %d workers concurrently, want exactly 1", winners)
	}
}
