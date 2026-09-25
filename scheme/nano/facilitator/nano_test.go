package facilitator

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	nanoscheme "github.com/gosuda/x402-facilitator/scheme/nano"
	"github.com/gosuda/x402-facilitator/types"
)

// stubNode stands in for the Nano node boundary: it returns a configured
// block_info response or an error.
type stubNode struct {
	info  *nanoscheme.BlockInfo
	err   error
	hash  string
	calls int
}

func (s *stubNode) BlockInfo(_ context.Context, hash string) (*nanoscheme.BlockInfo, error) {
	s.calls++
	s.hash = hash
	if s.err != nil {
		return nil, s.err
	}
	return s.info, nil
}

func sendBlock(payTo, amount string) *nanoscheme.BlockInfo {
	return &nanoscheme.BlockInfo{
		Account:   "nano_3t6k35gi95xu6ter3d73en7kzakssx9ft9mwewbfnx4y2k9a7wug74f4hacd",
		Subtype:   "send",
		Amount:    amount,
		Confirmed: "true",
		Contents: &nanoscheme.BlockContents{
			Type:        "send",
			Destination: payTo,
		},
	}
}

func nanoRequirements(payTo, amount string) *types.PaymentRequirements {
	return &types.PaymentRequirements{
		Scheme:            string(types.Exact),
		Network:           "nano:mainnet",
		Asset:             AssetNano,
		Amount:            amount,
		PayTo:             payTo,
		MaxTimeoutSeconds: 60,
	}
}

func nanoPayload(blockHash string, req *types.PaymentRequirements) *types.PaymentPayload {
	return &types.PaymentPayload{
		X402Version: int(types.X402VersionV2),
		Payload:     map[string]interface{}{"blockHash": blockHash},
		Accepted:    *req,
	}
}

const testBlockHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func nanoTestFacilitator(t *testing.T) (*NanoFacilitator, *stubNode) {
	t.Helper()
	node := &stubNode{}
	f, err := NewNanoFacilitator("nano:mainnet", "", "https://rpc.nano.to")
	require.NoError(t, err)
	f.block = node
	return f, node
}

func TestNewNanoFacilitatorValidatesInputs(t *testing.T) {
	_, err := NewNanoFacilitator("eip155:84532", "")
	require.ErrorContains(t, err, "invalid Nano network")

	_, err = NewNanoFacilitator("nano:", "")
	require.ErrorContains(t, err, "invalid Nano network")

	// Valid network builds and uses default endpoints when none supplied.
	f, err := NewNanoFacilitator("nano:mainnet", "")
	require.NoError(t, err)
	require.Equal(t, "nano:mainnet", f.network)
	require.Equal(t, nanoscheme.DefaultEndpoints[0], f.endpoint)
}

func TestNewNanoFacilitatorAcceptsExplicitEndpoint(t *testing.T) {
	f, err := NewNanoFacilitator("nano:mainnet", "", "https://rainstorm.city/api")
	require.NoError(t, err)
	require.Equal(t, "https://rainstorm.city/api", f.endpoint)
}

func TestSupportedAdvertisesNano(t *testing.T) {
	f, _ := nanoTestFacilitator(t)
	supported := f.Supported()
	require.NotNil(t, supported)
	require.Len(t, supported.Kinds, 1)
	kind := supported.Kinds[0]
	require.Equal(t, int(types.X402VersionV2), kind.X402Version)
	require.Equal(t, string(types.Exact), kind.Scheme)
	require.Equal(t, "nano:mainnet", kind.Network)
	require.Equal(t, AssetNano, kind.Extra["asset"])
	require.Equal(t, nanoscheme.DefaultEndpoints[0], kind.Extra["endpoint"])
	require.Equal(t, []string{}, supported.Signers["nano:*"])
}

