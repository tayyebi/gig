package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	htmltemplate "html/template"
	"net/http"
	"strconv"
	"strings"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/ledger"
	"github.com/tayyebi/gig/store"
)

// adminHome is the admin console landing page: a hub linking to every
// sub-console (moderation, disputes, payments, payouts, settings).
func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	body, err := components.AdminHomePage()
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin console", Body: body})
}

// escCell wraps plain text as a safe, already-escaped table cell for
// components.AdminTable, whose Rows are template.HTML so callers can also
// embed forms and links (built through typed templates elsewhere).
func escCell(s string) htmltemplate.HTML {
	return htmltemplate.HTML(htmltemplate.HTMLEscapeString(s))
}

// ---------------------------------------------------------------------------
// User moderation
// ---------------------------------------------------------------------------

func (s *Server) adminUsers(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	search := strings.TrimSpace(r.URL.Query().Get("q"))
	users, err := s.Store.ListUsersAdmin(r.Context(), status, search, 100)
	if err != nil {
		s.renderError(w, err)
		return
	}

	statusOptions := []components.FilterOption{{Value: "", Label: "Any", Selected: status == ""}}
	for _, st := range []string{store.UserActive, store.UserDisabled, store.UserDeleted} {
		statusOptions = append(statusOptions, components.FilterOption{Value: st, Label: st, Selected: st == status})
	}
	lead, err := components.FilterFormHTML(components.FilterForm{
		Action: "/admin/users",
		Fields: []components.FilterField{
			{Name: "q", Label: "Search", Value: search},
			{Name: "status", Label: "Status", Options: statusOptions},
		},
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	lead += htmltemplate.HTML(`<p><a href="/admin/users/export.csv">Export CSV</a></p>`)

	table := components.AdminTable{Columns: []string{"ID", "Name", "Email", "Status", "Action"}}
	for _, u := range users {
		actionHTML := htmltemplate.HTML("&mdash;")
		statusAction := fmt.Sprintf("/admin/users/%d/status", u.ID)
		switch u.Status {
		case store.UserActive:
			actionHTML, err = components.SuspendUserAction(u.ID, statusAction, s.csrfFor(r))
		case store.UserDisabled:
			actionHTML, err = components.RestoreUserAction(statusAction, s.csrfFor(r))
		}
		if err != nil {
			s.renderError(w, err)
			return
		}
		table.Rows = append(table.Rows, []htmltemplate.HTML{
			escCell(strconv.FormatInt(u.ID, 10)), escCell(u.Name), escCell(u.Email), escCell(u.Status), actionHTML,
		})
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Users", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - users", Body: body})
}

func (s *Server) adminUserStatus(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	status := r.FormValue("status")
	reason := strings.TrimSpace(r.FormValue("reason"))
	if status != store.UserActive && status != store.UserDisabled {
		s.flashError(r, "Invalid status.")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	if status == store.UserDisabled && reason == "" {
		s.flashError(r, "A reason is required to suspend a user.")
		http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
		return
	}
	if err := s.Store.SetUserStatusAdmin(r.Context(), id, status, reason); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "user.status_changed", "user", strconv.FormatInt(id, 10), map[string]any{"status": status, "reason": reason})
	s.flashNotice(r, "User status updated.")
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

// adminUsersExport streams a CSV of users. It is admin-only (route-gated),
// exposes email addresses (a sensitive field), and is itself audited so the
// export of that data is traceable.
func (s *Server) adminUsersExport(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	users, err := s.Store.ListUsersAdmin(r.Context(), "", "", 5000)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "admin.export_csv", "users", "", map[string]any{"row_count": len(users)})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="users.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"id", "name", "email", "status", "created_at"})
	for _, u := range users {
		_ = cw.Write([]string{strconv.FormatInt(u.ID, 10), u.Name, u.Email, u.Status, u.CreatedAt.Format("2006-01-02T15:04:05Z07:00")})
	}
	cw.Flush()
}

// ---------------------------------------------------------------------------
// Gig, media, and review moderation
// ---------------------------------------------------------------------------

func (s *Server) adminModerationGigs(w http.ResponseWriter, r *http.Request) {
	state := queryOr(r, "state", "pending")
	gigs, err := s.Store.ListGigsByModeration(r.Context(), state, 100)
	if err != nil {
		s.renderError(w, err)
		return
	}
	lead, err := components.ModerationFilterBar("/admin/moderation/gigs", state)
	if err != nil {
		s.renderError(w, err)
		return
	}
	table := components.AdminTable{Columns: []string{"ID", "Title", "Seller", "Status", "Action"}}
	for _, g := range gigs {
		actions, err := components.ModerationActions(fmt.Sprintf("/admin/moderation/gigs/%d", g.ID), s.csrfFor(r))
		if err != nil {
			s.renderError(w, err)
			return
		}
		table.Rows = append(table.Rows, []htmltemplate.HTML{
			escCell(strconv.FormatInt(g.ID, 10)), escCell(g.Title), escCell(strconv.FormatInt(g.SellerID, 10)), escCell(g.Status), actions,
		})
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Gig moderation", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - gig moderation", Body: body})
}

