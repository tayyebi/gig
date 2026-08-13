package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

var emailPattern = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

const maxNameLen = 100

func (s *Server) registerForm(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if s.userFrom(r) != nil {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	body, err := components.RegisterPage(components.AuthForm{CSRF: sess.CSRF})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title:       "Create your account",
		Description: "Sign up to buy and sell on Gig.",
		Body:        body,
	})
}

func (s *Server) register(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.registerFormError(w, r, sess, components.AuthForm{Errors: map[string]string{"_form": "Invalid form data."}})
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	form := components.AuthForm{
		CSRF:   sess.CSRF,
		Values: map[string]string{"name": name, "email": email},
		Errors: validateRegistration(name, email, password, confirm),
	}
	if len(form.Errors) > 0 {
		s.registerFormError(w, r, sess, form)
		return
	}

	hash, err := services.HashPassword(password)
	if err != nil {
		s.renderError(w, err)
		return
	}

	id, err := s.Store.CreateUser(r.Context(), email, hash, name, "en")
	if err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			form.Errors = map[string]string{"email": "That email is already registered. Try logging in."}
			s.registerFormError(w, r, sess, form)
			return
		}
		s.renderError(w, err)
		return
	}

	// Kick off email verification with a one-time token.
	if err := s.sendVerificationEmail(r, id, name, email); err != nil {
		s.Log.Error("send verification email", "user_id", id, "error", err)
	}

	// Registration is a privilege change: rotate to a fresh authenticated
	// session so the anonymous token cannot be replayed.
	newRaw, newHash, err := services.GenerateSessionToken()
	if err != nil {
		s.renderError(w, err)
		return
	}
	newCSRF, err := services.GenerateCSRF()
	if err != nil {
		s.renderError(w, err)
		return
	}
	newID, err := s.Store.RotateSession(r.Context(), sess.ID, id, newHash, newCSRF, s.Cfg.SessionTTL, r.UserAgent(), clientIP(r))
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.SetFlash(r.Context(), newID, &store.Flash{Kind: "success", Text: "Account created. Check your email to verify your address."}); err != nil {
		s.Log.Error("set flash", "error", err)
	}
	http.SetCookie(w, services.SessionCookie(s.Cfg, newRaw, s.Cfg.SessionTTL))

	s.audit(r.Context(), &id, r, "user.registered", "user", fmt.Sprintf("%d", id), nil)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) registerFormError(w http.ResponseWriter, r *http.Request, sess *store.Session, form components.AuthForm) {
	form.CSRF = sess.CSRF
	body, err := components.RegisterPage(form)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusUnprocessableEntity, components.PageData{
		Title: "Create your account",
		Body:  body,
	})
}

