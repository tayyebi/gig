package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

const (
	ordersPerPage       = 20
	maxOrderMessageLen  = 4000
	maxOrderReasonLen   = 2000
	maxOrderDecisionLen = 2000
)

var orderStatusLabels = map[string]string{
	services.OrderPendingPayment:    "Pending payment",
	services.OrderPaymentFailed:     "Payment failed",
	services.OrderPaid:              "Paid",
	services.OrderInProgress:        "In progress",
	services.OrderDelivered:         "Delivered",
	services.OrderRevisionRequested: "Revision requested",
	services.OrderAccepted:          "Accepted",
	services.OrderDisputed:          "Disputed",
	services.OrderCancelRequested:   "Cancellation requested",
	services.OrderCancelled:         "Cancelled",
	services.OrderPayoutPending:     "Payout pending",
	services.OrderPaidOut:           "Paid out",
	services.OrderClosed:            "Closed",
}

func orderStatusLabel(status string) string {
	if label, ok := orderStatusLabels[status]; ok {
		return label
	}
	return status
}

func formatTime(t time.Time) string {
	return t.Format("Jan 2, 2006 3:04 PM")
}

// ordersListBuyer lists the signed-in user's own orders as a buyer.
func (s *Server) ordersListBuyer(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	page := pageParam(r)
	summaries, total, err := s.Store.ListOrdersForBuyer(r.Context(), u.ID, page, ordersPerPage)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.renderOrdersList(w, r, "Your orders", false, summaries, total, page)
}

// ordersListSeller lists orders placed against the signed-in seller's gigs.
func (s *Server) ordersListSeller(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	page := pageParam(r)
	summaries, total, err := s.Store.ListOrdersForSeller(r.Context(), u.ID, page, ordersPerPage)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.renderOrdersList(w, r, "Orders to fulfill", true, summaries, total, page)
}

func pageParam(r *http.Request) int {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	return page
}

func (s *Server) renderOrdersList(w http.ResponseWriter, r *http.Request, heading string, asSeller bool, summaries []store.OrderSummary, total, page int) {
	rows := toOrderRows(summaries)
	base := "/orders"
	if asSeller {
		base = "/sell/orders"
	}
	pageHTML, err := components.Paginate(components.Pagination{Current: page, PerPage: ordersPerPage, Total: total, BaseURL: base})
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.OrdersListPage(components.OrdersListData{Heading: heading, AsSeller: asSeller, Orders: rows, Pagination: pageHTML})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: heading, Body: body})
}

// orderAccess bundles an order with the signed-in user's relationship to it.
type orderAccess struct {
	Order    *store.Order
	IsBuyer  bool
	IsSeller bool
	IsAdmin  bool
}

// loadOrderAccess resolves the {id} path value to an order and checks that
// the signed-in user is the buyer, the seller, or an admin. Anyone else gets
// a 404 rather than a 403, so an order's existence is not leaked.
func (s *Server) loadOrderAccess(w http.ResponseWriter, r *http.Request) (*orderAccess, bool) {
	u := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return nil, false
	}
	order, err := s.Store.GetOrder(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return nil, false
		}
		s.renderError(w, err)
		return nil, false
	}
	isAdmin, err := s.Store.UserHasRole(r.Context(), u.ID, store.RoleAdmin)
	if err != nil {
		s.renderError(w, err)
		return nil, false
	}
	access := &orderAccess{Order: order, IsBuyer: order.BuyerID == u.ID, IsSeller: order.SellerID == u.ID, IsAdmin: isAdmin}
	if !access.IsBuyer && !access.IsSeller && !access.IsAdmin {
		s.notFound(w, r)
		return nil, false
	}
	return access, true
}

// orderDetail renders the order workspace: summary, timeline, deliveries,
// messages, and whichever actions the viewer's role and the order's current
// status allow.
func (s *Server) orderDetail(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	data, err := s.buildOrderDetail(r.Context(), access, u.ID, sess.CSRF)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.OrderDetailPage(data)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: fmt.Sprintf("Order #%d", access.Order.ID), Body: body})
}

