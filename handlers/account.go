package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// accountForm renders the account settings page.
func (s *Server) accountForm(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	body, err := components.AccountPage(components.AccountData{
		CSRF:          sess.CSRF,
		Name:          u.Name,
		Email:         u.Email,
		EmailVerified: u.EmailVerifiedAt != nil,
		TotpEnabled:   u.TotpEnabled,
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: "Account settings",
		Body:  body,
	})
}

// accountUpdate changes the profile name and, optionally, the email address.
func (s *Server) accountUpdate(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	name := strings.TrimSpace(r.FormValue("name"))
	email := strings.TrimSpace(r.FormValue("email"))

	form := components.AccountData{
		CSRF:          sess.CSRF,
		Name:          name,
		Email:         email,
		EmailVerified: u.EmailVerifiedAt != nil,
		TotpEnabled:   u.TotpEnabled,
		Errors:        map[string]string{},
	}

	if name == "" {
		form.Errors["name"] = "Please enter your name."
	} else if len(name) > maxNameLen {
		form.Errors["name"] = "Name must be at most 100 characters."
	}
	if email == "" || !emailPattern.MatchString(email) {
		form.Errors["email"] = "Please enter a valid email address."
	}
	if len(form.Errors) > 0 {
		s.accountFormError(w, r, form)
		return
	}

	if name != u.Name {
		if err := s.Store.UpdateName(r.Context(), u.ID, name); err != nil {
			s.renderError(w, err)
			return
		}
		s.audit(r.Context(), &u.ID, r, "user.name_changed", "user", fmt.Sprintf("%d", u.ID), nil)
	}

	emailChanged := store.NormalizeEmail(email) != store.NormalizeEmail(u.Email)
	if emailChanged {
		if err := s.Store.UpdateEmail(r.Context(), u.ID, email); err != nil {
			if errors.Is(err, store.ErrEmailTaken) {
				form.Errors["email"] = "That email is already in use by another account."
				s.accountFormError(w, r, form)
				return
			}
			s.renderError(w, err)
			return
		}
		form.EmailVerified = false
		if err := s.sendVerificationEmail(r, u.ID, name, email); err != nil {
			s.Log.Error("send verification email", "user_id", u.ID, "error", err)
		}
		s.audit(r.Context(), &u.ID, r, "user.email_changed", "user", fmt.Sprintf("%d", u.ID), nil)
	}

	if err := s.Store.SetFlash(r.Context(), sess.ID, &store.Flash{Kind: "success", Text: "Your account was updated."}); err != nil {
		s.Log.Error("set flash", "error", err)
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

func (s *Server) accountFormError(w http.ResponseWriter, r *http.Request, form components.AccountData) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	form.CSRF = sess.CSRF
	body, err := components.AccountPage(form)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusUnprocessableEntity, components.PageData{
		Title: "Account settings",
		Body:  body,
	})
}

// passwordForm renders the password change page.
func (s *Server) passwordForm(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.AccountPasswordPage(components.AccountPasswordData{CSRF: sess.CSRF})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: "Change your password",
		Body:  body,
	})
}

// passwordUpdate changes the user's password after verifying the current one.
func (s *Server) passwordUpdate(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	current := r.FormValue("current_password")
	newPassword := r.FormValue("new_password")
	confirm := r.FormValue("new_password_confirm")

	form := components.AccountPasswordData{CSRF: sess.CSRF, Errors: map[string]string{}}

	ok, err := services.VerifyPassword(current, u.PasswordHash)
	if err != nil || !ok {
		form.Errors["current_password"] = "Current password is incorrect."
	}
	if err := services.ValidatePassword(newPassword); err != nil {
		form.Errors["new_password"] = err.Error()
	}
	if newPassword != confirm {
		form.Errors["new_password_confirm"] = "Passwords do not match."
	}
	if len(form.Errors) > 0 {
		body, err := components.AccountPasswordPage(form)
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Change your password", Body: body})
		return
	}

	hash, err := services.HashPassword(newPassword)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.UpdatePassword(r.Context(), u.ID, hash); err != nil {
		s.renderError(w, err)
		return
	}

	// Password change is a privilege change: revoke all other sessions and
	// rotate the current one.
	if err := s.Store.RevokeUserSessions(r.Context(), u.ID, sess.ID); err != nil {
		s.Log.Error("revoke sessions after password change", "user_id", u.ID, "error", err)
	}
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
	newID, err := s.Store.RotateSession(r.Context(), sess.ID, u.ID, newHash, newCSRF, s.Cfg.SessionTTL, r.UserAgent(), clientIP(r))
	if err != nil {
		s.renderError(w, err)
		return
	}
	http.SetCookie(w, services.SessionCookie(s.Cfg, newRaw, s.Cfg.SessionTTL))

	s.audit(r.Context(), &u.ID, r, "user.password_changed", "user", fmt.Sprintf("%d", u.ID), nil)
	if err := s.Store.SetFlash(r.Context(), newID, &store.Flash{Kind: "success", Text: "Your password was changed."}); err != nil {
		s.Log.Error("set flash", "error", err)
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}

// mfaForm shows TOTP status, or the enrollment secret when not yet enabled.
func (s *Server) mfaForm(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)

	data := components.MFAData{CSRF: sess.CSRF, Enabled: u.TotpEnabled}
	if !u.TotpEnabled {
		secret, err := services.GenerateTOTPSecret()
		if err != nil {
			s.renderError(w, err)
			return
		}
		data.Secret = secret
		data.OTPAuth = services.OTPAuthURI(secret, u.Email, "Gig")
		data.Account = u.Email
	}

	body, err := components.MFAPage(data)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: "Two-factor authentication",
		Body:  body,
	})
}

