// Package facilitator implements the Nano (XNO) exact x402 scheme as a
// stateless verifier of payer-broadcast send blocks.
package facilitator

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/gosuda/x402-facilitator/scheme"
	nanoscheme "github.com/gosuda/x402-facilitator/scheme/nano"
	"github.com/gosuda/x402-facilitator/types"
)

// AssetNano is the CAIP-19-style asset identifier for Nano's native coin. The
// x402 exact scheme names the native asset of a feeless DAG ledger by its
// ticker symbol; there is no token contract.
const AssetNano = "xno"

var _ scheme.Facilitator = (*NanoFacilitator)(nil)

// blockFetcher retrieves and parses a Nano block by hash. It exists so tests
// can stub the node boundary without a live network.
type blockFetcher interface {
	BlockInfo(ctx context.Context, hash string) (*nanoscheme.BlockInfo, error)
}

// NanoFacilitator verifies exact x402 payments on the Nano network. Nano is
// final-by-publication: the payer broadcasts its own signed send block
// directly to the ledger, so there is no facilitator broadcast, no gas token
// and no custody key. This facilitator reads the block back off the chain and
// proves it satisfies the payment requirements.
type NanoFacilitator struct {
	scheme   types.Scheme
	network  string
	block    blockFetcher
	endpoint string
}

// NewNanoFacilitator builds a Nano facilitator for a CAIP-2 Nano network
// (e.g. "nano:mainnet"). rpcURLs is the ordered list of Nano node endpoints
// to query; apiKey is an optional header value for nodes that require it
// (e.g. rpc.nano.to). No private key is needed — the facilitator never signs
// or broadcasts, it only verifies blocks the payer already published.
func NewNanoFacilitator(network, apiKey string, rpcURLs ...string) (*NanoFacilitator, error) {
	if !strings.HasPrefix(network, "nano:") || strings.TrimPrefix(network, "nano:") == "" {
		return nil, fmt.Errorf("invalid Nano network %q: expected a CAIP-2 identifier like nano:mainnet", network)
	}
	endpoints := rpcURLs
	if len(endpoints) == 0 {
		endpoints = nanoscheme.DefaultEndpoints
	}
	// The first endpoint is the active one the resource server advertises to
	// payers through /supported.
	return &NanoFacilitator{
		scheme:   types.Exact,
		network:  network,
		block:    nanoscheme.NewClient(endpoints, apiKey),
		endpoint: endpoints[0],
	}, nil
}

func (n *NanoFacilitator) Verify(ctx context.Context, payload *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentVerifyResponse, error) {
	if payload == nil || req == nil {
		return &types.PaymentVerifyResponse{
			IsValid:       false,
			InvalidReason: types.ErrInvalidPayloadFormat.Error(),
		}, nil
	}
	_, payer, invalid := n.verifyPayment(ctx, payload, req)
	if invalid != nil {
		invalid.Payer = payer
		return invalid, nil
	}
	return &types.PaymentVerifyResponse{
		IsValid: true,
		Payer:   payer,
	}, nil
}

func (n *NanoFacilitator) Settle(ctx context.Context, payload *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentSettleResponse, error) {
	network := types.Network("")
	if req != nil {
		network = types.Network(req.Network)
	}
	fail := func(reason, message, payer, blockHash string) (*types.PaymentSettleResponse, error) {
		return &types.PaymentSettleResponse{
			Success:      false,
			ErrorReason:  reason,
			ErrorMessage: message,
			Payer:        payer,
			Transaction:  blockHash,
			Network:      network,
		}, nil
	}
	if payload == nil || req == nil {
		return fail(types.ErrInvalidPayloadFormat.Error(), "payload or requirements is nil", "", "")
	}

	blockHash, payer, invalid := n.verifyPayment(ctx, payload, req)
	if invalid != nil {
		return fail(invalid.InvalidReason, invalid.InvalidMessage, payer, blockHash)
	}

	// Nano settlement is the payer's broadcast; it is already final on the
	// ledger. There is nothing for a facilitator to broadcast or sign: settle
	// reports success once the same block verifies, returning the block hash
	// as the on-chain proof of payment.
	return &types.PaymentSettleResponse{
		Success:     true,
		Payer:       payer,
		Transaction: blockHash,
		Network:     network,
	}, nil
}

