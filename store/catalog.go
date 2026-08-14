package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Gig lifecycle states. A gig must be "active" and "approved" to appear in
// public browse/search results.
const (
	GigDraft    = "draft"
	GigActive   = "active"
	GigPaused   = "paused"
	GigArchived = "archived"
)

// Gig package tiers. Exactly one package per tier is allowed per gig.
const (
	TierBasic    = "basic"
	TierStandard = "standard"
	TierPremium  = "premium"
)

// Category groups gigs for browsing. The starter set is seeded by migration;
// full CRUD is an admin (Phase 8) responsibility.
type Category struct {
	ID          int64
	Slug        string
	Name        string
	Description string
	Position    int
}

// ListCategories returns all categories in display order.
func (s *Store) ListCategories(ctx context.Context) ([]Category, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, slug, name, description, position FROM categories ORDER BY position, name`)
	if err != nil {
		return nil, fmt.Errorf("list categories: %w", err)
	}
	defer rows.Close()

	var cats []Category
	for rows.Next() {
		var c Category
		if err := rows.Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Position); err != nil {
			return nil, fmt.Errorf("scan category: %w", err)
		}
		cats = append(cats, c)
	}
	return cats, rows.Err()
}

// GetCategoryBySlug looks up a category by its URL slug.
func (s *Store) GetCategoryBySlug(ctx context.Context, slug string) (*Category, error) {
	var c Category
	err := s.db.QueryRowContext(ctx,
		`SELECT id, slug, name, description, position FROM categories WHERE slug = $1`, slug,
	).Scan(&c.ID, &c.Slug, &c.Name, &c.Description, &c.Position)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get category %q: %w", slug, err)
	}
	return &c, nil
}

// Gig is a seller's listing. Visibility to buyers requires status "active"
// and moderation_state "approved".
type Gig struct {
	ID              int64
	SellerID        int64
	CategoryID      *int64
	Slug            string
	Title           string
	Description     string
	Status          string
	ModerationState string
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// GigPackage is one fixed-price tier of a gig.
type GigPackage struct {
	ID              int64
	GigID           int64
	Tier            string
	Name            string
	Description     string
	PriceMinorUnits int64
	Currency        string
	DeliveryDays    int
	Revisions       int
}

// GigAddon is an optional extra a buyer can add to an order.
type GigAddon struct {
	ID                 int64
	GigID              int64
	Name               string
	Description        string
	PriceMinorUnits    int64
	Currency           string
	DeliveryDaysImpact int
	Position           int
}

// GigMedia is an image attached to a gig listing.
type GigMedia struct {
	ID              int64
	GigID           int64
	MediaPath       string
	AltText         string
	Position        int
	ModerationState string
}

// SellerSummary is the seller information shown alongside a gig.
type SellerSummary struct {
	UserID      int64
	Name        string
	DisplayName string
	RatingAvg   float64
	RatingCount int
}

// GigDetail bundles a gig with everything its detail page needs to render.
type GigDetail struct {
	Gig
	CategoryName string
	Packages     []GigPackage
	Addons       []GigAddon
	Media        []GigMedia
	Tags         []string
	Seller       SellerSummary
}

var slugNonAlnum = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(title string) string {
	s := strings.ToLower(strings.TrimSpace(title))
	s = slugNonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "gig"
	}
	if len(s) > 80 {
		s = strings.Trim(s[:80], "-")
	}
	return s
}

func randomSlugSuffix() (string, error) {
	b := make([]byte, 3)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate slug suffix: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CreateGig inserts a new draft gig owned by sellerID, generating a unique
// slug from the title.
func (s *Store) CreateGig(ctx context.Context, sellerID int64, categoryID *int64, title, description string) (*Gig, error) {
	base := slugify(title)
	catArg := nullInt64(categoryID)

	for attempt := 0; attempt < 5; attempt++ {
		candidate := base
		if attempt > 0 {
			suffix, err := randomSlugSuffix()
			if err != nil {
				return nil, err
			}
			candidate = base + "-" + suffix
		}

		g := Gig{}
		var cat sql.NullInt64
		err := s.db.QueryRowContext(ctx, `
			INSERT INTO gigs (seller_id, category_id, slug, title, description)
			VALUES ($1, $2, $3, $4, $5)
			RETURNING id, seller_id, category_id, slug, title, description, status, moderation_state, created_at, updated_at`,
			sellerID, catArg, candidate, title, description,
		).Scan(&g.ID, &g.SellerID, &cat, &g.Slug, &g.Title, &g.Description,
			&g.Status, &g.ModerationState, &g.CreatedAt, &g.UpdatedAt)
		if isUniqueViolation(err) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("create gig: %w", err)
		}
		if cat.Valid {
			v := cat.Int64
			g.CategoryID = &v
		}
		return &g, nil
	}
	return nil, fmt.Errorf("create gig: could not generate a unique slug after multiple attempts")
}

// UpdateGig changes a gig's editable fields. It is ownership-checked.
func (s *Store) UpdateGig(ctx context.Context, gigID, sellerID int64, categoryID *int64, title, description string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE gigs SET category_id = $3, title = $4, description = $5, updated_at = now()
		WHERE id = $1 AND seller_id = $2`,
		gigID, sellerID, nullInt64(categoryID), title, description,
	)
	if err != nil {
		return fmt.Errorf("update gig %d: %w", gigID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetGigStatus transitions a gig's lifecycle status. It is ownership-checked.
func (s *Store) SetGigStatus(ctx context.Context, gigID, sellerID int64, status string) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE gigs SET status = $3, updated_at = now() WHERE id = $1 AND seller_id = $2`,
		gigID, sellerID, status,
	)
	if err != nil {
		return fmt.Errorf("set gig %d status: %w", gigID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListGigsBySeller returns every gig a seller owns, regardless of status.
func (s *Store) ListGigsBySeller(ctx context.Context, sellerID int64) ([]Gig, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, seller_id, category_id, slug, title, description, status, moderation_state, created_at, updated_at
		FROM gigs WHERE seller_id = $1 ORDER BY created_at DESC`,
		sellerID,
	)
	if err != nil {
		return nil, fmt.Errorf("list gigs for seller %d: %w", sellerID, err)
	}
	defer rows.Close()

	var gigs []Gig
	for rows.Next() {
		g, err := scanGig(rows)
		if err != nil {
			return nil, err
		}
		gigs = append(gigs, g)
	}
	return gigs, rows.Err()
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanGig(row rowScanner) (Gig, error) {
	var g Gig
	var cat sql.NullInt64
	if err := row.Scan(&g.ID, &g.SellerID, &cat, &g.Slug, &g.Title, &g.Description,
		&g.Status, &g.ModerationState, &g.CreatedAt, &g.UpdatedAt); err != nil {
		return Gig{}, fmt.Errorf("scan gig: %w", err)
	}
	if cat.Valid {
		v := cat.Int64
		g.CategoryID = &v
	}
	return g, nil
}

// GetGigForOwner returns full gig detail regardless of status, for the
// owning seller's edit views.
func (s *Store) GetGigForOwner(ctx context.Context, gigID, sellerID int64) (*GigDetail, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, seller_id, category_id, slug, title, description, status, moderation_state, created_at, updated_at
		FROM gigs WHERE id = $1 AND seller_id = $2`,
		gigID, sellerID,
	)
	g, err := scanGig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.loadGigDetail(ctx, g)
}

// GetGigBySlug returns full gig detail for a publicly visible gig.
func (s *Store) GetGigBySlug(ctx context.Context, slug string) (*GigDetail, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, seller_id, category_id, slug, title, description, status, moderation_state, created_at, updated_at
		FROM gigs WHERE slug = $1 AND status = $2 AND moderation_state = $3`,
		slug, GigActive, ModerationApproved,
	)
	g, err := scanGig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.loadGigDetail(ctx, g)
}

