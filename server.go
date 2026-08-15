package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/tayyebi/gig/config"
	"github.com/tayyebi/gig/handlers"
	"github.com/tayyebi/gig/providers"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// Server wires the HTTP layer: middleware, routing, and graceful shutdown.
type Server struct {
	cfg      *config.Config
	log      *slog.Logger
	store    *store.Store
	handlers *handlers.Server
}

func newServer(cfg *config.Config, log *slog.Logger, st *store.Store) *Server {
	mailer := services.MailerFromConfig(cfg, log)
	registry := buildProviderRegistry(cfg)
	var wallet *services.WalletCrypto
	if cfg.WalletEncryptionKey != "" {
		var err error
		if wallet, err = services.NewWalletCrypto(cfg); err != nil {
			log.Error("wallet crypto disabled: invalid WALLET_ENCRYPTION_KEY", "error", err)
		}
	}
	return &Server{
		cfg:   cfg,
		log:   log,
		store: st,
		handlers: handlers.New(handlers.Options{
			Store:     st,
			Log:       log,
			Cfg:       cfg,
			Mailer:    mailer,
			Providers: registry,
			Wallet:    wallet,
		}),
	}
}

// buildProviderRegistry constructs the set of active payment provider
// adapters from configuration. Each adapter is included only when its
// required credentials are present, so a deployment can enable Stripe,
// BTCPay, both, or neither independently (PLAN.md section 9).
func buildProviderRegistry(cfg *config.Config) providers.Registry {
	if !cfg.PaymentsEnabled {
		return providers.Registry{}
	}
	var ps []providers.Provider
	if cfg.StripeSecretKey != "" {
		ps = append(ps, providers.NewStripe(cfg.StripeSecretKey))
	}
	if cfg.BTCPayURL != "" && cfg.BTCPayAPIKey != "" && cfg.BTCPayStoreID != "" {
		ps = append(ps, providers.NewBTCPay(cfg.BTCPayURL, cfg.BTCPayAPIKey, cfg.BTCPayStoreID))
	}
	if cfg.EVMBaseRPCURL != "" && cfg.EVMBaseTreasuryAddress != "" {
		ps = append(ps, providers.NewEVM("evm-base", cfg.EVMBaseRPCURL, cfg.EVMBaseChainID, cfg.EVMBaseTreasuryAddress,
			map[string]string{"usdc": cfg.EVMBaseUSDCContract, "usdt": cfg.EVMBaseUSDTContract}, cfg.EVMRequiredConfirmations))
	}
	if cfg.EVMPolygonRPCURL != "" && cfg.EVMPolygonTreasuryAddress != "" {
		ps = append(ps, providers.NewEVM("evm-polygon", cfg.EVMPolygonRPCURL, cfg.EVMPolygonChainID, cfg.EVMPolygonTreasuryAddress,
			map[string]string{"usdc": cfg.EVMPolygonUSDCContract, "usdt": cfg.EVMPolygonUSDTContract}, cfg.EVMRequiredConfirmations))
	}
	return providers.NewRegistry(ps...)
}

// handler builds the full middleware-wrapped route handler.
func (s *Server) handler() http.Handler {
	staticSub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(fmt.Sprintf("embed static sub: %v", err))
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(staticSub))))
	// Uploaded portfolio and gig images. These are public by design (they are
	// shown on public seller and gig pages). Order deliveries and dispute
	// evidence are private: they live under cfg.PrivateStorageDir, which is
	// never mounted here, and are only ever streamed back through the
	// authorization-checked handler at GET /orders/{id}/attachments/{id}.
	mux.Handle("GET /media/", http.StripPrefix("/media/", http.FileServer(http.Dir(s.cfg.StorageDir))))
	// Provider webhooks are server-to-server calls with no session cookie and
	// no CSRF token; they are registered outside s.handlers.Chain and verify
	// the provider's own request signature instead (PLAN.md section 9).
	mux.HandleFunc("POST /webhooks/stripe", s.handlers.StripeWebhook)
	mux.HandleFunc("POST /webhooks/btcpay", s.handlers.BTCPayWebhook)
	mux.Handle("/", s.handlers.Chain(s.handlers.Routes()))

	var h http.Handler = mux
	h = s.withRequestID(h)
	h = s.withRecover(h)
	h = s.withLogging(h)
	h = securityHeaders(h)
	return h
}

// Run starts the HTTP server and shuts it down gracefully when ctx is done.
func (s *Server) Run(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.cfg.HTTPAddr,
		Handler:           s.handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ln, err := net.Listen("tcp", s.cfg.HTTPAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.HTTPAddr, err)
	}

	errCh := make(chan error, 1)
	go func() {
		s.log.Info("http server listening", "addr", s.cfg.HTTPAddr)
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		s.log.Info("shutting down http server")
		shCtx, cancel := context.WithTimeout(context.Background(), s.cfg.ShutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shCtx); err != nil {
			s.log.Error("graceful shutdown failed", "error", err)
			return srv.Close()
		}
		return nil
	case err := <-errCh:
		return err
	}
}

type requestIDKey struct{}

// withRequestID assigns a request ID for correlation across logs.
func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if id == "" {
			b := make([]byte, 8)
			if _, err := rand.Read(b); err == nil {
				id = hex.EncodeToString(b)
			} else {
				id = "unknown"
			}
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey{}, id)))
	})
}

func requestID(ctx context.Context) string {
	if id, ok := ctx.Value(requestIDKey{}).(string); ok {
		return id
	}
	return "unknown"
}

// withLogging records a structured access log line per request.
func (s *Server) withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		s.log.Info("http request",
			"request_id", requestID(r.Context()),
			"method", r.Method,
			"path", r.URL.Path,
			"status", sw.status,
			"duration_ms", time.Since(start).Milliseconds(),
			"bytes", sw.bytes,
		)
	})
}

// withRecover converts panics into 500 responses instead of dropping connections.
func (s *Server) withRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.log.Error("panic recovered",
					"request_id", requestID(r.Context()),
					"panic", v,
					"stack", string(debug.Stack()),
				)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// statusWriter captures the response status and byte count for logging.
type statusWriter struct {
	http.ResponseWriter
	status int
	bytes  int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	w.bytes += n
	return n, err
}

func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
