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

const (
	maxGigTitleLen  = 140
	maxGigDescLen   = 5000
	maxPortfolioLen = 140

	// uploadFormOverhead allows room for multipart boundaries and the other
	// text fields alongside the file itself, on top of the image size cap.
	uploadFormOverhead = 16 << 10
)

var gigTiers = [3]string{store.TierBasic, store.TierStandard, store.TierPremium}

// sellStart grants the seller role to the signed-in buyer and starts
// onboarding, then sends them to fill in their public profile. Adding a role
// is a privilege change, so the session is rotated the same way login,
// password changes, and MFA toggles are.
func (s *Server) sellStart(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	sess := s.sessionFrom(r)
	if err := s.Store.AddRole(r.Context(), u.ID, store.RoleSeller); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.EnsureSellerOnboarding(r.Context(), u.ID); err != nil {
		s.renderError(w, err)
		return
	}

	newRaw, newHash, err := services.GenerateSessionToken()
	if err != nil {
		s.renderError(w, err)
		return
	}
	newCSRF, err := services.GenerateCSRF()
	if err != nil {
		s.renderError(w, err)
		return
	}
	if _, err := s.Store.RotateSession(r.Context(), sess.ID, u.ID, newHash, newCSRF, s.Cfg.SessionTTL, r.UserAgent(), clientIP(r)); err != nil {
		s.renderError(w, err)
		return
	}
	http.SetCookie(w, services.SessionCookie(s.Cfg, newRaw, s.Cfg.SessionTTL))

	s.audit(r.Context(), &u.ID, r, "user.became_seller", "user", fmt.Sprintf("%d", u.ID), nil)
	http.Redirect(w, r, "/sell/profile", http.StatusSeeOther)
}

// requireSeller gates a handler behind the seller role, same as requireRole
// but named for readability at call sites.
func (s *Server) requireSeller(next http.HandlerFunc) http.HandlerFunc {
	return s.requireRole(store.RoleSeller, next)
}

// sellDashboard is a seller's own console: onboarding status and gig list.
func (s *Server) sellDashboard(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	onboarding, err := s.Store.GetSellerOnboarding(r.Context(), u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			// A seller who was granted the role outside of /sell/start (or
			// before this migration existed) may not have a row yet.
			if err := s.Store.EnsureSellerOnboarding(r.Context(), u.ID); err != nil {
				s.renderError(w, err)
				return
			}
			onboarding, err = s.Store.GetSellerOnboarding(r.Context(), u.ID)
		}
		if err != nil {
			s.renderError(w, err)
			return
		}
	}

	gigs, err := s.Store.ListGigsBySeller(r.Context(), u.ID)
	if err != nil {
		s.renderError(w, err)
		return
	}
	rows := make([]components.SellerGigRow, 0, len(gigs))
	for _, g := range gigs {
		rows = append(rows, components.SellerGigRow{ID: g.ID, Title: g.Title, Status: g.Status})
	}

	recentOrders, _, err := s.Store.ListOrdersForSeller(r.Context(), u.ID, 1, dashboardRecentOrders)
	if err != nil {
		s.renderError(w, err)
		return
	}

	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.SellerDashboardPage(components.SellerDashboardData{
		CSRF:             sess.CSRF,
		OnboardingState:  onboardingLabel(onboarding.KYCState),
		CanRequestReview: onboarding.KYCState == store.KYCNotStarted || onboarding.KYCState == store.KYCRejected,
		Gigs:             rows,
		RecentOrders:     toOrderRows(recentOrders),
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Seller dashboard", Body: body})
}

func onboardingLabel(state string) string {
	switch state {
	case store.KYCPending:
		return "Submitted for review"
	case store.KYCVerified:
		return "Verified"
	case store.KYCRejected:
		return "Not approved"
	default:
		return "Not started"
	}
}

// sellOnboardingSubmit moves a seller's onboarding from not_started (or
// rejected) to pending. There is no reviewer workflow yet (Phase 5); this
// only records the seller's intent.
func (s *Server) sellOnboardingSubmit(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	if err := s.Store.SetOnboardingState(r.Context(), u.ID, store.KYCPending); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "seller.onboarding_submitted", "user", fmt.Sprintf("%d", u.ID), nil)
	http.Redirect(w, r, "/sell", http.StatusSeeOther)
}

