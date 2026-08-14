package components

import "html/template"

// GigCard is the compact gig representation used across listings: search
// results, category browse, favorites, and the homepage.
type GigCard struct {
	Slug          string
	Title         string
	SellerName    string
	CategoryName  string
	StartingPrice string // pre-formatted, e.g. "$25.00"
	RatingText    string // e.g. "4.8 (12 reviews)" or "No reviews yet"
}

// CategoriesData backs the category index page.
type CategoriesData struct {
	Categories []CategoryCard
}

// CategoriesPage renders the category browse index.
func CategoriesPage(d CategoriesData) (template.HTML, error) {
	return execute("categories", d)
}

// SearchData backs both /search and /browse/{category}: they render the
// same listing with different preset filters.
type SearchData struct {
	Query        string
	CategorySlug string
	CategoryName string
	Sort         string
	Categories   []CategoryCard
	Gigs         []GigCard
	Total        int
	Pagination   template.HTML
}

// SearchPage renders the gig search/browse results page.
func SearchPage(d SearchData) (template.HTML, error) {
	return execute("search", d)
}

// GigDetailData backs the public gig detail page.
type GigDetailData struct {
	Slug         string
	Title        string
	Description  string
	CategoryName string
	Tags         []string
	Media        []GigMediaView
	Packages     []GigPackageView
	Addons       []GigAddonView
	SellerName   string
	SellerID     int64
	RatingText   string
	Favorited    bool
	CanFavorite  bool
	CanOrder     bool
	CSRF         string
}

// GigMediaView is a displayable gig image.
type GigMediaView struct {
	Path    string
	AltText string
}

// GigPackageView is a displayable pricing tier.
type GigPackageView struct {
	ID           int64
	Tier         string
	Name         string
	Description  string
	Price        string
	DeliveryDays int
	Revisions    int
}

// GigAddonView is a displayable optional add-on.
type GigAddonView struct {
	ID          int64
	Name        string
	Description string
	Price       string
}

// GigDetailPage renders the public gig detail page.
func GigDetailPage(d GigDetailData) (template.HTML, error) {
	return execute("gigdetail", d)
}

// SellerProfileData backs the public seller profile page.
type SellerProfileData struct {
	DisplayName string
	Bio         string
	Location    string
	RatingText  string
	Portfolio   []PortfolioView
	Gigs        []GigCard
}

// PortfolioView is a displayable portfolio entry.
type PortfolioView struct {
	Title       string
	Description string
	MediaPath   string
}

// SellerProfilePage renders a seller's public profile.
func SellerProfilePage(d SellerProfileData) (template.HTML, error) {
	return execute("sellerprofile", d)
}
