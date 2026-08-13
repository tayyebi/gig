// Package handlers contains HTTP handlers grouped by concern. Handlers are
// thin: they parse input, call services or the store, and render a page or
// redirect. They never contain business rules.
package handlers

import (
	"io"
	"log/slog"
	"net/http"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/config"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// Options configures a handlers.Server.
type Options struct {
	Store  *store.Store
	Log    *slog.Logger
	Cfg    *config.Config
	Mailer services.Mailer
}

// Server holds shared dependencies for all handlers.
type Server struct {
	Store   *store.Store
	Log     *slog.Logger
	Cfg     *config.Config
	Mailer  services.Mailer
	limiter *services.RateLimiter
}

// New builds a handlers.Server.
func New(opts Options) *Server {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	if opts.Mailer == nil {
		opts.Mailer = &services.LogMailer{Log: log}
	}
	return &Server{
		Store:   opts.Store,
		Log:     log,
		Cfg:     opts.Cfg,
		Mailer:  opts.Mailer,
		limiter: services.NewRateLimiter(opts.Cfg.AuthRateLimit, opts.Cfg.AuthRateWindow),
	}
}

// Routes registers all application routes, wrapped in the session and CSRF
// middleware chain.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()

	// Health and public pages.
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("/", s.notFound)

	// Auth.
	mux.HandleFunc("GET /register", s.registerForm)
	mux.HandleFunc("POST /register", s.register)
	mux.HandleFunc("GET /login", s.loginForm)
	mux.HandleFunc("POST /login", s.login)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /verify-email", s.verifyEmail)
	mux.HandleFunc("GET /forgot-password", s.forgotPasswordForm)
	mux.HandleFunc("POST /forgot-password", s.forgotPassword)
	mux.HandleFunc("GET /reset-password", s.resetPasswordForm)
	mux.HandleFunc("POST /reset-password", s.resetPassword)

	// Account settings (authenticated).
	mux.HandleFunc("GET /account", s.requireAuth(s.accountForm))
	mux.HandleFunc("POST /account", s.requireAuth(s.accountUpdate))
	mux.HandleFunc("GET /account/password", s.requireAuth(s.passwordForm))
	mux.HandleFunc("POST /account/password", s.requireAuth(s.passwordUpdate))
	mux.HandleFunc("GET /account/mfa", s.requireAuth(s.mfaForm))
	mux.HandleFunc("POST /account/mfa", s.requireAuth(s.mfaEnable))
	mux.HandleFunc("POST /account/mfa/disable", s.requireAuth(s.mfaDisable))

	return mux
}

// Chain returns the full middleware stack around mux. withSession must run
// first so withCSRF can read the loaded session's token.
func (s *Server) Chain(mux *http.ServeMux) http.Handler {
	return s.withSession(s.withCSRF(mux))
}

// render writes a full page layout with the given status code. It populates
// the navigation user, CSRF token, and consumes the session flash.
func (s *Server) render(w http.ResponseWriter, r *http.Request, status int, p components.PageData) {
	p.User = s.viewUser(r)
	p.CSRF = s.csrfFor(r)
	if sess := s.sessionFrom(r); sess != nil {
		if f, err := s.Store.ConsumeFlash(r.Context(), sess.ID); err == nil && f != nil {
			p.Flash = &components.Flash{Kind: f.Kind, Text: f.Text}
		}
	}
	html, err := components.Layout(p)
	if err != nil {
		s.Log.Error("render layout", "error", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, string(html))
}

// viewUser maps a store user onto the component shell's user model.
func (s *Server) viewUser(r *http.Request) *components.User {
	u := s.userFrom(r)
	if u == nil {
		return nil
	}
	vu := &components.User{ID: u.ID, Name: u.Name, Email: u.Email}
	roles, err := s.Store.UserRoles(r.Context(), u.ID)
	if err != nil {
		return vu
	}
	for _, role := range roles {
		if role == store.RoleAdmin {
			vu.IsAdmin = true
		}
	}
	return vu
}

// csrfFor returns the session's CSRF token, if any.
func (s *Server) csrfFor(r *http.Request) string {
	if sess := s.sessionFrom(r); sess != nil {
		return sess.CSRF
	}
	return ""
}

func (s *Server) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ok\n")
}

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.Store == nil {
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	if err := s.Store.Ping(r.Context()); err != nil {
		s.Log.Error("readiness check failed", "error", err)
		http.Error(w, "not ready", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, "ready\n")
}

func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	body, err := components.Home(components.HomeData{
		Categories: []components.CategoryCard{
			{Name: "Design", Slug: "design", Blurb: "Logos, brand kits, and more"},
			{Name: "Development", Slug: "development", Blurb: "Websites, scripts, and integrations"},
			{Name: "Writing", Slug: "writing", Blurb: "Copy, editing, and translation"},
			{Name: "Marketing", Slug: "marketing", Blurb: "Social media, SEO, and ads"},
		},
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title:       "Gig Marketplace - Find the right freelancer",
		Description: "Browse gigs, get work done, and pay securely when you are satisfied.",
		Body:        body,
	})
}

func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	body, err := components.NotFound()
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusNotFound, components.PageData{
		Title: "Page not found",
		Body:  body,
	})
}

func (s *Server) renderError(w http.ResponseWriter, err error) {
	s.Log.Error("render error", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
