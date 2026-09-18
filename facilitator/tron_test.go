package facilitator

import (
	"errors"
	"testing"

	"github.com/gosuda/x402-facilitator/types"
	"github.com/stretchr/testify/require"
)

func TestNewTronFacilitatorFailsFast(t *testing.T) {
	f, err := NewTronFacilitator("tron:mainnet", "https://grpc.trongrid.io", "00")
	require.Nil(t, f)
	require.True(t, errors.Is(err, ErrTronNotImplemented), "constructor must fail fast, got %v", err)

	// The factory surfaces the same error so a tron config fails at boot.
	_, err = NewFacilitator(types.Exact, "tron:mainnet", "https://grpc.trongrid.io", "00")
	require.True(t, errors.Is(err, ErrTronNotImplemented), "factory must propagate the tron error, got %v", err)
}

func TestTronFacilitatorVerifySettleNotImplemented(t *testing.T) {
	f := &TronFacilitator{}

	_, err := f.Verify(t.Context(), nil, nil)
	require.True(t, errors.Is(err, ErrTronNotImplemented), "verify must fail loudly, got %v", err)

	_, err = f.Settle(t.Context(), nil, nil)
	require.True(t, errors.Is(err, ErrTronNotImplemented), "settle must fail loudly, got %v", err)

	require.Nil(t, f.Supported())
}
