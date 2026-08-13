package handlers

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tayyebi/gig/config"
	"github.com/tayyebi/gig/migrations"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"

	_ "github.com/jackc/pgx/v5/stdlib"
)

// captureMailer records sent email so tests can extract one-time links.
type captureMailer struct {
	mu   sync.Mutex
	sent []services.Email
}

func (m *captureMailer) Send(_ context.Context, email services.Email) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, email)
	return nil
}

func (m *captureMailer) link(t *testing.T, subjectContains, tokenName string) string {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.sent) - 1; i >= 0; i-- {
		e := m.sent[i]
		if !strings.Contains(e.Subject, subjectContains) {
			continue
		}
		for _, line := range strings.Fields(e.Body) {
			if strings.Contains(line, "token=") && strings.HasPrefix(line, "http") {
				return line
			}
		}
	}
	t.Fatalf("no email with subject %q containing a %s link", subjectContains, tokenName)
	return ""
}

type authServer struct {
	srv     *Server
	handler http.Handler
	mail    *captureMailer
	jar     []*http.Cookie
}

func newAuthServer(t *testing.T) *authServer {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping auth integration test")
	}
	cfg := &config.Config{
		BaseURL:           "http://localhost:8080",
		SessionCookieName: "gig_session",
		SessionTTL:        time.Hour,
		AuthRateLimit:     1000,
		AuthRateWindow:    time.Hour,
		AuthTokenTTL:      time.Hour,
		MaxLoginAttempts:  3,
		LoginLockout:      time.Minute,
		TOTPSkew:          1,
		DatabaseURL:       dsn,
		DBMaxOpenConns:    4,
		DBMaxIdleConns:    2,
		DBConnMaxLifetime: time.Minute,
	}
	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	raw, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open raw db: %v", err)
	}
	if _, err := raw.ExecContext(ctx, `TRUNCATE TABLE jobs, audit_log, auth_tokens, sessions, user_roles, users RESTART IDENTITY`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	raw.Close()
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	mail := &captureMailer{}
	srv := New(Options{
		Store:  st,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
		Cfg:    cfg,
		Mailer: mail,
	})
	return &authServer{srv: srv, handler: srv.Chain(srv.Routes()), mail: mail}
}

func (a *authServer) req(t *testing.T, method, path, form string) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if form != "" {
		body = strings.NewReader(form)
	}
	req := httptest.NewRequest(method, path, body)
	if form != "" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for _, c := range a.jar {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)
	// Merge Set-Cookie responses into the jar (replace same-name cookies).
	for _, c := range rec.Result().Cookies() {
		replaced := false
		for i, existing := range a.jar {
			if existing.Name == c.Name {
				a.jar[i] = c
				replaced = true
				break
			}
		}
		if !replaced {
			a.jar = append(a.jar, c)
		}
	}
	return rec
}

func (a *authServer) csrf(t *testing.T, path string) string {
	t.Helper()
	rec := a.req(t, http.MethodGet, path, "")
	body := rec.Body.String()
	idx := strings.Index(body, `name="_csrf" value="`)
	if idx < 0 {
		t.Fatalf("no csrf field on %s (status %d)", path, rec.Code)
	}
	rest := body[idx+len(`name="_csrf" value="`):]
	return rest[:strings.Index(rest, `"`)]
}

func TestRegisterLoginLogoutFlow(t *testing.T) {
	a := newAuthServer(t)

	rec := a.req(t, http.MethodGet, "/register", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /register = %d", rec.Code)
	}

	csrf := a.csrf(t, "/register")
	rec = a.req(t, http.MethodPost, "/register",
		"name=Ada+Lovelace&email=ada@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("register = %d, want 303", rec.Code)
	}

	// Registered users hold the buyer role and the session is authenticated.
	body := a.req(t, http.MethodGet, "/", "").Body.String()
	if !strings.Contains(body, `href="/account"`) || strings.Contains(body, "Log in") {
		t.Errorf("expected authenticated header after register")
	}

	// Logout revokes the session.
	csrf = a.csrf(t, "/")
	rec = a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("logout = %d", rec.Code)
	}
	body = a.req(t, http.MethodGet, "/", "").Body.String()
	if strings.Contains(body, `href="/account"`) {
		t.Error("expected anonymous header after logout")
	}
}

