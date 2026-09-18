package main

import (
	"testing"

	"github.com/gosuda/x402-facilitator/types"
	"github.com/stretchr/testify/require"
)

// TestNewFacilitatorRoutesNetworks pins the registry's eip155 routing (the
// case the root factory cannot carry) and the passthrough to the root
// factory. Error paths only — successful construction dials live endpoints
// and is covered by the chain packages' on-chain integration tests.
func TestNewFacilitatorRoutesNetworks(t *testing.T) {
	tests := []struct {
		name    string
		scheme  types.Scheme
		network string
	}{
		{name: "unknown eip155 network rejected by evm constructor", scheme: types.Exact, network: "eip155:999999"},
		{name: "non-evm networks pass through to the root factory", scheme: types.Exact, network: "casper:casper-dev"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance, err := newFacilitator(tt.scheme, tt.network, "https://example.invalid", "")
			require.Error(t, err)
			require.Nil(t, instance)
		})
	}
}
