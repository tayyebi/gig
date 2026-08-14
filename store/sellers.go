package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Seller onboarding KYC states. There is no real provider integration yet
// (Phase 5); "pending" only means the seller asked to be reviewed.
const (
	KYCNotStarted = "not_started"
	KYCPending    = "pending"
	KYCVerified   = "verified"
	KYCRejected   = "rejected"
)

// Moderation states shared by portfolio items, gigs, and gig media.
const (
	ModerationPending  = "pending"
	ModerationApproved = "approved"
	ModerationRejected = "rejected"
)

// SellerProfile is the public-facing seller identity shown on gig and
// profile pages.
type SellerProfile struct {
	UserID              int64
	DisplayName         string
	Bio                 string
	Location            string
	ResponseTimeMinutes *int
	RatingAvg           float64
	RatingCount         int
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// GetSellerProfile returns a seller's public profile.
func (s *Store) GetSellerProfile(ctx context.Context, userID int64) (*SellerProfile, error) {
	var p SellerProfile
	var responseTime sql.NullInt64
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, display_name, bio, location, response_time_minutes,
		       rating_avg, rating_count, created_at, updated_at
		FROM seller_profiles WHERE user_id = $1`,
		userID,
	).Scan(&p.UserID, &p.DisplayName, &p.Bio, &p.Location, &responseTime,
		&p.RatingAvg, &p.RatingCount, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get seller profile for user %d: %w", userID, err)
	}
	if responseTime.Valid {
		v := int(responseTime.Int64)
		p.ResponseTimeMinutes = &v
	}
	return &p, nil
}

// UpsertSellerProfile creates or updates a seller's public profile.
func (s *Store) UpsertSellerProfile(ctx context.Context, userID int64, displayName, bio, location string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO seller_profiles (user_id, display_name, bio, location)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (user_id) DO UPDATE
		SET display_name = EXCLUDED.display_name,
		    bio = EXCLUDED.bio,
		    location = EXCLUDED.location,
		    updated_at = now()`,
		userID, displayName, bio, location,
	)
	if err != nil {
		return fmt.Errorf("upsert seller profile for user %d: %w", userID, err)
	}
	return nil
}

// SellerOnboarding tracks a seller's readiness to receive payouts.
// StripeAccountID/ChargesEnabled/PayoutsEnabled reflect the seller's Stripe
// Connect account; KYCState is a separate, manually reviewed flag that
// predates the Connect integration and is not driven by it.
type SellerOnboarding struct {
	UserID          int64
	KYCState        string
	StripeAccountID string
	ChargesEnabled  bool
	PayoutsEnabled  bool
	ReviewedAt      *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// EnsureSellerOnboarding creates a not_started onboarding row if one does not
// already exist. It is idempotent.
func (s *Store) EnsureSellerOnboarding(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO seller_onboarding (user_id) VALUES ($1)
		ON CONFLICT (user_id) DO NOTHING`, userID)
	if err != nil {
		return fmt.Errorf("ensure seller onboarding for user %d: %w", userID, err)
	}
	return nil
}

// GetSellerOnboarding returns a seller's onboarding state.
func (s *Store) GetSellerOnboarding(ctx context.Context, userID int64) (*SellerOnboarding, error) {
	var o SellerOnboarding
	var reviewed sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, kyc_state, stripe_account_id, charges_enabled, payouts_enabled, reviewed_at, created_at, updated_at
		FROM seller_onboarding WHERE user_id = $1`,
		userID,
	).Scan(&o.UserID, &o.KYCState, &o.StripeAccountID, &o.ChargesEnabled, &o.PayoutsEnabled, &reviewed, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get seller onboarding for user %d: %w", userID, err)
	}
	if reviewed.Valid {
		t := reviewed.Time
		o.ReviewedAt = &t
	}
	return &o, nil
}

