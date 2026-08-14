package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// DraftOrder is a buyer's in-progress checkout for a gig. There is at most
// one per (buyer, gig) pair, so a refresh or a repeat visit to the gig page
// always resumes the same draft (PLAN.md section 11).
type DraftOrder struct {
	ID           int64
	BuyerID      int64
	GigID        int64
	PackageID    int64
	AddonIDs     []int64
	Requirements string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// UpsertDraftOrder starts (or restarts) a checkout draft with the chosen
// package, clearing any previously selected add-ons since they belonged to
// a possibly different package.
func (s *Store) UpsertDraftOrder(ctx context.Context, buyerID, gigID, packageID int64) (*DraftOrder, error) {
	var d DraftOrder
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, `
			INSERT INTO draft_orders (buyer_id, gig_id, package_id)
			VALUES ($1, $2, $3)
			ON CONFLICT (buyer_id, gig_id) DO UPDATE
			SET package_id = EXCLUDED.package_id, updated_at = now()
			RETURNING id, buyer_id, gig_id, package_id, requirements, created_at, updated_at`,
			buyerID, gigID, packageID,
		).Scan(&d.ID, &d.BuyerID, &d.GigID, &d.PackageID, &d.Requirements, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return fmt.Errorf("upsert draft order: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM draft_order_addons WHERE draft_order_id = $1`, d.ID); err != nil {
			return fmt.Errorf("clear draft order addons: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// GetDraftOrder returns a buyer's checkout draft for a gig, if any.
func (s *Store) GetDraftOrder(ctx context.Context, buyerID, gigID int64) (*DraftOrder, error) {
	return s.loadDraftOrder(ctx, `buyer_id = $1 AND gig_id = $2`, buyerID, gigID)
}

// GetDraftOrderByID returns a checkout draft by its own ID, for the checkout
// steps after the buyer has already been sent a draft-specific URL. Callers
// must check the returned draft's BuyerID against the requester.
func (s *Store) GetDraftOrderByID(ctx context.Context, id int64) (*DraftOrder, error) {
	return s.loadDraftOrder(ctx, `id = $1`, id)
}

func (s *Store) loadDraftOrder(ctx context.Context, where string, args ...any) (*DraftOrder, error) {
	var d DraftOrder
	err := s.db.QueryRowContext(ctx, `
		SELECT id, buyer_id, gig_id, package_id, requirements, created_at, updated_at
		FROM draft_orders WHERE `+where,
		args...,
	).Scan(&d.ID, &d.BuyerID, &d.GigID, &d.PackageID, &d.Requirements, &d.CreatedAt, &d.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get draft order: %w", err)
	}

	rows, err := s.db.QueryContext(ctx, `SELECT addon_id FROM draft_order_addons WHERE draft_order_id = $1`, d.ID)
	if err != nil {
		return nil, fmt.Errorf("list draft order addons: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan draft order addon: %w", err)
		}
		d.AddonIDs = append(d.AddonIDs, id)
	}
	return &d, rows.Err()
}

// SetDraftOrderRequirements saves the requirements text and selected
// add-ons for a checkout draft.
func (s *Store) SetDraftOrderRequirements(ctx context.Context, draftOrderID int64, requirements string, addonIDs []int64) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE draft_orders SET requirements = $2, updated_at = now() WHERE id = $1`,
			draftOrderID, requirements,
		); err != nil {
			return fmt.Errorf("set draft order requirements: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM draft_order_addons WHERE draft_order_id = $1`, draftOrderID); err != nil {
			return fmt.Errorf("clear draft order addons: %w", err)
		}
		for _, addonID := range addonIDs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO draft_order_addons (draft_order_id, addon_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				draftOrderID, addonID,
			); err != nil {
				return fmt.Errorf("add draft order addon: %w", err)
			}
		}
		return nil
	})
}

// DeleteDraftOrder removes a checkout draft, typically once it has been
// turned into a real order.
func (s *Store) DeleteDraftOrder(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, `DELETE FROM draft_orders WHERE id = $1`, id); err != nil {
		return fmt.Errorf("delete draft order %d: %w", id, err)
	}
	return nil
}
