package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/tayyebi/gig/migrations"
	"github.com/tayyebi/gig/services"
)

func openTestIdentityStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	if _, err := st.db.ExecContext(ctx, `TRUNCATE TABLE jobs, audit_log, auth_tokens, sessions, user_roles, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate identity tables: %v", err)
	}
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func TestUserCRUD(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "ada@example.com", "hash-a", "Ada Lovelace", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero user ID")
	}

	// Emails are unique on the normalized value.
	if _, err := st.CreateUser(ctx, "ada@example.com", "hash-b", "Ada", "en"); !errors.Is(err, ErrEmailTaken) {
		t.Fatalf("duplicate email: got %v, want ErrEmailTaken", err)
	}

	u, err := st.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u.Email != "ada@example.com" || u.Name != "Ada Lovelace" {
		t.Errorf("unexpected user: %+v", u)
	}
	if u.EmailVerifiedAt != nil {
		t.Error("email should start unverified")
	}

	byEmail, err := st.GetUserByEmail(ctx, "ADA@example.com")
	if err != nil {
		t.Fatalf("GetUserByEmail: %v", err)
	}
	if byEmail.ID != id {
		t.Errorf("GetUserByEmail.ID = %d, want %d", byEmail.ID, id)
	}

	if _, err := st.GetUserByEmail(ctx, "nobody@example.com"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing user: got %v, want ErrNotFound", err)
	}

	if err := st.SetEmailVerified(ctx, id); err != nil {
		t.Fatalf("SetEmailVerified: %v", err)
	}
	u, err = st.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("GetUserByID after verify: %v", err)
	}
	if u.EmailVerifiedAt == nil {
		t.Error("email should be verified")
	}

	if err := st.UpdateName(ctx, id, "Ada King"); err != nil {
		t.Fatalf("UpdateName: %v", err)
	}
	if err := st.UpdatePassword(ctx, id, "hash-c"); err != nil {
		t.Fatalf("UpdatePassword: %v", err)
	}
	if err := st.SetLastLogin(ctx, id); err != nil {
		t.Fatalf("SetLastLogin: %v", err)
	}
	u, err = st.GetUserByID(ctx, id)
	if err != nil {
		t.Fatalf("GetUserByID after updates: %v", err)
	}
	if u.Name != "Ada King" || u.PasswordHash != "hash-c" || u.LastLoginAt == nil {
		t.Errorf("updates did not persist: %+v", u)
	}
}

func TestUserRoles(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "admin@example.com", "hash", "Admin", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	// New users always hold the buyer role.
	has, err := st.UserHasRole(ctx, id, RoleBuyer)
	if err != nil {
		t.Fatalf("UserHasRole: %v", err)
	}
	if !has {
		t.Error("expected buyer role on creation")
	}

	if err := st.AddRole(ctx, id, RoleAdmin); err != nil {
		t.Fatalf("AddRole: %v", err)
	}
	if err := st.AddRole(ctx, id, RoleAdmin); err != nil {
		t.Fatalf("AddRole duplicate: %v", err)
	}

	roles, err := st.UserRoles(ctx, id)
	if err != nil {
		t.Fatalf("UserRoles: %v", err)
	}
	if len(roles) != 2 {
		t.Errorf("roles = %v, want 2", roles)
	}

	if err := st.RemoveRole(ctx, id, RoleAdmin); err != nil {
		t.Fatalf("RemoveRole: %v", err)
	}
	has, err = st.UserHasRole(ctx, id, RoleAdmin)
	if err != nil {
		t.Fatalf("UserHasRole: %v", err)
	}
	if has {
		t.Error("admin role should be removed")
	}
}

func TestSessionsLifecycle(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "sess@example.com", "hash", "Sess", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	tokenHash := "1111111111111111111111111111111111111111111111111111111111111111"
	sessID, err := st.CreateSession(ctx, &id, tokenHash, "csrf-token-1234567890abcdef", time.Hour, "agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	anonHash := "2222222222222222222222222222222222222222222222222222222222222222"
	anonID, err := st.CreateSession(ctx, nil, anonHash, "csrf-token-anon-1234567890", time.Hour, "agent", "127.0.0.1")
	if err != nil {
		t.Fatalf("CreateSession anonymous: %v", err)
	}

	sess, err := st.GetSessionByToken(ctx, tokenHash)
	if err != nil {
		t.Fatalf("GetSessionByToken: %v", err)
	}
	if sess.ID != sessID || sess.UserID == nil || *sess.UserID != id {
		t.Errorf("unexpected session: %+v", sess)
	}
	if sess.CSRF != "csrf-token-1234567890abcdef" {
		t.Errorf("csrf = %q", sess.CSRF)
	}

	if _, err := st.GetSessionByToken(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing session: got %v, want ErrNotFound", err)
	}

	// Flash round trip.
	if err := st.SetFlash(ctx, sessID, &Flash{Kind: "success", Text: "Welcome!"}); err != nil {
		t.Fatalf("SetFlash: %v", err)
	}
	f, err := st.ConsumeFlash(ctx, sessID)
	if err != nil {
		t.Fatalf("ConsumeFlash: %v", err)
	}
	if f == nil || f.Text != "Welcome!" {
		t.Errorf("flash = %+v", f)
	}
	f, err = st.ConsumeFlash(ctx, sessID)
	if err != nil {
		t.Fatalf("ConsumeFlash: %v", err)
	}
	if f != nil {
		t.Error("flash should be consumed after one read")
	}

	if err := st.TouchSession(ctx, sessID); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	// Revoking the user's session leaves the anonymous one intact.
	if err := st.RevokeSession(ctx, sessID); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	if _, err := st.GetSessionByToken(ctx, tokenHash); !errors.Is(err, ErrNotFound) {
		t.Errorf("revoked session: got %v, want ErrNotFound", err)
	}
	if _, err := st.GetSessionByToken(ctx, anonHash); err != nil {
		t.Errorf("anonymous session must survive: %v", err)
	}

	if err := st.RevokeSession(ctx, anonID); err != nil {
		t.Fatalf("RevokeSession anonymous: %v", err)
	}
}

func TestAttachSessionUser(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "login@example.com", "hash", "Login", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	sessID, err := st.CreateSession(ctx, nil, "3333333333333333333333333333333333333333333333333333333333333333", "csrf-token-0123456789abcdef", time.Hour, "agent", "1.2.3.4")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := st.AttachSessionUser(ctx, sessID, id); err != nil {
		t.Fatalf("AttachSessionUser: %v", err)
	}
	sess, err := st.GetSessionByID(ctx, sessID)
	if err != nil {
		t.Fatalf("GetSessionByID: %v", err)
	}
	if sess.UserID == nil || *sess.UserID != id {
		t.Errorf("session user = %v, want %d", sess.UserID, id)
	}
}

func TestRevokeUserSessions(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "revoke@example.com", "hash", "Revoke", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	var keep string
	for i := 0; i < 3; i++ {
		h := strings.Repeat(string(rune('a'+i)), 64)
		_, err := st.CreateSession(ctx, &id, h, "csrf-token-0123456789abcdef", time.Hour, "agent", "ip")
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		if i == 1 {
			keep = h
		}
	}
	if err := st.RevokeUserSessions(ctx, id, 0); err != nil {
		t.Fatalf("RevokeUserSessions: %v", err)
	}
	// exceptID=0 revokes everything.
	if _, err := st.GetSessionByToken(ctx, keep); !errors.Is(err, ErrNotFound) {
		t.Errorf("expected all sessions revoked, got %v", err)
	}
}

func TestAuthTokensLifecycle(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "token@example.com", "hash", "Token", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	raw, hash, err := services.GenerateSessionToken()
	if err != nil {
		t.Fatalf("GenerateSessionToken: %v", err)
	}

	tokenID, err := st.CreateAuthToken(ctx, id, TokenEmailVerification, hash, time.Hour)
	if err != nil {
		t.Fatalf("CreateAuthToken: %v", err)
	}

	tok, err := st.GetAuthToken(ctx, TokenEmailVerification, hash)
	if err != nil {
		t.Fatalf("GetAuthToken: %v", err)
	}
	if tok.ID != tokenID || tok.UserID != id {
		t.Errorf("unexpected token: %+v", tok)
	}

	// Wrong kind does not match.
	if _, err := st.GetAuthToken(ctx, TokenPasswordReset, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong kind: got %v, want ErrNotFound", err)
	}
	// Raw token is not the stored hash.
	if _, err := st.GetAuthToken(ctx, TokenEmailVerification, raw); !errors.Is(err, ErrNotFound) {
		t.Errorf("raw token: got %v, want ErrNotFound", err)
	}

	if err := st.UseAuthToken(ctx, tokenID); err != nil {
		t.Fatalf("UseAuthToken: %v", err)
	}
	if _, err := st.GetAuthToken(ctx, TokenEmailVerification, hash); !errors.Is(err, ErrNotFound) {
		t.Errorf("used token: got %v, want ErrNotFound", err)
	}
}

func TestAuditLog(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "audit@example.com", "hash", "Audit", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if err := st.LogAction(ctx, &id, "127.0.0.1", "user.login", "user", "7", map[string]any{"ok": true}); err != nil {
		t.Fatalf("LogAction: %v", err)
	}
	// Anonymous system action.
	if err := st.LogAction(ctx, nil, "", "cron.sweep", "", "", nil); err != nil {
		t.Fatalf("LogAction anonymous: %v", err)
	}

	entries, err := st.RecentActions(ctx, 10)
	if err != nil {
		t.Fatalf("RecentActions: %v", err)
	}
	if len(entries) < 2 {
		t.Fatalf("entries = %d, want >= 2", len(entries))
	}
	first := entries[0]
	if first.Action != "cron.sweep" {
		t.Errorf("order by id desc expected cron.sweep first, got %q", first.Action)
	}
	if first.CreatedAt.IsZero() {
		t.Error("CreatedAt should be set")
	}
	found := false
	for _, e := range entries {
		if e.Action == "user.login" {
			found = true
			if e.ActorUserID == nil || *e.ActorUserID != id {
				t.Errorf("actor = %v", e.ActorUserID)
			}
			if e.EntityID != "7" {
				t.Errorf("entity_id = %q", e.EntityID)
			}
		}
	}
	if !found {
		t.Error("user.login entry not found")
	}
}

func TestAuditLogRejectsUnknownActor(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	badID := int64(999999)
	err := st.LogAction(ctx, &badID, "", "user.login", "", "", nil)
	if err == nil {
		t.Fatal("insert with unknown actor must fail the FK constraint")
	}
	var pgErr interface{ Error() string }
	if !errors.As(err, &pgErr) {
		t.Errorf("expected a SQL error, got %v", err)
	}
}

func TestDeleteExpiredSessions(t *testing.T) {
	st := openTestIdentityStore(t)
	ctx := context.Background()

	id, err := st.CreateUser(ctx, "expire@example.com", "hash", "Expire", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	if _, err := st.CreateSession(ctx, &id, strings.Repeat("c", 64), "csrf-token-0123456789abcdef", time.Hour, "agent", "ip"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.CreateSession(ctx, &id, strings.Repeat("d", 64), "csrf-token-0123456789abcdef", time.Hour, "agent", "ip"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Age the short-lived session past its expiry.
	if _, err := st.db.ExecContext(ctx, `UPDATE sessions SET expires_at = now() - interval '1 minute' WHERE token_hash = $1`, strings.Repeat("d", 64)); err != nil {
		t.Fatalf("age session: %v", err)
	}

	n, err := st.DeleteExpiredSessions(ctx)
	if err != nil {
		t.Fatalf("DeleteExpiredSessions: %v", err)
	}
	if n < 1 {
		t.Errorf("deleted %d sessions, want >= 1", n)
	}
	if _, err := st.GetSessionByToken(ctx, strings.Repeat("c", 64)); err != nil {
		t.Errorf("live session should survive: %v", err)
	}
}
