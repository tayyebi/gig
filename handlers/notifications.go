package handlers

import (
	"net/http"

	"github.com/tayyebi/gig/components"
)

const notificationsPerPage = 20

// notificationsList shows a user's notifications, most recent first, and
// marks them all read: opening the page is how a user acknowledges them,
// there is no separate per-item "mark read" control without JavaScript.
func (s *Server) notificationsList(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	page := pageParam(r)
	items, total, err := s.Store.ListNotifications(r.Context(), u.ID, page, notificationsPerPage)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.MarkAllNotificationsRead(r.Context(), u.ID); err != nil {
		s.Log.Error("mark notifications read", "error", err)
	}

	rows := make([]components.NotificationRow, 0, len(items))
	for _, n := range items {
		rows = append(rows, components.NotificationRow{
			Body: n.Body, Link: n.Link, CreatedAt: formatTime(n.CreatedAt), Read: n.ReadAt != nil,
		})
	}
	pageHTML, err := components.Paginate(components.Pagination{Current: page, PerPage: notificationsPerPage, Total: total, BaseURL: "/notifications"})
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.NotificationsPage(components.NotificationsListData{Notifications: rows, Pagination: pageHTML})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Notifications", Body: body})
}
