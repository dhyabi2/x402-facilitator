package facilitator

import (
	"context"

	"github.com/gosuda/x402-facilitator/types"
)

// Facilitator verifies and settles x402 payments for one deployment.
type Facilitator interface {
	Verify(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentVerifyResponse, error)
	Settle(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentSettleResponse, error)
	Supported() *types.SupportedResponse
}