// sellProfileForm renders the seller's public-profile edit form.
func (s *Server) sellProfileForm(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	form := components.SellerProfileFormData{}
	if p, err := s.Store.GetSellerProfile(r.Context(), u.ID); err == nil {
		form.DisplayName, form.Bio, form.Location = p.DisplayName, p.Bio, p.Location
	} else if !errors.Is(err, store.ErrNotFound) {
		s.renderError(w, err)
		return
	} else {
		// First time setting up a seller profile: default to the account name.
		form.DisplayName = u.Name
	}
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	form.CSRF = sess.CSRF
	body, err := components.SellerProfileFormPage(form)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Seller profile", Body: body})
}

// sellProfileUpdate saves the seller's public profile.
func (s *Server) sellProfileUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	displayName := strings.TrimSpace(r.FormValue("display_name"))
	bio := strings.TrimSpace(r.FormValue("bio"))
	location := strings.TrimSpace(r.FormValue("location"))

	form := components.SellerProfileFormData{DisplayName: displayName, Bio: bio, Location: location, Errors: map[string]string{}}
	if displayName == "" {
		form.Errors["display_name"] = "Please enter a display name."
	} else if len(displayName) > maxNameLen {
		form.Errors["display_name"] = "Display name must be at most 100 characters."
	}
	if len(form.Errors) > 0 {
		sess, err := s.ensureSession(w, r)
		if err != nil {
			s.renderError(w, err)
			return
		}
		form.CSRF = sess.CSRF
		body, err := components.SellerProfileFormPage(form)
		if err != nil {
			s.renderError(w, err)
			return
		}
		s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Seller profile", Body: body})
		return
	}

	if err := s.Store.UpsertSellerProfile(r.Context(), u.ID, displayName, bio, location); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.EnsureSellerOnboarding(r.Context(), u.ID); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "seller.profile_updated", "user", fmt.Sprintf("%d", u.ID), nil)
	http.Redirect(w, r, "/sell", http.StatusSeeOther)
}

// sellPortfolioForm lists a seller's portfolio and shows the upload form.
func (s *Server) sellPortfolioForm(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	items, err := s.Store.ListPortfolioItems(r.Context(), u.ID)
	if err != nil {
		s.renderError(w, err)
		return
	}
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.PortfolioPage(components.PortfolioPageData{CSRF: sess.CSRF, Items: toPortfolioViews(items)})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Portfolio", Body: body})
}

// sellPortfolioCreate validates and saves a new portfolio item.
func (s *Server) sellPortfolioCreate(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	// Cap the request body before parsing: ParseMultipartForm's maxMemory
	// argument only controls the memory/disk split, it does not reject large
	// uploads, so an unbounded request could still make the server buffer an
	// oversized file to disk before SaveImage's size check ever runs.
	r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.UploadMaxBytes+uploadFormOverhead)
	if err := r.ParseMultipartForm(s.Cfg.UploadMaxBytes); err != nil {
		s.portfolioFormError(w, r, map[string]string{"image": "The upload was too large or malformed."})
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))

	errs := map[string]string{}
	if title == "" {
		errs["title"] = "Please enter a title."
	} else if len(title) > maxPortfolioLen {
		errs["title"] = "Title must be at most 140 characters."
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		errs["image"] = "Please choose an image."
		s.portfolioFormError(w, r, errs)
		return
	}
	defer file.Close()

	if len(errs) > 0 {
		s.portfolioFormError(w, r, errs)
		return
	}

	path, err := s.Storage.SaveImage(fmt.Sprintf("portfolio/%d", u.ID), file, header)
	if err != nil {
		errs["image"] = uploadErrorMessage(err)
		s.portfolioFormError(w, r, errs)
		return
	}

	if _, err := s.Store.CreatePortfolioItem(r.Context(), u.ID, title, description, path); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "seller.portfolio_added", "user", fmt.Sprintf("%d", u.ID), nil)
	http.Redirect(w, r, "/sell/portfolio", http.StatusSeeOther)
}

func (s *Server) portfolioFormError(w http.ResponseWriter, r *http.Request, errs map[string]string) {
	u := s.userFrom(r)
	items, err := s.Store.ListPortfolioItems(r.Context(), u.ID)
	if err != nil {
		s.renderError(w, err)
		return
	}
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.PortfolioPage(components.PortfolioPageData{CSRF: sess.CSRF, Items: toPortfolioViews(items), Errors: errs})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: "Portfolio", Body: body})
}

func uploadErrorMessage(err error) string {
	switch {
	case errors.Is(err, services.ErrUnsupportedMediaType):
		return "That file type is not supported. Use JPEG, PNG, GIF, or WebP."
	case errors.Is(err, services.ErrFileTooLarge):
		return "That file is too large."
	default:
		return "That upload could not be saved."
	}
}