// mfaEnable confirms a TOTP code and activates two-factor authentication.
func (s *Server) mfaEnable(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	if u.TotpEnabled {
		http.Redirect(w, r, "/account/mfa", http.StatusSeeOther)
		return
	}
	secret := strings.TrimSpace(r.FormValue("secret"))
	code := strings.TrimSpace(r.FormValue("code"))

	if secret == "" {
		body, err := components.MFAPage(components.MFAData{CSRF: sess.CSRF, Errors: map[string]string{"_form": "Enrollment session expired. Reload the page to generate a new secret."}})
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Two-factor authentication", Body: body})
		return
	}
	if len(code) != 6 {
		body, err := components.MFAPage(components.MFAData{CSRF: sess.CSRF, Secret: secret, OTPAuth: services.OTPAuthURI(secret, u.Email, "Gig"), Errors: map[string]string{"code": "Enter the 6-digit code from your app."}})
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Two-factor authentication", Body: body})
		return
	}
	valid, err := services.VerifyTOTP(secret, code, s.Cfg.TOTPSkew)
	if err != nil || !valid {
		body, err := components.MFAPage(components.MFAData{CSRF: sess.CSRF, Secret: secret, OTPAuth: services.OTPAuthURI(secret, u.Email, "Gig"), Errors: map[string]string{"code": "That code is not valid."}})
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Two-factor authentication", Body: body})
		return
	}

	if err := s.Store.SetTOTP(r.Context(), u.ID, secret); err != nil {
		s.renderError(w, err)
		return
	}
	s.rotateAfterSecurityChange(w, r, sess, u.ID, "user.mfa_enabled", "user", fmt.Sprintf("%d", u.ID), "Two-factor authentication is now enabled.")
}

// mfaDisable turns off two-factor authentication.
func (s *Server) mfaDisable(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	if u.TotpEnabled {
		if err := s.Store.DisableTOTP(r.Context(), u.ID); err != nil {
			s.renderError(w, err)
			return
		}
	}
	s.rotateAfterSecurityChange(w, r, sess, u.ID, "user.mfa_disabled", "user", fmt.Sprintf("%d", u.ID), "Two-factor authentication is now disabled.")
}

// rotateAfterSecurityChange applies the post-change session rotation and flash
// that follows a security-relevant account change.
func (s *Server) rotateAfterSecurityChange(w http.ResponseWriter, r *http.Request, sess *store.Session, userID int64, action, entityType, entityID, flashText string) {
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
	newID, err := s.Store.RotateSession(r.Context(), sess.ID, userID, newHash, newCSRF, s.Cfg.SessionTTL, r.UserAgent(), clientIP(r))
	if err != nil {
		s.renderError(w, err)
		return
	}
	http.SetCookie(w, services.SessionCookie(s.Cfg, newRaw, s.Cfg.SessionTTL))
	s.audit(r.Context(), &userID, r, action, entityType, entityID, nil)
	if err := s.Store.SetFlash(r.Context(), newID, &store.Flash{Kind: "success", Text: flashText}); err != nil {
		s.Log.Error("set flash", "error", err)
	}
	http.Redirect(w, r, "/account", http.StatusSeeOther)
}
