package store

import (
	"context"
	"errors"
	"testing"
)

func TestSellerProfileUpsert(t *testing.T) {
	st := openTestCatalogStore(t)
	ctx := context.Background()
	userID := mustCreateUser(t, st, "profile@example.com")

	if _, err := st.GetSellerProfile(ctx, userID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("before upsert: got %v, want ErrNotFound", err)
	}

	if err := st.UpsertSellerProfile(ctx, userID, "Ada", "I build things.", "Remote"); err != nil {
		t.Fatalf("UpsertSellerProfile create: %v", err)
	}
	p, err := st.GetSellerProfile(ctx, userID)
	if err != nil {
		t.Fatalf("GetSellerProfile: %v", err)
	}
	if p.DisplayName != "Ada" || p.Bio != "I build things." || p.Location != "Remote" {
		t.Errorf("unexpected profile: %+v", p)
	}
	if p.RatingCount != 0 || p.RatingAvg != 0 {
		t.Errorf("new profile should start with no rating: %+v", p)
	}

	if err := st.UpsertSellerProfile(ctx, userID, "Ada Lovelace", "Updated bio.", "London"); err != nil {
		t.Fatalf("UpsertSellerProfile update: %v", err)
	}
	p, err = st.GetSellerProfile(ctx, userID)
	if err != nil {
		t.Fatalf("GetSellerProfile after update: %v", err)
	}
	if p.DisplayName != "Ada Lovelace" || p.Location != "London" {
		t.Errorf("update did not persist: %+v", p)
	}
}

func TestSellerOnboardingLifecycle(t *testing.T) {
	st := openTestCatalogStore(t)
	ctx := context.Background()
	userID := mustCreateUser(t, st, "onboarding@example.com")

	if err := st.EnsureSellerOnboarding(ctx, userID); err != nil {
		t.Fatalf("EnsureSellerOnboarding: %v", err)
	}
	// Idempotent.
	if err := st.EnsureSellerOnboarding(ctx, userID); err != nil {
		t.Fatalf("EnsureSellerOnboarding idempotent: %v", err)
	}

	o, err := st.GetSellerOnboarding(ctx, userID)
	if err != nil {
		t.Fatalf("GetSellerOnboarding: %v", err)
	}
	if o.KYCState != KYCNotStarted {
		t.Errorf("kyc state = %q, want %q", o.KYCState, KYCNotStarted)
	}
	if o.ReviewedAt != nil {
		t.Errorf("reviewed_at should be nil before review")
	}

	if err := st.SetOnboardingState(ctx, userID, KYCPending); err != nil {
		t.Fatalf("SetOnboardingState pending: %v", err)
	}
	o, err = st.GetSellerOnboarding(ctx, userID)
	if err != nil {
		t.Fatalf("GetSellerOnboarding after pending: %v", err)
	}
	if o.KYCState != KYCPending || o.ReviewedAt != nil {
		t.Errorf("pending state unexpected: %+v", o)
	}

	if err := st.SetOnboardingState(ctx, userID, KYCVerified); err != nil {
		t.Fatalf("SetOnboardingState verified: %v", err)
	}
	o, err = st.GetSellerOnboarding(ctx, userID)
	if err != nil {
		t.Fatalf("GetSellerOnboarding after verified: %v", err)
	}
	if o.KYCState != KYCVerified || o.ReviewedAt == nil {
		t.Errorf("verified state should set reviewed_at: %+v", o)
	}
}

func TestPortfolioItems(t *testing.T) {
	st := openTestCatalogStore(t)
	ctx := context.Background()
	userID := mustCreateUser(t, st, "portfolio@example.com")
	otherID := mustCreateUser(t, st, "portfolio-other@example.com")

	id, err := st.CreatePortfolioItem(ctx, userID, "Brand refresh", "A logo project.", "portfolio/1/abc.jpg")
	if err != nil {
		t.Fatalf("CreatePortfolioItem: %v", err)
	}

	items, err := st.ListPortfolioItems(ctx, userID)
	if err != nil {
		t.Fatalf("ListPortfolioItems: %v", err)
	}
	if len(items) != 1 || items[0].ID != id || items[0].ModerationState != ModerationApproved {
		t.Fatalf("unexpected items: %+v", items)
	}

	// Ownership is enforced on delete.
	if err := st.DeletePortfolioItem(ctx, id, otherID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user delete: got %v, want ErrNotFound", err)
	}
	if err := st.DeletePortfolioItem(ctx, id, userID); err != nil {
		t.Fatalf("DeletePortfolioItem: %v", err)
	}
	items, err = st.ListPortfolioItems(ctx, userID)
	if err != nil {
		t.Fatalf("ListPortfolioItems after delete: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("items after delete = %+v", items)
	}
}
