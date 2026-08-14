package components

import "html/template"

// NotificationRow is one displayable notification.
type NotificationRow struct {
	Body      string
	Link      string
	CreatedAt string
	Read      bool
}

// NotificationsListData backs the notifications page.
type NotificationsListData struct {
	Notifications []NotificationRow
	Pagination    template.HTML
}

// NotificationsPage renders a user's notification list.
func NotificationsPage(d NotificationsListData) (template.HTML, error) {
	return execute("notifications", d)
}
