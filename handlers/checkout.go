package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

const maxRequirementsLen = 5000

// checkoutStart begins (or resumes) a checkout draft for a gig package and
// sends the buyer to the requirements step. There is at most one draft per
// (buyer, gig) pair, so revisiting a gig's checkout always resumes the same
// draft instead of creating duplicates.
func (s *Server) checkoutStart(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	slug := r.PathValue("slug")
	gig, err := s.Store.GetGigBySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	if gig.SellerID == u.ID {
		http.Error(w, "you cannot order your own gig", http.StatusForbidden)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	packageID, err := strconv.ParseInt(r.FormValue("package_id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if _, ok := findPackage(gig, packageID); !ok {
		s.notFound(w, r)
		return
	}

	draft, err := s.Store.UpsertDraftOrder(r.Context(), u.ID, gig.ID, packageID)
	if err != nil {
		s.renderError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/checkout/%d/requirements", draft.ID), http.StatusSeeOther)
}

// loadOwnedDraft resolves the {id} path value to a checkout draft owned by
// the signed-in buyer, and the gig it targets. It writes a 404 and returns
// ok=false for a missing draft, a draft belonging to someone else, or a gig
// that is no longer publicly visible (paused, archived, or unmoderated).
func (s *Server) loadOwnedDraft(w http.ResponseWriter, r *http.Request) (*store.DraftOrder, *store.GigDetail, bool) {
	u := s.userFrom(r)
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return nil, nil, false
	}
	draft, err := s.Store.GetDraftOrderByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return nil, nil, false
		}
		s.renderError(w, err)
		return nil, nil, false
	}
	if draft.BuyerID != u.ID {
		s.notFound(w, r)
		return nil, nil, false
	}
	gig, err := s.Store.GetGigByID(r.Context(), draft.GigID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return nil, nil, false
		}
		s.renderError(w, err)
		return nil, nil, false
	}
	return draft, gig, true
}

func findPackage(gig *store.GigDetail, id int64) (store.GigPackage, bool) {
	for _, p := range gig.Packages {
		if p.ID == id {
			return p, true
		}
	}
	return store.GigPackage{}, false
}

func findAddon(gig *store.GigDetail, id int64) (store.GigAddon, bool) {
	for _, a := range gig.Addons {
		if a.ID == id {
			return a, true
		}
	}
	return store.GigAddon{}, false
}

// selectedAddons filters a gig's add-ons down to the ones in ids, in the
// gig's canonical order, ignoring any ID that no longer belongs to the gig.
func selectedAddons(gig *store.GigDetail, ids []int64) []store.GigAddon {
	want := make(map[int64]bool, len(ids))
	for _, id := range ids {
		want[id] = true
	}
	var out []store.GigAddon
	for _, a := range gig.Addons {
		if want[a.ID] {
			out = append(out, a)
		}
	}
	return out
}

// checkoutRequirementsForm renders the requirements + add-ons step.
func (s *Server) checkoutRequirementsForm(w http.ResponseWriter, r *http.Request) {
	draft, gig, ok := s.loadOwnedDraft(w, r)
	if !ok {
		return
	}
	pkg, ok := findPackage(gig, draft.PackageID)
	if !ok {
		s.notFound(w, r)
		return
	}
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.CheckoutRequirementsPage(s.requirementsFormData(draft, gig, pkg, sess.CSRF, nil))
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Checkout - requirements", Body: body})
}