// sellGigNew renders a blank gig creation form.
func (s *Server) sellGigNew(w http.ResponseWriter, r *http.Request) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	form, err := s.blankGigForm(r, sess.CSRF)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.GigFormPage(form)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Create a gig", Body: body})
}

func (s *Server) blankGigForm(r *http.Request, csrf string) (components.GigFormData, error) {
	cats, err := s.Store.ListCategories(r.Context())
	if err != nil {
		return components.GigFormData{}, err
	}
	form := components.GigFormData{CSRF: csrf, Categories: toCategoryOptions(cats)}
	for i, tier := range gigTiers {
		form.Packages[i] = components.GigPackageForm{Tier: tier}
	}
	return form, nil
}

// sellGigCreate validates and creates a new gig.
func (s *Server) sellGigCreate(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	input, errs := s.parseGigForm(r)
	if len(errs) > 0 {
		s.gigFormError(w, r, input, errs, false, 0)
		return
	}

	gig, err := s.Store.CreateGig(r.Context(), u.ID, input.categoryID, input.title, input.description)
	if err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.ReplaceGigPackages(r.Context(), gig.ID, input.packages); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.ReplaceGigAddons(r.Context(), gig.ID, input.addons); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.SetGigTags(r.Context(), gig.ID, input.tags); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "gig.created", "gig", fmt.Sprintf("%d", gig.ID), nil)
	http.Redirect(w, r, fmt.Sprintf("/sell/gigs/%d/edit", gig.ID), http.StatusSeeOther)
}

// sellGigEdit renders the edit form for a gig the caller owns.
func (s *Server) sellGigEdit(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	gigID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	detail, err := s.Store.GetGigForOwner(r.Context(), gigID, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	form, err := s.gigFormFromDetail(r, detail, sess.CSRF)
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.GigFormPage(form)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: "Edit gig", Body: body})
}

func (s *Server) gigFormFromDetail(r *http.Request, detail *store.GigDetail, csrf string) (components.GigFormData, error) {
	cats, err := s.Store.ListCategories(r.Context())
	if err != nil {
		return components.GigFormData{}, err
	}
	form := components.GigFormData{
		CSRF: csrf, Editing: true, GigID: detail.ID,
		Title: detail.Title, Description: detail.Description,
		Categories: toCategoryOptions(cats),
		Tags:       strings.Join(detail.Tags, ", "),
		Media:      toMediaViews(detail.Media),
	}
	if detail.CategoryID != nil {
		form.CategoryID = strconv.FormatInt(*detail.CategoryID, 10)
	}
	for i, tier := range gigTiers {
		form.Packages[i] = components.GigPackageForm{Tier: tier}
		for _, p := range detail.Packages {
			if p.Tier == tier {
				form.Packages[i] = components.GigPackageForm{
					Tier: tier, Name: p.Name, Description: p.Description,
					Price:        formatMinorUnitsPlain(p.PriceMinorUnits),
					DeliveryDays: strconv.Itoa(p.DeliveryDays),
					Revisions:    strconv.Itoa(p.Revisions),
				}
			}
		}
	}
	for i := 0; i < len(form.Addons) && i < len(detail.Addons); i++ {
		a := detail.Addons[i]
		form.Addons[i] = components.GigAddonForm{
			Name: a.Name, Description: a.Description,
			Price: formatMinorUnitsPlain(a.PriceMinorUnits), DeliveryDays: strconv.Itoa(a.DeliveryDaysImpact),
		}
	}
	return form, nil
}

// sellGigUpdate validates and saves changes to a gig the caller owns.
func (s *Server) sellGigUpdate(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	gigID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if _, err := s.Store.GetGigForOwner(r.Context(), gigID, u.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	input, errs := s.parseGigForm(r)
	if len(errs) > 0 {
		s.gigFormError(w, r, input, errs, true, gigID)
		return
	}

	if err := s.Store.UpdateGig(r.Context(), gigID, u.ID, input.categoryID, input.title, input.description); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.ReplaceGigPackages(r.Context(), gigID, input.packages); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.ReplaceGigAddons(r.Context(), gigID, input.addons); err != nil {
		s.renderError(w, err)
		return
	}
	if err := s.Store.SetGigTags(r.Context(), gigID, input.tags); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "gig.updated", "gig", fmt.Sprintf("%d", gigID), nil)
	http.Redirect(w, r, fmt.Sprintf("/sell/gigs/%d/edit", gigID), http.StatusSeeOther)
}

