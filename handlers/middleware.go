package handlers

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// withSession loads the session and, when authenticated, the user into the
// request context. Invalid cookies are cleared rather than failing the request.
func (s *Server) withSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(s.Cfg.SessionCookieName)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		sess, err := s.Store.GetSessionByToken(r.Context(), hashToken(cookie.Value))
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.Log.Error("load session", "error", err)
			}
			http.SetCookie(w, services.ClearSessionCookie(s.Cfg))
			next.ServeHTTP(w, r)
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeySession, sess)
		if sess.UserID != nil {
			u, err := s.Store.GetUserByID(r.Context(), *sess.UserID)
			if err == nil {
				ctx = context.WithValue(ctx, ctxKeyUser, u)
			} else {
				s.Log.Error("load user for session", "session_id", sess.ID, "error", err)
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withCSRF rejects every state-changing request that lacks a valid CSRF token
// bound to the session. GET/HEAD/OPTIONS/TRACE are exempt.
//
// r.Body is capped here, before anything (including this middleware's own
// r.FormValue call below) ever touches the request body. FormValue is the
// first thing in the whole chain to parse a multipart body, and
// ParseMultipartForm/FormFile short-circuit once r.MultipartForm is non-nil
// ("if r.MultipartForm != nil { return nil }"), so a handler's own later
// http.MaxBytesReader + ParseMultipartForm call never actually runs if this
// one hasn't already limited the read: without it, an upload handler's size
// cap would be silently inert and a client could stream an unbounded body
// into memory/temp files before any handler code sees the request.
func (s *Server) withCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace:
			next.ServeHTTP(w, r)
			return
		}
		sess := s.sessionFrom(r)
		if sess == nil {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.UploadMaxBytes+uploadFormOverhead)
		if !services.VerifyCSRF(r.FormValue("_csrf"), sess.CSRF) {
			s.Log.Warn("csrf check failed", "path", r.URL.Path, "ip", clientIP(r))
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// requireAuth redirects anonymous users to the login page, preserving the
// requested path as the post-login destination.
func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.userFrom(r) == nil {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// requireRole gates a handler behind a specific role, 403 for everyone else.
func (s *Server) requireRole(role string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := s.userFrom(r)
		if u == nil {
			http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.Path), http.StatusSeeOther)
			return
		}
		ok, err := s.Store.UserHasRole(r.Context(), u.ID, role)
		if err != nil || !ok {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next(w, r)
	}
}

// authRateLimited reports whether the client has exceeded the auth rate limit.
func (s *Server) authRateLimited(r *http.Request) bool {
	return !s.limiter.Allow("auth:" + clientIP(r))
}
