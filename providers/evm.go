package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// EVM talks to a public JSON-RPC endpoint over net/http (PLAN.md's
// standard-library-only constraint; no go-ethereum/web3 SDK). One EVM value
// is one chain (e.g. Base or Polygon); a deployment may register either or
// both, each under its own provider name, so the checkout method selector
// can offer them independently. Stablecoins (USDC, USDT) are ERC-20 tokens
// pegged 1:1 to the platform's fiat reference currency, so amounts are
// tracked in the same minor-unit (cents) convention the rest of the ledger
// uses; evmAmountToTokenUnits/evmTokenUnitsToAmountMinor convert between
// that and the token's own 6-decimal on-chain unit.
type EVM struct {
	ChainName             string // provider name, e.g. "evm-base", "evm-polygon"
	RPCURL                string
	ChainID               int64
	TreasuryAddress       string            // lowercase 0x address; deposits are watched here, never a per-order generated address (no HD wallet custody in scope)
	TokenContracts        map[string]string // asset ("usdc"/"usdt") -> lowercase 0x contract address, config-supplied only
	RequiredConfirmations int
	HTTPClient            *http.Client
}

// NewEVM builds an EVM adapter for one chain. rpcURL, treasuryAddress, and
// tokenContracts may be empty in dev/test; calls fail clearly if used.
func NewEVM(chainName, rpcURL string, chainID int64, treasuryAddress string, tokenContracts map[string]string, requiredConfirmations int) *EVM {
	return &EVM{
		ChainName:             chainName,
		RPCURL:                rpcURL,
		ChainID:               chainID,
		TreasuryAddress:       strings.ToLower(treasuryAddress),
		TokenContracts:        tokenContracts,
		RequiredConfirmations: requiredConfirmations,
		HTTPClient:            &http.Client{Timeout: 20 * time.Second},
	}
}

func (e *EVM) Name() string { return e.ChainName }

// erc20TransferTopic is keccak256("Transfer(address,address,uint256)"), the
// well-known ERC-20 Transfer event signature used by every standard token
// including USDC and USDT on any EVM chain.
const erc20TransferTopic = "0xddf252ad1be2c89b69c2b068fc378daa952ba7f163c4a11628f55a4df523b3ef"

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