func validateRegistration(name, email, password, confirm string) map[string]string {
	errs := map[string]string{}
	if name == "" {
		errs["name"] = "Please enter your name."
	} else if len(name) > maxNameLen {
		errs["name"] = "Name must be at most 100 characters."
	}
	if email == "" || !emailPattern.MatchString(email) {
		errs["email"] = "Please enter a valid email address."
	}
	if err := services.ValidatePassword(password); err != nil {
		errs["password"] = err.Error()
	}
	if password != confirm {
		errs["password_confirm"] = "Passwords do not match."
	}
	return errs
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if s.userFrom(r) != nil {
		http.Redirect(w, r, safeNext(r.URL.Query().Get("next")), http.StatusSeeOther)
		return
	}
	body, err := components.LoginPage(components.AuthForm{
		CSRF: sess.CSRF,
		Next: safeNext(r.URL.Query().Get("next")),
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: "Log in",
		Body:  body,
	})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	password := r.FormValue("password")
	code := strings.TrimSpace(r.FormValue("totp_code"))
	next := safeNext(r.FormValue("next"))

	form := components.AuthForm{
		CSRF:   sess.CSRF,
		Next:   next,
		Values: map[string]string{"email": email},
	}
	formErr := func(msg string) {
		form.Errors = map[string]string{"_form": msg}
		s.loginFormError(w, r, sess, form)
	}

	if email == "" || password == "" {
		formErr("Please enter your email and password.")
		return
	}
	if s.authRateLimited(r) {
		formErr("Too many attempts. Please try again later.")
		return
	}

	user, err := s.Store.GetUserByEmail(r.Context(), email)
	if err != nil {
		if !errors.Is(err, store.ErrNotFound) {
			s.renderError(w, err)
			return
		}
		s.audit(r.Context(), nil, r, "user.login_failed", "user", "", map[string]any{"email": store.NormalizeEmail(email)})
		formErr("Incorrect email or password.")
		return
	}

	if user.LoginBlocked(time.Now()) {
		s.audit(r.Context(), &user.ID, r, "user.login_blocked", "user", fmt.Sprintf("%d", user.ID), nil)
		formErr("This account is temporarily locked. Try again later.")
		return
	}
	if user.Status != store.UserActive {
		s.audit(r.Context(), &user.ID, r, "user.login_failed", "user", fmt.Sprintf("%d", user.ID), map[string]any{"reason": user.Status})
		formErr("This account is not active. Contact support.")
		return
	}

	ok, err := services.VerifyPassword(password, user.PasswordHash)
	if err != nil || !ok {
		_ = s.Store.RecordLoginFailure(r.Context(), user.ID, s.Cfg.MaxLoginAttempts, s.Cfg.LoginLockout)
		s.audit(r.Context(), &user.ID, r, "user.login_failed", "user", fmt.Sprintf("%d", user.ID), nil)
		formErr("Incorrect email or password.")
		return
	}

	if user.TotpEnabled {
		secret, err := s.Store.GetTOTPSecret(r.Context(), user.ID)
		if err != nil {
			s.renderError(w, err)
			return
		}
		if code == "" {
			form.Errors = map[string]string{"totp_code": "Enter your authenticator code."}
			s.loginFormError(w, r, sess, form)
			return
		}
		valid, err := services.VerifyTOTP(secret, code, s.Cfg.TOTPSkew)
		if err != nil {
			s.renderError(w, err)
			return
		}
		if !valid {
			_ = s.Store.RecordLoginFailure(r.Context(), user.ID, s.Cfg.MaxLoginAttempts, s.Cfg.LoginLockout)
			s.audit(r.Context(), &user.ID, r, "user.login_failed", "user", fmt.Sprintf("%d", user.ID), map[string]any{"reason": "totp"})
			formErr("Incorrect authenticator code.")
			return
		}
	}

	if err := s.Store.SetLastLogin(r.Context(), user.ID); err != nil {
		s.Log.Error("set last login", "user_id", user.ID, "error", err)
	}

	// Successful login is a privilege change: rotate the session token.
	newRaw, newHash, err := services.GenerateSessionToken()
	if err != nil {
		s.renderError(w, err)
		return
	}
	newCSRF, err := services.GenerateCSRF()
	if err != nil {
		s.renderError(w, err)
		return
	}
	newID, err := s.Store.RotateSession(r.Context(), sess.ID, user.ID, newHash, newCSRF, s.Cfg.SessionTTL, r.UserAgent(), clientIP(r))
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.SetFlash(r.Context(), newID, &store.Flash{Kind: "success", Text: "Welcome back, " + user.Name + "."}); err != nil {
		s.Log.Error("set flash", "error", err)
	}
	http.SetCookie(w, services.SessionCookie(s.Cfg, newRaw, s.Cfg.SessionTTL))

	s.audit(r.Context(), &user.ID, r, "user.login", "user", fmt.Sprintf("%d", user.ID), nil)
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) loginFormError(w http.ResponseWriter, r *http.Request, sess *store.Session, form components.AuthForm) {
	form.CSRF = sess.CSRF
	body, err := components.LoginPage(form)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusUnprocessableEntity, components.PageData{
		Title: "Log in",
		Body:  body,
	})
}

