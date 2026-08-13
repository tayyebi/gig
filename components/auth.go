package components

import "html/template"

// AuthForm supplies the auth pages. Errors maps field names to messages and
// Values holds submitted values for repopulation after a failed submit.
type AuthForm struct {
	CSRF   string
	Errors map[string]string
	Values map[string]string
	Next   string
}

// RegisterPage renders the account creation form.
func RegisterPage(d AuthForm) (template.HTML, error) {
	return execute("authregister", d)
}

// LoginPage renders the sign-in form.
func LoginPage(d AuthForm) (template.HTML, error) {
	return execute("authlogin", d)
}

// ForgotPasswordPage renders the password reset request form.
func ForgotPasswordPage(d AuthForm) (template.HTML, error) {
	return execute("authforgot", d)
}

// ResetPasswordPage renders the new-password form.
func ResetPasswordPage(d AuthForm) (template.HTML, error) {
	return execute("authreset", d)
}

// AuthInfoPage renders a simple informational page (e.g. "check your email").
type AuthInfoData struct {
	Title string
	Body  string
}

func AuthInfoPage(d AuthInfoData) (template.HTML, error) {
	return execute("authinfo", d)
}