func (s *Server) adminModerationGigDecide(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	state, ok := moderationDecision(r)
	if !ok {
		s.flashError(r, "Invalid decision.")
		http.Redirect(w, r, "/admin/moderation/gigs", http.StatusSeeOther)
		return
	}
	if err := s.Store.SetGigModerationStateAdmin(r.Context(), id, state); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "gig.moderation_decided", "gig", strconv.FormatInt(id, 10), map[string]any{"state": state})
	s.flashNotice(r, "Gig moderation updated.")
	http.Redirect(w, r, "/admin/moderation/gigs", http.StatusSeeOther)
}

func (s *Server) adminModerationMedia(w http.ResponseWriter, r *http.Request) {
	state := queryOr(r, "state", "pending")
	media, err := s.Store.ListGigMediaByModeration(r.Context(), state, 100)
	if err != nil {
		s.renderError(w, err)
		return
	}
	lead, err := components.ModerationFilterBar("/admin/moderation/media", state)
	if err != nil {
		s.renderError(w, err)
		return
	}
	table := components.AdminTable{Columns: []string{"ID", "Gig", "Image", "Action"}}
	for _, m := range media {
		actions, err := components.ModerationActions(fmt.Sprintf("/admin/moderation/media/%d", m.ID), s.csrfFor(r))
		if err != nil {
			s.renderError(w, err)
			return
		}
		img := htmltemplate.HTML(fmt.Sprintf(`<img src="%s" alt="%s" width="120" height="90" loading="lazy">`,
			htmltemplate.HTMLEscapeString(m.MediaPath), htmltemplate.HTMLEscapeString(m.AltText)))
		table.Rows = append(table.Rows, []htmltemplate.HTML{
			escCell(strconv.FormatInt(m.ID, 10)), escCell(m.GigTitle), img, actions,
		})
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Media moderation", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - media moderation", Body: body})
}

func (s *Server) adminModerationMediaDecide(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	state, ok := moderationDecision(r)
	if !ok {
		s.flashError(r, "Invalid decision.")
		http.Redirect(w, r, "/admin/moderation/media", http.StatusSeeOther)
		return
	}
	if err := s.Store.SetGigMediaModerationState(r.Context(), id, state); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "gig_media.moderation_decided", "gig_media", strconv.FormatInt(id, 10), map[string]any{"state": state})
	s.flashNotice(r, "Media moderation updated.")
	http.Redirect(w, r, "/admin/moderation/media", http.StatusSeeOther)
}

func (s *Server) adminModerationReviews(w http.ResponseWriter, r *http.Request) {
	state := queryOr(r, "state", "pending")
	reviews, err := s.Store.ListReviewsByModeration(r.Context(), state, 100)
	if err != nil {
		s.renderError(w, err)
		return
	}
	lead, err := components.ModerationFilterBar("/admin/moderation/reviews", state)
	if err != nil {
		s.renderError(w, err)
		return
	}
	table := components.AdminTable{Columns: []string{"ID", "Order", "Rating", "Body", "Action"}}
	for _, rv := range reviews {
		actions, err := components.ModerationActions(fmt.Sprintf("/admin/moderation/reviews/%d", rv.ID), s.csrfFor(r))
		if err != nil {
			s.renderError(w, err)
			return
		}
		table.Rows = append(table.Rows, []htmltemplate.HTML{
			escCell(strconv.FormatInt(rv.ID, 10)), escCell(strconv.FormatInt(rv.OrderID, 10)),
			escCell(strconv.Itoa(rv.Rating)), escCell(rv.Body), actions,
		})
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Review moderation", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - review moderation", Body: body})
}

