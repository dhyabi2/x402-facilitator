package main

import (
	"fmt"
	"strings"

	"github.com/gosuda/x402-facilitator/facilitator"
	evmfacilitator "github.com/gosuda/x402-facilitator/scheme/evm/facilitator"
	"github.com/gosuda/x402-facilitator/types"
)

// newFacilitator wires the scheme dispatch for the shipped facilitator
// binary. It lives in the cmd module rather than the root facilitator
// package so the root module's dependency graph stays free of chain SDKs
// that only some deployments need: routing eip155 networks is what pulls
// go-ethereum into a build, and that choice belongs to the distribution,
// not the library.
func newFacilitator(scheme types.Scheme, network, rpcURL, privateKeyHex string) (facilitator.Facilitator, error) {
	if scheme != types.Exact {
		return nil, fmt.Errorf("unsupported scheme %q (only %q is implemented)", scheme, types.Exact)
	}

	// Route by CAIP-2 network prefix
	switch {
	case strings.HasPrefix(network, "eip155:"):
		return evmfacilitator.NewEVMFacilitator(network, rpcURL, privateKeyHex)
	case strings.HasPrefix(network, "solana:"):
		return facilitator.NewSolanaFacilitator(network, rpcURL, privateKeyHex)
	case strings.HasPrefix(network, "sui:"):
		return facilitator.NewSuiFacilitator(network, rpcURL, privateKeyHex)
	case strings.HasPrefix(network, "tron:"):
		return facilitator.NewTronFacilitator(network, rpcURL, privateKeyHex)
	case strings.HasPrefix(network, "casper:"):
		return facilitator.NewCasperFacilitator(network, rpcURL, privateKeyHex)
	default:
		return nil, fmt.Errorf("unsupported network %q: expected a CAIP-2 identifier (eip155:*, solana:*, sui:*, tron:*, casper:*)", network)
	}
}
