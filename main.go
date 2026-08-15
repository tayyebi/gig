package main

import (
	"context"
	"embed"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/tayyebi/gig/config"
	"github.com/tayyebi/gig/migrations"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

//go:embed all:static
var staticFS embed.FS

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config: %v\n", err)
		return 1
	}
	log := newLogger(cfg)

	switch cfg.AppRole {
	case config.RoleWorker:
		if err := runWorker(cfg, log); err != nil {
			log.Error("worker failed", "error", err)
			return 1
		}
		return 0
	case config.RoleWeb:
		if err := runWeb(cfg, log); err != nil {
			log.Error("web failed", "error", err)
			return 1
		}
		return 0
	default:
		log.Error("unknown APP_ROLE", "role", cfg.AppRole)
		return 1
	}
}

func runWeb(cfg *config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	log.Info("applying migrations")
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}

	srv := newServer(cfg, log, st)
	return srv.Run(ctx)
}

func runWorker(cfg *config.Config, log *slog.Logger) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.OpenWithRetry(ctx, cfg, func(err error) {
		log.Warn("database not ready", "error", err)
	})
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	mailer := services.MailerFromConfig(cfg, log)
	registry := buildProviderRegistry(cfg)
	q := newJobQueue(cfg, log, st, mailer, registry)
	jc := &jobContext{Store: st, Log: log, Cfg: cfg, Mailer: mailer, Providers: registry}
	if err := registerMaintenance(q, jc); err != nil {
		return fmt.Errorf("register maintenance jobs: %w", err)
	}
	registerNotifications(q)
	if err := registerOrderJobs(q, jc); err != nil {
		return fmt.Errorf("register order jobs: %w", err)
	}
	if err := registerPaymentJobs(q, jc); err != nil {
		return fmt.Errorf("register payment jobs: %w", err)
	}
	return q.Run(ctx)
}
