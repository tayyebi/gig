package components

import "html/template"

// CheckoutAddonOption is one selectable add-on shown on the requirements
// step, with its current selection state so the form redisplays correctly.
type CheckoutAddonOption struct {
	ID          int64
	Name        string
	Description string
	Price       string
	Selected    bool
}

// CheckoutRequirementsData backs the checkout requirements + add-ons step.
type CheckoutRequirementsData struct {
	DraftID      int64
	GigSlug      string
	GigTitle     string
	PackageName  string
	PackagePrice string
	Addons       []CheckoutAddonOption
	Requirements string
	CSRF         string
	Errors       map[string]string
}

// CheckoutRequirementsPage renders the checkout requirements step.
func CheckoutRequirementsPage(d CheckoutRequirementsData) (template.HTML, error) {
	return execute("checkoutrequirements", d)
}

// CheckoutLineItem is one priced line on the checkout review step.
type CheckoutLineItem struct {
	Name  string
	Price string
}

// CheckoutPaymentMethodOption is one selectable payment rail on the review
// step, backed by whichever provider adapters are actually configured
// (PLAN.md section 9).
type CheckoutPaymentMethodOption struct {
	Value   string // form value posted as payment_method
	Label   string
	Checked bool
}

// CheckoutReviewData backs the final checkout review, payment-method
// selection, and confirm step.
type CheckoutReviewData struct {
	DraftID        int64
	GigSlug        string
	GigTitle       string
	Requirements   string
	Items          []CheckoutLineItem
	Subtotal       string
	PlatformFee    string
	Total          string
	PaymentMethods []CheckoutPaymentMethodOption // empty when no provider is configured
	CSRF           string
}

// CheckoutReviewPage renders the checkout review/confirm step.
func CheckoutReviewPage(d CheckoutReviewData) (template.HTML, error) {
	return execute("checkoutreview", d)
}
