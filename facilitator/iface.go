package facilitator

import (
	"context"
	"fmt"
	"strings"

	"github.com/gosuda/x402-facilitator/types"
)

// Facilitator verifies and settles x402 payments for one deployment.
type Facilitator interface {
	Verify(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentVerifyResponse, error)
	Settle(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentSettleResponse, error)
	Supported() *types.SupportedResponse
}

// NewFacilitator routes the chains shipped with the root module. EVM is
// intentionally absent: go-ethereum lives behind the scheme/evm module
// boundary, so EVM facilitators are composed explicitly through
// github.com/gosuda/x402-facilitator/scheme/evm/facilitator.
func NewFacilitator(scheme types.Scheme, network, rpcUrl string, privateKeyHex string) (Facilitator, error) {
	if scheme != types.Exact {
		return nil, fmt.Errorf("unsupported scheme %q (only %q is implemented)", scheme, types.Exact)
	}

	// Route by CAIP-2 network prefix
	switch {
	case strings.HasPrefix(network, "eip155:"):
		return nil, fmt.Errorf("network %q requires the EVM facilitator from the scheme/evm module: github.com/gosuda/x402-facilitator/scheme/evm/facilitator", network)
	case strings.HasPrefix(network, "solana:"):
		return NewSolanaFacilitator(network, rpcUrl, privateKeyHex)
	case strings.HasPrefix(network, "sui:"):
		return NewSuiFacilitator(network, rpcUrl, privateKeyHex)
	case strings.HasPrefix(network, "tron:"):
		return NewTronFacilitator(network, rpcUrl, privateKeyHex)
	case strings.HasPrefix(network, "casper:"):
		return NewCasperFacilitator(network, rpcUrl, privateKeyHex)
	default:
		return nil, fmt.Errorf("unsupported network %q: expected a CAIP-2 identifier (eip155:*, solana:*, sui:*, tron:*, casper:*)", network)
	}
}
