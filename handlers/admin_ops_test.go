package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/tayyebi/gig/store"
)

// adminTestUser registers a user, grants it the admin role, and logs it in,
// returning the authServer and the created user's ID.
func adminTestUser(t *testing.T, a *authServer, email string) int64 {
	t.Helper()
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Ops+Admin&email="+email+"&password=password123&password_confirm=password123&_csrf="+csrf)
	u, err := a.srv.Store.GetUserByEmail(context.Background(), email)
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if err := a.srv.Store.AddRole(context.Background(), u.ID, store.RoleAdmin); err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	return u.ID
}

func TestAdminUserSuspendAndRestore(t *testing.T) {
	a := newAuthServer(t)
	adminTestUser(t, a, "admin1@example.com")

	// A second, plain buyer account to suspend.
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Buyer+Two&email=buyer2@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	target, err := a.srv.Store.GetUserByEmail(context.Background(), "buyer2@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}

	// Log back in as the admin (registering the second user replaced the session).
	logoutCSRF := a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+logoutCSRF)
	loginCSRF := a.csrf(t, "/login")
	a.req(t, http.MethodPost, "/login", "email=admin1@example.com&password=password123&_csrf="+loginCSRF)

	rec := a.req(t, http.MethodGet, "/admin/users", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/users = %d", rec.Code)
	}

	csrfSuspend := a.csrf(t, "/admin/users")
	suspendPath := "/admin/users/" + strconv.FormatInt(target.ID, 10) + "/status"
	rec = a.req(t, http.MethodPost, suspendPath, "status=disabled&reason=fraud+review&_csrf="+csrfSuspend)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("suspend = %d, want 303", rec.Code)
	}
	got, err := a.srv.Store.GetUserByEmail(context.Background(), "buyer2@example.com")
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Status != store.UserDisabled {
		t.Fatalf("status = %s, want disabled", got.Status)
	}

	// Suspending without a reason is rejected.
	csrfBad := a.csrf(t, "/admin/users")
	rec = a.req(t, http.MethodPost, suspendPath, "status=disabled&reason=&_csrf="+csrfBad)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("suspend without reason = %d", rec.Code)
	}

	// Restore.
	csrfRestore := a.csrf(t, "/admin/users")
	rec = a.req(t, http.MethodPost, suspendPath, "status=active&reason=cleared&_csrf="+csrfRestore)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("restore = %d, want 303", rec.Code)
	}
	got, err = a.srv.Store.GetUserByEmail(context.Background(), "buyer2@example.com")
	if err != nil {
		t.Fatalf("reload user: %v", err)
	}
	if got.Status != store.UserActive {
		t.Fatalf("status = %s, want active", got.Status)
	}
}

func TestAdminSettingsUpdate(t *testing.T) {
	a := newAuthServer(t)
	adminTestUser(t, a, "admin2@example.com")

	rec := a.req(t, http.MethodGet, "/admin/settings", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "payouts_paused") {
		t.Fatalf("GET /admin/settings = %d, body missing seeded setting", rec.Code)
	}

	csrf := a.csrf(t, "/admin/settings")
	rec = a.req(t, http.MethodPost, "/admin/settings", "key=platform_fee_bps&value=1500&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("update setting = %d, want 303", rec.Code)
	}
	v, err := a.srv.Store.GetSetting(context.Background(), "platform_fee_bps")
	if err != nil || v != "1500" {
		t.Fatalf("GetSetting platform_fee_bps = %q, %v", v, err)
	}
}

func TestAdminLedgerAdjustment(t *testing.T) {
	a := newAuthServer(t)
	adminTestUser(t, a, "admin3@example.com")

	rec := a.req(t, http.MethodGet, "/admin/ledger/adjust", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/ledger/adjust = %d", rec.Code)
	}

	csrf := a.csrf(t, "/admin/ledger/adjust")
	form := "from_kind=platform_revenue&to_kind=reserves&amount=500&currency=usd&reason=test+adjustment&_csrf=" + csrf
	rec = a.req(t, http.MethodPost, "/admin/ledger/adjust", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("post adjustment = %d, want 303, body=%s", rec.Code, rec.Body.String())
	}

	balances, err := a.srv.Store.PlatformBalances(context.Background())
	if err != nil {
		t.Fatalf("PlatformBalances: %v", err)
	}
	var reserves int64
	for _, b := range balances {
		if b.Kind == "reserves" {
			reserves = b.BalanceMinor
		}
	}
	if reserves != 500 {
		t.Fatalf("reserves balance = %d, want 500", reserves)
	}

	// Zero amount is rejected before it ever reaches the ledger package.
	csrfBad := a.csrf(t, "/admin/ledger/adjust")
	rec = a.req(t, http.MethodPost, "/admin/ledger/adjust",
		"from_kind=platform_revenue&to_kind=reserves&amount=0&currency=usd&reason=bad&_csrf="+csrfBad)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("zero-amount adjustment = %d", rec.Code)
	}
}

func TestAdminAuditLogAndExport(t *testing.T) {
	a := newAuthServer(t)
	adminTestUser(t, a, "admin4@example.com")

	// The registration and role grant above already produced audit entries.
	rec := a.req(t, http.MethodGet, "/admin/audit", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/audit = %d", rec.Code)
	}
	rec = a.req(t, http.MethodGet, "/admin/audit/export.csv", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /admin/audit/export.csv = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Fatalf("Content-Type = %q, want text/csv", ct)
	}
}