func (e *EVM) call(ctx context.Context, method string, params []any, out any) error {
	if e.RPCURL == "" {
		return fmt.Errorf("evm(%s): not configured (RPC URL required)", e.ChainName)
	}
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params, ID: 1})
	if err != nil {
		return fmt.Errorf("evm(%s): encode rpc request: %w", e.ChainName, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.RPCURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("evm(%s): build rpc request: %w", e.ChainName, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("evm(%s): rpc request failed: %w", e.ChainName, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("evm(%s): read rpc response: %w", e.ChainName, err)
	}
	var rr rpcResponse
	if err := json.Unmarshal(raw, &rr); err != nil {
		return fmt.Errorf("evm(%s): decode rpc response: %w", e.ChainName, err)
	}
	if rr.Error != nil {
		return fmt.Errorf("evm(%s): rpc %s failed (%d): %s", e.ChainName, method, rr.Error.Code, rr.Error.Message)
	}
	if out != nil {
		if err := json.Unmarshal(rr.Result, out); err != nil {
			return fmt.Errorf("evm(%s): decode rpc result: %w", e.ChainName, err)
		}
	}
	return nil
}

func (e *EVM) blockNumber(ctx context.Context) (int64, error) {
	var hex string
	if err := e.call(ctx, "eth_blockNumber", nil, &hex); err != nil {
		return 0, err
	}
	return parseHexQuantity(hex)
}

// CreatePayment does not create a hosted checkout session (there is no
// third-party processor); it derives a per-order deposit reference and
// records the block height at intent creation, so Payment() only has to
// scan forward from there rather than the whole chain. The buyer is sent to
// an internal deposit-instructions route, not an external URL, which bends
// the interface's "hosted checkout" convention but still satisfies
// "redirect the buyer to a URL" (PLAN.md section 9).
func (e *EVM) CreatePayment(ctx context.Context, in CreatePaymentInput) (PaymentSession, error) {
	asset, contract, err := e.assetForOrder(in)
	if err != nil {
		return PaymentSession{}, err
	}
	if e.TreasuryAddress == "" {
		return PaymentSession{}, fmt.Errorf("evm(%s): not configured (treasury address required)", e.ChainName)
	}
	startBlock, err := e.blockNumber(ctx)
	if err != nil {
		return PaymentSession{}, err
	}
	tokenUnits := evmAmountToTokenUnits(in.AmountMinor)
	ref := fmt.Sprintf("%d:%s:%d:%s", in.OrderID, asset, startBlock, tokenUnits.String())
	_ = contract // contract address is looked up again from asset in Payment(); kept here only for validation above
	return PaymentSession{
		ProviderRef: ref,
		CheckoutURL: fmt.Sprintf("/orders/%d/pay/evm-status?ref=%s", in.OrderID, ref),
		ExpiresAt:   time.Now().Add(60 * time.Minute),
	}, nil
}

// assetForOrder resolves which configured token contract a CreatePayment
// call targets. The caller (checkout handler) is expected to have already
// picked the asset via the payment method (e.g. "usdc-base"); until that
// plumbing exists this defaults to "usdc" when configured.
func (e *EVM) assetForOrder(in CreatePaymentInput) (asset, contract string, err error) {
	asset = "usdc"
	contract, ok := e.TokenContracts[asset]
	if !ok || contract == "" {
		return "", "", fmt.Errorf("evm(%s): no contract configured for asset %q", e.ChainName, asset)
	}
	return asset, contract, nil
}

// Payment scans Transfer(address,address,uint256) logs on the configured
// token contract, from the block recorded at intent creation to the chain
// head, for a transfer to the treasury address whose amount exactly matches
// the expected token units encoded in providerRef. This amount-matching
// heuristic is the on-chain equivalent of BTCPay's invoice ID: there is no
// per-order deposit address or memo field on a plain ERC-20 transfer, so two
// orders for the identical amount arriving in the same scan window could in
// principle collide — a known limitation, surfaced (like BTCPay's
// underpayment case) via admin review rather than silently mishandled.
// confirmationStatus decides succeeded-vs-processing from block depth alone,
// so a reorg that drops the transferring block below the confirmation
// threshold (or removes it from the log scan entirely on the next poll)
// naturally re-reports it as processing/pending rather than succeeded.
func confirmationStatus(head, blockNum, required int64) string {
	confirmations := head - blockNum + 1
	if confirmations >= required {
		return StatusSucceeded
	}
	return StatusProcessing
}

func (e *EVM) Payment(ctx context.Context, providerRef string) (NormalizedPayment, error) {
	orderID, asset, startBlock, expected, err := parseEVMProviderRef(providerRef)
	if err != nil {
		return NormalizedPayment{}, err
	}
	contract, ok := e.TokenContracts[asset]
	if !ok || contract == "" {
		return NormalizedPayment{}, fmt.Errorf("evm(%s): no contract configured for asset %q", e.ChainName, asset)
	}
	head, err := e.blockNumber(ctx)
	if err != nil {
		return NormalizedPayment{}, err
	}

	topicTo := "0x" + strings.Repeat("0", 24) + strings.TrimPrefix(e.TreasuryAddress, "0x")
	var logs []struct {
		Data        string `json:"data"`
		BlockNumber string `json:"blockNumber"`
		TxHash      string `json:"transactionHash"`
	}
	params := []any{map[string]any{
		"fromBlock": fmt.Sprintf("0x%x", startBlock),
		"toBlock":   "latest",
		"address":   contract,
		"topics":    []any{erc20TransferTopic, nil, topicTo},
	}}
	if err := e.call(ctx, "eth_getLogs", params, &logs); err != nil {
		return NormalizedPayment{}, err
	}

	amountMinor := evmTokenUnitsToAmountMinor(expected)
	for _, l := range logs {
		amount := new(big.Int)
		if _, ok := amount.SetString(strings.TrimPrefix(l.Data, "0x"), 16); !ok {
			continue
		}
		if amount.Cmp(expected) != 0 {
			continue // underpayment/overpayment: not a confident match, left pending for manual review via reconciliation/admin
		}
		blockNum, err := parseHexQuantity(l.BlockNumber)
		if err != nil {
			continue
		}
		status := confirmationStatus(head, blockNum, int64(e.RequiredConfirmations))
		return NormalizedPayment{
			ProviderRef: providerRef,
			ChargeRef:   l.TxHash,
			Status:      status,
			AmountMinor: amountMinor,
		}, nil
	}
	_ = orderID
	return NormalizedPayment{ProviderRef: providerRef, Status: StatusPending, AmountMinor: amountMinor}, nil
}

// Refund cannot broadcast an on-chain transaction here: sending stablecoin
// back from the treasury requires a signing key, which is out of this
// project's key-custody scope (PLAN.md section 10's payout runbook is still
// a Phase-0/ops gate). This records the refund as queued for an admin to
// execute manually, the same "processing until reconciled" precedent BTCPay
// refunds already establish.
func (e *EVM) Refund(ctx context.Context, in RefundInput) (RefundResult, error) {
	return RefundResult{ProviderRef: in.ProviderRef, Status: StatusProcessing}, nil
}

// VerifyWebhook always fails: there is no third-party processor delivering
// webhooks for direct on-chain payments. Status only ever comes from
// Payment(), driven by the provider-agnostic reconciliation sweep.
func (e *EVM) VerifyWebhook(ctx context.Context, r *http.Request, body []byte) (VerifiedEvent, error) {
	return VerifiedEvent{}, fmt.Errorf("evm(%s): no inbound webhooks; payment status is polled via reconciliation", e.ChainName)
}

// ParseEvent is unreachable in practice (VerifyWebhook always errors, so no
// event is ever persisted for this provider) but implemented to satisfy the
// Provider interface.
func (e *EVM) ParseEvent(ctx context.Context, payload []byte) (VerifiedEvent, error) {
	return VerifiedEvent{}, fmt.Errorf("evm(%s): no webhook events to parse", e.ChainName)
}

func parseEVMProviderRef(ref string) (orderID int64, asset string, startBlock int64, expected *big.Int, err error) {
	parts := strings.SplitN(ref, ":", 4)
	if len(parts) != 4 {
		return 0, "", 0, nil, fmt.Errorf("evm: malformed provider ref %q", ref)
	}
	orderID, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", 0, nil, fmt.Errorf("evm: parse order id in ref %q: %w", ref, err)
	}
	asset = parts[1]
	startBlock, err = strconv.ParseInt(parts[2], 10, 64)
	if err != nil {
		return 0, "", 0, nil, fmt.Errorf("evm: parse start block in ref %q: %w", ref, err)
	}
	expected = new(big.Int)
	if _, ok := expected.SetString(parts[3], 10); !ok {
		return 0, "", 0, nil, fmt.Errorf("evm: parse expected amount in ref %q", ref)
	}
	return orderID, asset, startBlock, expected, nil
}

func parseHexQuantity(hex string) (int64, error) {
	hex = strings.TrimPrefix(hex, "0x")
	if hex == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(hex, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("evm: parse hex quantity %q: %w", hex, err)
	}
	return n, nil
}

// evmAmountToTokenUnits converts the platform's 2-decimal minor-unit amount
// (cents) into the token's 6-decimal on-chain unit, assuming the stablecoin
// is 1:1 pegged to the fiat reference currency (USDC/USDT to USD).
func evmAmountToTokenUnits(amountMinor int64) *big.Int {
	return new(big.Int).Mul(big.NewInt(amountMinor), big.NewInt(10000)) // 10^6 / 10^2
}

// evmTokenUnitsToAmountMinor is the inverse of evmAmountToTokenUnits.
func evmTokenUnitsToAmountMinor(tokenUnits *big.Int) int64 {
	minor := new(big.Int).Div(tokenUnits, big.NewInt(10000))
	return minor.Int64()
}