// GetSellerOnboardingByStripeAccount resolves the seller a Stripe Connect
// account webhook refers to.
func (s *Store) GetSellerOnboardingByStripeAccount(ctx context.Context, accountID string) (*SellerOnboarding, error) {
	var o SellerOnboarding
	var reviewed sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT user_id, kyc_state, stripe_account_id, charges_enabled, payouts_enabled, reviewed_at, created_at, updated_at
		FROM seller_onboarding WHERE stripe_account_id = $1`,
		accountID,
	).Scan(&o.UserID, &o.KYCState, &o.StripeAccountID, &o.ChargesEnabled, &o.PayoutsEnabled, &reviewed, &o.CreatedAt, &o.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get seller onboarding for stripe account %s: %w", accountID, err)
	}
	if reviewed.Valid {
		t := reviewed.Time
		o.ReviewedAt = &t
	}
	return &o, nil
}

// SetStripeAccount records the Stripe Connect account a seller has started
// or completed onboarding with.
func (s *Store) SetStripeAccount(ctx context.Context, userID int64, accountID string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE seller_onboarding SET stripe_account_id = $2, updated_at = now() WHERE user_id = $1`,
		userID, accountID)
	if err != nil {
		return fmt.Errorf("set stripe account for user %d: %w", userID, err)
	}
	return nil
}

// SetStripeAccountCapabilities updates a seller's Connect account status,
// driven by an account.updated webhook or a direct status check.
func (s *Store) SetStripeAccountCapabilities(ctx context.Context, accountID string, chargesEnabled, payoutsEnabled bool) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE seller_onboarding SET charges_enabled = $2, payouts_enabled = $3, updated_at = now()
		WHERE stripe_account_id = $1`,
		accountID, chargesEnabled, payoutsEnabled)
	if err != nil {
		return fmt.Errorf("set stripe account capabilities for account %s: %w", accountID, err)
	}
	return nil
}

// SetOnboardingState transitions a seller's KYC state.
func (s *Store) SetOnboardingState(ctx context.Context, userID int64, state string) error {
	_, err := s.db.ExecContext(ctx, `
		UPDATE seller_onboarding
		SET kyc_state = $2,
		    reviewed_at = CASE WHEN $2 IN ('verified', 'rejected') THEN now() ELSE reviewed_at END,
		    updated_at = now()
		WHERE user_id = $1`,
		userID, state,
	)
	if err != nil {
		return fmt.Errorf("set onboarding state for user %d: %w", userID, err)
	}
	return nil
}

// PortfolioItem is a sample of a seller's past work shown on their public
// profile.
type PortfolioItem struct {
	ID              int64
	UserID          int64
	Title           string
	Description     string
	MediaPath       string
	ModerationState string
	Position        int
	CreatedAt       time.Time
}

// ListPortfolioItems returns a seller's portfolio, most recent first.
func (s *Store) ListPortfolioItems(ctx context.Context, userID int64) ([]PortfolioItem, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, title, description, media_path, moderation_state, position, created_at
		FROM portfolio_items
		WHERE user_id = $1
		ORDER BY position, created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list portfolio items for user %d: %w", userID, err)
	}
	defer rows.Close()

	var items []PortfolioItem
	for rows.Next() {
		var it PortfolioItem
		if err := rows.Scan(&it.ID, &it.UserID, &it.Title, &it.Description, &it.MediaPath,
			&it.ModerationState, &it.Position, &it.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan portfolio item: %w", err)
		}
		items = append(items, it)
	}
	return items, rows.Err()
}

// CreatePortfolioItem adds a portfolio entry for a seller.
func (s *Store) CreatePortfolioItem(ctx context.Context, userID int64, title, description, mediaPath string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO portfolio_items (user_id, title, description, media_path)
		VALUES ($1, $2, $3, $4)
		RETURNING id`,
		userID, title, description, mediaPath,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create portfolio item for user %d: %w", userID, err)
	}
	return id, nil
}

// DeletePortfolioItem removes a portfolio item the caller owns.
func (s *Store) DeletePortfolioItem(ctx context.Context, id, userID int64) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM portfolio_items WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete portfolio item %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
