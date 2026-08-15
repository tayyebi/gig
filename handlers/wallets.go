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
	s.flashNotice(r, fmt.Sprintf("Wallet confirmed. It becomes payout-eligible after %s.", s.Cfg.WalletChangeCooldown.String()))
	http.Redirect(w, r, "/sell/wallet", http.StatusSeeOther)
}