// GetGigBasic returns a gig's core row regardless of status or moderation
// state, for callers such as an order workspace that must keep working even
// after the gig is later paused, archived, or removed from the catalog.
func (s *Store) GetGigBasic(ctx context.Context, gigID int64) (*Gig, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, seller_id, category_id, slug, title, description, status, moderation_state, created_at, updated_at
		FROM gigs WHERE id = $1`, gigID)
	g, err := scanGig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &g, nil
}

// GetGigByID returns full gig detail for a publicly visible gig, for callers
// that only have the numeric ID (such as a checkout draft).
func (s *Store) GetGigByID(ctx context.Context, gigID int64) (*GigDetail, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, seller_id, category_id, slug, title, description, status, moderation_state, created_at, updated_at
		FROM gigs WHERE id = $1 AND status = $2 AND moderation_state = $3`,
		gigID, GigActive, ModerationApproved,
	)
	g, err := scanGig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.loadGigDetail(ctx, g)
}

func (s *Store) loadGigDetail(ctx context.Context, g Gig) (*GigDetail, error) {
	d := &GigDetail{Gig: g}

	if g.CategoryID != nil {
		var name string
		if err := s.db.QueryRowContext(ctx, `SELECT name FROM categories WHERE id = $1`, *g.CategoryID).Scan(&name); err == nil {
			d.CategoryName = name
		}
	}

	pkgRows, err := s.db.QueryContext(ctx, `
		SELECT id, gig_id, tier, name, description, price_minor_units, currency, delivery_days, revisions
		FROM gig_packages WHERE gig_id = $1
		ORDER BY CASE tier WHEN 'basic' THEN 1 WHEN 'standard' THEN 2 WHEN 'premium' THEN 3 END`,
		g.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("list gig packages for gig %d: %w", g.ID, err)
	}
	for pkgRows.Next() {
		var p GigPackage
		if err := pkgRows.Scan(&p.ID, &p.GigID, &p.Tier, &p.Name, &p.Description,
			&p.PriceMinorUnits, &p.Currency, &p.DeliveryDays, &p.Revisions); err != nil {
			pkgRows.Close()
			return nil, fmt.Errorf("scan gig package: %w", err)
		}
		d.Packages = append(d.Packages, p)
	}
	if err := pkgRows.Err(); err != nil {
		pkgRows.Close()
		return nil, err
	}
	pkgRows.Close()

	addonRows, err := s.db.QueryContext(ctx, `
		SELECT id, gig_id, name, description, price_minor_units, currency, delivery_days_impact, position
		FROM gig_addons WHERE gig_id = $1 ORDER BY position`,
		g.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("list gig addons for gig %d: %w", g.ID, err)
	}
	for addonRows.Next() {
		var a GigAddon
		if err := addonRows.Scan(&a.ID, &a.GigID, &a.Name, &a.Description,
			&a.PriceMinorUnits, &a.Currency, &a.DeliveryDaysImpact, &a.Position); err != nil {
			addonRows.Close()
			return nil, fmt.Errorf("scan gig addon: %w", err)
		}
		d.Addons = append(d.Addons, a)
	}
	if err := addonRows.Err(); err != nil {
		addonRows.Close()
		return nil, err
	}
	addonRows.Close()

	mediaRows, err := s.db.QueryContext(ctx, `
		SELECT id, gig_id, media_path, alt_text, position, moderation_state
		FROM gig_media WHERE gig_id = $1 ORDER BY position`,
		g.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("list gig media for gig %d: %w", g.ID, err)
	}
	for mediaRows.Next() {
		var m GigMedia
		if err := mediaRows.Scan(&m.ID, &m.GigID, &m.MediaPath, &m.AltText, &m.Position, &m.ModerationState); err != nil {
			mediaRows.Close()
			return nil, fmt.Errorf("scan gig media: %w", err)
		}
		d.Media = append(d.Media, m)
	}
	if err := mediaRows.Err(); err != nil {
		mediaRows.Close()
		return nil, err
	}
	mediaRows.Close()

	tagRows, err := s.db.QueryContext(ctx, `
		SELECT t.name FROM gig_tags gt JOIN tags t ON t.id = gt.tag_id
		WHERE gt.gig_id = $1 ORDER BY t.name`,
		g.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("list gig tags for gig %d: %w", g.ID, err)
	}
	for tagRows.Next() {
		var name string
		if err := tagRows.Scan(&name); err != nil {
			tagRows.Close()
			return nil, fmt.Errorf("scan gig tag: %w", err)
		}
		d.Tags = append(d.Tags, name)
	}
	if err := tagRows.Err(); err != nil {
		tagRows.Close()
		return nil, err
	}
	tagRows.Close()

	seller := SellerSummary{UserID: g.SellerID}
	var userName string
	if err := s.db.QueryRowContext(ctx, `SELECT name FROM users WHERE id = $1`, g.SellerID).Scan(&userName); err == nil {
		seller.Name = userName
	}
	var displayName string
	var ratingAvg float64
	var ratingCount int
	if err := s.db.QueryRowContext(ctx,
		`SELECT display_name, rating_avg, rating_count FROM seller_profiles WHERE user_id = $1`, g.SellerID,
	).Scan(&displayName, &ratingAvg, &ratingCount); err == nil {
		seller.DisplayName = displayName
		seller.RatingAvg = ratingAvg
		seller.RatingCount = ratingCount
	}
	d.Seller = seller

	return d, nil
}

