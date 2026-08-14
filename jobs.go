package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"time"

	"github.com/tayyebi/gig/config"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// jobContext bundles dependencies available to every job handler.
type jobContext struct {
	Store  *store.Store
	Log    *slog.Logger
	Cfg    *config.Config
	Mailer services.Mailer
}

// jobHandler processes a single claimed job. Returning an error records the
// failure and retries (or dead-letters) the job.
type jobHandler func(ctx context.Context, jc *jobContext, job store.Job) error

// jobQueue polls the PostgreSQL job table and dispatches work to a bounded
// pool of goroutines. Claims use FOR UPDATE SKIP LOCKED in the store, so
// multiple workers can run safely.
type jobQueue struct {
	cfg      *config.Config
	log      *slog.Logger
	st       *store.Store
	mailer   services.Mailer
	workerID string
	handlers map[string]jobHandler
	slots    chan struct{}
	wg       sync.WaitGroup
}

func newJobQueue(cfg *config.Config, log *slog.Logger, st *store.Store, mailer services.Mailer) *jobQueue {
	return &jobQueue{
		cfg:      cfg,
		log:      log,
		st:       st,
		mailer:   mailer,
		workerID: fmt.Sprintf("%s-%d", cfg.Hostname(), os.Getpid()),
		handlers: make(map[string]jobHandler),
		slots:    make(chan struct{}, cfg.JobConcurrency),
	}
}

// Register adds a handler for a job kind.
func (q *jobQueue) Register(kind string, h jobHandler) {
	q.handlers[kind] = h
}

// Run polls the queue until ctx is cancelled, then drains in-flight jobs.
func (q *jobQueue) Run(ctx context.Context) error {
	jc := &jobContext{Store: q.st, Log: q.log, Cfg: q.cfg, Mailer: q.mailer}
	q.log.Info("job queue started", "worker", q.workerID, "concurrency", q.cfg.JobConcurrency)

	ticker := time.NewTicker(q.cfg.JobPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			q.log.Info("job queue draining")
			q.wg.Wait()
			q.log.Info("job queue stopped")
			return nil
		case <-ticker.C:
			q.poll(ctx, jc)
		}
	}
}

func (q *jobQueue) poll(ctx context.Context, jc *jobContext) {
	for {
		select {
		case <-ctx.Done():
			return
		case q.slots <- struct{}{}:
		default:
			return
		}
		job, err := q.st.ClaimJob(ctx, q.workerID, q.cfg.JobStaleThreshold)
		if err != nil {
			<-q.slots
			if !errors.Is(err, store.ErrNoJob) {
				q.log.Error("claim job failed", "error", err)
			}
			return
		}
		q.wg.Add(1)
		go func(job store.Job) {
			defer q.wg.Done()
			defer func() { <-q.slots }()
			q.process(ctx, jc, job)
		}(*job)
	}
}

func (q *jobQueue) process(ctx context.Context, jc *jobContext, job store.Job) {
	log := q.log.With("job_id", job.ID, "kind", job.Kind, "attempt", job.Attempts)
	defer func() {
		if v := recover(); v != nil {
			log.Error("job handler panic", "panic", v)
			_, _ = q.st.FailJob(ctx, job.ID, fmt.Errorf("panic: %v", v), retryBackoff(job.Attempts))
		}
	}()

	handler, ok := q.handlers[job.Kind]
	if !ok {
		log.Error("no handler registered for job kind")
		_, _ = q.st.FailJob(ctx, job.ID, fmt.Errorf("no handler for kind %q", job.Kind), 0)
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, q.cfg.JobClaimTimeout)
	defer cancel()

	if err := handler(jobCtx, jc, job); err != nil {
		status, ferr := q.st.FailJob(ctx, job.ID, err, retryBackoff(job.Attempts))
		if ferr != nil {
			log.Error("record job failure", "error", ferr)
			return
		}
		log.Warn("job failed", "status", status, "error", err)
		return
	}

	if err := q.st.CompleteJob(ctx, job.ID); err != nil {
		log.Error("complete job", "error", err)
		return
	}
	log.Info("job completed")
}

// retryBackoff returns an exponential backoff capped at roughly one minute.
func retryBackoff(attempt int) time.Duration {
	exp := attempt
	if exp > 6 {
		exp = 6
	}
	return time.Duration(1<<uint(exp)) * time.Second
}
