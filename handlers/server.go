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
	"github.com/tayyebi/gig/providers"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// Options configures a handlers.Server.
type Options struct {
	Store    *store.Store
	Log      *slog.Logger
	Cfg      *config.Config
	Mailer   services.Mailer
	Provider providers.Provider // nil disables checkout when Cfg.PaymentsEnabled is false
}

// Server holds shared dependencies for all handlers.
type Server struct {
	Store          *store.Store
	Log            *slog.Logger
	Cfg            *config.Config
	Mailer         services.Mailer
	Storage        *services.Storage
	PrivateStorage *services.Storage
	Provider       providers.Provider
	limiter        *services.RateLimiter
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
		Store:          opts.Store,
		Log:            log,
		Cfg:            opts.Cfg,
		Mailer:         opts.Mailer,
		Storage:        services.NewStorage(opts.Cfg.StorageDir, opts.Cfg.UploadMaxBytes),
		PrivateStorage: services.NewStorage(opts.Cfg.PrivateStorageDir, opts.Cfg.UploadMaxBytes),
		Provider:       opts.Provider,
		limiter:        services.NewRateLimiter(opts.Cfg.AuthRateLimit, opts.Cfg.AuthRateWindow),
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
	mux.HandleFunc("GET /search", s.search)
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

	// Admin (role-gated).
	mux.HandleFunc("GET /admin", s.requireRole(store.RoleAdmin, s.adminHome))
	mux.HandleFunc("GET /admin/payments", s.requireRole(store.RoleAdmin, s.adminPayments))
	mux.HandleFunc("GET /admin/orders/{id}/payments", s.requireRole(store.RoleAdmin, s.adminOrderPayments))
	mux.HandleFunc("POST /admin/orders/{id}/refund", s.requireRole(store.RoleAdmin, s.adminOrderRefund))

	// Public catalog.
	mux.HandleFunc("GET /browse", s.browseCategories)
	mux.HandleFunc("GET /browse/{slug}", s.browseCategory)
	mux.HandleFunc("GET /gigs/{slug}", s.gigDetail)
	mux.HandleFunc("POST /gigs/{slug}/favorite", s.requireAuth(s.favoriteToggle))
	mux.HandleFunc("GET /sellers/{id}", s.sellerProfile)

	// Buyer dashboard (authenticated).
	mux.HandleFunc("GET /dashboard", s.requireAuth(s.buyerDashboard))

	// Checkout (authenticated buyer).
	mux.HandleFunc("POST /gigs/{slug}/checkout", s.requireAuth(s.checkoutStart))
	mux.HandleFunc("GET /checkout/{id}/requirements", s.requireAuth(s.checkoutRequirementsForm))
	mux.HandleFunc("POST /checkout/{id}/requirements", s.requireAuth(s.checkoutRequirementsSubmit))
	mux.HandleFunc("GET /checkout/{id}/review", s.requireAuth(s.checkoutReview))
	mux.HandleFunc("POST /checkout/{id}/confirm", s.requireAuth(s.checkoutConfirm))

	// Payment return paths. The provider redirects the buyer's browser here
	// after hosted checkout; the order's actual status is never trusted from
	// this request, only from the verified webhook (PLAN.md section 9).
	mux.HandleFunc("GET /orders/{id}/pay/return", s.requireAuth(s.paymentReturn))
	mux.HandleFunc("GET /orders/{id}/pay/cancel", s.requireAuth(s.paymentCancelReturn))

	// Orders: workspace shared by buyer, seller, and admin, gated per action
	// inside each handler rather than at the route (a single order page must
	// serve all three roles).
	mux.HandleFunc("GET /orders", s.requireAuth(s.ordersListBuyer))
	mux.HandleFunc("GET /orders/{id}", s.requireAuth(s.orderDetail))
	mux.HandleFunc("GET /orders/{id}/attachments/{attachmentID}", s.requireAuth(s.orderAttachmentServe))
	mux.HandleFunc("POST /orders/{id}/messages", s.requireAuth(s.orderMessageCreate))
	mux.HandleFunc("POST /orders/{id}/deliver", s.requireAuth(s.orderDeliver))
	mux.HandleFunc("POST /orders/{id}/revise", s.requireAuth(s.orderRevise))
	mux.HandleFunc("POST /orders/{id}/accept", s.requireAuth(s.orderAccept))
	mux.HandleFunc("POST /orders/{id}/cancel/request", s.requireAuth(s.orderCancelRequest))
	mux.HandleFunc("POST /orders/{id}/cancel/resolve", s.requireAuth(s.orderCancelResolve))
	mux.HandleFunc("POST /orders/{id}/dispute", s.requireAuth(s.orderDisputeOpen))
	mux.HandleFunc("POST /orders/{id}/dispute/resolve", s.requireAuth(s.orderDisputeResolve))
	mux.HandleFunc("POST /orders/{id}/review", s.requireAuth(s.orderReviewCreate))

	// Notifications.
	mux.HandleFunc("GET /notifications", s.requireAuth(s.notificationsList))

	// Selling: onboarding, profile, portfolio, and gig management (seller role).
	mux.HandleFunc("POST /sell/start", s.requireAuth(s.sellStart))
	mux.HandleFunc("GET /sell", s.requireSeller(s.sellDashboard))
	mux.HandleFunc("GET /sell/orders", s.requireSeller(s.ordersListSeller))
	mux.HandleFunc("POST /sell/onboarding/submit", s.requireSeller(s.sellOnboardingSubmit))
	mux.HandleFunc("POST /sell/onboarding/stripe/start", s.requireSeller(s.sellOnboardingStripeStart))
	mux.HandleFunc("GET /sell/onboarding/stripe/refresh", s.requireSeller(s.sellOnboardingStripeRefresh))
	mux.HandleFunc("GET /sell/onboarding/stripe/return", s.requireSeller(s.sellOnboardingStripeReturn))
	mux.HandleFunc("GET /sell/profile", s.requireSeller(s.sellProfileForm))
	mux.HandleFunc("POST /sell/profile", s.requireSeller(s.sellProfileUpdate))
	mux.HandleFunc("GET /sell/portfolio", s.requireSeller(s.sellPortfolioForm))
	mux.HandleFunc("POST /sell/portfolio", s.requireSeller(s.sellPortfolioCreate))
	mux.HandleFunc("GET /sell/gigs/new", s.requireSeller(s.sellGigNew))
	mux.HandleFunc("POST /sell/gigs", s.requireSeller(s.sellGigCreate))
	mux.HandleFunc("GET /sell/gigs/{id}/edit", s.requireSeller(s.sellGigEdit))
	mux.HandleFunc("POST /sell/gigs/{id}", s.requireSeller(s.sellGigUpdate))
	mux.HandleFunc("POST /sell/gigs/{id}/status", s.requireSeller(s.sellGigStatus))
	mux.HandleFunc("POST /sell/gigs/{id}/media", s.requireSeller(s.sellGigMedia))

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
	if n, err := s.Store.CountUnreadNotifications(r.Context(), u.ID); err == nil {
		vu.UnreadCount = n
	}
	roles, err := s.Store.UserRoles(r.Context(), u.ID)
	if err != nil {
		return vu
	}
	for _, role := range roles {
		switch role {
		case store.RoleAdmin:
			vu.IsAdmin = true
		case store.RoleSeller:
			vu.IsSeller = true
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
	cats, err := s.Store.ListCategories(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	featured, err := s.Store.FeaturedGigs(r.Context(), 6)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.Home(components.HomeData{
		Categories: toCategoryCards(cats),
		Featured:   toGigCards(featured),
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