func (s *Server) buildOrderDetail(ctx context.Context, access *orderAccess, viewerID int64, csrf string) (components.OrderDetailData, error) {
	o := access.Order

	gig, err := s.Store.GetGigBasic(ctx, o.GigID)
	if err != nil {
		return components.OrderDetailData{}, err
	}
	buyer, err := s.Store.GetUserByID(ctx, o.BuyerID)
	if err != nil {
		return components.OrderDetailData{}, err
	}
	seller, err := s.Store.GetUserByID(ctx, o.SellerID)
	if err != nil {
		return components.OrderDetailData{}, err
	}

	items, err := s.Store.GetOrderItems(ctx, o.ID)
	if err != nil {
		return components.OrderDetailData{}, err
	}
	lineItems := make([]components.CheckoutLineItem, 0, len(items))
	for _, it := range items {
		lineItems = append(lineItems, components.CheckoutLineItem{Name: it.Name, Price: formatMoney(it.PriceMinorUnits, it.Currency)})
	}

	deliveries, err := s.Store.ListDeliveries(ctx, o.ID)
	if err != nil {
		return components.OrderDetailData{}, err
	}
	deliveryViews := make([]components.DeliveryView, 0, len(deliveries))
	for _, d := range deliveries {
		deliveryViews = append(deliveryViews, components.DeliveryView{Version: d.Version, Message: d.Message, CreatedAt: formatTime(d.CreatedAt)})
	}
	deliveryAttachments, err := s.Store.ListOrderAttachments(ctx, o.ID, store.AttachmentDelivery)
	if err != nil {
		return components.OrderDetailData{}, err
	}

	messages, err := s.Store.ListOrderMessages(ctx, o.ID)
	if err != nil {
		return components.OrderDetailData{}, err
	}
	messageViews := make([]components.OrderMessageView, 0, len(messages))
	for _, m := range messages {
		messageViews = append(messageViews, components.OrderMessageView{
			SenderName: m.SenderName, Body: m.Body, CreatedAt: formatTime(m.CreatedAt), Mine: m.SenderID == viewerID,
		})
	}

	cancellation, err := s.Store.GetOpenCancellationRequest(ctx, o.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return components.OrderDetailData{}, err
	}
	var cancellationView *components.CancellationView
	canCancelRequest := (access.IsBuyer || access.IsSeller) && o.Status == services.OrderInProgress
	canCancelResolve := false
	if cancellation != nil {
		cancellationView = &components.CancellationView{
			RequestedByMe: cancellation.RequestedBy == viewerID, Reason: cancellation.Reason, Status: cancellation.Status,
		}
		canCancelRequest = false
		canCancelResolve = (access.IsBuyer || access.IsSeller) && cancellation.RequestedBy != viewerID
	}

	dispute, err := s.Store.GetLatestDispute(ctx, o.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return components.OrderDetailData{}, err
	}
	var disputeView *components.DisputeView
	var disputeAttachments []store.OrderAttachment
	disputeOpen := false
	canDispute := (access.IsBuyer || access.IsSeller) && isDisputable(o.Status)
	if dispute != nil {
		disputeView = &components.DisputeView{
			OpenedByMe: dispute.OpenedBy == viewerID, Reason: dispute.Reason, Status: dispute.Status, Decision: dispute.Decision,
		}
		disputeOpen = dispute.Status == store.DisputeOpen
		if disputeOpen {
			canDispute = false
			disputeAttachments, err = s.Store.ListOrderAttachments(ctx, o.ID, store.AttachmentDispute)
			if err != nil {
				return components.OrderDetailData{}, err
			}
		}
	}

	review, err := s.Store.GetReviewByOrder(ctx, o.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return components.OrderDetailData{}, err
	}
	var reviewView *components.ReviewView
	if review != nil {
		reviewView = &components.ReviewView{Rating: review.Rating, Body: review.Body}
	}

	return components.OrderDetailData{
		ID: o.ID, GigSlug: gig.Slug, GigTitle: gig.Title, StatusLabel: orderStatusLabel(o.Status),
		BuyerName: buyer.Name, SellerName: seller.Name,
		IsBuyer: access.IsBuyer, IsSeller: access.IsSeller, IsAdmin: access.IsAdmin,
		Requirements: o.Requirements, Items: lineItems,
		Subtotal: formatMoney(o.SubtotalMinorUnits, o.Currency), PlatformFee: formatMoney(o.PlatformFeeMinorUnits, o.Currency),
		Total: formatMoney(o.TotalMinorUnits, o.Currency), Timeline: orderTimeline(o),

		RevisionsUsed: o.RevisionsUsed, RevisionsAllowed: o.RevisionsAllowed,
		Messages: messageViews, Deliveries: deliveryViews, DeliveryAttachment: toAttachmentViews(deliveryAttachments),
		Cancellation: cancellationView, Dispute: disputeView, DisputeAttachment: toAttachmentViews(disputeAttachments),
		Review: reviewView,

		CanMessage: (access.IsBuyer || access.IsSeller) && o.Status != services.OrderCancelled && o.Status != services.OrderClosed,
		CanDeliver: access.IsSeller && (o.Status == services.OrderInProgress || o.Status == services.OrderRevisionRequested),
		CanRevise:  access.IsBuyer && o.Status == services.OrderDelivered && o.RevisionsUsed < o.RevisionsAllowed,
		CanAccept:  access.IsBuyer && o.Status == services.OrderDelivered,

		CanCancelRequest: canCancelRequest,
		CanCancelResolve: canCancelResolve,

		CanDispute:        canDispute,
		CanResolveDispute: access.IsAdmin && disputeOpen,

		CanReview: access.IsBuyer && o.Status == services.OrderAccepted && review == nil,

		CSRF: csrf,
	}, nil
}

