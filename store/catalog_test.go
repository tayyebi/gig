package store

import (
	"context"
	"errors"
	"testing"

	"github.com/tayyebi/gig/migrations"
)

func openTestCatalogStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	ctx := context.Background()
	// CASCADE is required now that seller/catalog tables carry foreign keys
	// into users and gigs; categories and tags are not downstream of users,
	// so the migration-seeded rows survive the truncate.
	if _, err := st.db.ExecContext(ctx, `TRUNCATE TABLE jobs, audit_log, auth_tokens, sessions, user_roles, users RESTART IDENTITY CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if err := st.Migrate(ctx, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return st
}

func mustCreateUser(t *testing.T, st *Store, email string) int64 {
	t.Helper()
	id, err := st.CreateUser(context.Background(), email, "hash", "Test Seller", "en")
	if err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return id
}

func TestListCategoriesSeeded(t *testing.T) {
	st := openTestCatalogStore(t)
	cats, err := st.ListCategories(context.Background())
	if err != nil {
		t.Fatalf("ListCategories: %v", err)
	}
	if len(cats) < 4 {
		t.Fatalf("expected the seeded starter categories, got %d", len(cats))
	}
	if _, err := st.GetCategoryBySlug(context.Background(), "design"); err != nil {
		t.Errorf("GetCategoryBySlug(design): %v", err)
	}
	if _, err := st.GetCategoryBySlug(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown category: got %v, want ErrNotFound", err)
	}
}

func TestGigLifecycleAndVisibility(t *testing.T) {
	st := openTestCatalogStore(t)
	ctx := context.Background()
	sellerID := mustCreateUser(t, st, "seller@example.com")
	design, err := st.GetCategoryBySlug(ctx, "design")
	if err != nil {
		t.Fatalf("GetCategoryBySlug: %v", err)
	}

	gig, err := st.CreateGig(ctx, sellerID, &design.ID, "Modern Logo Design", "I will design a modern logo.")
	if err != nil {
		t.Fatalf("CreateGig: %v", err)
	}
	if gig.Slug != "modern-logo-design" {
		t.Errorf("slug = %q, want %q", gig.Slug, "modern-logo-design")
	}
	if gig.Status != GigDraft || gig.ModerationState != ModerationApproved {
		t.Errorf("unexpected initial gig state: %+v", gig)
	}

	// A second gig with the same title gets a unique slug.
	gig2, err := st.CreateGig(ctx, sellerID, nil, "Modern Logo Design", "Another one.")
	if err != nil {
		t.Fatalf("CreateGig duplicate title: %v", err)
	}
	if gig2.Slug == gig.Slug {
		t.Errorf("expected a unique slug, got %q twice", gig.Slug)
	}

	// Draft gigs are invisible to buyers.
	if _, err := st.GetGigBySlug(ctx, gig.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("draft gig visible: got %v, want ErrNotFound", err)
	}

	if err := st.ReplaceGigPackages(ctx, gig.ID, []GigPackage{
		{Tier: TierBasic, Name: "Basic", PriceMinorUnits: 2500, Currency: "USD", DeliveryDays: 3, Revisions: 1},
	}); err != nil {
		t.Fatalf("ReplaceGigPackages: %v", err)
	}
	if err := st.ReplaceGigAddons(ctx, gig.ID, []GigAddon{
		{Name: "Extra fast delivery", PriceMinorUnits: 1000, Currency: "USD", DeliveryDaysImpact: -1},
	}); err != nil {
		t.Fatalf("ReplaceGigAddons: %v", err)
	}
	if err := st.SetGigTags(ctx, gig.ID, []string{"logo", "branding", "Logo"}); err != nil {
		t.Fatalf("SetGigTags: %v", err)
	}

	if err := st.SetGigStatus(ctx, gig.ID, sellerID, GigActive); err != nil {
		t.Fatalf("SetGigStatus: %v", err)
	}

	detail, err := st.GetGigBySlug(ctx, gig.Slug)
	if err != nil {
		t.Fatalf("GetGigBySlug after publish: %v", err)
	}
	if len(detail.Packages) != 1 || detail.Packages[0].PriceMinorUnits != 2500 {
		t.Errorf("packages = %+v", detail.Packages)
	}
	if len(detail.Addons) != 1 {
		t.Errorf("addons = %+v", detail.Addons)
	}
	if len(detail.Tags) != 2 {
		t.Errorf("expected tags to dedupe case-insensitively, got %v", detail.Tags)
	}
	if detail.CategoryName != "Design" {
		t.Errorf("category name = %q", detail.CategoryName)
	}
	if detail.Seller.Name != "Test Seller" {
		t.Errorf("seller name = %q", detail.Seller.Name)
	}

	// Ownership is enforced: another seller cannot edit or see it via
	// GetGigForOwner.
	otherID := mustCreateUser(t, st, "other@example.com")
	if _, err := st.GetGigForOwner(ctx, gig.ID, otherID); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-seller GetGigForOwner: got %v, want ErrNotFound", err)
	}
	if err := st.UpdateGig(ctx, gig.ID, otherID, nil, "Hijacked", "desc"); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-seller UpdateGig: got %v, want ErrNotFound", err)
	}
	if err := st.SetGigStatus(ctx, gig.ID, otherID, GigArchived); !errors.Is(err, ErrNotFound) {
		t.Errorf("cross-seller SetGigStatus: got %v, want ErrNotFound", err)
	}

	if err := st.SetGigStatus(ctx, gig.ID, sellerID, GigPaused); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if _, err := st.GetGigBySlug(ctx, gig.Slug); !errors.Is(err, ErrNotFound) {
		t.Errorf("paused gig visible: got %v, want ErrNotFound", err)
	}

	gigs, err := st.ListGigsBySeller(ctx, sellerID)
	if err != nil {
		t.Fatalf("ListGigsBySeller: %v", err)
	}
	if len(gigs) != 2 {
		t.Errorf("seller gigs = %d, want 2", len(gigs))
	}
}

func TestSearchGigsFiltersSortsAndPaginates(t *testing.T) {
	st := openTestCatalogStore(t)
	ctx := context.Background()
	sellerID := mustCreateUser(t, st, "search-seller@example.com")
	design, err := st.GetCategoryBySlug(ctx, "design")
	if err != nil {
		t.Fatalf("GetCategoryBySlug: %v", err)
	}
	dev, err := st.GetCategoryBySlug(ctx, "development")
	if err != nil {
		t.Fatalf("GetCategoryBySlug: %v", err)
	}

	makeGig := func(title string, categoryID int64, price int64) {
		gig, err := st.CreateGig(ctx, sellerID, &categoryID, title, "Description for "+title)
		if err != nil {
			t.Fatalf("CreateGig: %v", err)
		}
		if err := st.ReplaceGigPackages(ctx, gig.ID, []GigPackage{
			{Tier: TierBasic, Name: "Basic", PriceMinorUnits: price, Currency: "USD", DeliveryDays: 2, Revisions: 0},
		}); err != nil {
			t.Fatalf("ReplaceGigPackages: %v", err)
		}
		if err := st.SetGigStatus(ctx, gig.ID, sellerID, GigActive); err != nil {
			t.Fatalf("SetGigStatus: %v", err)
		}
	}
	makeGig("Cheap logo design", design.ID, 1000)
	makeGig("Expensive logo design", design.ID, 9000)
	makeGig("Website development", dev.ID, 5000)

	// Category filter.
	results, total, err := st.SearchGigs(ctx, SearchParams{CategorySlug: "design"})
	if err != nil {
		t.Fatalf("SearchGigs category: %v", err)
	}
	if total != 2 || len(results) != 2 {
		t.Fatalf("category filter: total=%d len=%d, want 2", total, len(results))
	}

	// Full-text query.
	results, total, err = st.SearchGigs(ctx, SearchParams{Query: "website"})
	if err != nil {
		t.Fatalf("SearchGigs query: %v", err)
	}
	if total != 1 || results[0].Title != "Website development" {
		t.Fatalf("query filter: total=%d results=%+v", total, results)
	}

	// Sort by price ascending across all gigs.
	results, total, err = st.SearchGigs(ctx, SearchParams{Sort: "price_asc"})
	if err != nil {
		t.Fatalf("SearchGigs sort: %v", err)
	}
	if total != 3 || results[0].Title != "Cheap logo design" {
		t.Fatalf("price_asc sort: total=%d first=%q", total, results[0].Title)
	}
	if results[len(results)-1].Title != "Expensive logo design" {
		t.Fatalf("price_asc sort: last=%q", results[len(results)-1].Title)
	}

	// Pagination.
	page1, total, err := st.SearchGigs(ctx, SearchParams{PerPage: 2, Page: 1, Sort: "price_asc"})
	if err != nil {
		t.Fatalf("SearchGigs page1: %v", err)
	}
	if total != 3 || len(page1) != 2 {
		t.Fatalf("page1: total=%d len=%d", total, len(page1))
	}
	page2, _, err := st.SearchGigs(ctx, SearchParams{PerPage: 2, Page: 2, Sort: "price_asc"})
	if err != nil {
		t.Fatalf("SearchGigs page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("page2 len = %d, want 1", len(page2))
	}
	if page1[0].ID == page2[0].ID {
		t.Error("page1 and page2 overlap")
	}

	// Seller filter, used by the public seller profile page.
	results, _, err = st.SearchGigs(ctx, SearchParams{SellerID: sellerID})
	if err != nil {
		t.Fatalf("SearchGigs seller filter: %v", err)
	}
	if len(results) != 3 {
		t.Errorf("seller filter results = %d, want 3", len(results))
	}
}

func TestFavorites(t *testing.T) {
	st := openTestCatalogStore(t)
	ctx := context.Background()
	sellerID := mustCreateUser(t, st, "fav-seller@example.com")
	buyerID := mustCreateUser(t, st, "fav-buyer@example.com")

	gig, err := st.CreateGig(ctx, sellerID, nil, "Favorite me", "desc")
	if err != nil {
		t.Fatalf("CreateGig: %v", err)
	}

	on, err := st.ToggleFavorite(ctx, buyerID, gig.ID)
	if err != nil {
		t.Fatalf("ToggleFavorite on: %v", err)
	}
	if !on {
		t.Fatal("expected favorite to be on after first toggle")
	}
	is, err := st.IsFavorite(ctx, buyerID, gig.ID)
	if err != nil {
		t.Fatalf("IsFavorite: %v", err)
	}
	if !is {
		t.Error("IsFavorite should report true")
	}

	favs, err := st.ListFavoriteGigs(ctx, buyerID)
	if err != nil {
		t.Fatalf("ListFavoriteGigs: %v", err)
	}
	if len(favs) != 1 || favs[0].ID != gig.ID {
		t.Errorf("favorites = %+v", favs)
	}

	off, err := st.ToggleFavorite(ctx, buyerID, gig.ID)
	if err != nil {
		t.Fatalf("ToggleFavorite off: %v", err)
	}
	if off {
		t.Fatal("expected favorite to be off after second toggle")
	}
	favs, err = st.ListFavoriteGigs(ctx, buyerID)
	if err != nil {
		t.Fatalf("ListFavoriteGigs after unfavorite: %v", err)
	}
	if len(favs) != 0 {
		t.Errorf("favorites after unfavorite = %+v", favs)
	}
}
