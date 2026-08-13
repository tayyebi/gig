package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// Job statuses.
const (
	JobStatusQueued  = "queued"
	JobStatusClaimed = "claimed"
	JobStatusDone    = "done"
	JobStatusFailed  = "failed"
	JobStatusDead    = "dead"
)

// ErrNoJob is returned by ClaimJob when no job is available for processing.
var ErrNoJob = errors.New("no job available")

// Job is a row in the PostgreSQL-backed job queue.
type Job struct {
	ID          int64
	Kind        string
	Payload     []byte
	Status      string
	Attempts    int
	MaxAttempts int
	RunAt       time.Time
	LockedBy    string
	LockedAt    *time.Time
	LastError   *string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// DecodePayload unmarshals the job payload into v.
func (j *Job) DecodePayload(v any) error { return json.Unmarshal(j.Payload, v) }

// EnqueueJob inserts a job into the queue.
func (s *Store) EnqueueJob(ctx context.Context, kind string, payload any, runAt time.Time, maxAttempts int) (int64, error) {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("marshal job payload: %w", err)
	}
	if runAt.IsZero() {
		runAt = time.Now()
	}
	var id int64
	err = s.db.QueryRowContext(ctx,
		`INSERT INTO jobs (kind, payload, run_at, max_attempts)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id`,
		kind, raw, runAt, maxAttempts,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("enqueue job: %w", err)
	}
	return id, nil
}

// ClaimJob atomically claims the next eligible job using FOR UPDATE SKIP LOCKED
// so concurrent workers never process the same job. A job claimed by a worker
// that has not checked in within staleAfter is reclaimed.
func (s *Store) ClaimJob(ctx context.Context, workerID string, staleAfter time.Duration) (*Job, error) {
	var j Job
	var lastError sql.NullString
	err := s.db.QueryRowContext(ctx, `
		UPDATE jobs SET
			status     = 'claimed',
			locked_by  = $1,
			locked_at  = now(),
			attempts   = attempts + 1,
			updated_at = now()
		WHERE id = (
			SELECT id FROM jobs
			WHERE (status IN ('queued', 'failed') AND run_at <= now())
			   OR (status = 'claimed' AND locked_at < now() - $2::interval)
			ORDER BY run_at, id
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, kind, payload, status, attempts, max_attempts, run_at,
		          locked_by, locked_at, last_error, created_at, updated_at`,
		workerID, staleAfter.String(),
	).Scan(&j.ID, &j.Kind, &j.Payload, &j.Status, &j.Attempts, &j.MaxAttempts, &j.RunAt,
		&j.LockedBy, &j.LockedAt, &lastError, &j.CreatedAt, &j.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNoJob
	}
	if err != nil {
		return nil, fmt.Errorf("claim job: %w", err)
	}
	if lastError.Valid {
		j.LastError = &lastError.String
	}
	return &j, nil
}

// CompleteJob marks a claimed job as done.
func (s *Store) CompleteJob(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE jobs SET status = 'done', locked_by = NULL, locked_at = NULL, updated_at = now()
		 WHERE id = $1 AND status = 'claimed'`,
		id,
	)
	if err != nil {
		return fmt.Errorf("complete job %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("complete job %d: not claimed", id)
	}
	return nil
}

// FailJob records a failure for a claimed job. Jobs that exhaust their attempts
// become dead letters; others are retried after the given backoff.
func (s *Store) FailJob(ctx context.Context, id int64, cause error, backoff time.Duration) (string, error) {
	var status string
	err := s.db.QueryRowContext(ctx, `
		UPDATE jobs SET
			status     = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'failed' END,
			locked_by  = NULL,
			locked_at  = NULL,
			last_error = $2,
			run_at     = CASE WHEN attempts < max_attempts THEN now() + $3::interval ELSE run_at END,
			updated_at = now()
		WHERE id = $1 AND status = 'claimed'
		RETURNING status`,
		id, cause.Error(), backoff.String(),
	).Scan(&status)
	if err != nil {
		return "", fmt.Errorf("fail job %d: %w", id, err)
	}
	return status, nil
}
