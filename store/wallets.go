package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Seller wallet lifecycle states.
const (
	WalletStatusPending    = "pending"
	WalletStatusConfirmed  = "confirmed"
	WalletStatusSuperseded = "superseded"
)

// Payout queue states.
const (
	PayoutQueued                  = "queued"
	PayoutNeedsManualReview       = "needs_manual_review"
	PayoutReadyForManualExecution = "ready_for_manual_execution"
	PayoutCompleted               = "completed"
	PayoutCanceled                = "canceled"
)

// TokenWalletConfirmation is the auth_tokens kind used to confirm a new
// wallet address, reusing the existing email-verification token machinery.
const TokenWalletConfirmation = "wallet_confirmation"

// SellerWallet is a seller's payout destination for one network+asset pair.
// AddressEncrypted is opaque here; only services.WalletCrypto can decrypt it.
type SellerWallet struct {
	ID                 int64
	UserID             int64
	Network            string
	Asset              string
	AddressEncrypted   []byte
	AddressFingerprint string
	Status             string
	ConfirmedAt        *time.Time
	EligibleAt         *time.Time
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// CreatePendingWallet inserts a new unconfirmed wallet, superseding any
// existing pending wallet for the same user/network/asset (never edits a
// confirmed wallet in place; a change always goes through a fresh
// confirmation cycle).
func (s *Store) CreatePendingWallet(ctx context.Context, userID int64, network, asset string, addressEncrypted []byte, fingerprint string) (int64, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("create pending wallet: begin tx: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx,
		`UPDATE seller_wallets SET status = 'superseded', updated_at = now()
		 WHERE user_id = $1 AND network = $2 AND asset = $3 AND status = 'pending'`,
		userID, network, asset); err != nil {
		return 0, fmt.Errorf("supersede prior pending wallet: %w", err)
	}

	var id int64
	err = tx.QueryRowContext(ctx, `
		INSERT INTO seller_wallets (user_id, network, asset, address_encrypted, address_fingerprint, status)
		VALUES ($1, $2, $3, $4, $5, 'pending')
		RETURNING id`,
		userID, network, asset, addressEncrypted, fingerprint,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create pending wallet: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("create pending wallet: commit: %w", err)
	}
	return id, nil
}

// ConfirmWallet marks a pending wallet confirmed and sets its cooling-off
// eligibility timestamp, superseding any previously confirmed wallet for the
// same user/network/asset.
func (s *Store) ConfirmWallet(ctx context.Context, walletID int64, cooldown time.Duration) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("confirm wallet: begin tx: %w", err)
	}
	defer tx.Rollback()

	var userID int64
	var network, asset string
	err = tx.QueryRowContext(ctx,
		`SELECT user_id, network, asset FROM seller_wallets WHERE id = $1 AND status = 'pending'`, walletID,
	).Scan(&userID, &network, &asset)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("confirm wallet: lookup: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE seller_wallets SET status = 'superseded', updated_at = now()
		 WHERE user_id = $1 AND network = $2 AND asset = $3 AND status = 'confirmed'`,
		userID, network, asset); err != nil {
		return fmt.Errorf("supersede prior confirmed wallet: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE seller_wallets SET status = 'confirmed', confirmed_at = now(),
		 eligible_at = now() + $2::interval, updated_at = now() WHERE id = $1`,
		walletID, cooldown.String()); err != nil {
		return fmt.Errorf("confirm wallet: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("confirm wallet: commit: %w", err)
	}
	return nil
}

// GetWallet fetches one wallet row by ID.
func (s *Store) GetWallet(ctx context.Context, id int64) (*SellerWallet, error) {
	return s.scanWallet(s.db.QueryRowContext(ctx, `
		SELECT id, user_id, network, asset, address_encrypted, address_fingerprint, status, confirmed_at, eligible_at, created_at, updated_at
		FROM seller_wallets WHERE id = $1`, id))
}

// GetConfirmedWallet returns the current confirmed wallet for a
// user/network/asset, if any.
func (s *Store) GetConfirmedWallet(ctx context.Context, userID int64, network, asset string) (*SellerWallet, error) {
	return s.scanWallet(s.db.QueryRowContext(ctx, `
		SELECT id, user_id, network, asset, address_encrypted, address_fingerprint, status, confirmed_at, eligible_at, created_at, updated_at
		FROM seller_wallets WHERE user_id = $1 AND network = $2 AND asset = $3 AND status = 'confirmed'`, userID, network, asset))
}

func (s *Store) scanWallet(row *sql.Row) (*SellerWallet, error) {
	var w SellerWallet
	var confirmedAt, eligibleAt sql.NullTime
	err := row.Scan(&w.ID, &w.UserID, &w.Network, &w.Asset, &w.AddressEncrypted, &w.AddressFingerprint,
		&w.Status, &confirmedAt, &eligibleAt, &w.CreatedAt, &w.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("scan wallet: %w", err)
	}
	if confirmedAt.Valid {
		t := confirmedAt.Time
		w.ConfirmedAt = &t
	}
	if eligibleAt.Valid {
		t := eligibleAt.Time
		w.EligibleAt = &t
	}
	return &w, nil
}