// ReplaceGigPackages atomically replaces all packages for a gig.
func (s *Store) ReplaceGigPackages(ctx context.Context, gigID int64, packages []GigPackage) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gig_packages WHERE gig_id = $1`, gigID); err != nil {
			return fmt.Errorf("clear gig packages for gig %d: %w", gigID, err)
		}
		for _, p := range packages {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO gig_packages (gig_id, tier, name, description, price_minor_units, currency, delivery_days, revisions)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
				gigID, p.Tier, p.Name, p.Description, p.PriceMinorUnits, p.Currency, p.DeliveryDays, p.Revisions,
			); err != nil {
				return fmt.Errorf("insert gig package %s for gig %d: %w", p.Tier, gigID, err)
			}
		}
		return nil
	})
}

// ReplaceGigAddons atomically replaces all add-ons for a gig.
func (s *Store) ReplaceGigAddons(ctx context.Context, gigID int64, addons []GigAddon) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gig_addons WHERE gig_id = $1`, gigID); err != nil {
			return fmt.Errorf("clear gig addons for gig %d: %w", gigID, err)
		}
		for i, a := range addons {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO gig_addons (gig_id, name, description, price_minor_units, currency, delivery_days_impact, position)
				VALUES ($1, $2, $3, $4, $5, $6, $7)`,
				gigID, a.Name, a.Description, a.PriceMinorUnits, a.Currency, a.DeliveryDaysImpact, i,
			); err != nil {
				return fmt.Errorf("insert gig addon for gig %d: %w", gigID, err)
			}
		}
		return nil
	})
}

// AddGigMedia attaches an image to a gig listing.
func (s *Store) AddGigMedia(ctx context.Context, gigID int64, path, altText string) (int64, error) {
	var position int
	if err := s.db.QueryRowContext(ctx,
		`SELECT coalesce(max(position) + 1, 0) FROM gig_media WHERE gig_id = $1`, gigID,
	).Scan(&position); err != nil {
		return 0, fmt.Errorf("next media position for gig %d: %w", gigID, err)
	}
	var id int64
	err := s.db.QueryRowContext(ctx, `
		INSERT INTO gig_media (gig_id, media_path, alt_text, position)
		VALUES ($1, $2, $3, $4) RETURNING id`,
		gigID, path, altText, position,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("add gig media for gig %d: %w", gigID, err)
	}
	return id, nil
}

// SetGigTags replaces a gig's tags from free-text names, upserting the tags
// themselves as needed. Blank names are ignored.
func (s *Store) SetGigTags(ctx context.Context, gigID int64, names []string) error {
	return s.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM gig_tags WHERE gig_id = $1`, gigID); err != nil {
			return fmt.Errorf("clear gig tags for gig %d: %w", gigID, err)
		}
		seen := map[string]bool{}
		for _, name := range names {
			name = strings.TrimSpace(name)
			if name == "" {
				continue
			}
			slug := slugify(name)
			if seen[slug] {
				continue
			}
			seen[slug] = true

			var tagID int64
			err := tx.QueryRowContext(ctx, `
				INSERT INTO tags (slug, name) VALUES ($1, $2)
				ON CONFLICT (slug) DO UPDATE SET slug = EXCLUDED.slug
				RETURNING id`,
				slug, name,
			).Scan(&tagID)
			if err != nil {
				return fmt.Errorf("upsert tag %q: %w", name, err)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO gig_tags (gig_id, tag_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
				gigID, tagID,
			); err != nil {
				return fmt.Errorf("link tag %q to gig %d: %w", name, gigID, err)
			}
		}
		return nil
	})
}

