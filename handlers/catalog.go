package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/tayyebi/gig/components"
	"github.com/tayyebi/gig/store"
)

const searchPerPage = 20

// search renders the full-catalog results page. It supersedes the Phase 2
// placeholder now that gigs exist to search.
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	s.renderSearch(w, r, q.Get("q"), q.Get("category"), q.Get("sort"), q.Get("page"))
}

// browseCategories lists every category.
func (s *Server) browseCategories(w http.ResponseWriter, r *http.Request) {
	cats, err := s.Store.ListCategories(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}
	body, err := components.CategoriesPage(components.CategoriesData{Categories: toCategoryCards(cats)})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: "Browse categories",
		Body:  body,
	})
}

// browseCategory lists gigs within one category, reusing the search results
// rendering with the category preset.
func (s *Server) browseCategory(w http.ResponseWriter, r *http.Request) {
	slug := r.PathValue("slug")
	if _, err := s.Store.GetCategoryBySlug(r.Context(), slug); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	q := r.URL.Query()
	s.renderSearch(w, r, q.Get("q"), slug, q.Get("sort"), q.Get("page"))
}

func (s *Server) renderSearch(w http.ResponseWriter, r *http.Request, query, categorySlug, sort, pageStr string) {
	page, _ := strconv.Atoi(pageStr)
	if page < 1 {
		page = 1
	}
	if sort != "price_asc" && sort != "price_desc" {
		sort = "newest"
	}

	cats, err := s.Store.ListCategories(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}

	summaries, total, err := s.Store.SearchGigs(r.Context(), store.SearchParams{
		Query: query, CategorySlug: categorySlug, Sort: sort, Page: page, PerPage: searchPerPage,
	})
	if err != nil {
		s.renderError(w, err)
		return
	}

	pageHTML, err := components.Paginate(components.Pagination{
		Current: page, PerPage: searchPerPage, Total: total,
		BaseURL: searchBaseURL(query, categorySlug, sort),
	})
	if err != nil {
		s.renderError(w, err)
		return
	}

	categoryName := ""
	for _, c := range cats {
		if c.Slug == categorySlug {
			categoryName = c.Name
		}
	}

	body, err := components.SearchPage(components.SearchData{
		Query:        query,
		CategorySlug: categorySlug,
		CategoryName: categoryName,
		Sort:         sort,
		Categories:   toCategoryCards(cats),
		Gigs:         toGigCards(summaries),
		Total:        total,
		Pagination:   pageHTML,
	})
	if err != nil {
		s.renderError(w, err)
		return
	}

	title := "Search gigs"
	if categoryName != "" {
		title = categoryName
	}
	s.render(w, r, http.StatusOK, components.PageData{Title: title, Body: body})
}

// searchBaseURL builds the canonical results path (without a page number) so
// pagination links preserve the active filters.
func searchBaseURL(query, categorySlug, sort string) string {
	v := url.Values{}
	if query != "" {
		v.Set("q", query)
	}
	if categorySlug != "" {
		v.Set("category", categorySlug)
	}
	if sort != "" && sort != "newest" {
		v.Set("sort", sort)
	}
	if len(v) == 0 {
		return "/search"
	}
	return "/search?" + v.Encode()
}

// gigDetail renders a public gig page.
func (s *Server) gigDetail(w http.ResponseWriter, r *http.Request) {
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

	sess, err := s.ensureSession(w, r)
	if err != nil {
		s.renderError(w, err)
		return
	}
	u := s.userFrom(r)
	var favorited bool
	if u != nil {
		favorited, err = s.Store.IsFavorite(r.Context(), u.ID, gig.ID)
		if err != nil {
			s.renderError(w, err)
			return
		}
	}

	body, err := components.GigDetailPage(components.GigDetailData{
		Slug:         gig.Slug,
		Title:        gig.Title,
		Description:  gig.Description,
		CategoryName: gig.CategoryName,
		Tags:         gig.Tags,
		Media:        toMediaViews(gig.Media),
		Packages:     toPackageViews(gig.Packages),
		Addons:       toAddonViews(gig.Addons),
		SellerName:   sellerDisplayName(gig.Seller),
		SellerID:     gig.SellerID,
		RatingText:   formatRating(gig.Seller.RatingAvg, gig.Seller.RatingCount),
		Favorited:    favorited,
		CanFavorite:  u != nil,
		CanOrder:     u != nil && u.ID != gig.SellerID,
		CSRF:         sess.CSRF,
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title:       gig.Title,
		Description: gig.Description,
		Body:        body,
	})
}

// favoriteToggle flips whether the signed-in buyer has favorited a gig.
func (s *Server) favoriteToggle(w http.ResponseWriter, r *http.Request) {
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
	u := s.userFrom(r)
	if _, err := s.Store.ToggleFavorite(r.Context(), u.ID, gig.ID); err != nil {
		s.renderError(w, err)
		return
	}
	http.Redirect(w, r, "/gigs/"+slug, http.StatusSeeOther)
}