func (n *NanoFacilitator) Supported() *types.SupportedResponse {
	return &types.SupportedResponse{
		Kinds: []types.SupportedKind{{
			X402Version: int(types.X402VersionV2),
			Scheme:      string(n.scheme),
			Network:     n.network,
			Extra: map[string]interface{}{
				"asset":    AssetNano,
				"endpoint": n.endpoint,
			},
		}},
		Extensions: []string{},
		Signers:    map[string][]string{"nano:*": {}},
	}
}

// verifyPayment validates the envelope and returns the block, payer and any
// invalid response. payer is recovered from the block itself (its author),
// never from the request body or payload.
func (n *NanoFacilitator) verifyPayment(ctx context.Context, payload *types.PaymentPayload, req *types.PaymentRequirements) (string, string, *types.PaymentVerifyResponse) {
	if invalid := n.validateEnvelope(payload, req); invalid != nil {
		return "", "", invalid
	}

	raw, ok := payload.Payload["blockHash"].(string)
	if !ok || strings.TrimSpace(raw) == "" {
		return "", "", invalidNanoPayload("payload.blockHash (64-hex Nano send block hash) is required")
	}
	blockHash := strings.TrimSpace(raw)
	if !isHex64(blockHash) {
		return "", "", invalidNanoPayload("payload.blockHash must be a 64-hex Nano block hash")
	}

	block, err := n.block.BlockInfo(ctx, blockHash)
	if err != nil {
		return blockHash, "", &types.PaymentVerifyResponse{
			InvalidReason:  types.ErrInvalidTransaction.Error(),
			InvalidMessage: "failed to read block from the Nano ledger: " + err.Error(),
		}
	}
	if block == nil {
		return blockHash, "", &types.PaymentVerifyResponse{
			InvalidReason:  types.ErrInvalidTransaction.Error(),
			InvalidMessage: "block not found on the Nano ledger",
		}
	}

	// Always read the destination, amount and subtype from the on-chain block,
	// never from the request body or payload — the block is the settlement.
	subtype := strings.ToLower(block.Subtype)
	if subtype == "" && block.Contents != nil {
		subtype = strings.ToLower(block.Contents.Type)
	}
	if subtype != "send" && subtype != "state" {
		return blockHash, "", &types.PaymentVerifyResponse{
			InvalidReason:  types.ErrInvalidTransaction.Error(),
			InvalidMessage: fmt.Sprintf("block is not a send (subtype %q)", subtype),
		}
	}

	destination := ""
	if block.Contents != nil {
		destination = block.Contents.Destination
	}
	if !strings.EqualFold(destination, req.PayTo) {
		return blockHash, "", &types.PaymentVerifyResponse{
			InvalidReason:  types.ErrRecipientMismatch.Error(),
			InvalidMessage: "block destination does not match the required payTo",
		}
	}

	if !rawAmountEqual(block.Amount, req.Amount) {
		return blockHash, "", &types.PaymentVerifyResponse{
			InvalidReason:  types.ErrAmountMismatch.Error(),
			InvalidMessage: "block amount does not match the required amount",
		}
	}

	// Cementation is the chit402 concern: require the block to be confirmed
	// (cemented) on the node before it is accepted, opt-in via extra.
	if confirmRequired(req) && !confirmed(block.Confirmed) {
		return blockHash, "", &types.PaymentVerifyResponse{
			InvalidReason:  types.ErrTransactionFailed.Error(),
			InvalidMessage: "block is not yet confirmed on the Nano ledger",
		}
	}

	return blockHash, block.Account, nil
}