// sellGigStatus transitions a gig's lifecycle status. Publishing requires at
// least one priced package.
func (s *Server) sellGigStatus(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	gigID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	status := r.FormValue("status")
	switch status {
	case store.GigActive, store.GigPaused, store.GigArchived:
	default:
		http.Error(w, "invalid status", http.StatusUnprocessableEntity)
		return
	}

	detail, err := s.Store.GetGigForOwner(r.Context(), gigID, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	if status == store.GigActive && len(detail.Packages) == 0 {
		if err := s.Store.SetFlash(r.Context(), s.sessionFrom(r).ID, &store.Flash{Kind: "error", Text: "Add at least one package before publishing."}); err != nil {
			s.Log.Error("set flash", "error", err)
		}
		http.Redirect(w, r, fmt.Sprintf("/sell/gigs/%d/edit", gigID), http.StatusSeeOther)
		return
	}

	if err := s.Store.SetGigStatus(r.Context(), gigID, u.ID, status); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "gig.status_changed", "gig", fmt.Sprintf("%d", gigID), map[string]any{"status": status})
	http.Redirect(w, r, "/sell", http.StatusSeeOther)
}

// sellGigMedia uploads an image for a gig the caller owns.
func (s *Server) sellGigMedia(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	gigID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	detail, err := s.Store.GetGigForOwner(r.Context(), gigID, u.ID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.UploadMaxBytes+uploadFormOverhead)
	if err := r.ParseMultipartForm(s.Cfg.UploadMaxBytes); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/sell/gigs/%d/edit", gigID), http.StatusSeeOther)
		return
	}
	file, header, err := r.FormFile("image")
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/sell/gigs/%d/edit", gigID), http.StatusSeeOther)
		return
	}
	defer file.Close()

	path, err := s.Storage.SaveImage(fmt.Sprintf("gigs/%d", gigID), file, header)
	if err != nil {
		if err := s.Store.SetFlash(r.Context(), s.sessionFrom(r).ID, &store.Flash{Kind: "error", Text: uploadErrorMessage(err)}); err != nil {
			s.Log.Error("set flash", "error", err)
		}
		http.Redirect(w, r, fmt.Sprintf("/sell/gigs/%d/edit", gigID), http.StatusSeeOther)
		return
	}
	if _, err := s.Store.AddGigMedia(r.Context(), gigID, path, detail.Title); err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "gig.media_added", "gig", fmt.Sprintf("%d", gigID), nil)
	http.Redirect(w, r, fmt.Sprintf("/sell/gigs/%d/edit", gigID), http.StatusSeeOther)
}

// gigFormInput is the parsed, validated content of a gig create/edit form.
type gigFormInput struct {
	title       string
	description string
	categoryID  *int64
	tags        []string
	packages    []store.GigPackage
	addons      []store.GigAddon
}

// parseGigForm reads and validates the shared gig create/edit form fields.
// r.ParseForm must already have been called.
func (s *Server) parseGigForm(r *http.Request) (gigFormInput, map[string]string) {
	errs := map[string]string{}
	title := strings.TrimSpace(r.FormValue("title"))
	description := strings.TrimSpace(r.FormValue("description"))

	if title == "" {
		errs["title"] = "Please enter a title."
	} else if len(title) > maxGigTitleLen {
		errs["title"] = fmt.Sprintf("Title must be at most %d characters.", maxGigTitleLen)
	}
	if description == "" {
		errs["description"] = "Please describe what you offer."
	} else if len(description) > maxGigDescLen {
		errs["description"] = fmt.Sprintf("Description must be at most %d characters.", maxGigDescLen)
	}

	var categoryID *int64
	if raw := strings.TrimSpace(r.FormValue("category_id")); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil {
			categoryID = &id
		}
	}

	var tags []string
	if raw := strings.TrimSpace(r.FormValue("tags")); raw != "" {
		tags = strings.Split(raw, ",")
	}

	var packages []store.GigPackage
	for i, tier := range gigTiers {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("pkg_name_%d", i)))
		if name == "" {
			continue
		}
		price, err := parseMoney(r.FormValue(fmt.Sprintf("pkg_price_%d", i)))
		if err != nil || price <= 0 {
			errs["packages"] = "Each package needs a price greater than zero."
			continue
		}
		delivery, err := strconv.Atoi(strings.TrimSpace(r.FormValue(fmt.Sprintf("pkg_delivery_%d", i))))
		if err != nil || delivery < 1 {
			errs["packages"] = "Each package needs a delivery time of at least one day."
			continue
		}
		revisions, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(fmt.Sprintf("pkg_revisions_%d", i))))
		if revisions < 0 {
			revisions = 0
		}
		packages = append(packages, store.GigPackage{
			Tier: tier, Name: name, Description: strings.TrimSpace(r.FormValue(fmt.Sprintf("pkg_description_%d", i))),
			PriceMinorUnits: price, Currency: "USD", DeliveryDays: delivery, Revisions: revisions,
		})
	}
	if len(packages) == 0 && errs["packages"] == "" {
		errs["packages"] = "Add at least one package."
	}

	var addons []store.GigAddon
	for i := 0; i < 3; i++ {
		name := strings.TrimSpace(r.FormValue(fmt.Sprintf("addon_name_%d", i)))
		if name == "" {
			continue
		}
		price, err := parseMoney(r.FormValue(fmt.Sprintf("addon_price_%d", i)))
		if err != nil || price <= 0 {
			errs["addons"] = "Each add-on needs a price greater than zero."
			continue
		}
		deliveryImpact, _ := strconv.Atoi(strings.TrimSpace(r.FormValue(fmt.Sprintf("addon_delivery_%d", i))))
		if deliveryImpact < 0 {
			deliveryImpact = 0
		}
		addons = append(addons, store.GigAddon{
			Name: name, Description: strings.TrimSpace(r.FormValue(fmt.Sprintf("addon_description_%d", i))),
			PriceMinorUnits: price, Currency: "USD", DeliveryDaysImpact: deliveryImpact,
		})
	}

	return gigFormInput{
		title: title, description: description, categoryID: categoryID,
		tags: tags, packages: packages, addons: addons,
	}, errs
}

