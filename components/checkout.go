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

// CheckoutReviewData backs the final checkout review, payment-method notice,
// and confirm step. There is no live payment provider yet (Phase 5), so the
// only "method" on offer is confirming the order as pending payment.
type CheckoutReviewData struct {
	DraftID      int64
	GigSlug      string
	GigTitle     string
	Requirements string
	Items        []CheckoutLineItem
	Subtotal     string
	PlatformFee  string
	Total        string
	CSRF         string
}

// CheckoutReviewPage renders the checkout review/confirm step.
func CheckoutReviewPage(d CheckoutReviewData) (template.HTML, error) {
	return execute("checkoutreview", d)
}