func (s *Server) requirementsFormData(draft *store.DraftOrder, gig *store.GigDetail, pkg store.GigPackage, csrf string, errs map[string]string) components.CheckoutRequirementsData {
	selected := make(map[int64]bool, len(draft.AddonIDs))
	for _, id := range draft.AddonIDs {
		selected[id] = true
	}
	var addons []components.CheckoutAddonOption
	for _, a := range gig.Addons {
		addons = append(addons, components.CheckoutAddonOption{
			ID: a.ID, Name: a.Name, Description: a.Description,
			Price: formatMoney(a.PriceMinorUnits, a.Currency), Selected: selected[a.ID],
		})
	}
	return components.CheckoutRequirementsData{
		DraftID: draft.ID, GigSlug: gig.Slug, GigTitle: gig.Title,
		PackageName: pkg.Name, PackagePrice: formatMoney(pkg.PriceMinorUnits, pkg.Currency),
		Addons: addons, Requirements: draft.Requirements, CSRF: csrf, Errors: errs,
	}
}

// checkoutRequirementsSubmit validates and saves the requirements + add-ons
// step, then moves to review.
func (s *Server) checkoutRequirementsSubmit(w http.ResponseWriter, r *http.Request) {
	draft, gig, ok := s.loadOwnedDraft(w, r)
	if !ok {
		return
	}
	pkg, ok := findPackage(gig, draft.PackageID)
	if !ok {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	requirements := strings.TrimSpace(r.FormValue("requirements"))
	errs := map[string]string{}
	if requirements == "" {
		errs["requirements"] = "Please describe what the seller needs to get started."
	} else if len(requirements) > maxRequirementsLen {
		errs["requirements"] = fmt.Sprintf("Requirements must be at most %d characters.", maxRequirementsLen)
	}

	var addonIDs []int64
	for _, raw := range r.Form["addon_id"] {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			continue
		}
		if _, found := findAddon(gig, id); found {
			addonIDs = append(addonIDs, id)
		}
	}

	if len(errs) > 0 {
		sess, err := s.ensureSession(w, r)
		if err != nil {
			s.renderError(w, err)
			return
		}
		draft.Requirements = requirements
		draft.AddonIDs = addonIDs
		body, err := components.CheckoutRequirementsPage(s.requirementsFormData(draft, gig, pkg, sess.CSRF, errs))
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Checkout - requirements", Body: body})
		return
	}

	if err := s.Store.SetDraftOrderRequirements(r.Context(), draft.ID, requirements, addonIDs); err != nil {
		s.renderError(w, err)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/checkout/%d/review", draft.ID), http.StatusSeeOther)
}

// availablePaymentMethods builds the review step's payment-method options
// from whichever provider adapters are actually configured. Card (Stripe)
// is offered first and pre-selected when available; Bitcoin/Lightning
// (BTCPay) is offered whenever the "btcpay" provider is registered.
func (s *Server) availablePaymentMethods() []components.CheckoutPaymentMethodOption {
	var opts []components.CheckoutPaymentMethodOption
	if s.Providers.Has("stripe") {
		opts = append(opts, components.CheckoutPaymentMethodOption{Value: "card", Label: "Card (Stripe)", Checked: true})
	}
	if s.Providers.Has("btcpay") {
		opts = append(opts, components.CheckoutPaymentMethodOption{
			Value: "bitcoin", Label: "Bitcoin / Lightning (BTCPay)", Checked: len(opts) == 0,
		})
	}
	if s.Providers.Has("evm-base") {
		opts = append(opts, components.CheckoutPaymentMethodOption{
			Value: "usdc-base", Label: "USDC on Base", Checked: len(opts) == 0,
		})
	}
	if s.Providers.Has("evm-polygon") {
		opts = append(opts, components.CheckoutPaymentMethodOption{
			Value: "usdc-polygon", Label: "USDC on Polygon", Checked: len(opts) == 0,
		})
	}
	return opts
}

// checkoutReviewData recomputes authoritative totals from stored package/
// add-on prices for both the review page and the confirm handler, so the
// two can never disagree about what the buyer is about to pay.
func checkoutReviewData(pkg store.GigPackage, addons []store.GigAddon, platformFeeBps int) (items []components.CheckoutLineItem, totals services.OrderTotals) {
	items = append(items, components.CheckoutLineItem{
		Name: pkg.Name + " package", Price: formatMoney(pkg.PriceMinorUnits, pkg.Currency),
	})
	addonPrices := make([]int64, 0, len(addons))
	for _, a := range addons {
		items = append(items, components.CheckoutLineItem{Name: a.Name, Price: formatMoney(a.PriceMinorUnits, a.Currency)})
		addonPrices = append(addonPrices, a.PriceMinorUnits)
	}
	totals = services.CalculateOrderTotals(pkg.PriceMinorUnits, addonPrices, platformFeeBps)
	return items, totals
}

// checkoutReview renders the final review, payment notice, and confirm step.
func (s *Server) checkoutReview(w http.ResponseWriter, r *http.Request) {
	draft, gig, ok := s.loadOwnedDraft(w, r)
	if !ok {
		return
	}
	pkg, ok := findPackage(gig, draft.PackageID)
	if !ok {
		s.notFound(w, r)
		return
	}
	if strings.TrimSpace(draft.Requirements) == "" {
		http.Redirect(w, r, fmt.Sprintf("/checkout/%d/requirements", draft.ID), http.StatusSeeOther)
		return
	}
	addons := selectedAddons(gig, draft.AddonIDs)
	items, totals := checkoutReviewData(pkg, addons, s.Cfg.PlatformFeeBps)

	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.CheckoutReviewPage(components.CheckoutReviewData{
		DraftID: draft.ID, GigSlug: gig.Slug, GigTitle: gig.Title, Requirements: draft.Requirements,
		Items:          items,
		Subtotal:       formatMoney(totals.SubtotalMinorUnits, pkg.Currency),
		PlatformFee:    formatMoney(totals.PlatformFeeMinorUnits, pkg.Currency),
		Total:          formatMoney(totals.TotalMinorUnits, pkg.Currency),
		PaymentMethods: s.availablePaymentMethods(),
		CSRF:           sess.CSRF,
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Checkout - review", Body: body})
}

// checkoutConfirm creates the order in pending_payment from the draft's
// server-stored selections, never from anything supplied in this request.
func (s *Server) checkoutConfirm(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	draft, gig, ok := s.loadOwnedDraft(w, r)
	if !ok {
		return
	}
	pkg, ok := findPackage(gig, draft.PackageID)
	if !ok {
		s.notFound(w, r)
		return
	}
	if strings.TrimSpace(draft.Requirements) == "" {
		http.Redirect(w, r, fmt.Sprintf("/checkout/%d/requirements", draft.ID), http.StatusSeeOther)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	method := r.FormValue("payment_method")
	addons := selectedAddons(gig, draft.AddonIDs)
	_, totals := checkoutReviewData(pkg, addons, s.Cfg.PlatformFeeBps)

	deliveryDays := pkg.DeliveryDays
	items := []store.OrderItemInput{{
		Kind: "package", Name: pkg.Name, Description: pkg.Description,
		PriceMinorUnits: pkg.PriceMinorUnits, Currency: pkg.Currency, DeliveryDays: &deliveryDays,
	}}
	for _, a := range addons {
		items = append(items, store.OrderItemInput{
			Kind: "addon", Name: a.Name, Description: a.Description,
			PriceMinorUnits: a.PriceMinorUnits, Currency: a.Currency,
		})
	}

	order, err := s.Store.CreateOrderFromDraft(r.Context(), draft.ID, u.ID, gig.SellerID, gig.ID, draft.Requirements,
		items, totals.SubtotalMinorUnits, totals.PlatformFeeMinorUnits, totals.TotalMinorUnits, pkg.Currency, pkg.Revisions)
	if err != nil {
		s.renderError(w, err)
		return
	}

	s.audit(r.Context(), &u.ID, r, "order.created", "order", fmt.Sprintf("%d", order.ID),
		map[string]any{"gig_id": gig.ID, "total_minor_units": order.TotalMinorUnits})
	s.checkFraudSignals(r, u.ID, order)

	s.startPayment(w, r, u, order, method)
}
