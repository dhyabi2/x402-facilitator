package main

import (
	"strings"

	"github.com/gosuda/x402-facilitator/facilitator"
	evmfacilitator "github.com/gosuda/x402-facilitator/scheme/evm/facilitator"
	"github.com/gosuda/x402-facilitator/types"
)

// newFacilitator extends the root factory with the EVM case that the root
// module cannot carry: routing eip155 is what pulls go-ethereum into this
// binary's build, and that choice belongs to the distribution, not the
// library.
func newFacilitator(scheme types.Scheme, network, rpcURL, privateKeyHex string) (facilitator.Facilitator, error) {
	if scheme != types.Exact {
		return facilitator.NewFacilitator(scheme, network, rpcURL, privateKeyHex)
	}
	if strings.HasPrefix(network, "eip155:") {
		return evmfacilitator.NewEVMFacilitator(network, rpcURL, privateKeyHex)
	}
	return facilitator.NewFacilitator(scheme, network, rpcURL, privateKeyHex)
}
