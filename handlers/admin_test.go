package handlers

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tayyebi/gig/store"
)

func TestAdminRouteRequiresAdminRole(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Plain+Buyer&email=buyer@example.com&password=password123&password_confirm=password123&_csrf="+csrf)

	// A signed-in user without the admin role is forbidden.
	rec := a.req(t, http.MethodGet, "/admin", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /admin as buyer = %d, want 403", rec.Code)
	}

	// Anonymous users are redirected to log in rather than seeing a 404.
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	rec = a.req(t, http.MethodGet, "/admin", "")
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/login") {
		t.Fatalf("GET /admin anonymous = %d, location %q; want 303 to /login", rec.Code, rec.Header().Get("Location"))
	}
}

func TestAdminRouteServesGrantedAdmin(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Ops+Admin&email=admin@example.com&password=password123&password_confirm=password123&_csrf="+csrf)

	u, err := a.srv.Store.GetUserByEmail(context.Background(), "admin@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if err := a.srv.Store.AddRole(context.Background(), u.ID, store.RoleAdmin); err != nil {
		t.Fatalf("AddRole: %v", err)
	}

	rec := a.req(t, http.MethodGet, "/admin", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin as admin = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Admin console") {
		t.Errorf("expected admin console body")
	}

	// The nav link is now live for an admin instead of pointing at a 404.
	home := a.req(t, http.MethodGet, "/", "").Body.String()
	if !strings.Contains(home, `href="/admin"`) {
		t.Errorf("expected admin nav link for admin user")
	}
}
