package handlers

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// TestHomeRendersLayout, TestHomeBottomNavOnlyForSignedInUsers, and search
// coverage moved here from server_test.go: home() and search() now query the
// catalog, so they need the database-backed newAuthServer harness.

func TestHomeRendersLayout(t *testing.T) {
	a := newAuthServer(t)
	rec := a.req(t, http.MethodGet, "/", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	body := rec.Body.String()
	for _, want := range []string{`<html lang="en" dir="ltr">`, `action="/search"`, `app.css`, "Design"} {
		if !strings.Contains(body, want) {
			t.Errorf("home page missing %q", want)
		}
	}
	if strings.Contains(body, "<script") {
		t.Error("home page must not contain script elements")
	}
}

func TestHomeBottomNavOnlyForSignedInUsers(t *testing.T) {
	a := newAuthServer(t)
	rec := a.req(t, http.MethodGet, "/", "")
	if strings.Contains(rec.Body.String(), `class="bottomnav"`) {
		t.Error("anonymous users must not see the account bottom nav")
	}
}

func TestSearchAndBrowseRoutes(t *testing.T) {
	a := newAuthServer(t)

	rec := a.req(t, http.MethodGet, "/search?q=logo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /search = %d", rec.Code)
	}

	rec = a.req(t, http.MethodGet, "/browse", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Design") {
		t.Fatalf("GET /browse = %d", rec.Code)
	}

	rec = a.req(t, http.MethodGet, "/browse/design", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /browse/design = %d", rec.Code)
	}

	rec = a.req(t, http.MethodGet, "/browse/does-not-exist", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET /browse/does-not-exist = %d, want 404", rec.Code)
	}
}

// TestSellerGigLifecycleEndToEnd drives the full seller journey through
// HTTP, the same path a browser would take: register, become a seller, set
// up a profile, create a gig, and publish it, then confirms the gig is
// invisible while a draft and visible everywhere it should be once active.
func TestSellerGigLifecycleEndToEnd(t *testing.T) {
	a := newAuthServer(t)

	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Selling+Sam&email=sam@example.com&password=password123&password_confirm=password123&_csrf="+csrf)

	// Seller-only routes are forbidden before opting in.
	rec := a.req(t, http.MethodGet, "/sell", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("GET /sell before selling = %d, want 403", rec.Code)
	}

	csrf = a.csrf(t, "/")
	rec = a.req(t, http.MethodPost, "/sell/start", "_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /sell/start = %d", rec.Code)
	}

	csrf = a.csrf(t, "/sell/profile")
	rec = a.req(t, http.MethodPost, "/sell/profile",
		"display_name=Sam+the+Designer&bio=I+design+logos.&location=Remote&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /sell/profile = %d", rec.Code)
	}

	csrf = a.csrf(t, "/sell/gigs/new")
	form := "title=Modern+Logo+Design&description=I+will+design+a+modern+logo+for+your+brand." +
		"&category_id=&tags=logo%2C+branding" +
		"&pkg_name_0=Basic&pkg_description_0=One+concept&pkg_price_0=25.00&pkg_delivery_0=3&pkg_revisions_0=1" +
		"&_csrf=" + csrf
	rec = a.req(t, http.MethodPost, "/sell/gigs", form)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST /sell/gigs = %d, body=%s", rec.Code, rec.Body.String())
	}
	editLocation := rec.Header().Get("Location")
	if !strings.HasPrefix(editLocation, "/sell/gigs/") || !strings.HasSuffix(editLocation, "/edit") {
		t.Fatalf("unexpected redirect location %q", editLocation)
	}
	gigIDStr := strings.TrimSuffix(strings.TrimPrefix(editLocation, "/sell/gigs/"), "/edit")

	// Drafts are not visible in search yet.
	rec = a.req(t, http.MethodGet, "/search?q=logo", "")
	if strings.Contains(rec.Body.String(), "Modern Logo Design") {
		t.Error("draft gig should not appear in search")
	}

	csrf = a.csrf(t, editLocation)
	rec = a.req(t, http.MethodPost, "/sell/gigs/"+gigIDStr+"/status", "status=active&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("publish = %d", rec.Code)
	}

	rec = a.req(t, http.MethodGet, "/search?q=logo", "")
	body := rec.Body.String()
	if !strings.Contains(body, "Modern Logo Design") {
		t.Fatalf("published gig should appear in search, body=%s", body)
	}

	idx := strings.Index(body, `href="/gigs/`)
	if idx < 0 {
		t.Fatal("no gig link in search results")
	}
	rest := body[idx+len(`href="/gigs/`):]
	slug := rest[:strings.Index(rest, `"`)]

	rec = a.req(t, http.MethodGet, "/gigs/"+slug, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "$25.00") {
		t.Fatalf("gig detail = %d, body=%s", rec.Code, rec.Body.String())
	}

	// The gig also shows up on the seller's public profile.
	u, err := a.srv.Store.GetUserByEmail(context.Background(), "sam@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	rec = a.req(t, http.MethodGet, "/sellers/"+strconv.FormatInt(u.ID, 10), "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sam the Designer") ||
		!strings.Contains(rec.Body.String(), "Modern Logo Design") {
		t.Fatalf("seller profile page = %d, body=%s", rec.Code, rec.Body.String())
	}

	// Anonymous visitors cannot favorite; they are sent to log in. Swap in a
	// fresh, cookie-less jar so this genuinely exercises an anonymous
	// session (with a validly-issued CSRF token) rather than reusing Sam's
	// still-logged-in session from earlier in this test.
	sellerJar := a.jar
	a.jar = nil
	// The gig detail page's favorite form (and its CSRF field) only
	// renders for a signed-in visitor (CanFavorite requires u != nil,
	// handlers/catalog.go); an anonymous session's CSRF token still
	// exists (ensureSession issues one to every visitor), just not on
	// this particular page, so pull it from /login, which always renders
	// a CSRF-protected form regardless of auth state.
	anonCSRF := a.csrf(t, "/login")
	rec = a.req(t, http.MethodPost, "/gigs/"+slug+"/favorite", "_csrf="+anonCSRF)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "/login") {
		t.Fatalf("anonymous favorite = %d location=%q, want redirect to /login", rec.Code, rec.Header().Get("Location"))
	}
	a.jar = sellerJar

	// A signed-in buyer can favorite the gig, and it shows up on their
	// dashboard. Log Sam out first: /register redirects an already
	// authenticated visitor away instead of rendering the form.
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Buying+Bella&email=bella@example.com&password=password123&password_confirm=password123&_csrf="+csrf)

	csrf = a.csrf(t, "/gigs/"+slug)
	rec = a.req(t, http.MethodPost, "/gigs/"+slug+"/favorite", "_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("favorite toggle = %d", rec.Code)
	}

	rec = a.req(t, http.MethodGet, "/dashboard", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Modern Logo Design") {
		t.Fatalf("buyer dashboard = %d, body=%s", rec.Code, rec.Body.String())
	}
}
