package handlers

import (
	"net/http"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/store"
)

const dashboardRecentOrders = 5

// buyerDashboard is the signed-in buyer's landing page: recent orders and
// favorites.
func (s *Server) buyerDashboard(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	favorites, err := s.Store.ListFavoriteGigs(r.Context(), u.ID)
	if err != nil {
		s.renderError(w, err)
		return
	}
	recent, _, err := s.Store.ListOrdersForBuyer(r.Context(), u.ID, 1, dashboardRecentOrders)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.BuyerDashboardPage(components.BuyerDashboardData{
		RecentOrders: toOrderRows(recent),
		Favorites:    toGigCards(favorites),
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Your dashboard", Body: body})
}

func toOrderRows(summaries []store.OrderSummary) []components.OrderRow {
	rows := make([]components.OrderRow, 0, len(summaries))
	for _, o := range summaries {
		rows = append(rows, components.OrderRow{
			ID: o.ID, GigSlug: o.GigSlug, GigTitle: o.GigTitle, CounterpartyName: o.CounterpartyName,
			StatusLabel: orderStatusLabel(o.Status), Total: formatMoney(o.TotalMinorUnits, o.Currency),
			CreatedAt: formatTime(o.CreatedAt),
		})
	}
	return rows
}
