package handlers

import (
	"encoding/csv"
	"errors"
	"fmt"
	"html"
	"net/http"
	"strconv"
	"strings"

	"github.com/tayyebi/gig/ledger"
	"github.com/tayyebi/gig/store"
)

// adminHome is the admin console landing page: a hub linking to every
// sub-console (moderation, disputes, payments, payouts, settings). Full
// pages render as hand-built escaped HTML fragments via pageWithRawBody,
// matching the existing convention in handlers/payments.go rather than
// introducing a parallel html/template-based admin component tree.
func (s *Server) adminHome(w http.ResponseWriter, r *http.Request) {
	body := `<section class="container">
<h1>Admin console</h1>
<ul>
<li><a href="/admin/users">Users</a></li>
<li><a href="/admin/moderation/gigs">Gig moderation</a></li>
<li><a href="/admin/moderation/media">Media moderation</a></li>
<li><a href="/admin/moderation/reviews">Review moderation</a></li>
<li><a href="/admin/moderation/messages">Message moderation</a></li>
<li><a href="/admin/disputes">Disputes</a></li>
<li><a href="/admin/payments">Payments &amp; reconciliation</a></li>
<li><a href="/admin/payments/search">Payment search</a></li>
<li><a href="/admin/payouts">Payouts</a></li>
<li><a href="/admin/settings">Settings</a></li>
<li><a href="/admin/ledger/adjust">Ledger adjustment</a></li>
<li><a href="/admin/audit">Audit log</a></li>
</ul>
</section>`
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin console", body))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Users</h1>
<form method="get" action="/admin/users" role="search">
<label for="q">Search</label>
<input id="q" name="q" type="search" value="` + html.EscapeString(search) + `">
<label for="status">Status</label>
<select id="status" name="status">
<option value="">Any</option>`)
	for _, st := range []string{store.UserActive, store.UserDisabled, store.UserDeleted} {
		sel := ""
		if st == status {
			sel = " selected"
		}
		sb.WriteString(fmt.Sprintf(`<option value="%s"%s>%s</option>`, st, sel, st))
	}
	sb.WriteString(`</select>
<button class="btn" type="submit">Filter</button>
</form>
<p><a href="/admin/users/export.csv">Export CSV</a></p>
<table><thead><tr><th scope="col">ID</th><th scope="col">Name</th><th scope="col">Email</th><th scope="col">Status</th><th scope="col">Action</th></tr></thead><tbody>`)
	for _, u := range users {
		sb.WriteString(fmt.Sprintf("<tr><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td>",
			u.ID, html.EscapeString(u.Name), html.EscapeString(u.Email), html.EscapeString(u.Status)))
		if u.Status == store.UserActive {
			sb.WriteString(fmt.Sprintf(`<form method="post" action="/admin/users/%d/status" novalidate>%s
<input type="hidden" name="status" value="disabled">
<label for="reason-%d">Reason</label>
<input id="reason-%d" name="reason" type="text" maxlength="500" required>
<button class="btn" type="submit">Suspend</button></form>`, u.ID, csrfInputHTML(s.csrfFor(r)), u.ID, u.ID))
		} else if u.Status == store.UserDisabled {
			sb.WriteString(fmt.Sprintf(`<form method="post" action="/admin/users/%d/status" novalidate>%s
<input type="hidden" name="status" value="active">
<input type="hidden" name="reason" value="restored by admin">
<button class="btn" type="submit">Restore</button></form>`, u.ID, csrfInputHTML(s.csrfFor(r))))
		} else {
			sb.WriteString("&mdash;")
		}
		sb.WriteString("</td></tr>")
	}
	sb.WriteString(`</tbody></table><p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - users", sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Gig moderation</h1>`)
	sb.WriteString(moderationStateFilterHTML("/admin/moderation/gigs", state))
	if len(gigs) == 0 {
		sb.WriteString("<p>Nothing in this queue.</p>")
	} else {
		sb.WriteString(`<table><thead><tr><th scope="col">ID</th><th scope="col">Title</th><th scope="col">Seller</th><th scope="col">Status</th><th scope="col">Action</th></tr></thead><tbody>`)
		for _, g := range gigs {
			sb.WriteString(fmt.Sprintf("<tr><td>%d</td><td>%s</td><td>%d</td><td>%s</td><td>%s</td></tr>",
				g.ID, html.EscapeString(g.Title), g.SellerID, html.EscapeString(g.Status),
				moderationActionsHTML(s.csrfFor(r), fmt.Sprintf("/admin/moderation/gigs/%d", g.ID))))
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`<p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - gig moderation", sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Media moderation</h1>`)
	sb.WriteString(moderationStateFilterHTML("/admin/moderation/media", state))
	if len(media) == 0 {
		sb.WriteString("<p>Nothing in this queue.</p>")
	} else {
		sb.WriteString(`<table><thead><tr><th scope="col">ID</th><th scope="col">Gig</th><th scope="col">Image</th><th scope="col">Action</th></tr></thead><tbody>`)
		for _, m := range media {
			sb.WriteString(fmt.Sprintf(`<tr><td>%d</td><td>%s</td><td><img src="%s" alt="%s" width="120" height="90" loading="lazy"></td><td>%s</td></tr>`,
				m.ID, html.EscapeString(m.GigTitle), html.EscapeString(m.MediaPath), html.EscapeString(m.AltText),
				moderationActionsHTML(s.csrfFor(r), fmt.Sprintf("/admin/moderation/media/%d", m.ID))))
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`<p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - media moderation", sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Review moderation</h1>`)
	sb.WriteString(moderationStateFilterHTML("/admin/moderation/reviews", state))
	if len(reviews) == 0 {
		sb.WriteString("<p>Nothing in this queue.</p>")
	} else {
		sb.WriteString(`<table><thead><tr><th scope="col">ID</th><th scope="col">Order</th><th scope="col">Rating</th><th scope="col">Body</th><th scope="col">Action</th></tr></thead><tbody>`)
		for _, rv := range reviews {
			sb.WriteString(fmt.Sprintf("<tr><td>%d</td><td>%d</td><td>%d</td><td>%s</td><td>%s</td></tr>",
				rv.ID, rv.OrderID, rv.Rating, html.EscapeString(rv.Body),
				moderationActionsHTML(s.csrfFor(r), fmt.Sprintf("/admin/moderation/reviews/%d", rv.ID))))
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`<p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - review moderation", sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Message moderation</h1>
<form method="get" action="/admin/moderation/messages">
<label for="order_id">Order ID</label>
<input id="order_id" name="order_id" type="number" min="1">
<button class="btn" type="submit">Filter</button>
</form>
<table><thead><tr><th scope="col">ID</th><th scope="col">Order</th><th scope="col">Sender</th><th scope="col">Body</th><th scope="col">Action</th></tr></thead><tbody>`)
	for _, m := range messages {
		action := fmt.Sprintf(`<form method="post" action="/admin/moderation/messages/%d/hide" novalidate>%s<button class="btn" type="submit">Hide</button></form>`, m.ID, csrfInputHTML(s.csrfFor(r)))
		sb.WriteString(fmt.Sprintf("<tr><td>%d</td><td><a href=\"/orders/%d\">%d</a></td><td>%s</td><td>%s</td><td>%s</td></tr>",
			m.ID, m.OrderID, m.OrderID, html.EscapeString(m.SenderName), html.EscapeString(m.Body), action))
	}
	sb.WriteString(`</tbody></table><p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - message moderation", sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Disputes</h1>
<p><a href="/admin/disputes?status=open">Open</a> | <a href="/admin/disputes?status=resolved">Resolved</a></p>`)
	if len(disputes) == 0 {
		sb.WriteString("<p>No disputes in this state.</p>")
	} else {
		sb.WriteString(`<table><thead><tr><th scope="col">ID</th><th scope="col">Order</th><th scope="col">Reason</th><th scope="col">Status</th><th scope="col"></th></tr></thead><tbody>`)
		for _, d := range disputes {
			sb.WriteString(fmt.Sprintf(`<tr><td>%d</td><td><a href="/orders/%d">%d</a></td><td>%s</td><td>%s</td><td><a href="/admin/disputes/%d">Review</a></td></tr>`,
				d.ID, d.OrderID, d.OrderID, html.EscapeString(d.Reason), html.EscapeString(d.Status), d.ID))
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`<p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - disputes", sb.String()))
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

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<section class="container"><h1>Dispute #%d</h1>
<dl>
<dt>Order</dt><dd><a href="/orders/%d">#%d</a></dd>
<dt>Opened by user</dt><dd>%d</dd>
<dt>Reason</dt><dd>%s</dd>
<dt>Status</dt><dd>%s</dd>
<dt>Decision</dt><dd>%s</dd>
</dl>`, d.ID, d.OrderID, d.OrderID, d.OpenedBy, html.EscapeString(d.Reason), html.EscapeString(d.Status), html.EscapeString(d.Decision)))

	sb.WriteString("<h2>Evidence</h2>")
	if len(evidence) == 0 {
		sb.WriteString("<p>No evidence uploaded.</p>")
	} else {
		sb.WriteString("<ul>")
		for _, a := range evidence {
			sb.WriteString(fmt.Sprintf(`<li><a href="/orders/%d/attachments/%d">%s</a></li>`, d.OrderID, a.ID, html.EscapeString(a.FileName)))
		}
		sb.WriteString("</ul>")
	}

	sb.WriteString(fmt.Sprintf(`<h2>Internal notes</h2>
<form method="post" action="/admin/disputes/%d/notes" novalidate>
%s
<label for="notes">Notes (never shown to buyer or seller)</label>
<textarea id="notes" name="notes" rows="4">%s</textarea>
<button class="btn" type="submit">Save notes</button>
</form>`, d.ID, csrfInputHTML(s.csrfFor(r)), html.EscapeString(d.InternalNotes)))

	if d.Status == store.DisputeOpen {
		sb.WriteString(fmt.Sprintf(`<h2>Resolve</h2>
<form method="post" action="/orders/%d/dispute/resolve" novalidate>
%s
<label for="decision">Decision (shown to buyer and seller)</label>
<textarea id="decision" name="decision" rows="4" required></textarea>
<button class="btn" type="submit">Resolve dispute</button>
</form>`, d.OrderID, csrfInputHTML(s.csrfFor(r))))
	}
	sb.WriteString(`<p><a href="/admin/disputes">Back to disputes</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody(fmt.Sprintf("Admin - dispute #%d", d.ID), sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Payment search</h1>
<form method="get" action="/admin/payments/search" role="search">
<label for="q">Order ID, payment ID, or provider reference</label>
<input id="q" name="q" type="search" value="` + html.EscapeString(q) + `">
<button class="btn" type="submit">Search</button>
</form>`)
	if q != "" {
		intents, err := s.Store.SearchPaymentIntents(r.Context(), q, 25)
		if err != nil {
			s.renderError(w, err)
			return
		}
		if len(intents) == 0 {
			sb.WriteString("<p>No matches.</p>")
		} else {
			sb.WriteString(`<table><thead><tr><th scope="col">Intent ID</th><th scope="col">Order</th><th scope="col">Provider</th><th scope="col">Status</th><th scope="col">Amount</th><th scope="col"></th></tr></thead><tbody>`)
			for _, in := range intents {
				currency := strings.ToUpper(in.Currency)
				sb.WriteString(fmt.Sprintf(`<tr><td>%d</td><td>%d</td><td>%s</td><td>%s</td><td>%s</td><td><a href="/admin/orders/%d/timeline">Timeline</a></td></tr>`,
					in.ID, in.OrderID, html.EscapeString(in.Provider), html.EscapeString(in.Status), html.EscapeString(formatMoney(in.AmountMinor, currency)), in.OrderID))
			}
			sb.WriteString(`</tbody></table>`)
		}
	}
	sb.WriteString(`<p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - payment search", sb.String()))
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
		s.render(w, r, http.StatusOK, pageWithRawBody("Admin - order timeline",
			fmt.Sprintf(`<section class="container"><h1>Order #%d timeline</h1><p>No payment intent yet.</p></section>`, orderID)))
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

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf(`<section class="container"><h1>Order #%d timeline</h1>
<h2>Payment intent %d</h2>
<dl><dt>Provider</dt><dd>%s</dd><dt>Status</dt><dd>%s</dd><dt>Created</dt><dd>%s</dd></dl>`,
		orderID, intent.ID, html.EscapeString(intent.Provider), html.EscapeString(intent.Status), intent.CreatedAt.Format("2006-01-02 15:04:05")))

	sb.WriteString("<h2>Attempt history</h2>")
	if len(attempts) == 0 {
		sb.WriteString("<p>No recorded attempts.</p>")
	} else {
		sb.WriteString(`<table><thead><tr><th scope="col">When</th><th scope="col">Provider status</th><th scope="col">Failure</th></tr></thead><tbody>`)
		for _, a := range attempts {
			sb.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td></tr>",
				a.CreatedAt.Format("2006-01-02 15:04:05"), html.EscapeString(a.ProviderStatus), html.EscapeString(a.FailureMessage)))
		}
		sb.WriteString(`</tbody></table>`)
	}

	sb.WriteString("<h2>Webhook events</h2>")
	if len(events) == 0 {
		sb.WriteString("<p>No matched webhook events.</p>")
	} else {
		sb.WriteString(`<table><thead><tr><th scope="col">Event ID</th><th scope="col">Type</th><th scope="col">Status</th><th scope="col">Attempts</th></tr></thead><tbody>`)
		for _, e := range events {
			sb.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%d</td></tr>",
				html.EscapeString(e.EventID), html.EscapeString(e.EventType), html.EscapeString(e.Status), e.Attempts))
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`<p><a href="/admin/payments">Back to payments</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - order timeline", sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Settings</h1>
<table><thead><tr><th scope="col">Key</th><th scope="col">Value</th><th scope="col">Updated</th><th scope="col">Action</th></tr></thead><tbody>`)
	for _, st := range settings {
		sb.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>%s</td><td>%s</td><td>
<form method="post" action="/admin/settings" novalidate>
%s
<input type="hidden" name="key" value="%s">
<label for="value-%s">New value</label>
<input id="value-%s" name="value" type="text" value="%s">
<button class="btn" type="submit">Update</button>
</form></td></tr>`, html.EscapeString(st.Key), html.EscapeString(st.Value), st.UpdatedAt.Format("2006-01-02 15:04"),
			csrfInputHTML(s.csrfFor(r)), html.EscapeString(st.Key), html.EscapeString(st.Key), html.EscapeString(st.Key), html.EscapeString(st.Value)))
	}
	sb.WriteString(`</tbody></table>
<h2>Add a new setting</h2>
<form method="post" action="/admin/settings" novalidate>
` + csrfInputHTML(s.csrfFor(r)) + `
<label for="new-key">Key</label>
<input id="new-key" name="key" type="text" required>
<label for="new-value">Value</label>
<input id="new-value" name="value" type="text">
<button class="btn" type="submit">Add / update</button>
</form>
<p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - settings", sb.String()))
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

func (s *Server) adminAuditLog(w http.ResponseWriter, r *http.Request) {
	logs, err := s.Store.RecentActions(r.Context(), 200)
	if err != nil {
		s.renderError(w, err)
		return
	}
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Audit log</h1>
<p><a href="/admin/audit/export.csv">Export CSV</a></p>
<table><thead><tr><th scope="col">When</th><th scope="col">Actor</th><th scope="col">Action</th><th scope="col">Entity</th></tr></thead><tbody>`)
	for _, l := range logs {
		actor := "system"
		if l.ActorUserID != nil {
			actor = strconv.FormatInt(*l.ActorUserID, 10)
		}
		sb.WriteString(fmt.Sprintf("<tr><td>%s</td><td>%s</td><td>%s</td><td>%s %s</td></tr>",
			l.CreatedAt.Format("2006-01-02 15:04:05"), html.EscapeString(actor), html.EscapeString(l.Action),
			html.EscapeString(l.EntityType), html.EscapeString(l.EntityID)))
	}
	sb.WriteString(`</tbody></table><p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - audit log", sb.String()))
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
	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Ledger adjustment</h1>
<p class="help">Moves funds between two ledger accounts with a required reason. Every adjustment is recorded in the audit log and posted as a balanced double-entry transaction; there is no undo.</p>
<form method="post" action="/admin/ledger/adjust" novalidate>` + csrfInputHTML(s.csrfFor(r)))
	sb.WriteString(`<fieldset><legend>From account</legend>
<label for="from_kind">Kind</label><select id="from_kind" name="from_kind">`)
	for _, k := range ledgerAccountKinds {
		sb.WriteString(fmt.Sprintf(`<option value="%s">%s</option>`, k, k))
	}
	sb.WriteString(`</select>
<label for="from_owner">Owner user ID (blank for platform accounts)</label>
<input id="from_owner" name="from_owner" type="number" min="1"></fieldset>`)
	sb.WriteString(`<fieldset><legend>To account</legend>
<label for="to_kind">Kind</label><select id="to_kind" name="to_kind">`)
	for _, k := range ledgerAccountKinds {
		sb.WriteString(fmt.Sprintf(`<option value="%s">%s</option>`, k, k))
	}
	sb.WriteString(`</select>
<label for="to_owner">Owner user ID (blank for platform accounts)</label>
<input id="to_owner" name="to_owner" type="number" min="1"></fieldset>`)
	sb.WriteString(`<label for="amount">Amount (minor units)</label>
<input id="amount" name="amount" type="number" min="1" required>
<label for="currency">Currency</label>
<input id="currency" name="currency" type="text" value="usd" required>
<label for="order_id">Related order ID (optional)</label>
<input id="order_id" name="order_id" type="number" min="1">
<label for="reason">Reason</label>
<input id="reason" name="reason" type="text" maxlength="500" required>
<button class="btn" type="submit">Post adjustment</button>
</form>
<p><a href="/admin">Back to admin</a></p></section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Admin - ledger adjustment", sb.String()))
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

func moderationStateFilterHTML(base, current string) string {
	var sb strings.Builder
	sb.WriteString("<p>")
	for i, st := range []string{"pending", "approved", "rejected"} {
		if i > 0 {
			sb.WriteString(" | ")
		}
		if st == current {
			sb.WriteString(fmt.Sprintf("<strong>%s</strong>", st))
		} else {
			sb.WriteString(fmt.Sprintf(`<a href="%s?state=%s">%s</a>`, base, st, st))
		}
	}
	sb.WriteString("</p>")
	return sb.String()
}

func moderationActionsHTML(csrf, action string) string {
	return fmt.Sprintf(`<form method="post" action="%s" novalidate>%s<input type="hidden" name="decision" value="approve"><button class="btn" type="submit">Approve</button></form>
<form method="post" action="%s" novalidate>%s<input type="hidden" name="decision" value="reject"><button class="btn" type="submit">Reject</button></form>`,
		action, csrfInputHTML(csrf), action, csrfInputHTML(csrf))
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