// GigSummary is the compact representation used in listings: search
// results, browse pages, favorites, and the homepage.
type GigSummary struct {
	ID                      int64
	Slug                    string
	Title                   string
	SellerID                int64
	SellerName              string
	CategoryName            string
	StartingPriceMinorUnits int64
	Currency                string
	RatingAvg               float64
	RatingCount             int
}

// SearchParams filters and orders a public gig listing.
type SearchParams struct {
	Query        string
	CategorySlug string
	TagSlug      string
	SellerID     int64  // 0 matches any seller
	Sort         string // "newest" (default), "price_asc", "price_desc"
	Page         int
	PerPage      int
}

const searchListingSQL = `
	SELECT g.id, g.slug, g.title, g.seller_id, u.name, coalesce(c.name, ''),
	       coalesce(p.starting_price, 0), coalesce(p.currency, 'USD'),
	       coalesce(sp.rating_avg, 0), coalesce(sp.rating_count, 0),
	       count(*) OVER () AS total
	FROM gigs g
	JOIN users u ON u.id = g.seller_id
	LEFT JOIN categories c ON c.id = g.category_id
	LEFT JOIN seller_profiles sp ON sp.user_id = g.seller_id
	LEFT JOIN (
		SELECT gig_id, MIN(price_minor_units) AS starting_price, MIN(currency) AS currency
		FROM gig_packages GROUP BY gig_id
	) p ON p.gig_id = g.id
	WHERE g.status = 'active' AND g.moderation_state = 'approved'
	  AND ($1 = '' OR g.search_vector @@ plainto_tsquery('english', $1))
	  AND ($2 = '' OR c.slug = $2)
	  AND ($3 = '' OR EXISTS (
	        SELECT 1 FROM gig_tags gt JOIN tags t ON t.id = gt.tag_id
	        WHERE gt.gig_id = g.id AND t.slug = $3))
	  AND ($7 = 0 OR g.seller_id = $7)
	ORDER BY
	  CASE WHEN $4 = 'price_asc'  THEN coalesce(p.starting_price, 0) END ASC NULLS LAST,
	  CASE WHEN $4 = 'price_desc' THEN coalesce(p.starting_price, 0) END DESC NULLS LAST,
	  CASE WHEN $4 NOT IN ('price_asc', 'price_desc') THEN g.created_at END DESC NULLS LAST,
	  g.id DESC
	LIMIT $5 OFFSET $6`