// logout revokes the session server-side and clears the cookie.
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFrom(r)
	if sess != nil {
		if err := s.Store.RevokeSession(r.Context(), sess.ID); err != nil {
			s.Log.Error("revoke session on logout", "session_id", sess.ID, "error", err)
		}
		s.audit(r.Context(), s.userID(r), r, "user.logout", "user", "", nil)
	}
	http.SetCookie(w, services.ClearSessionCookie(s.Cfg))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// verifyEmail consumes a one-time verification token.
func (s *Server) verifyEmail(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		s.renderAuthInfo(w, r, http.StatusBadRequest, "Invalid verification link",
			"The link is missing its token. Request a new one by logging in.")
		return
	}
	tok, err := s.Store.GetAuthToken(r.Context(), store.TokenEmailVerification, hashToken(token))
	if err != nil {
		s.renderAuthInfo(w, r, http.StatusBadRequest, "Invalid or expired link",
			"Please log in and request a new verification email.")
		return
	}
	if err := s.Store.SetEmailVerified(r.Context(), tok.UserID); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.UseAuthToken(r.Context(), tok.ID); err != nil {
		s.Log.Error("use verification token", "token_id", tok.ID, "error", err)
	}
	s.audit(r.Context(), &tok.UserID, r, "user.email_verified", "user", fmt.Sprintf("%d", tok.UserID), nil)

	sess, err := s.ensureSession(w, r)
	if err == nil {
		_ = s.Store.SetFlash(r.Context(), sess.ID, &store.Flash{Kind: "success", Text: "Your email is verified. You can now log in."})
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) forgotPasswordForm(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.ForgotPasswordPage(components.AuthForm{CSRF: sess.CSRF})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: "Reset your password",
		Body:  body,
	})
}

