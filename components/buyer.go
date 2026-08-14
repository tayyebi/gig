package components

import "html/template"

// BuyerDashboardData backs the buyer's own dashboard.
type BuyerDashboardData struct {
	RecentOrders []OrderRow
	Favorites    []GigCard
}

// BuyerDashboardPage renders the buyer dashboard.
func BuyerDashboardPage(d BuyerDashboardData) (template.HTML, error) {
	return execute("buyerdashboard", d)
}