// SearchGigs returns a page of publicly visible gigs matching the given
// filters, plus the total number of matches across all pages.
func (s *Store) SearchGigs(ctx context.Context, params SearchParams) ([]GigSummary, int, error) {
	page := params.Page
	if page < 1 {
		page = 1
	}
	perPage := params.PerPage
	if perPage < 1 || perPage > 100 {
		perPage = 20
	}

	rows, err := s.db.QueryContext(ctx, searchListingSQL,
		strings.TrimSpace(params.Query), params.CategorySlug, params.TagSlug, params.Sort,
		perPage, (page-1)*perPage, params.SellerID,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("search gigs: %w", err)
	}
	defer rows.Close()

	var summaries []GigSummary
	total := 0
	for rows.Next() {
		var g GigSummary
		if err := rows.Scan(&g.ID, &g.Slug, &g.Title, &g.SellerID, &g.SellerName, &g.CategoryName,
			&g.StartingPriceMinorUnits, &g.Currency, &g.RatingAvg, &g.RatingCount, &total); err != nil {
			return nil, 0, fmt.Errorf("scan gig summary: %w", err)
		}
		summaries = append(summaries, g)
	}
	return summaries, total, rows.Err()
}

// FeaturedGigs returns the most recently published gigs for the homepage.
func (s *Store) FeaturedGigs(ctx context.Context, limit int) ([]GigSummary, error) {
	summaries, _, err := s.SearchGigs(ctx, SearchParams{Sort: "newest", PerPage: limit})
	return summaries, err
}