func (s *Server) forgotPassword(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	email := strings.TrimSpace(r.FormValue("email"))
	form := components.AuthForm{CSRF: sess.CSRF, Values: map[string]string{"email": email}}

	if email == "" || !emailPattern.MatchString(email) {
		form.Errors = map[string]string{"email": "Please enter a valid email address."}
		body, err := components.ForgotPasswordPage(form)
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Reset your password", Body: body})
		return
	}
	if s.authRateLimited(r) {
		body, err := components.ForgotPasswordPage(components.AuthForm{CSRF: sess.CSRF, Values: form.Values, Errors: map[string]string{"_form": "Too many attempts. Please try again later."}})
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusTooManyRequests, components.PageData{Title: "Reset your password", Body: body})
		return
	}

	user, err := s.Store.GetUserByEmail(r.Context(), email)
	if err == nil {
		if err := s.sendPasswordReset(r, user); err != nil {
			s.Log.Error("send password reset", "user_id", user.ID, "error", err)
		}
	} else if !errors.Is(err, store.ErrNotFound) {
		s.renderError(w, err)
		return
	}

	body, err := components.AuthInfoPage(components.AuthInfoData{
		Title: "Check your email",
		Body:  "If an account exists for that address, a password reset link is on its way. The link expires in 24 hours.",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Check your email", Body: body})
}

func (s *Server) resetPasswordForm(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	if token == "" {
		s.renderAuthInfo(w, r, http.StatusBadRequest, "Invalid reset link",
			"The link is missing its token. Request a new one.")
		return
	}
	if _, err := s.Store.GetAuthToken(r.Context(), store.TokenPasswordReset, hashToken(token)); err != nil {
		s.renderAuthInfo(w, r, http.StatusBadRequest, "Invalid or expired link",
			"Request a new password reset link.")
		return
	}
	body, err := components.ResetPasswordPage(components.AuthForm{
		CSRF:   sess.CSRF,
		Values: map[string]string{"token": token},
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Reset your password", Body: body})
}

func (s *Server) resetPassword(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	token := strings.TrimSpace(r.FormValue("token"))
	password := r.FormValue("password")
	confirm := r.FormValue("password_confirm")

	form := components.AuthForm{
		CSRF:   sess.CSRF,
		Values: map[string]string{"token": token},
		Errors: map[string]string{},
	}
	if token == "" {
		form.Errors["_form"] = "Invalid reset link."
	}
	if err := services.ValidatePassword(password); err != nil {
		form.Errors["password"] = err.Error()
	}
	if password != confirm {
		form.Errors["password_confirm"] = "Passwords do not match."
	}
	if len(form.Errors) > 0 {
		body, err := components.ResetPasswordPage(form)
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Reset your password", Body: body})
		return
	}

	tok, err := s.Store.GetAuthToken(r.Context(), store.TokenPasswordReset, hashToken(token))
	if err != nil {
		body, _ := components.ResetPasswordPage(components.AuthForm{CSRF: sess.CSRF, Errors: map[string]string{"_form": "This link is invalid or has expired. Request a new one."}})
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Reset your password", Body: body})
		return
	}

	hash, err := services.HashPassword(password)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.UpdatePassword(r.Context(), tok.UserID, hash); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.UseAuthToken(r.Context(), tok.ID); err != nil {
		s.Log.Error("use reset token", "token_id", tok.ID, "error", err)
	}
	// Password change is a privilege change: kill every session.
	if err := s.Store.RevokeUserSessions(r.Context(), tok.UserID, 0); err != nil {
		s.Log.Error("revoke sessions after reset", "user_id", tok.UserID, "error", err)
	}
	http.SetCookie(w, services.ClearSessionCookie(s.Cfg))
	s.audit(r.Context(), &tok.UserID, r, "user.password_reset", "user", fmt.Sprintf("%d", tok.UserID), nil)

	if err := s.Store.SetFlash(r.Context(), sess.ID, &store.Flash{Kind: "success", Text: "Your password was reset. Log in with your new password."}); err != nil {
		s.Log.Error("set flash", "error", err)
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *Server) sendVerificationEmail(r *http.Request, userID int64, name, email string) error {
	raw, hash, err := services.GenerateSessionToken()
	if err != nil {
		return err
	}
	if _, err := s.Store.CreateAuthToken(r.Context(), userID, store.TokenEmailVerification, hash, s.Cfg.AuthTokenTTL); err != nil {
		return err
	}
	link := s.Cfg.BaseURL + "/verify-email?token=" + url.QueryEscape(raw)
	return s.Mailer.Send(r.Context(), services.Email{
		To:      email,
		Subject: "Verify your email",
		Body: fmt.Sprintf(`Hi %s,

Please confirm your email address by opening this link:

%s

If you did not create an account, you can ignore this email.

- Gig`, name, link),
	})
}

func (s *Server) sendPasswordReset(r *http.Request, user *store.User) error {
	raw, hash, err := services.GenerateSessionToken()
	if err != nil {
		return err
	}
	if _, err := s.Store.CreateAuthToken(r.Context(), user.ID, store.TokenPasswordReset, hash, s.Cfg.AuthTokenTTL); err != nil {
		return err
	}
	link := s.Cfg.BaseURL + "/reset-password?token=" + url.QueryEscape(raw)
	return s.Mailer.Send(r.Context(), services.Email{
		To:      user.Email,
		Subject: "Reset your password",
		Body: fmt.Sprintf(`Hi %s,

Someone asked to reset your password. Open this link to choose a new one:

%s

If this was not you, you can safely ignore this email.

- Gig`, user.Name, link),
	})
}

func (s *Server) renderAuthInfo(w http.ResponseWriter, r *http.Request, status int, title, body string) {
	html, err := components.AuthInfoPage(components.AuthInfoData{Title: title, Body: body})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, status, components.PageData{Title: title, Body: html})
}

func (s *Server) userID(r *http.Request) *int64 {
	if u := s.userFrom(r); u != nil {
		return &u.ID
	}
	return nil
}

func (s *Server) audit(ctx context.Context, actor *int64, r *http.Request, action, entityType, entityID string, details map[string]any) {
	if err := s.Store.LogAction(ctx, actor, clientIP(r), action, entityType, entityID, details); err != nil {
		s.Log.Error("audit log", "action", action, "error", err)
	}
}

// safeNext restricts a post-login redirect to a local path, preventing open
// redirects.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		return "/"
	}
	return next
}