func TestNanoVerifyRejectsInvalidEnvelopes(t *testing.T) {
	f, node := nanoTestFacilitator(t)
	req := nanoRequirements("nano_1111111111111111111111111111111111111111111111111111111111111111", "1000000000000000000000000000")
	payload := nanoPayload(testBlockHash, req)
	node.info = sendBlock(req.PayTo, req.Amount)

	tests := []struct {
		name   string
		mutate func(p *types.PaymentPayload, r *types.PaymentRequirements)
		reason string
	}{
		{"wrong version", func(p *types.PaymentPayload, r *types.PaymentRequirements) { p.X402Version = 1 }, types.ErrInvalidPayloadFormat.Error()},
		{"wrong scheme", func(p *types.PaymentPayload, r *types.PaymentRequirements) { r.Scheme = "other" }, types.ErrIncompatibleScheme.Error()},
		{"wrong network", func(p *types.PaymentPayload, r *types.PaymentRequirements) { r.Network = "nano:test" }, types.ErrNetworkMismatch.Error()},
		{"accepted amount drift", func(p *types.PaymentPayload, r *types.PaymentRequirements) { p.Accepted.Amount = "1" }, types.ErrAmountMismatch.Error()},
		{"accepted payTo drift", func(p *types.PaymentPayload, r *types.PaymentRequirements) {
			p.Accepted.PayTo = "nano_2222222222222222222222222222222222222222222222222222222222222222"
		}, types.ErrRecipientMismatch.Error()},
		{"empty payTo", func(p *types.PaymentPayload, r *types.PaymentRequirements) { r.PayTo = " " }, types.ErrRecipientMismatch.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := *payload
			r := *req
			tt.mutate(&p, &r)
			res, err := f.Verify(t.Context(), &p, &r)
			require.NoError(t, err)
			require.False(t, res.IsValid)
			require.Equal(t, tt.reason, res.InvalidReason)
		})
	}
}

func TestNanoVerifyRejectsMalformedPayload(t *testing.T) {
	payTo := "nano_1111111111111111111111111111111111111111111111111111111111111111"
	amount := "1000000000000000000000000000"
	req := nanoRequirements(payTo, amount)

	tests := []struct {
		name    string
		payload *types.PaymentPayload
		message string
		reason  string
	}{
		{"missing blockHash", &types.PaymentPayload{X402Version: int(types.X402VersionV2), Payload: map[string]interface{}{}, Accepted: *req}, "required", types.ErrInvalidPayloadFormat.Error()},
		{"not hex blockHash", &types.PaymentPayload{X402Version: int(types.X402VersionV2), Payload: map[string]interface{}{"blockHash": "not-a-hash!!!"}, Accepted: *req}, "64-hex", types.ErrInvalidPayloadFormat.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, _ := nanoTestFacilitator(t)
			res, err := f.Verify(t.Context(), tt.payload, req)
			require.NoError(t, err)
			require.False(t, res.IsValid)
			require.Equal(t, tt.reason, res.InvalidReason)
			require.Contains(t, res.InvalidMessage, tt.message)
		})
	}
}

func TestNanoVerifyRejectsNodeFailures(t *testing.T) {
	payTo := "nano_1111111111111111111111111111111111111111111111111111111111111111"
	amount := "1000000000000000000000000000"
	req := nanoRequirements(payTo, amount)

	tests := []struct {
		name    string
		nodeErr error
		info    *nanoscheme.BlockInfo
		message string
	}{
		{"node error", errors.New("rpc unavailable"), nil, "failed to read block"},
		{"block not found", nil, nil, "block not found"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, node := nanoTestFacilitator(t)
			node.err = tt.nodeErr
			node.info = tt.info
			res, err := f.Verify(t.Context(), nanoPayload(testBlockHash, req), req)
			require.NoError(t, err)
			require.False(t, res.IsValid)
			require.Equal(t, types.ErrInvalidTransaction.Error(), res.InvalidReason)
			require.Contains(t, res.InvalidMessage, tt.message)
		})
	}
}

func TestNanoVerifyRejectsInvalidBlocks(t *testing.T) {
	payTo := "nano_1111111111111111111111111111111111111111111111111111111111111111"
	amount := "1000000000000000000000000000"
	req := nanoRequirements(payTo, amount)

	cases := []struct {
		name    string
		info    *nanoscheme.BlockInfo
		message string
		reason  string
		confirm bool
	}{
		{"receive subtype", &nanoscheme.BlockInfo{Account: "nano_3t6k35gi95xu6ter3d73en7kzakssx9ft9mwewbfnx4y2k9a7wug74f4hacd", Subtype: "receive", Amount: amount, Confirmed: "true", Contents: &nanoscheme.BlockContents{Type: "receive", Destination: payTo}}, "not a send", types.ErrInvalidTransaction.Error(), false},
		{"wrong destination", sendBlock("nano_2222222222222222222222222222222222222222222222222222222222222222", amount), "does not match the required payTo", types.ErrRecipientMismatch.Error(), false},
		{"wrong amount", sendBlock(payTo, "1"), "does not match the required amount", types.ErrAmountMismatch.Error(), false},
		{"unconfirmed when required", sendBlock(payTo, amount), "not yet confirmed", types.ErrTransactionFailed.Error(), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, node := nanoTestFacilitator(t)
			r := *req
			if c.confirm {
				r.Extra = map[string]interface{}{"confirmRequired": true}
				c.info.Confirmed = "false"
			}
			node.info = c.info
			res, err := f.Verify(t.Context(), nanoPayload(testBlockHash, &r), &r)
			require.NoError(t, err)
			require.False(t, res.IsValid)
			require.Equal(t, c.reason, res.InvalidReason)
			require.Contains(t, res.InvalidMessage, c.message)
		})
	}
}