// sellerProfile renders a seller's public profile page.
func (s *Server) sellerProfile(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		s.notFound(w, r)
		return
	}
	profile, err := s.Store.GetSellerProfile(r.Context(), id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.notFound(w, r)
			return
		}
		s.renderError(w, err)
		return
	}
	portfolio, err := s.Store.ListPortfolioItems(r.Context(), id)
	if err != nil {
		s.renderError(w, err)
		return
	}
	gigs, _, err := s.Store.SearchGigs(r.Context(), store.SearchParams{SellerID: id, PerPage: 100})
	if err != nil {
		s.renderError(w, err)
		return
	}

	body, err := components.SellerProfilePage(components.SellerProfileData{
		DisplayName: profile.DisplayName,
		Bio:         profile.Bio,
		Location:    profile.Location,
		RatingText:  formatRating(profile.RatingAvg, profile.RatingCount),
		Portfolio:   toPortfolioViews(portfolio),
		Gigs:        toGigCards(gigs),
	})
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.render(w, r, http.StatusOK, components.PageData{
		Title: profile.DisplayName,
		Body:  body,
	})
}

// --- store-to-view conversions and formatting helpers ---

func toCategoryCards(cats []store.Category) []components.CategoryCard {
	out := make([]components.CategoryCard, 0, len(cats))
	for _, c := range cats {
		out = append(out, components.CategoryCard{Name: c.Name, Slug: c.Slug, Blurb: c.Description})
	}
	return out
}

func toGigCards(summaries []store.GigSummary) []components.GigCard {
	out := make([]components.GigCard, 0, len(summaries))
	for _, g := range summaries {
		out = append(out, components.GigCard{
			Slug:          g.Slug,
			Title:         g.Title,
			SellerName:    g.SellerName,
			CategoryName:  g.CategoryName,
			StartingPrice: formatStartingPrice(g.StartingPriceMinorUnits, g.Currency),
			RatingText:    formatRating(g.RatingAvg, g.RatingCount),
		})
	}
	return out
}

func toMediaViews(media []store.GigMedia) []components.GigMediaView {
	out := make([]components.GigMediaView, 0, len(media))
	for _, m := range media {
		if m.ModerationState != store.ModerationApproved {
			continue
		}
		out = append(out, components.GigMediaView{Path: m.MediaPath, AltText: m.AltText})
	}
	return out
}

func toPackageViews(packages []store.GigPackage) []components.GigPackageView {
	out := make([]components.GigPackageView, 0, len(packages))
	for _, p := range packages {
		out = append(out, components.GigPackageView{
			ID: p.ID, Tier: p.Tier, Name: p.Name, Description: p.Description,
			Price:        formatMoney(p.PriceMinorUnits, p.Currency),
			DeliveryDays: p.DeliveryDays, Revisions: p.Revisions,
		})
	}
	return out
}

func toAddonViews(addons []store.GigAddon) []components.GigAddonView {
	out := make([]components.GigAddonView, 0, len(addons))
	for _, a := range addons {
		out = append(out, components.GigAddonView{
			ID: a.ID, Name: a.Name, Description: a.Description, Price: formatMoney(a.PriceMinorUnits, a.Currency),
		})
	}
	return out
}

func toPortfolioViews(items []store.PortfolioItem) []components.PortfolioView {
	out := make([]components.PortfolioView, 0, len(items))
	for _, it := range items {
		if it.ModerationState != store.ModerationApproved {
			continue
		}
		out = append(out, components.PortfolioView{Title: it.Title, Description: it.Description, MediaPath: it.MediaPath})
	}
	return out
}

func sellerDisplayName(s store.SellerSummary) string {
	if s.DisplayName != "" {
		return s.DisplayName
	}
	return s.Name
}

// formatMoney renders integer minor units as a decimal amount, e.g.
// (2500, "USD") -> "$25.00". Only USD is supported today; the symbol falls
// back to the currency code for anything else.
func formatMoney(minorUnits int64, currency string) string {
	symbol := "$"
	if currency != "" && currency != "USD" {
		symbol = currency + " "
	}
	return fmt.Sprintf("%s%d.%02d", symbol, minorUnits/100, minorUnits%100)
}

func formatStartingPrice(minorUnits int64, currency string) string {
	if minorUnits <= 0 {
		return "No pricing yet"
	}
	return "From " + formatMoney(minorUnits, currency)
}

func formatRating(avg float64, count int) string {
	if count <= 0 {
		return "No reviews yet"
	}
	return fmt.Sprintf("%.1f (%d review%s)", avg, count, pluralS(count))
}

func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