// IsPayoutEligible reports whether a wallet is confirmed and past its
// cooling-off period.
func (w *SellerWallet) IsPayoutEligible(now time.Time) bool {
	return w.Status == WalletStatusConfirmed && w.EligibleAt != nil && !now.Before(*w.EligibleAt)
}

// Payout is one queued seller payout, always resolved against a stored,
// confirmed wallet — never a raw client-supplied address.
type Payout struct {
	ID          int64
	SellerID    int64
	WalletID    int64
	AmountMinor int64
	Currency    string
	Network     string
	Asset       string
	Status      string
	TxHash      string
	ReviewedBy  *int64
	CreatedAt   time.Time
	ExecutedAt  *time.Time
	UpdatedAt   time.Time
}

// CreatePayout inserts a queued payout. initialStatus lets the caller apply
// allowlist/threshold checks (queued vs needs_manual_review) before insert.
func (s *Store) CreatePayout(ctx context.Context, sellerID, walletID int64, amountMinor int64, currency, network, asset, initialStatus string) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO payouts (seller_id, wallet_id, amount_minor, currency, network, asset, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id`,
		sellerID, walletID, amountMinor, currency, network, asset, initialStatus,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("create payout: %w", err)
	}
	return id, nil
}

// ListPayoutsByStatus returns payouts in a given status, most recent first.
func (s *Store) ListPayoutsByStatus(ctx context.Context, status string, limit int) ([]Payout, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, seller_id, wallet_id, amount_minor, currency, network, asset, status, tx_hash, reviewed_by, created_at, executed_at, updated_at
		FROM payouts WHERE status = $1 ORDER BY created_at DESC LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list payouts: %w", err)
	}
	defer rows.Close()
	var out []Payout
	for rows.Next() {
		var p Payout
		var reviewedBy sql.NullInt64
		var executedAt sql.NullTime
		if err := rows.Scan(&p.ID, &p.SellerID, &p.WalletID, &p.AmountMinor, &p.Currency, &p.Network, &p.Asset,
			&p.Status, &p.TxHash, &reviewedBy, &p.CreatedAt, &executedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan payout: %w", err)
		}
		if reviewedBy.Valid {
			v := reviewedBy.Int64
			p.ReviewedBy = &v
		}
		if executedAt.Valid {
			t := executedAt.Time
			p.ExecutedAt = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetPayout returns one payout by ID, for the admin high-value alert check
// on approval.
func (s *Store) GetPayout(ctx context.Context, id int64) (*Payout, error) {
	var p Payout
	var reviewedBy sql.NullInt64
	var executedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
		SELECT id, seller_id, wallet_id, amount_minor, currency, network, asset, status, tx_hash, reviewed_by, created_at, executed_at, updated_at
		FROM payouts WHERE id = $1`, id,
	).Scan(&p.ID, &p.SellerID, &p.WalletID, &p.AmountMinor, &p.Currency, &p.Network, &p.Asset,
		&p.Status, &p.TxHash, &reviewedBy, &p.CreatedAt, &executedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get payout %d: %w", id, err)
	}
	if reviewedBy.Valid {
		v := reviewedBy.Int64
		p.ReviewedBy = &v
	}
	if executedAt.Valid {
		t := executedAt.Time
		p.ExecutedAt = &t
	}
	return &p, nil
}

// TransitionPayout moves a payout between states, recording who reviewed it
// and, on completion, the on-chain transaction hash the admin executed
// manually (there is no automated treasury signing in this project's scope).
func (s *Store) TransitionPayout(ctx context.Context, id int64, from, to string, reviewedBy *int64, txHash string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE payouts SET status = $3, reviewed_by = COALESCE($4, reviewed_by),
		tx_hash = CASE WHEN $5 <> '' THEN $5 ELSE tx_hash END,
		executed_at = CASE WHEN $3 = 'completed' THEN now() ELSE executed_at END,
		updated_at = now()
		WHERE id = $1 AND status = $2`, id, from, to, reviewedBy, txHash)
	if err != nil {
		return fmt.Errorf("transition payout %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("transition payout %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// PayoutsPaused reports the platform-wide emergency pause flag.
func (s *Store) PayoutsPaused(ctx context.Context) (bool, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM platform_settings WHERE key = 'payouts_paused'`).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read payouts_paused: %w", err)
	}
	return value == "true", nil
}

// SetPayoutsPaused flips the platform-wide emergency pause flag.
func (s *Store) SetPayoutsPaused(ctx context.Context, paused bool) error {
	value := "false"
	if paused {
		value = "true"
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO platform_settings (key, value) VALUES ('payouts_paused', $1)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, value)
	if err != nil {
		return fmt.Errorf("set payouts_paused: %w", err)
	}
	return nil
}
