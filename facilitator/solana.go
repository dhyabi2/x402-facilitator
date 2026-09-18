package facilitator

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/blocto/solana-go-sdk/client"
	solTypes "github.com/blocto/solana-go-sdk/types"

	"github.com/gosuda/x402-facilitator/types"
)

type SolanaFacilitator struct {
	scheme   types.Scheme
	client   *client.Client
	feePayer solTypes.Account
}

func NewSolanaFacilitator(network string, url string, privateKeyHex string) (*SolanaFacilitator, error) {
	if network == "" {
		return nil, fmt.Errorf("network is required")
	}
	if url == "" {
		return nil, fmt.Errorf("rpc url is required")
	}

	solClient := client.NewClient(url)

	privKey, err := hex.DecodeString(privateKeyHex)
	if err != nil {
		return nil, fmt.Errorf("invalid hex private key: %w", err)
	}

	feePayer, err := solTypes.AccountFromBytes(privKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key format: %w", err)
	}

	return &SolanaFacilitator{
		scheme:   types.Exact,
		client:   solClient,
		feePayer: feePayer,
	}, nil
}

// ErrNotImplemented reports that the Solana facilitator cannot yet verify
// or settle payments; it stays gated from discovery until a v2 follow-up lands.
var ErrNotImplemented = errors.New("solana facilitator not implemented")

func (t *SolanaFacilitator) Verify(ctx context.Context, payload *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentVerifyResponse, error) {
	return nil, fmt.Errorf("%w: verify", ErrNotImplemented)
}

func (t *SolanaFacilitator) Settle(ctx context.Context, payload *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentSettleResponse, error) {
	return nil, fmt.Errorf("%w: settle", ErrNotImplemented)
}

// Supported returns nil: Verify and Settle are not yet v2-compliant, so
// this facilitator is gated from discovery until a follow-up lands.
func (t *SolanaFacilitator) Supported() *types.SupportedResponse {
	return nil
}
