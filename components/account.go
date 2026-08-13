package components

import "html/template"

// AccountData backs the account settings page.
type AccountData struct {
	CSRF          string
	Errors        map[string]string
	Values        map[string]string
	Name          string
	Email         string
	EmailVerified bool
	TotpEnabled   bool
}

// AccountPage renders the account settings form.
func AccountPage(d AccountData) (template.HTML, error) {
	return execute("account", d)
}

// AccountPasswordData backs the password change page.
type AccountPasswordData struct {
	CSRF   string
	Errors map[string]string
}

// AccountPasswordPage renders the password change form.
func AccountPasswordPage(d AccountPasswordData) (template.HTML, error) {
	return execute("accountpassword", d)
}

// MFAData backs the MFA page. Secret and URI are only present when TOTP is
// not yet enabled.
type MFAData struct {
	CSRF    string
	Errors  map[string]string
	Enabled bool
	Secret  string
	OTPAuth string
	Account string
}

// MFAPage renders the MFA management page.
func MFAPage(d MFAData) (template.HTML, error) {
	return execute("accountmfa", d)
}
