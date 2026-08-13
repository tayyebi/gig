package main

import (
	"context"
	"fmt"
	"time"

	"github.com/tayyebi/gig/store"
)

// maintenanceSweepKind is a recurring job that deletes expired sessions and
// one-time auth tokens. It re-enqueues itself so exactly one sweep is ever
// outstanding, even with multiple workers.
const maintenanceSweepKind = "maintenance.sweep"

// registerMaintenance wires the recurring sweep job and seeds the first run.
func registerMaintenance(q *jobQueue, jc *jobContext) error {
	q.Register(maintenanceSweepKind, maintenanceSweep)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if _, err := q.st.EnqueueJob(ctx, maintenanceSweepKind, nil, time.Now(), q.cfg.JobMaxAttempts); err != nil {
		return fmt.Errorf("seed maintenance sweep: %w", err)
	}
	return nil
}

// maintenanceSweep expires stale sessions and tokens, then schedules the next
// sweep for MaintenanceInterval from now.
func maintenanceSweep(ctx context.Context, jc *jobContext, _ store.Job) error {
	expiredSessions, err := jc.Store.DeleteExpiredSessions(ctx)
	if err != nil {
		return fmt.Errorf("delete expired sessions: %w", err)
	}
	expiredTokens, err := jc.Store.DeleteExpiredAuthTokens(ctx)
	if err != nil {
		return fmt.Errorf("delete expired auth tokens: %w", err)
	}
	jc.Log.Info("maintenance sweep complete",
		"expired_sessions", expiredSessions, "expired_tokens", expiredTokens)

	next := time.Now().Add(jc.Cfg.MaintenanceInterval)
	if _, err := jc.Store.EnqueueJob(ctx, maintenanceSweepKind, nil, next, jc.Cfg.JobMaxAttempts); err != nil {
		return fmt.Errorf("reschedule maintenance sweep: %w", err)
	}
	return nil
}