// ToggleFavorite flips whether userID has favorited gigID, returning the new
// state.
func (s *Store) ToggleFavorite(ctx context.Context, userID, gigID int64) (bool, error) {
	var favorited bool
	err := s.Tx(ctx, func(tx *sql.Tx) error {
		var exists bool
		if err := tx.QueryRowContext(ctx,
			`SELECT EXISTS (SELECT 1 FROM favorites WHERE user_id = $1 AND gig_id = $2)`,
			userID, gigID,
		).Scan(&exists); err != nil {
			return fmt.Errorf("check favorite: %w", err)
		}
		if exists {
			if _, err := tx.ExecContext(ctx,
				`DELETE FROM favorites WHERE user_id = $1 AND gig_id = $2`, userID, gigID); err != nil {
				return fmt.Errorf("remove favorite: %w", err)
			}
			favorited = false
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO favorites (user_id, gig_id) VALUES ($1, $2)`, userID, gigID); err != nil {
			return fmt.Errorf("add favorite: %w", err)
		}
		favorited = true
		return nil
	})
	return favorited, err
}

// IsFavorite reports whether userID has favorited gigID.
func (s *Store) IsFavorite(ctx context.Context, userID, gigID int64) (bool, error) {
	var exists bool
	err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM favorites WHERE user_id = $1 AND gig_id = $2)`,
		userID, gigID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is favorite: %w", err)
	}
	return exists, nil
}

// ListFavoriteGigs returns the gigs a user has favorited, most recent first.
func (s *Store) ListFavoriteGigs(ctx context.Context, userID int64) ([]GigSummary, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.slug, g.title, g.seller_id, u.name, coalesce(c.name, ''),
		       coalesce(p.starting_price, 0), coalesce(p.currency, 'USD'),
		       coalesce(sp.rating_avg, 0), coalesce(sp.rating_count, 0)
		FROM favorites f
		JOIN gigs g ON g.id = f.gig_id
		JOIN users u ON u.id = g.seller_id
		LEFT JOIN categories c ON c.id = g.category_id
		LEFT JOIN seller_profiles sp ON sp.user_id = g.seller_id
		LEFT JOIN (
			SELECT gig_id, MIN(price_minor_units) AS starting_price, MIN(currency) AS currency
			FROM gig_packages GROUP BY gig_id
		) p ON p.gig_id = g.id
		WHERE f.user_id = $1
		ORDER BY f.created_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list favorite gigs for user %d: %w", userID, err)
	}
	defer rows.Close()

	var summaries []GigSummary
	for rows.Next() {
		var g GigSummary
		if err := rows.Scan(&g.ID, &g.Slug, &g.Title, &g.SellerID, &g.SellerName, &g.CategoryName,
			&g.StartingPriceMinorUnits, &g.Currency, &g.RatingAvg, &g.RatingCount); err != nil {
			return nil, fmt.Errorf("scan favorite gig: %w", err)
		}
		summaries = append(summaries, g)
	}
	return summaries, rows.Err()
}

func nullInt64(v *int64) sql.NullInt64 {
	if v == nil {
		return sql.NullInt64{}
	}
	return sql.NullInt64{Int64: *v, Valid: true}
}