func (s *Server) adminModerationReviewDecide(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	state, ok := moderationDecision(r)
	if !ok {
		s.flashError(r, "Invalid decision.")
		http.Redirect(w, r, "/admin/moderation/reviews", http.StatusSeeOther)
		return
	}
	if err := s.Store.SetReviewModerationStateAdmin(r.Context(), id, state); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "review.moderation_decided", "review", strconv.FormatInt(id, 10), map[string]any{"state": state})
	s.flashNotice(r, "Review moderation updated.")
	http.Redirect(w, r, "/admin/moderation/reviews", http.StatusSeeOther)
}

func (s *Server) adminModerationMessages(w http.ResponseWriter, r *http.Request) {
	var orderID int64
	if v := r.URL.Query().Get("order_id"); v != "" {
		orderID, _ = strconv.ParseInt(v, 10, 64)
	}
	messages, err := s.Store.RecentOrderMessages(r.Context(), orderID, 100)
	if err != nil {
		s.renderError(w, err)
		return
	}
	orderIDValue := ""
	if orderID != 0 {
		orderIDValue = strconv.FormatInt(orderID, 10)
	}
	lead, err := components.FilterFormHTML(components.FilterForm{
		Action: "/admin/moderation/messages",
		Fields: []components.FilterField{{Name: "order_id", Label: "Order ID", Value: orderIDValue}},
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	table := components.AdminTable{Columns: []string{"ID", "Order", "Sender", "Body", "Action"}}
	for _, m := range messages {
		action, err := components.HideAction(fmt.Sprintf("/admin/moderation/messages/%d/hide", m.ID), s.csrfFor(r))
		if err != nil {
			s.renderError(w, err)
			return
		}
		orderLink := htmltemplate.HTML(fmt.Sprintf(`<a href="/orders/%d">%d</a>`, m.OrderID, m.OrderID))
		table.Rows = append(table.Rows, []htmltemplate.HTML{
			escCell(strconv.FormatInt(m.ID, 10)), orderLink, escCell(m.SenderName), escCell(m.Body), action,
		})
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Message moderation", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - message moderation", Body: body})
}

func (s *Server) adminModerationMessageHide(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.Store.HideOrderMessage(r.Context(), id, admin.ID, true); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "order_message.hidden", "order_message", strconv.FormatInt(id, 10), nil)
	s.flashNotice(r, "Message hidden.")
	http.Redirect(w, r, "/admin/moderation/messages", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Dispute resolution console
// ---------------------------------------------------------------------------

func (s *Server) adminDisputes(w http.ResponseWriter, r *http.Request) {
	status := queryOr(r, "status", store.DisputeOpen)
	disputes, err := s.Store.ListDisputesByStatus(r.Context(), status, 100)
	if err != nil {
		s.renderError(w, err)
		return
	}
	lead := htmltemplate.HTML(`<p><a href="/admin/disputes?status=open">Open</a> | <a href="/admin/disputes?status=resolved">Resolved</a></p>`)
	table := components.AdminTable{Columns: []string{"ID", "Order", "Reason", "Status", ""}}
	for _, d := range disputes {
		orderLink := htmltemplate.HTML(fmt.Sprintf(`<a href="/orders/%d">%d</a>`, d.OrderID, d.OrderID))
		reviewLink := htmltemplate.HTML(fmt.Sprintf(`<a href="/admin/disputes/%d">Review</a>`, d.ID))
		table.Rows = append(table.Rows, []htmltemplate.HTML{
			escCell(strconv.FormatInt(d.ID, 10)), orderLink, escCell(d.Reason), escCell(d.Status), reviewLink,
		})
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Disputes", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - disputes", Body: body})
}

// adminDisputeDetail shows a single dispute with its evidence (dispute
// attachments filed on the order), an internal-notes form, and (if still
// open) a resolution decision form.
func (s *Server) adminDisputeDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	d, err := s.Store.GetDispute(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	evidence, err := s.Store.ListOrderAttachments(r.Context(), d.OrderID, store.AttachmentDispute)
	if err != nil {
		s.renderError(w, err)
		return
	}

	data := components.AdminDisputeDetailData{
		ID: d.ID, OrderID: d.OrderID, OpenedBy: d.OpenedBy, Reason: d.Reason, Status: d.Status,
		Decision: d.Decision, CSRF: s.csrfFor(r), InternalNotes: d.InternalNotes, Open: d.Status == store.DisputeOpen,
	}
	for _, a := range evidence {
		data.Evidence = append(data.Evidence, components.AdminDisputeEvidence{ID: a.ID, FileName: a.FileName})
	}
	body, err := components.AdminDisputeDetailPage(data)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: fmt.Sprintf("Admin - dispute #%d", d.ID), Body: body})
}