func isDisputable(status string) bool {
	switch status {
	case services.OrderInProgress, services.OrderDelivered, services.OrderRevisionRequested, services.OrderAccepted:
		return true
	default:
		return false
	}
}

func orderTimeline(o *store.Order) []components.TimelineEntry {
	entries := []components.TimelineEntry{{Label: "Placed", At: formatTime(o.CreatedAt)}}
	add := func(label string, t *time.Time) {
		if t != nil {
			entries = append(entries, components.TimelineEntry{Label: label, At: formatTime(*t)})
		}
	}
	add("Paid", o.PaidAt)
	add("Delivered", o.DeliveredAt)
	add("Accepted", o.AcceptedAt)
	add("Cancelled", o.CancelledAt)
	add("Closed", o.ClosedAt)
	return entries
}

func toAttachmentViews(atts []store.OrderAttachment) []components.AttachmentView {
	out := make([]components.AttachmentView, 0, len(atts))
	for _, a := range atts {
		out = append(out, components.AttachmentView{ID: a.ID, FileName: a.FileName, Kind: a.Kind})
	}
	return out
}

// notify records an in-app notification and enqueues best-effort email
// delivery for it. Failures are logged, not surfaced to the triggering
// request: a buyer accepting a delivery should not fail because a
// notification could not be written.
func (s *Server) notify(ctx context.Context, userID int64, kind, body, link string) {
	if _, err := s.Store.CreateNotification(ctx, userID, kind, body, link); err != nil {
		s.Log.Error("create notification", "error", err)
		return
	}
	if _, err := s.Store.EnqueueJob(ctx, notificationEmailJobKind, map[string]any{
		"user_id": userID, "body": body, "link": link,
	}, time.Time{}, 3); err != nil {
		s.Log.Error("enqueue notification email", "error", err)
	}
}

// notificationEmailJobKind is the job kind the worker registers a handler
// for (main.notificationEmailKind); duplicated here as a string literal
// since handlers cannot import the main package.
const notificationEmailJobKind = "notification.email"

func (s *Server) flashError(r *http.Request, text string) {
	sess := s.sessionFrom(r)
	if sess == nil {
		return
	}
	if err := s.Store.SetFlash(r.Context(), sess.ID, &store.Flash{Kind: "error", Text: text}); err != nil {
		s.Log.Error("set flash", "error", err)
	}
}

func orderRedirect(id int64) string { return fmt.Sprintf("/orders/%d", id) }

