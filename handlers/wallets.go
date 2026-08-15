package handlers

import (
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tayyebi/gig/services"
	"github.com/tayyebi/gig/store"
)

// evmAddressPattern is a basic EIP-55-shaped check (0x + 40 hex chars), not
// a full checksum validation — good enough to reject obvious typos and
// non-address input before it is ever encrypted and stored.
var evmAddressPattern = regexp.MustCompile(`^0x[0-9a-fA-F]{40}$`)

// walletSettings shows the seller's current and pending payout wallets and
// the form to submit a new one. Rendered as a hand-built fragment, the same
// pattern as the admin payment views, since a dedicated settings template is
// out of scope for this pass.
func (s *Server) walletSettings(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	sess := s.sessionFrom(r)
	if s.Wallet == nil {
		s.render(w, r, http.StatusOK, pageWithRawBody("Payout wallets",
			`<section class="container"><h1>Payout wallets</h1><p>Wallet payouts are not enabled on this deployment yet.</p></section>`))
		return
	}

	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Payout wallets</h1>`)
	for _, network := range []string{"base", "polygon"} {
		for _, asset := range []string{"usdc", "usdt"} {
			if wallet, err := s.Store.GetConfirmedWallet(r.Context(), u.ID, network, asset); err == nil {
				status := "confirmed, in cooling-off period"
				if wallet.IsPayoutEligible(time.Now()) {
					status = "confirmed, eligible for payout"
				}
				sb.WriteString(fmt.Sprintf(`<p>%s %s: wallet on file (%s)</p>`,
					html.EscapeString(strings.ToUpper(asset)), html.EscapeString(network), html.EscapeString(status)))
			}
		}
	}
	sb.WriteString(fmt.Sprintf(`<h2>Add or change a wallet</h2>
<p class="help">Changing a wallet requires confirming it by email, and a %s cooling-off period after confirmation before it becomes payout-eligible.</p>
<form method="post" action="/sell/wallet" novalidate>
%s
<label for="network">Network</label>
<select id="network" name="network" required>
<option value="base">Base</option>
<option value="polygon">Polygon</option>
</select>
<label for="asset">Asset</label>
<select id="asset" name="asset" required>
<option value="usdc">USDC</option>
<option value="usdt">USDT</option>
</select>
<label for="address">Wallet address</label>
<input id="address" name="address" type="text" required pattern="0x[0-9a-fA-F]{40}" maxlength="42" placeholder="0x...">
<button class="btn" type="submit">Submit wallet</button>
</form>
</section>`, s.Cfg.WalletChangeCooldown.String(), csrfInputHTML(sess.CSRF)))
	s.render(w, r, http.StatusOK, pageWithRawBody("Payout wallets", sb.String()))
}

// submitWallet validates and stores a new pending wallet, then emails a
// one-time confirmation link, reusing the same auth-token machinery as
// email verification and password reset.
func (s *Server) submitWallet(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	if s.Wallet == nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	network := strings.TrimSpace(r.FormValue("network"))
	asset := strings.TrimSpace(r.FormValue("asset"))
	address := strings.TrimSpace(r.FormValue("address"))
	if network != "base" && network != "polygon" {
		s.flashError(r, "Choose a supported network.")
		http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
		return
	}
	if asset != "usdc" && asset != "usdt" {
		s.flashError(r, "Choose a supported asset.")
		http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
		return
	}
	if !evmAddressPattern.MatchString(address) {
		s.flashError(r, "That does not look like a valid wallet address.")
		http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
		return
	}
	address = strings.ToLower(address)

	encrypted, err := s.Wallet.Encrypt(address)
	if err != nil {
		s.renderError(w, err)
		return
	}
	fingerprint := services.WalletAddressFingerprint(address)
	walletID, err := s.Store.CreatePendingWallet(r.Context(), u.ID, network, asset, encrypted, fingerprint)
	if err != nil {
		s.renderError(w, err)
		return
	}

	raw, hash, err := services.GenerateSessionToken()
	if err != nil {
		s.renderError(w, err)
		return
	}
	if _, err := s.Store.CreateAuthToken(r.Context(), u.ID, store.TokenWalletConfirmation, hash, s.Cfg.AuthTokenTTL); err != nil {
		s.renderError(w, err)
		return
	}
	link := fmt.Sprintf("%s/sell/wallet/confirm?token=%s&wallet_id=%d", s.Cfg.BaseURL, url.QueryEscape(raw), walletID)
	if err := s.Mailer.Send(r.Context(), services.Email{
		To:      u.Email,
		Subject: "Confirm your payout wallet",
		Body: fmt.Sprintf(`Hi %s,

Confirm the %s %s payout wallet you just added by opening this link:

%s

If you did not request this, ignore this email; the wallet will not become active.

- Gig`, u.Name, strings.ToUpper(asset), network, link),
	}); err != nil {
		s.Log.Error("send wallet confirmation email", "error", err, "user_id", u.ID)
	}

	s.audit(r.Context(), &u.ID, r, "wallet.submitted", "seller_wallet", strconv.FormatInt(walletID, 10),
		map[string]any{"network": network, "asset": asset})
	s.flashNotice(r, "Check your email to confirm this wallet.")
	http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
}

// nonTerminalPayoutStatuses are payouts that still tie up seller_available
// balance: not yet completed or canceled.
var nonTerminalPayoutStatuses = []string{store.PayoutQueued, store.PayoutNeedsManualReview, store.PayoutReadyForManualExecution}

// availablePayoutBalance returns the seller's seller_available ledger
// balance for currency, minus the sum of that seller's own payouts still in
// flight, so a seller cannot queue more payouts than they actually have
// available (the ledger balance alone does not reflect money already
// promised to a pending payout).
func (s *Server) availablePayoutBalance(r *http.Request, sellerID int64, currency string) (int64, error) {
	balances, err := s.Store.SellerBalances(r.Context(), sellerID)
	if err != nil {
		return 0, err
	}
	var available int64
	for _, b := range balances {
		if b.Kind == "seller_available" && strings.EqualFold(b.Currency, currency) {
			available = b.BalanceMinor
		}
	}
	payouts, err := s.Store.ListPayoutsBySeller(r.Context(), sellerID, 200)
	if err != nil {
		return 0, err
	}
	for _, p := range payouts {
		if !strings.EqualFold(p.Currency, currency) {
			continue
		}
		for _, status := range nonTerminalPayoutStatuses {
			if p.Status == status {
				available -= p.AmountMinor
				break
			}
		}
	}
	return available, nil
}

// parseDollarsToMinor parses a plain decimal string like "12.34" into
// 2-decimal minor units (cents). Payout amounts are seller-entered text, so
// this is the one place in the codebase that needs to turn user input into
// money rather than working from a server-computed total.
func parseDollarsToMinor(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, fmt.Errorf("empty amount")
	}
	whole, frac, hasFrac := strings.Cut(s, ".")
	if !hasFrac {
		frac = "00"
	}
	if len(frac) == 1 {
		frac += "0"
	}
	if len(frac) != 2 {
		return 0, fmt.Errorf("invalid amount")
	}
	w, err := strconv.ParseInt(whole, 10, 63)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("invalid amount")
	}
	f, err := strconv.ParseInt(frac, 10, 63)
	if err != nil || f < 0 || f > 99 {
		return 0, fmt.Errorf("invalid amount")
	}
	return w*100 + f, nil
}

// payoutRequestForm shows the seller's available balance, in-flight
// payouts, and a form to request a new one against their confirmed,
// cooling-off-cleared wallet.
func (s *Server) payoutRequestForm(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	sess := s.sessionFrom(r)
	if s.Wallet == nil {
		s.render(w, r, http.StatusOK, pageWithRawBody("Request payout",
			`<section class="container"><h1>Request payout</h1><p>Wallet payouts are not enabled on this deployment yet.</p></section>`))
		return
	}

	paused, err := s.Store.PayoutsPaused(r.Context())
	if err != nil {
		s.renderError(w, err)
		return
	}

	var sb strings.Builder
	sb.WriteString(`<section class="container"><h1>Request payout</h1>`)
	if paused {
		sb.WriteString(`<p><strong>Payouts are paused platform-wide right now.</strong> You can still request one; it will be held until payouts resume.</p>`)
	}

	type eligibleWallet struct {
		Network, Asset string
		WalletID       int64
		AvailableMinor int64
	}
	var eligible []eligibleWallet
	for _, network := range []string{"base", "polygon"} {
		for _, asset := range []string{"usdc", "usdt"} {
			wallet, err := s.Store.GetConfirmedWallet(r.Context(), u.ID, network, asset)
			if err != nil || !wallet.IsPayoutEligible(time.Now()) {
				continue
			}
			available, err := s.availablePayoutBalance(r, u.ID, "USD")
			if err != nil {
				s.renderError(w, err)
				return
			}
			eligible = append(eligible, eligibleWallet{Network: network, Asset: asset, WalletID: wallet.ID, AvailableMinor: available})
		}
	}
	if len(eligible) == 0 {
		sb.WriteString(`<p>No confirmed, payout-eligible wallet on file yet. <a href="/sell/wallet">Add or confirm a wallet</a> first.</p>`)
	} else {
		for _, e := range eligible {
			sb.WriteString(fmt.Sprintf(`<h2>%s %s</h2>
<p>Available: $%.2f</p>
<form method="post" action="/sell/payouts/request" novalidate>
%s
<input type="hidden" name="wallet_id" value="%d">
<label for="amount-%d">Amount (USD)</label>
<input id="amount-%d" name="amount" type="text" required inputmode="decimal" placeholder="0.00">
<button class="btn" type="submit">Request payout</button>
</form>`,
				html.EscapeString(strings.ToUpper(e.Asset)), html.EscapeString(e.Network),
				float64(e.AvailableMinor)/100, csrfInputHTML(sess.CSRF), e.WalletID, e.WalletID, e.WalletID))
		}
	}

	payouts, err := s.Store.ListPayoutsBySeller(r.Context(), u.ID, 20)
	if err != nil {
		s.renderError(w, err)
		return
	}
	sb.WriteString(`<h2>Payout history</h2>`)
	if len(payouts) == 0 {
		sb.WriteString(`<p>No payouts requested yet.</p>`)
	} else {
		sb.WriteString(`<table><thead><tr><th>Requested</th><th>Amount</th><th>Network/asset</th><th>Status</th></tr></thead><tbody>`)
		for _, p := range payouts {
			sb.WriteString(fmt.Sprintf(`<tr><td>%s</td><td>$%.2f %s</td><td>%s / %s</td><td>%s</td></tr>`,
				html.EscapeString(p.CreatedAt.Format("2006-01-02 15:04")),
				float64(p.AmountMinor)/100, html.EscapeString(p.Currency),
				html.EscapeString(p.Network), html.EscapeString(p.Asset), html.EscapeString(p.Status)))
		}
		sb.WriteString(`</tbody></table>`)
	}
	sb.WriteString(`</section>`)
	s.render(w, r, http.StatusOK, pageWithRawBody("Request payout", sb.String()))
}

// submitPayoutRequest validates the requested amount against the seller's
// actual available balance and their in-flight payouts, then queues it —
// high-value requests are routed to manual review, mirroring the same
// services.IsHighValue threshold used for admin-created alerts.
func (s *Server) submitPayoutRequest(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	if s.Wallet == nil {
		s.notFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		s.renderError(w, err)
		return
	}
	walletID, _ := strconv.ParseInt(r.FormValue("wallet_id"), 10, 64)
	amountMinor, err := parseDollarsToMinor(r.FormValue("amount"))
	if err != nil || amountMinor <= 0 {
		s.flashError(r, "Enter a valid payout amount.")
		http.Redirect(w, r, "/sell/payouts", http.StatusSeeOther)
		return
	}

	wallet, err := s.Store.GetWallet(r.Context(), walletID)
	if err != nil || wallet.UserID != u.ID {
		s.flashError(r, "That wallet is not on your account.")
		http.Redirect(w, r, "/sell/payouts", http.StatusSeeOther)
		return
	}
	if !wallet.IsPayoutEligible(time.Now()) {
		s.flashError(r, "That wallet is not confirmed and past its cooling-off period yet.")
		http.Redirect(w, r, "/sell/payouts", http.StatusSeeOther)
		return
	}

	available, err := s.availablePayoutBalance(r, u.ID, "USD")
	if err != nil {
		s.renderError(w, err)
		return
	}
	if amountMinor > available {
		s.flashError(r, "That amount is more than your available balance.")
		http.Redirect(w, r, "/sell/payouts", http.StatusSeeOther)
		return
	}

	initialStatus := store.PayoutQueued
	if services.IsHighValue(amountMinor) {
		initialStatus = store.PayoutNeedsManualReview
	}
	payoutID, err := s.Store.CreatePayout(r.Context(), u.ID, walletID, amountMinor, "USD", wallet.Network, wallet.Asset, initialStatus)
	if err != nil {
		s.renderError(w, err)
		return
	}
	s.audit(r.Context(), &u.ID, r, "payout.requested", "payout", strconv.FormatInt(payoutID, 10),
		map[string]any{"amount_minor": amountMinor, "network": wallet.Network, "asset": wallet.Asset, "status": initialStatus})
	if initialStatus == store.PayoutNeedsManualReview {
		s.flashNotice(r, "Payout requested. This amount needs admin review before it is queued for execution.")
	} else {
		s.flashNotice(r, "Payout requested and queued.")
	}
	http.Redirect(w, r, "/sell/payouts", http.StatusSeeOther)
}

// confirmWallet consumes the one-time token and marks the wallet confirmed,
// starting its cooling-off period before it is payout-eligible.
func (s *Server) confirmWallet(w http.ResponseWriter, r *http.Request) {
	u := s.userFrom(r)
	token := strings.TrimSpace(r.URL.Query().Get("token"))
	walletID, _ := strconv.ParseInt(r.URL.Query().Get("wallet_id"), 10, 64)
	if token == "" || walletID == 0 {
		s.flashError(r, "That confirmation link is invalid.")
		http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
		return
	}
	tok, err := s.Store.GetAuthToken(r.Context(), store.TokenWalletConfirmation, hashToken(token))
	if err != nil {
		s.flashError(r, "That confirmation link is invalid or expired.")
		http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
		return
	}
	wallet, err := s.Store.GetWallet(r.Context(), walletID)
	if err != nil || wallet.UserID != tok.UserID || (u != nil && wallet.UserID != u.ID) {
		s.flashError(r, "That confirmation link does not match this wallet.")
		http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
		return
	}
	if err := s.Store.ConfirmWallet(r.Context(), walletID, s.Cfg.WalletChangeCooldown); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.flashError(r, "This wallet was already confirmed or replaced.")
			http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
			return
		}
		s.renderError(w, err)
		return
	}
	if err := s.Store.UseAuthToken(r.Context(), tok.ID); err != nil {
		s.Log.Error("use wallet confirmation token", "token_id", tok.ID, "error", err)
	}
	s.audit(r.Context(), &tok.UserID, r, "wallet.confirmed", "seller_wallet", strconv.FormatInt(walletID, 10), nil)
	s.alertWalletChange(r, tok.UserID, walletID, wallet.Network, wallet.Asset)
	s.flashNotice(r, fmt.Sprintf("Wallet confirmed. It becomes payout-eligible after %s.", s.Cfg.WalletChangeCooldown.String()))
	http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
}