func TestRegisterDuplicateEmail(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=One&email=dup@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	// Registration rotates the session, so log out to test as an anonymous user.
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)
	csrf = a.csrf(t, "/register")
	rec := a.req(t, http.MethodPost, "/register",
		"name=Two&email=dup@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("duplicate register = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "already registered") {
		t.Errorf("expected duplicate-email error message")
	}
}

func TestCSRFRequiredForStateChanges(t *testing.T) {
	a := newAuthServer(t)
	rec := a.req(t, http.MethodPost, "/login", "email=a@b.com&password=x")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST without CSRF = %d, want 403", rec.Code)
	}
}

func TestLoginFlowAndSessionRotation(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Loggy&email=loggy@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	// Log out so we can test login cleanly.
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)

	csrf = a.csrf(t, "/login")
	rec := a.req(t, http.MethodPost, "/login",
		"email=loggy@example.com&password=password123&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login = %d, want 303", rec.Code)
	}
	body := a.req(t, http.MethodGet, "/", "").Body.String()
	if !strings.Contains(body, "Welcome back, Loggy") {
		t.Errorf("expected welcome flash, got body without it")
	}
}

func TestLoginWrongPassword(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Bad&email=bad@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)

	csrf = a.csrf(t, "/login")
	rec := a.req(t, http.MethodPost, "/login",
		"email=bad@example.com&password=nope&_csrf="+csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("wrong password = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Incorrect email or password") {
		t.Errorf("expected generic error message")
	}
}

func TestLoginLockout(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Locks&email=locks@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)

	for i := 0; i < 3; i++ {
		csrf = a.csrf(t, "/login")
		a.req(t, http.MethodPost, "/login",
			"email=locks@example.com&password=nope&_csrf="+csrf)
	}
	// Even the correct password is rejected while locked.
	csrf = a.csrf(t, "/login")
	rec := a.req(t, http.MethodPost, "/login",
		"email=locks@example.com&password=password123&_csrf="+csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("login while locked = %d, want 422", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "temporarily locked") {
		t.Errorf("expected lockout message")
	}
}

func TestVerifyEmailFlow(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Verifier&email=verifier@example.com&password=password123&password_confirm=password123&_csrf="+csrf)
	// Log out so the verification link is visited from an anonymous session,
	// mirroring the real-world case of clicking a link in a new browser.
	csrf = a.csrf(t, "/")
	a.req(t, http.MethodPost, "/logout", "_csrf="+csrf)

	link := a.mail.link(t, "Verify your email", "verify-email")
	rec := a.req(t, http.MethodGet, link, "")
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("verify = %d, want 303", rec.Code)
	}
	if rec.Header().Get("Location") != "/login" {
		t.Errorf("verify redirect = %q", rec.Header().Get("Location"))
	}

	rec = a.req(t, http.MethodGet, "/login", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "email is verified") {
		t.Errorf("expected verified flash on login page (status %d)", rec.Code)
	}

	// Reusing the link must fail.
	rec = a.req(t, http.MethodGet, link, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("reused verify link = %d, want 400", rec.Code)
	}
}

func TestPasswordResetFlow(t *testing.T) {
	a := newAuthServer(t)
	csrf := a.csrf(t, "/register")
	a.req(t, http.MethodPost, "/register",
		"name=Reset&email=reset@example.com&password=password123&password_confirm=password123&_csrf="+csrf)

	csrf = a.csrf(t, "/forgot-password")
	rec := a.req(t, http.MethodPost, "/forgot-password", "email=reset@example.com&_csrf="+csrf)
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "Check your email") {
		t.Errorf("expected info page")
	}

	resetLink := a.mail.link(t, "Reset your password", "reset-password")
	csrf = a.csrf(t, resetLink)
	token := resetLink[strings.Index(resetLink, "token=")+len("token="):]
	rec = a.req(t, http.MethodPost, "/reset-password",
		"token="+token+"&password=newpassword456&password_confirm=newpassword456&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("reset = %d, want 303", rec.Code)
	}

	// Old password no longer works; new password does.
	csrf = a.csrf(t, "/login")
	rec = a.req(t, http.MethodPost, "/login",
		"email=reset@example.com&password=password123&_csrf="+csrf)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("login with old password = %d, want 422", rec.Code)
	}
	csrf = a.csrf(t, "/login")
	rec = a.req(t, http.MethodPost, "/login",
		"email=reset@example.com&password=newpassword456&_csrf="+csrf)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("login with new password = %d, want 303", rec.Code)
	}
}