// orderMessageCreate posts a message to an order's thread.
func (s *Server) orderMessageCreate(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsBuyer && !access.IsSeller {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	body := strings.TrimSpace(r.FormValue("message_body"))
	if body == "" {
		s.flashError(r, "Message cannot be empty.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if len(body) > maxOrderMessageLen {
		body = body[:maxOrderMessageLen]
	}
	u := s.userFrom(r)
	if _, err := s.Store.CreateOrderMessage(r.Context(), access.Order.ID, u.ID, body); err != nil {
		s.renderError(w, err)
		return
	}
	recipient := access.Order.SellerID
	if access.IsSeller {
		recipient = access.Order.BuyerID
	}
	s.notify(r.Context(), recipient, "order.message", fmt.Sprintf("New message on order #%d", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderDeliver lets the seller submit a new delivery version, moving the
// order to "delivered".
func (s *Server) orderDeliver(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsSeller {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	from := access.Order.Status
	if !services.CanTransitionOrder(from, services.OrderDelivered) {
		s.flashError(r, "This order cannot be delivered in its current state.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.UploadMaxBytes+uploadFormOverhead)
	if err := r.ParseMultipartForm(s.Cfg.UploadMaxBytes); err != nil {
		s.flashError(r, "The upload was too large or malformed.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	message := strings.TrimSpace(r.FormValue("delivery_message"))
	if message == "" {
		s.flashError(r, "Please describe what you are delivering.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}

	u := s.userFrom(r)
	if _, err := s.Store.CreateDelivery(r.Context(), access.Order.ID, u.ID, message); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.saveOptionalOrderFile(r, access.Order.ID, u.ID, "delivery_file", store.AttachmentDelivery); err != nil {
		s.flashError(r, uploadErrorMessage(err))
	}
	if err := s.Store.TransitionOrder(r.Context(), access.Order.ID, from, services.OrderDelivered); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "order.delivered", "order", fmt.Sprintf("%d", access.Order.ID), nil)
	s.notify(r.Context(), access.Order.BuyerID, "order.delivered", fmt.Sprintf("Order #%d has a new delivery", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// saveOptionalOrderFile saves an optional multipart file field into private
// order storage. A field the buyer left empty is not an error.
func (s *Server) saveOptionalOrderFile(r *http.Request, orderID, uploaderID int64, field, kind string) error {
	file, header, err := r.FormFile(field)
	if err != nil {
		if errors.Is(err, http.ErrMissingFile) {
			return nil
		}
		return err
	}
	defer file.Close()

	path, err := s.PrivateStorage.SaveOrderFile(fmt.Sprintf("orders/%d", orderID), file, header)
	if err != nil {
		return err
	}
	_, err = s.Store.CreateOrderAttachment(r.Context(), orderID, uploaderID, kind, path, header.Filename)
	return err
}

// orderRevise lets the buyer request a revision on a delivered order,
// consuming one unit of the package's revision allowance.
func (s *Server) orderRevise(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsBuyer {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	from := access.Order.Status
	if !services.CanTransitionOrder(from, services.OrderRevisionRequested) {
		s.flashError(r, "A revision cannot be requested in this order's current state.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	reason := strings.TrimSpace(r.FormValue("revise_reason"))
	if reason == "" {
		s.flashError(r, "Please describe what needs to change.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if len(reason) > maxOrderReasonLen {
		reason = reason[:maxOrderReasonLen]
	}

	u := s.userFrom(r)
	if _, err := s.Store.CreateRevisionRequest(r.Context(), access.Order.ID, u.ID, reason); err != nil {
		if errors.Is(err, store.ErrRevisionLimitReached) {
			s.flashError(r, "This order has no revisions remaining.")
			http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
			return
		}
		s.renderError(w, err)
		return
	}
	if err := s.Store.TransitionOrder(r.Context(), access.Order.ID, from, services.OrderRevisionRequested); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "order.revision_requested", "order", fmt.Sprintf("%d", access.Order.ID), nil)
	s.notify(r.Context(), access.Order.SellerID, "order.revision_requested", fmt.Sprintf("A revision was requested on order #%d", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderAccept lets the buyer accept a delivery.
func (s *Server) orderAccept(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsBuyer {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	from := access.Order.Status
	if !services.CanTransitionOrder(from, services.OrderAccepted) {
		s.flashError(r, "This order cannot be accepted in its current state.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if err := s.Store.TransitionOrder(r.Context(), access.Order.ID, from, services.OrderAccepted); err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	s.audit(r.Context(), &u.ID, r, "order.accepted", "order", fmt.Sprintf("%d", access.Order.ID), nil)
	s.notify(r.Context(), access.Order.SellerID, "order.accepted", fmt.Sprintf("Order #%d was accepted", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderCancelRequest lets either party ask to cancel an in-progress order.
func (s *Server) orderCancelRequest(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsBuyer && !access.IsSeller {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	from := access.Order.Status
	if !services.CanTransitionOrder(from, services.OrderCancelRequested) {
		s.flashError(r, "Cancellation cannot be requested in this order's current state.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	reason := strings.TrimSpace(r.FormValue("cancel_reason"))
	if reason == "" {
		s.flashError(r, "Please explain why you are requesting cancellation.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if len(reason) > maxOrderReasonLen {
		reason = reason[:maxOrderReasonLen]
	}

	u := s.userFrom(r)
	if _, err := s.Store.CreateCancellationRequest(r.Context(), access.Order.ID, u.ID, reason); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.TransitionOrder(r.Context(), access.Order.ID, from, services.OrderCancelRequested); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "order.cancel_requested", "order", fmt.Sprintf("%d", access.Order.ID), nil)
	recipient := access.Order.SellerID
	if access.IsSeller {
		recipient = access.Order.BuyerID
	}
	s.notify(r.Context(), recipient, "order.cancel_requested", fmt.Sprintf("Cancellation requested on order #%d", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderCancelResolve lets the party who did not request cancellation approve
// or decline it.
func (s *Server) orderCancelResolve(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsBuyer && !access.IsSeller {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	cancellation, err := s.Store.GetOpenCancellationRequest(r.Context(), access.Order.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.flashError(r, "There is no pending cancellation request.")
			http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
			return
		}
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	if cancellation.RequestedBy == u.ID {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	decision := r.FormValue("decision")

	from := access.Order.Status
	var to, resolution string
	switch decision {
	case "approve":
		to, resolution = services.OrderCancelled, store.CancellationApproved
	case "decline":
		to, resolution = services.OrderInProgress, store.CancellationDeclined
	default:
		http.Error(w, "invalid decision", http.StatusUnprocessableEntity)
		return
	}
	if !services.CanTransitionOrder(from, to) {
		s.flashError(r, "This order's state changed before the cancellation could be resolved.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if err := s.Store.ResolveCancellationRequest(r.Context(), cancellation.ID, resolution); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.TransitionOrder(r.Context(), access.Order.ID, from, to); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "order.cancel_resolved", "order", fmt.Sprintf("%d", access.Order.ID), map[string]any{"decision": decision})
	s.notify(r.Context(), cancellation.RequestedBy, "order.cancel_resolved", fmt.Sprintf("Your cancellation request on order #%d was %sd", access.Order.ID, decision), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderDisputeOpen lets either party escalate an order for admin review.
func (s *Server) orderDisputeOpen(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsBuyer && !access.IsSeller {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	from := access.Order.Status
	if !services.CanTransitionOrder(from, services.OrderDisputed) {
		s.flashError(r, "A dispute cannot be opened in this order's current state.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.UploadMaxBytes+uploadFormOverhead)
	if err := r.ParseMultipartForm(s.Cfg.UploadMaxBytes); err != nil {
		s.flashError(r, "The upload was too large or malformed.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	reason := strings.TrimSpace(r.FormValue("dispute_reason"))
	if reason == "" {
		s.flashError(r, "Please describe the issue.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if len(reason) > maxOrderReasonLen {
		reason = reason[:maxOrderReasonLen]
	}

	u := s.userFrom(r)
	if _, err := s.Store.CreateDispute(r.Context(), access.Order.ID, u.ID, reason); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.saveOptionalOrderFile(r, access.Order.ID, u.ID, "dispute_evidence", store.AttachmentDispute); err != nil {
		s.flashError(r, uploadErrorMessage(err))
	}
	if err := s.Store.TransitionOrder(r.Context(), access.Order.ID, from, services.OrderDisputed); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "order.dispute_opened", "order", fmt.Sprintf("%d", access.Order.ID), nil)
	recipient := access.Order.SellerID
	if access.IsSeller {
		recipient = access.Order.BuyerID
	}
	s.notify(r.Context(), recipient, "order.dispute_opened", fmt.Sprintf("A dispute was opened on order #%d", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderDisputeResolve lets an admin record a decision on an open dispute and
// move the order to whichever outcome the decision calls for.
func (s *Server) orderDisputeResolve(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	dispute, err := s.Store.GetOpenDispute(r.Context(), access.Order.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.flashError(r, "There is no open dispute on this order.")
			http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
			return
		}
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	decisionNote := strings.TrimSpace(r.FormValue("dispute_decision"))
	if decisionNote == "" {
		s.flashError(r, "Please record a decision note.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if len(decisionNote) > maxOrderDecisionLen {
		decisionNote = decisionNote[:maxOrderDecisionLen]
	}
	outcome := r.FormValue("dispute_outcome")
	switch outcome {
	case services.OrderInProgress, services.OrderCancelled, services.OrderClosed:
	default:
		http.Error(w, "invalid outcome", http.StatusUnprocessableEntity)
		return
	}
	from := access.Order.Status
	if !services.CanTransitionOrder(from, outcome) {
		s.flashError(r, "That outcome is not valid for this order's current state.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}

	u := s.userFrom(r)
	if err := s.Store.ResolveDispute(r.Context(), dispute.ID, u.ID, decisionNote); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.TransitionOrder(r.Context(), access.Order.ID, from, outcome); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "order.dispute_resolved", "order", fmt.Sprintf("%d", access.Order.ID), map[string]any{"outcome": outcome})
	s.notify(r.Context(), access.Order.BuyerID, "order.dispute_resolved", fmt.Sprintf("The dispute on order #%d was resolved", access.Order.ID), orderRedirect(access.Order.ID))
	s.notify(r.Context(), access.Order.SellerID, "order.dispute_resolved", fmt.Sprintf("The dispute on order #%d was resolved", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderReviewCreate lets the buyer leave a review after accepting delivery.
func (s *Server) orderReviewCreate(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	if !access.IsBuyer {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if access.Order.Status != services.OrderAccepted {
		s.flashError(r, "You can review an order once you have accepted delivery.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	rating, err := strconv.Atoi(r.FormValue("review_rating"))
	if err != nil || rating < 1 || rating > 5 {
		s.flashError(r, "Please choose a rating between 1 and 5.")
		http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
		return
	}
	body := strings.TrimSpace(r.FormValue("review_body"))

	u := s.userFrom(r)
	if _, err := s.Store.CreateReview(r.Context(), access.Order.ID, access.Order.GigID, u.ID, access.Order.SellerID, rating, body); err != nil {
		if errors.Is(err, store.ErrAlreadyReviewed) {
			s.flashError(r, "You have already reviewed this order.")
			http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
			return
		}
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "order.reviewed", "order", fmt.Sprintf("%d", access.Order.ID), map[string]any{"rating": rating})
	s.notify(r.Context(), access.Order.SellerID, "order.reviewed", fmt.Sprintf("Order #%d received a review", access.Order.ID), orderRedirect(access.Order.ID))
	http.Redirect(w, r, orderRedirect(access.Order.ID), http.StatusSeeOther)
}

// orderAttachmentServe streams a private order file (delivery or dispute
// evidence) after checking the caller is the order's buyer, seller, or an
// admin. It is never reachable through the public /media/ static mount.
func (s *Server) orderAttachmentServe(w http.ResponseWriter, r *http.Request) {
	access, ok := s.loadOrderAccess(w, r)
	if !ok {
		return
	}
	attachmentID, err := strconv.ParseInt(r.PathValue("attachmentID"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	att, err := s.Store.GetOrderAttachment(r.Context(), attachmentID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	if att.OrderID != access.Order.ID {
		s.notFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.PrivateStorage.Root, filepath.FromSlash(att.FilePath)))
}
