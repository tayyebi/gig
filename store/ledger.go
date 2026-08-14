package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"

	"github.com/tayyebi/gig/ledger"
)

// LedgerEntry is one persisted, immutable posting.
type LedgerEntry struct {
	ID                int64
	TransactionGroup   string
	AccountID          int64
	Direction          string
	AmountMinor        int64
	Currency           string
	OrderID            *int64
	Description        string
}

// getOrCreateLedgerAccount resolves the (kind, ownerID, currency) account,
// creating it on first use. Runs inside the caller's transaction so account
// creation and the postings that reference it commit atomically.
func getOrCreateLedgerAccount(ctx context.Context, tx *sql.Tx, kind string, ownerID *int64, currency string) (int64, error) {
	var owner sql.NullInt64
	if ownerID != nil {
		owner = sql.NullInt64{Int64: *ownerID, Valid: true}
	}
	var id int64
	err := tx.QueryRowContext(ctx, `
		INSERT INTO ledger_accounts (kind, owner_id, currency) VALUES ($1, $2, $3)
		ON CONFLICT (kind, owner_id, currency) DO UPDATE SET kind = EXCLUDED.kind
		RETURNING id`, kind, owner, currency).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("get or create ledger account %s/%v/%s: %w", kind, ownerID, currency, err)
	}
	return id, nil
}

// PostLedgerEntries persists a balanced set of ledger entries (see
// ledger.Validate, re-checked here so a caller can never bypass it) as one
// transaction group, creating any missing accounts. It is the only write
// path into ledger_entries; nothing updates a posting after insert.
func (s *Store) PostLedgerEntries(ctx context.Context, entries []ledger.Entry) (transactionGroup string, err error) {
	if err := ledger.Validate(entries); err != nil {
		return "", err
	}
	group, err := randomUUID()
	if err != nil {
		return "", fmt.Errorf("generate ledger transaction group: %w", err)
	}
	err = s.Tx(ctx, func(tx *sql.Tx) error {
		for _, e := range entries {
			accountID, err := getOrCreateLedgerAccount(ctx, tx, e.AccountKind, e.OwnerID, e.Currency)
			if err != nil {
				return err
			}
			var orderID sql.NullInt64
			if e.OrderID != nil {
				orderID = sql.NullInt64{Int64: *e.OrderID, Valid: true}
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO ledger_entries (transaction_group, account_id, direction, amount_minor_units, currency, order_id, description)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				group, accountID, e.Direction, e.AmountMinor, e.Currency, orderID, e.Description,
			); err != nil {
				return fmt.Errorf("insert ledger entry: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return group, nil
}

// AccountBalance is a running balance for one ledger account (credits minus
// debits), used for seller earnings summaries and admin reconciliation.
type AccountBalance struct {
	Kind          string
	Currency      string
	BalanceMinor  int64
}

// SellerBalances returns a seller's pending and available earnings balances
// per currency (PLAN.md section 12's "separate balances" requirement).
func (s *Store) SellerBalances(ctx context.Context, sellerID int64) ([]AccountBalance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.kind, a.currency,
		       COALESCE(SUM(CASE WHEN e.direction = 'credit' THEN e.amount_minor_units ELSE -e.amount_minor_units END), 0)
		FROM ledger_accounts a
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		WHERE a.owner_id = $1 AND a.kind IN ('seller_pending', 'seller_available')
		GROUP BY a.kind, a.currency`, sellerID)
	if err != nil {
		return nil, fmt.Errorf("seller balances for %d: %w", sellerID, err)
	}
	defer rows.Close()

	var out []AccountBalance
	for rows.Next() {
		var b AccountBalance
		if err := rows.Scan(&b.Kind, &b.Currency, &b.BalanceMinor); err != nil {
			return nil, fmt.Errorf("scan seller balance: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// PlatformBalances returns every platform-owned account balance (revenue,
// refunds, reserves, provider clearing) for the admin console.
func (s *Store) PlatformBalances(ctx context.Context) ([]AccountBalance, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT a.kind, a.currency,
		       COALESCE(SUM(CASE WHEN e.direction = 'credit' THEN e.amount_minor_units ELSE -e.amount_minor_units END), 0)
		FROM ledger_accounts a
		LEFT JOIN ledger_entries e ON e.account_id = a.id
		WHERE a.owner_id IS NULL
		GROUP BY a.kind, a.currency
		ORDER BY a.kind`)
	if err != nil {
		return nil, fmt.Errorf("platform balances: %w", err)
	}
	defer rows.Close()

	var out []AccountBalance
	for rows.Next() {
		var b AccountBalance
		if err := rows.Scan(&b.Kind, &b.Currency, &b.BalanceMinor); err != nil {
			return nil, fmt.Errorf("scan platform balance: %w", err)
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func randomUUID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hex.EncodeToString(b[0:4]), hex.EncodeToString(b[4:6]), hex.EncodeToString(b[6:8]),
		hex.EncodeToString(b[8:10]), hex.EncodeToString(b[10:16])), nil
}
