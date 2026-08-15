package components

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// This is the automatable slice of TODO.md's "verify mobile performance and
// page weight budgets": actual field performance (device CPU, real network
// conditions) needs a real device/browser lab, but page weight — the byte
// count a mobile connection actually has to transfer — is deterministic
// from the same data this codebase already uses to render pages in tests,
// so it doesn't need one. Budgets are generous (this is a zero-JS,
// zero-framework app; there is no bundle to worry about, just the HTML
// document itself, once per navigation, plus the one shared stylesheet).
const (
	maxHTMLPageBytes = 200 * 1024 // one rendered page, full layout included
	maxStylesheetKB  = 64 * 1024  // static/app.css, cached after first load
)

func TestStylesheetSizeBudget(t *testing.T) {
	data, err := os.ReadFile("../static/app.css")
	if err != nil {
		t.Fatalf("read app.css: %v", err)
	}
	if len(data) > maxStylesheetKB {
		t.Errorf("static/app.css is %d bytes, want <= %d (mobile page-weight budget)", len(data), maxStylesheetKB)
	}
}

// TestGigDetailPageWeightBudget renders a representative, fairly heavy gig
// detail page (multiple media items, three packages, several add-ons — the
// densest real page in the catalog) and checks the full rendered HTML
// document against the budget.
func TestGigDetailPageWeightBudget(t *testing.T) {
	media := make([]GigMediaView, 0, 6)
	for i := 0; i < 6; i++ {
		media = append(media, GigMediaView{Path: fmt.Sprintf("gigs/1/photo-%d.jpg", i), AltText: fmt.Sprintf("Sample project photo %d", i)})
	}
	packages := []GigPackageView{
		{ID: 1, Tier: "basic", Name: "Basic", Description: strings.Repeat("A concise scope description. ", 8), Price: "$25.00", DeliveryDays: 2, Revisions: 1},
		{ID: 2, Tier: "standard", Name: "Standard", Description: strings.Repeat("A concise scope description. ", 8), Price: "$75.00", DeliveryDays: 4, Revisions: 3},
		{ID: 3, Tier: "premium", Name: "Premium", Description: strings.Repeat("A concise scope description. ", 8), Price: "$150.00", DeliveryDays: 7, Revisions: 5},
	}
	addons := make([]GigAddonView, 0, 4)
	for i := 0; i < 4; i++ {
		addons = append(addons, GigAddonView{ID: int64(i), Name: fmt.Sprintf("Add-on %d", i), Description: "A short add-on description.", Price: "$10.00"})
	}

	html, err := GigDetailPage(GigDetailData{
		Slug:         "sample-gig",
		Title:        "A Sample Gig With a Reasonably Descriptive Title",
		Description:  strings.Repeat("This is a paragraph of gig description text. ", 40),
		CategoryName: "Design",
		Tags:         []string{"logo", "branding", "illustration", "identity"},
		Media:        media,
		Packages:     packages,
		Addons:       addons,
		SellerName:   "Sample Seller",
		SellerID:     1,
		RatingText:   "4.8 (37 reviews)",
		CanFavorite:  true,
		CanOrder:     true,
		CSRF:         "test-csrf-token-placeholder-value",
	})
	if err != nil {
		t.Fatalf("GigDetailPage: %v", err)
	}
	full, err := Layout(PageData{Title: "Sample Gig", Body: html})
	if err != nil {
		t.Fatalf("Layout: %v", err)
	}
	if len(full) > maxHTMLPageBytes {
		t.Errorf("rendered gig detail page is %d bytes, want <= %d (mobile page-weight budget)", len(full), maxHTMLPageBytes)
	}
}
