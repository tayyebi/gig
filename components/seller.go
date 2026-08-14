package components

import "html/template"

// SellerDashboardData backs a seller's own management console.
type SellerDashboardData struct {
	CSRF             string
	OnboardingState  string
	CanRequestReview bool
	Gigs             []SellerGigRow
	RecentOrders     []OrderRow
}

// SellerGigRow is one row in a seller's gig list.
type SellerGigRow struct {
	ID     int64
	Title  string
	Status string
}

// SellerDashboardPage renders the seller console.
func SellerDashboardPage(d SellerDashboardData) (template.HTML, error) {
	return execute("sellerdashboard", d)
}

// SellerProfileFormData backs the seller profile edit form.
type SellerProfileFormData struct {
	CSRF        string
	DisplayName string
	Bio         string
	Location    string
	Errors      map[string]string
}

// SellerProfileFormPage renders the seller profile edit form.
func SellerProfileFormPage(d SellerProfileFormData) (template.HTML, error) {
	return execute("sellerprofileform", d)
}

// PortfolioPageData backs the portfolio management page.
type PortfolioPageData struct {
	CSRF   string
	Items  []PortfolioView
	Errors map[string]string
}

// PortfolioPage renders the seller's portfolio management page.
func PortfolioPage(d PortfolioPageData) (template.HTML, error) {
	return execute("sellerportfolio", d)
}

// CategoryOption is a select-list entry for the gig form.
type CategoryOption struct {
	ID   int64
	Name string
}

// GigPackageForm is one pricing tier as edited on the gig form. Values are
// kept as strings so invalid input can be redisplayed without a parse step.
type GigPackageForm struct {
	Tier         string
	Name         string
	Description  string
	Price        string
	DeliveryDays string
	Revisions    string
}

// GigAddonForm is one optional add-on slot as edited on the gig form.
type GigAddonForm struct {
	Name         string
	Description  string
	Price        string
	DeliveryDays string
}

// GigFormData backs the gig create/edit form. Packages always has exactly
// three entries (basic, standard, premium); a blank name skips that tier.
// Addons has a fixed number of optional slots since there is no JavaScript
// to add rows dynamically.
type GigFormData struct {
	CSRF        string
	Editing     bool
	GigID       int64
	Title       string
	Description string
	CategoryID  string
	Categories  []CategoryOption
	Tags        string
	Packages    [3]GigPackageForm
	Addons      [3]GigAddonForm
	Media       []GigMediaView
	Errors      map[string]string
}

// GigFormPage renders the gig create/edit form.
func GigFormPage(d GigFormData) (template.HTML, error) {
	return execute("gigform", d)
}
