package main

import (
	"testing"

	"github.com/gosuda/x402-facilitator/types"
	"github.com/stretchr/testify/require"
)

// TestNewFacilitatorRoutesNetworks pins the dispatch table of the shipped
// binary: each CAIP-2 family reaches its own chain implementation, and
// unknown networks or schemes are rejected before any construction work.
// Error paths only — successful construction dials live endpoints and is
// covered by the chain packages' on-chain integration tests.
func TestNewFacilitatorRoutesNetworks(t *testing.T) {
	tests := []struct {
		name    string
		scheme  types.Scheme
		network string
	}{
		{name: "unknown eip155 network", scheme: types.Exact, network: "eip155:999999"},
		{name: "unsupported casper network", scheme: types.Exact, network: "casper:casper-dev"},
		{name: "unsupported scheme", scheme: types.Scheme("upto"), network: "eip155:84532"},
		{name: "tron fails fast", scheme: types.Exact, network: "tron:mainnet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance, err := newFacilitator(tt.scheme, tt.network, "https://example.invalid", "")
			require.Error(t, err)
			require.Nil(t, instance)
		})
	}
}
