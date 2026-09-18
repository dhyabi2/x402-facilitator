package facilitator

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func solanaTestKeyHex(t *testing.T) string {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	return hex.EncodeToString(priv) // 64 bytes: seed + public key
}

func TestNewSolanaFacilitatorValidatesInputs(t *testing.T) {
	key := solanaTestKeyHex(t)

	_, err := NewSolanaFacilitator("", "https://api.devnet.solana.com", key)
	require.ErrorContains(t, err, "network is required")

	_, err = NewSolanaFacilitator("solana:devnet", "", key)
	require.ErrorContains(t, err, "rpc url is required")

	_, err = NewSolanaFacilitator("solana:devnet", "https://api.devnet.solana.com", "not-hex")
	require.ErrorContains(t, err, "invalid hex private key")

	_, err = NewSolanaFacilitator("solana:devnet", "https://api.devnet.solana.com", hex.EncodeToString([]byte("short")))
	require.ErrorContains(t, err, "invalid private key format")
}

func TestNewSolanaFacilitatorAcceptsValidConfig(t *testing.T) {
	f, err := NewSolanaFacilitator("solana:devnet", "https://api.devnet.solana.com", solanaTestKeyHex(t))
	require.NoError(t, err)
	require.NotNil(t, f)
	// Verify and Settle are not implemented yet, so the facilitator must
	// stay gated from discovery.
	require.Nil(t, f.Supported())
}

func TestSolanaFacilitatorVerifySettleNotImplemented(t *testing.T) {
	f, err := NewSolanaFacilitator("solana:devnet", "https://api.devnet.solana.com", solanaTestKeyHex(t))
	require.NoError(t, err)

	_, err = f.Verify(t.Context(), nil, nil)
	require.True(t, errors.Is(err, ErrNotImplemented), "verify must fail loudly, got %v", err)

	_, err = f.Settle(t.Context(), nil, nil)
	require.True(t, errors.Is(err, ErrNotImplemented), "settle must fail loudly, got %v", err)
}
