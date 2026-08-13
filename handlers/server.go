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
	"github.com/tayyebi/gig/store"
)

// Options configures a handlers.Server.
type Options struct {
	Store *store.Store
	Log   *slog.Logger
	Cfg   *config.Config
}

// Server holds shared dependencies for all handlers.
type Server struct {
	Store *store.Store
	Log   *slog.Logger
	Cfg   *config.Config
}

// New builds a handlers.Server.
func New(opts Options) *Server {
	log := opts.Log
	if log == nil {
		log = slog.Default()
	}
	return &Server{Store: opts.Store, Log: log, Cfg: opts.Cfg}
}

// Routes registers all application routes.
func (s *Server) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /readyz", s.readyz)
	mux.HandleFunc("GET /{$}", s.home)
	mux.HandleFunc("/", s.notFound)
	return mux
}

// render writes a full page layout with the given status code.
func (s *Server) render(w http.ResponseWriter, status int, p components.PageData) {
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
	s.render(w, http.StatusOK, components.PageData{
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
	s.render(w, http.StatusNotFound, components.PageData{
		Title: "Page not found",
		Body:  body,
	})
}

func (s *Server) renderError(w http.ResponseWriter, err error) {
	s.Log.Error("render error", "error", err)
	http.Error(w, "internal server error", http.StatusInternalServerError)
}