func (s *Server) adminDisputeNotes(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	if err := s.Store.SetDisputeInternalNotes(r.Context(), id, notes); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "dispute.internal_notes_updated", "dispute", strconv.FormatInt(id, 10), nil)
	s.flashNotice(r, "Notes saved.")
	http.Redirect(w, r, fmt.Sprintf("/admin/disputes/%d", id), http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Payment search, timeline, and webhook retry
// ---------------------------------------------------------------------------

func (s *Server) adminPaymentSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	lead, err := components.FilterFormHTML(components.FilterForm{
		Action: "/admin/payments/search",
		Fields: []components.FilterField{{Name: "q", Label: "Order ID, payment ID, or provider reference", Value: q}},
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	table := components.AdminTable{Columns: []string{"Intent ID", "Order", "Provider", "Status", "Amount", ""}}
	if q != "" {
		intents, err := s.Store.SearchPaymentIntents(r.Context(), q, 25)
		if err != nil {
			s.renderError(w, err)
			return
		}
		for _, in := range intents {
			currency := strings.ToUpper(in.Currency)
			timelineLink := htmltemplate.HTML(fmt.Sprintf(`<a href="/admin/orders/%d/timeline">Timeline</a>`, in.OrderID))
			table.Rows = append(table.Rows, []htmltemplate.HTML{
				escCell(strconv.FormatInt(in.ID, 10)), escCell(strconv.FormatInt(in.OrderID, 10)),
				escCell(in.Provider), escCell(in.Status), escCell(formatMoney(in.AmountMinor, currency)), timelineLink,
			})
		}
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Payment search", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - payment search", Body: body})
}

// adminOrderTimeline shows the full attempt/webhook history for an order's
// latest payment intent, not just its current status (closes the Phase 5
// TODO item asking for a timeline view rather than a snapshot).
func (s *Server) adminOrderTimeline(w http.ResponseWriter, r *http.Request) {
	orderID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	intent, err := s.Store.LatestPaymentIntentForOrder(r.Context(), orderID)
	if errors.Is(err, store.ErrNotFound) {
		body, err := components.AdminOrderTimelinePage(components.AdminOrderTimelineData{OrderID: orderID})
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - order timeline", Body: body})
		return
	}
	if err != nil {
		s.renderError(w, err)
		return
	}
	attempts, err := s.Store.ListPaymentAttempts(r.Context(), intent.ID)
	if err != nil {
		s.renderError(w, err)
		return
	}
	events, _ := s.Store.ListWebhookEventsForOrder(r.Context(), orderID)

	data := components.AdminOrderTimelineData{
		OrderID: orderID, HasIntent: true, IntentID: intent.ID, Provider: intent.Provider,
		Status: intent.Status, Created: intent.CreatedAt.Format("2006-01-02 15:04:05"),
	}
	for _, a := range attempts {
		data.Attempts = append(data.Attempts, components.AdminAttemptRow{
			When: a.CreatedAt.Format("2006-01-02 15:04:05"), Status: a.ProviderStatus, Failure: a.FailureMessage,
		})
	}
	for _, e := range events {
		data.Events = append(data.Events, components.AdminWebhookEventRow{
			EventID: e.EventID, EventType: e.EventType, Status: e.Status, Attempts: e.Attempts,
		})
	}
	body, err := components.AdminOrderTimelinePage(data)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - order timeline", Body: body})
}

// adminJobRetry resets one dead/failed job to queued so a worker picks it up
// again, for the safe webhook-retry tool (PLAN.md section 15/16).
func (s *Server) adminJobRetry(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.Store.RetryJob(r.Context(), id); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "job.retried", "job", strconv.FormatInt(id, 10), nil)
	s.flashNotice(r, "Job re-queued.")
	http.Redirect(w, r, "/admin/payments", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Settings and audit log
// ---------------------------------------------------------------------------

func (s *Server) adminSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.Store.ListSettings(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	data := components.AdminSettingsData{CSRF: s.csrfFor(r)}
	for _, st := range settings {
		data.Settings = append(data.Settings, components.AdminSettingRow{
			Key: st.Key, Value: st.Value, Updated: st.UpdatedAt.Format("2006-01-02 15:04"),
		})
	}
	body, err := components.AdminSettingsPage(data)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - settings", Body: body})
}

func (s *Server) adminSettingsUpdate(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	key := strings.TrimSpace(r.FormValue("key"))
	value := r.FormValue("value")
	if key == "" {
		s.flashError(r, "A setting key is required.")
		http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
		return
	}
	if err := s.Store.SetSetting(r.Context(), key, value); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "settings.updated", "platform_settings", key, map[string]any{"value": value})
	s.flashNotice(r, "Setting saved.")
	http.Redirect(w, r, "/admin/settings", http.StatusSeeOther)
}

// adminCategories lists and edits browse categories, and lists seller-
// supplied tags with their usage counts so an admin can prune or
// consolidate free-text tag sprawl (categories are curated; tags are not).
func (s *Server) adminCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.Store.ListCategories(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	tags, err := s.Store.ListTagsWithUsage(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}

	data := components.AdminCategoriesTagsData{CSRF: s.csrfFor(r)}
	for _, c := range cats {
		data.Categories = append(data.Categories, components.AdminCategoryRow{
			ID: c.ID, Slug: c.Slug, Name: c.Name, Description: c.Description, Position: c.Position,
		})
	}
	for _, t := range tags {
		data.Tags = append(data.Tags, components.AdminTagRow{ID: t.ID, Name: t.Name, GigCount: t.GigCount})
	}
	body, err := components.AdminCategoriesTagsPage(data)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - categories and tags", Body: body})
}

func (s *Server) adminCategoryCreate(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	slug := strings.TrimSpace(r.FormValue("slug"))
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	position, _ := strconv.Atoi(r.FormValue("position"))
	if slug == "" || name == "" {
		s.flashError(r, "A slug and name are required.")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}
	id, err := s.Store.CreateCategory(r.Context(), slug, name, description, position)
	if err != nil {
		s.flashError(r, "Could not create category (slug may already be in use).")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "category.created", "category", strconv.FormatInt(id, 10),
		map[string]any{"slug": slug, "name": name})
	s.flashNotice(r, "Category added.")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func (s *Server) adminCategoryUpdate(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	description := strings.TrimSpace(r.FormValue("description"))
	position, _ := strconv.Atoi(r.FormValue("position"))
	if name == "" {
		s.flashError(r, "A name is required.")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}
	if err := s.Store.UpdateCategory(r.Context(), id, name, description, position); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "category.updated", "category", strconv.FormatInt(id, 10),
		map[string]any{"name": name, "position": position})
	s.flashNotice(r, "Category updated.")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func (s *Server) adminCategoryDelete(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.Store.DeleteCategory(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "category.deleted", "category", strconv.FormatInt(id, 10), nil)
	s.flashNotice(r, "Category deleted.")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func (s *Server) adminTagRename(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		s.flashError(r, "A name is required.")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}
	if err := s.Store.RenameTag(r.Context(), id, name); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.flashError(r, "Could not rename tag (the new name may collide with an existing tag).")
		http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "tag.renamed", "tag", strconv.FormatInt(id, 10), map[string]any{"name": name})
	s.flashNotice(r, "Tag renamed.")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func (s *Server) adminTagDelete(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := s.Store.DeleteTag(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "tag.deleted", "tag", strconv.FormatInt(id, 10), nil)
	s.flashNotice(r, "Tag deleted.")
	http.Redirect(w, r, "/admin/categories", http.StatusSeeOther)
}

func (s *Server) adminAuditLog(w http.ResponseWriter, r *http.Request) {
	logs, err := s.Store.RecentActions(r.Context(), 200)
	if err != nil {
		s.renderError(w, err)
		return
	}
	lead := htmltemplate.HTML(`<p><a href="/admin/audit/export.csv">Export CSV</a></p>`)
	table := components.AdminTable{Columns: []string{"When", "Actor", "Action", "Entity"}}
	for _, l := range logs {
		actor := "system"
		if l.ActorUserID != nil {
			actor = strconv.FormatInt(*l.ActorUserID, 10)
		}
		table.Rows = append(table.Rows, []htmltemplate.HTML{
			escCell(l.CreatedAt.Format("2006-01-02 15:04:05")), escCell(actor), escCell(l.Action),
			escCell(l.EntityType + " " + l.EntityID),
		})
	}
	body, err := components.AdminListPage(components.AdminListData{
		Title: "Audit log", Lead: lead, Table: table, BackHref: "/admin", BackLabel: "Back to admin",
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - audit log", Body: body})
}

func (s *Server) adminAuditExport(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	logs, err := s.Store.RecentActions(r.Context(), 500)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "admin.export_csv", "audit_log", "", map[string]any{"row_count": len(logs)})
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="audit_log.csv"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"created_at", "actor_user_id", "action", "entity_type", "entity_id"})
	for _, l := range logs {
		actor := ""
		if l.ActorUserID != nil {
			actor = strconv.FormatInt(*l.ActorUserID, 10)
		}
		_ = cw.Write([]string{l.CreatedAt.Format("2006-01-02T15:04:05Z07:00"), actor, l.Action, l.EntityType, l.EntityID})
	}
	cw.Flush()
}

// ---------------------------------------------------------------------------
// Free-form ledger adjustment
// ---------------------------------------------------------------------------

var ledgerAccountKinds = []string{
	ledger.AccountPlatformRevenue, ledger.AccountSellerPending, ledger.AccountSellerAvailable,
	ledger.AccountRefunds, ledger.AccountReserves, ledger.AccountProviderClearing,
}

// adminLedgerAdjustForm and adminLedgerAdjust implement the audited,
// permissioned manual adjustment tool (PLAN.md section 12; TODO.md Phase 5
// "Implement audited, permissioned manual adjustments with reason").
func (s *Server) adminLedgerAdjustForm(w http.ResponseWriter, r *http.Request) {
	body, err := components.AdminLedgerAdjustPage(components.AdminLedgerAdjustData{
		CSRF: s.csrfFor(r), AccountKinds: ledgerAccountKinds,
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Admin - ledger adjustment", Body: body})
}

func (s *Server) adminLedgerAdjust(w http.ResponseWriter, r *http.Request) {
	admin := s.userFrom(r)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	fromKind := r.FormValue("from_kind")
	toKind := r.FormValue("to_kind")
	amount, err := strconv.ParseInt(r.FormValue("amount"), 10, 64)
	if err != nil || amount <= 0 {
		s.flashError(r, "A positive amount is required.")
		http.Redirect(w, r, "/admin/ledger/adjust", http.StatusSeeOther)
		return
	}
	currency := strings.ToLower(strings.TrimSpace(r.FormValue("currency")))
	reason := strings.TrimSpace(r.FormValue("reason"))
	if currency == "" || reason == "" {
		s.flashError(r, "Currency and reason are required.")
		http.Redirect(w, r, "/admin/ledger/adjust", http.StatusSeeOther)
		return
	}
	fromOwner := parseOptionalID(r.FormValue("from_owner"))
	toOwner := parseOptionalID(r.FormValue("to_owner"))
	orderID := parseOptionalID(r.FormValue("order_id"))

	entries, err := ledger.ManualAdjustment(fromKind, fromOwner, toKind, toOwner, amount, currency, orderID, reason)
	if err != nil {
		s.flashError(r, "Adjustment does not balance: "+err.Error())
		http.Redirect(w, r, "/admin/ledger/adjust", http.StatusSeeOther)
		return
	}
	group, err := s.Store.PostLedgerEntries(r.Context(), entries)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &admin.ID, r, "ledger.manual_adjustment", "ledger_transaction", group, map[string]any{
		"from_kind": fromKind, "to_kind": toKind, "amount_minor_units": amount, "currency": currency, "reason": reason,
	})
	s.flashNotice(r, "Adjustment posted.")
	http.Redirect(w, r, "/admin/ledger/adjust", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Small shared helpers
// ---------------------------------------------------------------------------

func queryOr(r *http.Request, key, def string) string {
	if v := r.URL.Query().Get(key); v != "" {
		return v
	}
	return def
}

func moderationDecision(r *http.Request) (string, bool) {
	if err := r.ParseForm(); err != nil {
		return "", false
	}
	switch r.FormValue("decision") {
	case "approve":
		return store.ModerationApproved, true
	case "reject":
		return store.ModerationRejected, true
	default:
		return "", false
	}
}

func parseOptionalID(v string) *int64 {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	id, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil
	}
	return &id
}
