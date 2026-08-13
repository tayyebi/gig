package handlers

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

type ctxKey int

const (
	ctxKeySession ctxKey = iota
	ctxKeyUser
)

// hashToken hashes a raw session or one-time token for storage and lookup.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return fmt.Sprintf("%x", sum[:])
}

func (s *Server) sessionFrom(r *http.Request) *store.Session {
	if sess, ok := r.Context().Value(ctxKeySession).(*store.Session); ok {
		return sess
	}
	return nil
}

func (s *Server) userFrom(r *http.Request) *store.User {
	if u, ok := r.Context().Value(ctxKeyUser).(*store.User); ok {
		return u
	}
	return nil
}

// ensureSession returns the request's session, creating an anonymous one if
// the cookie is missing or invalid. The new session is stored in the request
// context so downstream renderers can read its flash.
func (s *Server) ensureSession(w http.ResponseWriter, r *http.Request) (*store.Session, error) {
	if sess := s.sessionFrom(r); sess != nil {
		return sess, nil
	}
	raw, hash, err := services.GenerateSessionToken()
	if err != nil {
		return nil, err
	}
	csrf, err := services.GenerateCSRF()
	if err != nil {
		return nil, err
	}
	id, err := s.Store.CreateSession(r.Context(), nil, hash, csrf, s.Cfg.SessionTTL, r.UserAgent(), clientIP(r))
	if err != nil {
		return nil, err
	}
	http.SetCookie(w, services.SessionCookie(s.Cfg, raw, s.Cfg.SessionTTL))
	sess := &store.Session{
		ID:        id,
		TokenHash: hash,
		CSRF:      csrf,
		UserAgent: r.UserAgent(),
		IP:        clientIP(r),
		ExpiresAt: time.Now().Add(s.Cfg.SessionTTL),
	}
	ctx := context.WithValue(r.Context(), ctxKeySession, sess)
	*r = *r.WithContext(ctx)
	return sess, nil
}

// clientIP extracts the client address from the request.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