func TestNanoVerifyNoRPCWhenNotRequired(t *testing.T) {
	// When confirmRequired is not set, a confirmed block is not mandatory but
	// the endpoint must still be consulted for the block.
	f, node := nanoTestFacilitator(t)
	req := nanoRequirements("nano_1111111111111111111111111111111111111111111111111111111111111111", "1000000000000000000000000000")
	node.info = sendBlock(req.PayTo, req.Amount)
	node.info.Confirmed = "false"
	res, err := f.Verify(t.Context(), nanoPayload(testBlockHash, req), req)
	require.NoError(t, err)
	require.True(t, res.IsValid)
	require.Equal(t, 1, node.calls)
}

func TestNanoVerifyAcceptsValidPayment(t *testing.T) {
	f, node := nanoTestFacilitator(t)
	req := nanoRequirements("nano_1111111111111111111111111111111111111111111111111111111111111111", "1000000000000000000000000000")
	node.info = sendBlock(req.PayTo, req.Amount)

	res, err := f.Verify(t.Context(), nanoPayload(testBlockHash, req), req)
	require.NoError(t, err)
	require.True(t, res.IsValid)
	require.Equal(t, node.info.Account, res.Payer)
	require.Equal(t, testBlockHash, node.hash)
}

func TestNanoVerifyAcceptsCementedWhenRequired(t *testing.T) {
	f, node := nanoTestFacilitator(t)
	req := nanoRequirements("nano_1111111111111111111111111111111111111111111111111111111111111111", "1000000000000000000000000000")
	req.Extra = map[string]interface{}{"confirmRequired": true}
	node.info = sendBlock(req.PayTo, req.Amount)
	node.info.Confirmed = "true"

	res, err := f.Verify(t.Context(), nanoPayload(testBlockHash, req), req)
	require.NoError(t, err)
	require.True(t, res.IsValid)
}

func TestNanoSettleIsStatelessReVerification(t *testing.T) {
	// Settle must be a stateless re-verification: it consults the node once,
	// converts nothing, and succeeds with the block hash as the transaction —
	// no broadcast, no custody key. A node that has no write path and a
	// facilitator with no key proves settle never needs one.
	f, node := nanoTestFacilitator(t)
	req := nanoRequirements("nano_1111111111111111111111111111111111111111111111111111111111111111", "1000000000000000000000000000")
	node.info = sendBlock(req.PayTo, req.Amount)

	res, err := f.Settle(t.Context(), nanoPayload(testBlockHash, req), req)
	require.NoError(t, err)
	require.True(t, res.Success)
	require.Equal(t, testBlockHash, res.Transaction)
	require.Equal(t, node.info.Account, res.Payer)
	require.Equal(t, types.Network("nano:mainnet"), res.Network)
	require.Equal(t, 1, node.calls, "settle must be a single read, nothing more")
}

func TestNanoSettleRejectsInvalid(t *testing.T) {
	f, node := nanoTestFacilitator(t)
	req := nanoRequirements("nano_1111111111111111111111111111111111111111111111111111111111111111", "1000000000000000000000000000")
	node.info = sendBlock(req.PayTo, req.Amount)

	// Wrong amount in the block -> settle fails, no success.
	node.info.Amount = "5"
	res, err := f.Settle(t.Context(), nanoPayload(testBlockHash, req), req)
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Equal(t, types.ErrAmountMismatch.Error(), res.ErrorReason)
	require.Equal(t, testBlockHash, res.Transaction)
}

func TestNanoSettleRejectsNilInputs(t *testing.T) {
	f, _ := nanoTestFacilitator(t)
	res, err := f.Settle(t.Context(), nil, nil)
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Equal(t, types.ErrInvalidPayloadFormat.Error(), res.ErrorReason)
}

func TestRawAmountEqual(t *testing.T) {
	require.True(t, rawAmountEqual("1000", "1000"))
	require.True(t, rawAmountEqual("01000", "1000"))
	require.True(t, rawAmountEqual("205676479", "205676479"))
	require.False(t, rawAmountEqual("1000", "1001"))
}
