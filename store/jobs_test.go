package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tayyebi/gig/migrations"
)

func openTestJobsStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx, `TRUNCATE TABLE jobs RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate jobs: %v", err)
	}
	return st
}

func TestJobsRoundTrip(t *testing.T) {
	st := openTestJobsStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	id, err := st.EnqueueJob(ctx, "test.kind", map[string]any{"n": 1}, time.Time{}, 3)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	job, err := st.ClaimJob(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if job.ID != id {
		t.Fatalf("job.ID = %d, want %d", job.ID, id)
	}
	if job.Kind != "test.kind" {
		t.Errorf("job.Kind = %q", job.Kind)
	}
	var payload struct{ N int }
	if err := job.DecodePayload(&payload); err != nil {
		t.Fatalf("DecodePayload: %v", err)
	}
	if payload.N != 1 {
		t.Errorf("payload.N = %d", payload.N)
	}

	// A claimed job is not available to another worker.
	if _, err := st.ClaimJob(ctx, "worker-2", time.Minute); !errors.Is(err, ErrNoJob) {
		t.Fatalf("expected ErrNoJob after claim, got %v", err)
	}

	if err := st.CompleteJob(ctx, id); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	// Completing an already-done job must fail.
	if err := st.CompleteJob(ctx, id); err == nil {
		t.Fatal("expected error completing an already-done job")
	}
}

func TestJobsFailAndDeadLetter(t *testing.T) {
	st := openTestJobsStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	// max_attempts = 2: one failure -> retry, second failure -> dead.
	id, err := st.EnqueueJob(ctx, "flaky", map[string]string{}, time.Time{}, 2)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	for attempt := 1; attempt <= 2; attempt++ {
		job, err := st.ClaimJob(ctx, "w", time.Minute)
		if err != nil {
			t.Fatalf("ClaimJob #%d: %v", attempt, err)
		}
		// Zero backoff keeps the failed job immediately retryable.
		status, err := st.FailJob(ctx, job.ID, errors.New("boom"), 0)
		if err != nil {
			t.Fatalf("FailJob #%d: %v", attempt, err)
		}
		if attempt == 1 && status != JobStatusFailed {
			t.Errorf("first failure status = %q, want failed", status)
		}
		if attempt == 2 && status != JobStatusDead {
			t.Errorf("second failure status = %q, want dead", status)
		}
	}

	// Dead jobs are not claimable.
	if _, err := st.ClaimJob(ctx, "w", time.Minute); !errors.Is(err, ErrNoJob) {
		t.Fatalf("expected ErrNoJob for dead job, got %v", err)
	}

	var lastError *string
	if err := st.db.QueryRowContext(ctx, `SELECT last_error FROM jobs WHERE id = $1`, id).Scan(&lastError); err != nil {
		t.Fatalf("query last_error: %v", err)
	}
	if lastError == nil || *lastError != "boom" {
		t.Errorf("last_error = %v", lastError)
	}
}

func TestClaimReclaimsStaleJobs(t *testing.T) {
	st := openTestJobsStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	id, err := st.EnqueueJob(ctx, "stale", map[string]string{}, time.Time{}, 3)
	if err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// Claim, then "crash": the stale threshold lets another worker reclaim it.
	if _, err := st.ClaimJob(ctx, "crashed", time.Minute); err != nil {
		t.Fatalf("first ClaimJob: %v", err)
	}
	if _, err := st.ClaimJob(ctx, "new", time.Duration(0)); err != nil {
		t.Fatalf("reclaim stale job: %v", err)
	}
	job, err := st.ClaimJob(ctx, "new2", time.Duration(0))
	if err != nil {
		t.Fatalf("reclaim again after second crash: %v", err)
	}
	if job.ID != id {
		t.Errorf("reclaimed job.ID = %d, want %d", job.ID, id)
	}
}

func TestEnqueueScheduledJobNotClaimableYet(t *testing.T) {
	st := openTestJobsStore(t)
	ctx := context.Background()
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	future := time.Now().Add(time.Hour)
	if _, err := st.EnqueueJob(ctx, "later", map[string]string{}, future, 3); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := st.ClaimJob(ctx, "w", time.Minute); !errors.Is(err, ErrNoJob) {
		t.Fatalf("expected ErrNoJob for future job, got %v", err)
	}
}