func (s *Server) gigFormError(w http.ResponseWriter, r *http.Request, input gigFormInput, errs map[string]string, editing bool, gigID int64) {
	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	cats, err := s.Store.ListCategories(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	form := components.GigFormData{
		CSRF: sess.CSRF, Editing: editing, GigID: gigID,
		Title: input.title, Description: input.description,
		Categories: toCategoryOptions(cats), Errors: errs,
	}
	if input.categoryID != nil {
		form.CategoryID = strconv.FormatInt(*input.categoryID, 10)
	}
	form.Tags = strings.Join(input.tags, ", ")
	for i, tier := range gigTiers {
		form.Packages[i] = components.GigPackageForm{Tier: tier}
		for _, p := range input.packages {
			if p.Tier == tier {
				form.Packages[i] = components.GigPackageForm{
					Tier: tier, Name: p.Name, Description: p.Description,
					Price: formatMinorUnitsPlain(p.PriceMinorUnits), DeliveryDays: strconv.Itoa(p.DeliveryDays),
					Revisions: strconv.Itoa(p.Revisions),
				}
			}
		}
	}
	for i := 0; i < len(form.Addons) && i < len(input.addons); i++ {
		a := input.addons[i]
		form.Addons[i] = components.GigAddonForm{
			Name: a.Name, Description: a.Description,
			Price: formatMinorUnitsPlain(a.PriceMinorUnits), DeliveryDays: strconv.Itoa(a.DeliveryDaysImpact),
		}
	}

	body, err := components.GigFormPage(form)
	if err != nil {
		s.renderError(w, err)
		return
	}
	title := "Create a gig"
	if editing {
		title = "Edit gig"
	}
	s.render(w, r, http.StatusUnprocessableEntity, components.PageData{Title: title, Body: body})
}

func toCategoryOptions(cats []store.Category) []components.CategoryOption {
	out := make([]components.CategoryOption, 0, len(cats))
	for _, c := range cats {
		out = append(out, components.CategoryOption{ID: c.ID, Name: c.Name})
	}
	return out
}

// parseMoney converts a decimal dollar string ("25", "25.5", "25.99") into
// integer minor units (cents) without binary floating point.
func parseMoney(raw string) (int64, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, fmt.Errorf("amount is required")
	}
	whole, frac, hasFrac := strings.Cut(raw, ".")
	if whole == "" {
		whole = "0"
	}
	if !hasFrac {
		frac = "00"
	}
	switch len(frac) {
	case 0:
		frac = "00"
	case 1:
		frac += "0"
	default:
		frac = frac[:2]
	}
	for _, c := range whole + frac {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("invalid amount")
		}
	}
	wholeUnits, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %w", err)
	}
	fracUnits, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid amount: %w", err)
	}
	return wholeUnits*100 + fracUnits, nil
}

// formatMinorUnitsPlain renders minor units back into a plain "25.00" string
// for redisplaying in a form input (no currency symbol).
func formatMinorUnitsPlain(minorUnits int64) string {
	return fmt.Sprintf("%d.%02d", minorUnits/100, minorUnits%100)
}