func (n *NanoFacilitator) validateEnvelope(payload *types.PaymentPayload, req *types.PaymentRequirements) *types.PaymentVerifyResponse {
	if payload.X402Version != int(types.X402VersionV2) {
		return &types.PaymentVerifyResponse{InvalidReason: types.ErrInvalidPayloadFormat.Error()}
	}
	if payload.Accepted.Scheme != string(n.scheme) || req.Scheme != string(n.scheme) {
		return &types.PaymentVerifyResponse{InvalidReason: types.ErrIncompatibleScheme.Error()}
	}
	if payload.Accepted.Network != n.network || req.Network != n.network {
		return &types.PaymentVerifyResponse{InvalidReason: types.ErrNetworkMismatch.Error()}
	}
	// Asset: the native coin is identified by its ticker. Empty asset on
	// either side is tolerated (native-coin payments may not state a mint).
	if strings.TrimSpace(payload.Accepted.Asset) != "" && strings.TrimSpace(req.Asset) != "" &&
		strings.TrimSpace(payload.Accepted.Asset) != strings.TrimSpace(req.Asset) {
		return &types.PaymentVerifyResponse{InvalidReason: types.ErrTokenMismatch.Error()}
	}
	if payload.Accepted.Amount != req.Amount {
		return &types.PaymentVerifyResponse{InvalidReason: types.ErrAmountMismatch.Error()}
	}
	if strings.TrimSpace(payload.Accepted.PayTo) != strings.TrimSpace(req.PayTo) || strings.TrimSpace(req.PayTo) == "" {
		return &types.PaymentVerifyResponse{InvalidReason: types.ErrRecipientMismatch.Error()}
	}
	return nil
}

func invalidNanoPayload(message string) *types.PaymentVerifyResponse {
	return &types.PaymentVerifyResponse{
		InvalidReason:  types.ErrInvalidPayloadFormat.Error(),
		InvalidMessage: message,
	}
}

// isHex64 reports whether s is a 64-character uppercase or lowercase hex
// string (a Nano block hash).
func isHex64(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')) {
			return false
		}
	}
	return true
}

// rawAmountEqual compares two raw Nano amounts (integer atomic units) for
// equality, tolerating formatting differences (leading zeros, a bare
// decimal). Bits are exact because raw amounts are integers, larger than 2^64.
func rawAmountEqual(a, b string) bool {
	ai, okA := new(big.Int).SetString(strings.TrimSpace(a), 10)
	bi, okB := new(big.Int).SetString(strings.TrimSpace(b), 10)
	if !okA || !okB {
		// Tolerate a trailing ".0" style decimal (some nodes emit "205676479").
		if ai, okA = parseRawTolerant(a); !okA {
			return false
		}
		if bi, okB = parseRawTolerant(b); !okB {
			return false
		}
	}
	return ai.Cmp(bi) == 0
}

func parseRawTolerant(s string) (*big.Int, bool) {
	trimmed := strings.TrimSpace(s)
	if i := strings.IndexByte(trimmed, '.'); i >= 0 {
		trimmed = trimmed[:i] + trimmed[i+1:]
		if trimmed == "" {
			trimmed = "0"
		}
	}
	return new(big.Int).SetString(trimmed, 10)
}

// confirmRequired reads the opt-in "confirmRequired" flag from the
// requirements extra.
func confirmRequired(req *types.PaymentRequirements) bool {
	if req == nil || req.Extra == nil {
		return false
	}
	if v, ok := req.Extra["confirmRequired"]; ok {
		switch t := v.(type) {
		case bool:
			return t
		case string:
			return strings.EqualFold(t, "true")
		}
	}
	return false
}

// confirmed reports whether a node's confirmation field asserts cementation.
func confirmed(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1":
		return true
	}
	return false
}
