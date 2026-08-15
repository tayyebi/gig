// Package store: admin console queries (Phase 8). These sit in their own
// file since they cut across users, gigs, media, reviews, messages,
// disputes, payments, and platform settings rather than belonging to any
// one domain file.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ListUsersAdmin returns users, optionally filtered by status ("" = any)
// and a case-insensitive substring match against name/email ("" = any),
// newest first, for the moderation console.
func (s *Store) ListUsersAdmin(ctx context.Context, status, search string, limit int) ([]User, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, email, password_hash, name, locale, status, email_verified_at, totp_enabled_at,
			failed_login_attempts, locked_until, last_login_at, created_at, updated_at
		FROM users
		WHERE ($1 = '' OR status = $1)
		  AND ($2 = '' OR email ILIKE '%' || $2 || '%' OR name ILIKE '%' || $2 || '%')
		ORDER BY created_at DESC LIMIT $3`, status, search, limit)
	if err != nil {
		return nil, fmt.Errorf("list users (admin): %w", err)
	}
	defer rows.Close()

	var out []User
	for rows.Next() {
		var u User
		var verified, totpEnabled, lockedUntil, lastLogin sql.NullTime
		if err := rows.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Name, &u.Locale, &u.Status, &verified, &totpEnabled,
			&u.FailedLoginAttempts, &lockedUntil, &lastLogin, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user (admin): %w", err)
		}
		if verified.Valid {
			t := verified.Time
			u.EmailVerifiedAt = &t
		}
		if lockedUntil.Valid {
			t := lockedUntil.Time
			u.LockedUntil = &t
		}
		if lastLogin.Valid {
			t := lastLogin.Time
			u.LastLoginAt = &t
		}
		u.TotpEnabled = totpEnabled.Valid
		out = append(out, u)
	}
	return out, rows.Err()
}

// SetUserStatusAdmin changes a user's account status (active/disabled/
// deleted) and records the reason inline on the row for support visibility;
// the caller is also expected to write an AuditLog entry.
func (s *Store) SetUserStatusAdmin(ctx context.Context, userID int64, status, reason string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE users SET status = $2, restriction_reason = $3, updated_at = now() WHERE id = $1`,
		userID, status, reason)
	if err != nil {
		return fmt.Errorf("set user %d status: %w", userID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListGigsByModeration returns gigs in a given moderation state, oldest
// first (so the queue clears in submission order).
func (s *Store) ListGigsByModeration(ctx context.Context, state string, limit int) ([]Gig, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, seller_id, category_id, slug, title, description, status, moderation_state, created_at, updated_at
		FROM gigs WHERE moderation_state = $1 ORDER BY created_at LIMIT $2`, state, limit)
	if err != nil {
		return nil, fmt.Errorf("list gigs by moderation state: %w", err)
	}
	defer rows.Close()

	var out []Gig
	for rows.Next() {
		var g Gig
		if err := rows.Scan(&g.ID, &g.SellerID, &g.CategoryID, &g.Slug, &g.Title, &g.Description,
			&g.Status, &g.ModerationState, &g.CreatedAt, &g.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan gig (admin): %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetGigModerationStateAdmin approves or rejects a gig, bypassing the
// seller-ownership check SetGigStatus enforces (this is an admin-only path).
func (s *Store) SetGigModerationStateAdmin(ctx context.Context, gigID int64, state string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE gigs SET moderation_state = $2, updated_at = now() WHERE id = $1`, gigID, state)
	if err != nil {
		return fmt.Errorf("set gig %d moderation state: %w", gigID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListGigMediaByModeration returns gig media images pending (or in any
// given) moderation state, joined with the owning gig's title for display.
type GigMediaAdmin struct {
	GigMedia
	GigTitle string
}

func (s *Store) ListGigMediaByModeration(ctx context.Context, state string, limit int) ([]GigMediaAdmin, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT gm.id, gm.gig_id, gm.media_path, gm.alt_text, gm.position, gm.moderation_state, g.title
		FROM gig_media gm JOIN gigs g ON g.id = gm.gig_id
		WHERE gm.moderation_state = $1 ORDER BY gm.id LIMIT $2`, state, limit)
	if err != nil {
		return nil, fmt.Errorf("list gig media by moderation state: %w", err)
	}
	defer rows.Close()

	var out []GigMediaAdmin
	for rows.Next() {
		var m GigMediaAdmin
		if err := rows.Scan(&m.ID, &m.GigID, &m.MediaPath, &m.AltText, &m.Position, &m.ModerationState, &m.GigTitle); err != nil {
			return nil, fmt.Errorf("scan gig media (admin): %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetGigMediaModerationState approves or rejects one gig media item.
func (s *Store) SetGigMediaModerationState(ctx context.Context, mediaID int64, state string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE gig_media SET moderation_state = $2 WHERE id = $1`, mediaID, state)
	if err != nil {
		return fmt.Errorf("set gig media %d moderation state: %w", mediaID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListReviewsByModeration returns reviews in a given moderation state.
func (s *Store) ListReviewsByModeration(ctx context.Context, state string, limit int) ([]Review, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, gig_id, buyer_id, seller_id, rating, body, moderation_state, created_at
		FROM reviews WHERE moderation_state = $1 ORDER BY created_at LIMIT $2`, state, limit)
	if err != nil {
		return nil, fmt.Errorf("list reviews by moderation state: %w", err)
	}
	defer rows.Close()

	var out []Review
	for rows.Next() {
		var rv Review
		if err := rows.Scan(&rv.ID, &rv.OrderID, &rv.GigID, &rv.BuyerID, &rv.SellerID, &rv.Rating, &rv.Body, &rv.ModerationState, &rv.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan review (admin): %w", err)
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

// SetReviewModerationStateAdmin updates a review's moderation state and
// recomputes the seller's public rating summary, mirroring CreateReview so
// the aggregate never drifts from the underlying approved rows.
func (s *Store) SetReviewModerationStateAdmin(ctx context.Context, reviewID int64, state string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		var sellerID int64
		if err := tx.QueryRowContext(ctx, `UPDATE reviews SET moderation_state = $2 WHERE id = $1 RETURNING seller_id`, reviewID, state).Scan(&sellerID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return fmt.Errorf("set review %d moderation state: %w", reviewID, err)
		}
		if _, err := tx.ExecContext(ctx, `
			UPDATE seller_profiles SET
				rating_avg = coalesce((SELECT avg(rating) FROM reviews WHERE seller_id = $1 AND moderation_state = 'approved'), 0),
				rating_count = (SELECT count(*) FROM reviews WHERE seller_id = $1 AND moderation_state = 'approved'),
				updated_at = now()
			WHERE user_id = $1`, sellerID); err != nil {
			return fmt.Errorf("recalculate seller rating: %w", err)
		}
		return nil
	})
}

// HideOrderMessage marks a message hidden from normal order-thread display
// (it still appears to admins with the hidden marker, for accountability).
func (s *Store) HideOrderMessage(ctx context.Context, messageID, adminID int64, hide bool) error {
	var res sql.Result
	var err error
	if hide {
		res, err = s.db.ExecContext(ctx, `UPDATE order_messages SET hidden = true, hidden_by = $2, hidden_at = now() WHERE id = $1`, messageID, adminID)
	} else {
		res, err = s.db.ExecContext(ctx, `UPDATE order_messages SET hidden = false, hidden_by = NULL, hidden_at = NULL WHERE id = $1`, messageID)
	}
	if err != nil {
		return fmt.Errorf("hide order message %d: %w", messageID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RecentOrderMessages returns the most recent order messages across the
// platform for moderation review (flagged content is reported by users
// through support channels outside this app; this view lets an admin scan
// recent activity or jump to a specific order).
func (s *Store) RecentOrderMessages(ctx context.Context, orderID int64, limit int) ([]OrderMessage, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	var rows *sql.Rows
	var err error
	if orderID > 0 {
		rows, err = s.db.QueryContext(ctx, `
			SELECT om.id, om.order_id, om.sender_id, u.name, om.body, om.created_at
			FROM order_messages om JOIN users u ON u.id = om.sender_id
			WHERE om.order_id = $1 ORDER BY om.created_at DESC LIMIT $2`, orderID, limit)
	} else {
		rows, err = s.db.QueryContext(ctx, `
			SELECT om.id, om.order_id, om.sender_id, u.name, om.body, om.created_at
			FROM order_messages om JOIN users u ON u.id = om.sender_id
			ORDER BY om.created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("recent order messages: %w", err)
	}
	defer rows.Close()

	var out []OrderMessage
	for rows.Next() {
		var m OrderMessage
		if err := rows.Scan(&m.ID, &m.OrderID, &m.SenderID, &m.SenderName, &m.Body, &m.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan order message (admin): %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListDisputesByStatus returns disputes in a given status, oldest first.
func (s *Store) ListDisputesByStatus(ctx context.Context, status string, limit int) ([]Dispute, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, order_id, opened_by, reason, status, decision, resolved_by, resolved_at, created_at, internal_notes
		FROM disputes WHERE status = $1 ORDER BY created_at LIMIT $2`, status, limit)
	if err != nil {
		return nil, fmt.Errorf("list disputes by status: %w", err)
	}
	defer rows.Close()

	var out []Dispute
	for rows.Next() {
		d, err := scanDisputeRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

// GetDispute looks up a single dispute by ID for the resolution console.
func (s *Store) GetDispute(ctx context.Context, id int64) (*Dispute, error) {
	d, err := scanDisputeRow(s.db.QueryRowContext(ctx, `
		SELECT id, order_id, opened_by, reason, status, decision, resolved_by, resolved_at, created_at, internal_notes
		FROM disputes WHERE id = $1`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// SetDisputeInternalNotes appends/overwrites the admin-only internal notes
// on a dispute, independent of the buyer/seller-visible decision text.
func (s *Store) SetDisputeInternalNotes(ctx context.Context, id int64, notes string) error {
	res, err := s.db.ExecContext(ctx, `UPDATE disputes SET internal_notes = $2 WHERE id = $1`, id, notes)
	if err != nil {
		return fmt.Errorf("set dispute %d internal notes: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanDisputeRow(row rowScanner) (*Dispute, error) {
	var d Dispute
	var resolvedBy sql.NullInt64
	var resolvedAt sql.NullTime
	err := row.Scan(&d.ID, &d.OrderID, &d.OpenedBy, &d.Reason, &d.Status, &d.Decision, &resolvedBy, &resolvedAt, &d.CreatedAt, &d.InternalNotes)
	if err != nil {
		return nil, fmt.Errorf("scan dispute: %w", err)
	}
	if resolvedBy.Valid {
		v := resolvedBy.Int64
		d.ResolvedBy = &v
	}
	if resolvedAt.Valid {
		t := resolvedAt.Time
		d.ResolvedAt = &t
	}
	return &d, nil
}

// SearchPaymentIntents looks up payment intents by exact numeric ID, or by
// a substring match against provider_ref/charge_ref, for the admin payment
// search tool (PLAN.md section 15).
func (s *Store) SearchPaymentIntents(ctx context.Context, query string, limit int) ([]PaymentIntent, error) {
	if limit < 1 || limit > 200 {
		limit = 25
	}
	var rows *sql.Rows
	var err error
	rows, err = s.db.QueryContext(ctx, `
		SELECT `+paymentIntentColumns+` FROM payment_intents
		WHERE provider_ref ILIKE '%' || $1 || '%'
		   OR charge_ref ILIKE '%' || $1 || '%'
		   OR id::text = $1
		   OR order_id::text = $1
		ORDER BY created_at DESC LIMIT $2`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("search payment intents: %w", err)
	}
	defer rows.Close()

	var out []PaymentIntent
	for rows.Next() {
		p, err := scanPaymentIntent(rows)
		if err != nil {
			return nil, fmt.Errorf("scan payment intent (search): %w", err)
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

// PaymentAttemptRecord is one raw provider status observation, for the
// order+payment timeline view.
type PaymentAttemptRecord struct {
	ID              int64
	PaymentIntentID int64
	ProviderStatus  string
	FailureCode     string
	FailureMessage  string
	CreatedAt       time.Time
}

// ListPaymentAttempts returns every recorded attempt for a payment intent,
// oldest first, so the admin timeline shows the full history rather than
// only the latest status.
func (s *Store) ListPaymentAttempts(ctx context.Context, intentID int64) ([]PaymentAttemptRecord, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, payment_intent_id, provider_status, failure_code, failure_message, created_at
		FROM payment_attempts WHERE payment_intent_id = $1 ORDER BY created_at`, intentID)
	if err != nil {
		return nil, fmt.Errorf("list payment attempts for intent %d: %w", intentID, err)
	}
	defer rows.Close()

	var out []PaymentAttemptRecord
	for rows.Next() {
		var a PaymentAttemptRecord
		if err := rows.Scan(&a.ID, &a.PaymentIntentID, &a.ProviderStatus, &a.FailureCode, &a.FailureMessage, &a.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan payment attempt: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ListWebhookEventsForOrder returns the webhook deliveries associated with
// an order's payment intents, matched by (provider, provider_ref) — the
// provider's own reference recorded alongside each delivery at receipt time
// (handlers.receiveWebhookEvent) — for the order+payment timeline view.
func (s *Store) ListWebhookEventsForOrder(ctx context.Context, orderID int64) ([]WebhookEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.provider, e.event_id, e.event_type, e.payload, e.status, e.attempts
		FROM payment_webhook_events e
		JOIN payment_intents pi ON pi.provider = e.provider AND pi.provider_ref = e.provider_ref AND e.provider_ref <> ''
		WHERE pi.order_id = $1
		ORDER BY e.created_at`, orderID)
	if err != nil {
		return nil, fmt.Errorf("list webhook events for order %d: %w", orderID, err)
	}
	defer rows.Close()

	var out []WebhookEvent
	for rows.Next() {
		var e WebhookEvent
		if err := rows.Scan(&e.ID, &e.Provider, &e.EventID, &e.EventType, &e.Payload, &e.Status, &e.Attempts); err != nil {
			return nil, fmt.Errorf("scan webhook event (order timeline): %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RetryJob resets a failed/dead job back to queued for immediate re-claim by
// a worker, for the admin's "safe webhook retry" tool. It does not bypass
// max_attempts permanently — attempts is reset too, so a job that dead-
// lettered from exhausting retries gets a fresh budget, matching the
// operator's explicit decision to retry rather than an automatic bypass.
func (s *Store) RetryJob(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET status = 'queued', attempts = 0, locked_by = NULL, locked_at = NULL, run_at = now(), updated_at = now()
		WHERE id = $1 AND status IN ('dead', 'failed')`, id)
	if err != nil {
		return fmt.Errorf("retry job %d: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetSetting reads one platform_settings value, or "" if unset.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM platform_settings WHERE key = $1`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get setting %s: %w", key, err)
	}
	return v, nil
}

// SetSetting upserts one platform_settings value, for the admin settings
// console (fee schedule, feature flags, network toggles). Callers must
// audit-log the change; this is a plain write with no history.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO platform_settings (key, value) VALUES ($1, $2)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %s: %w", key, err)
	}
	return nil
}

// PlatformSettingRow is one key/value row for the settings console listing.
type PlatformSettingRow struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// ListSettings returns every platform_settings row, for the settings
// console.
func (s *Store) ListSettings(ctx context.Context) ([]PlatformSettingRow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value, updated_at FROM platform_settings ORDER BY key`)
	if err != nil {
		return nil, fmt.Errorf("list settings: %w", err)
	}
	defer rows.Close()

	var out []PlatformSettingRow
	for rows.Next() {
		var p PlatformSettingRow
		if err := rows.Scan(&p.Key, &p.Value, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan setting: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
