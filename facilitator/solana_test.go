package facilitator

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strconv"
	"testing"

	solcommon "github.com/blocto/solana-go-sdk/common"
	soltypes "github.com/blocto/solana-go-sdk/types"
	"github.com/mr-tron/base58"
	"github.com/stretchr/testify/require"

	"github.com/gosuda/x402-facilitator/types"
)

// stubSubmitter records what the facilitator submits so tests can inspect
// the co-signed transaction without an RPC endpoint.
type stubSubmitter struct {
	signature string
	err       error
	got       *soltypes.Transaction
}

func (s *stubSubmitter) SendTransaction(_ context.Context, tx soltypes.Transaction) (string, error) {
	s.got = &tx
	return s.signature, s.err
}

func solanaTestAccount(t *testing.T) soltypes.Account {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	account, err := soltypes.AccountFromBytes(priv) // 64 bytes: seed + public key
	require.NoError(t, err)
	return account
}

func solanaTestPubkey() solcommon.PublicKey {
	raw := make([]byte, 32)
	_, _ = rand.Read(raw)
	return solcommon.PublicKeyFromBytes(raw)
}

// solanaTestTx builds a legacy transaction whose single instruction is an SPL
// Token TransferChecked paying amount base units of mint from a source token
// account to dest, signed by the payer and leaving the fee payer slot empty.
func solanaTestTx(t *testing.T, feePayer soltypes.Account, payer soltypes.Account, mint solcommon.PublicKey, dest solcommon.PublicKey, amount uint64, decimals uint8) soltypes.Transaction {
	t.Helper()
	accounts := []solcommon.PublicKey{
		feePayer.PublicKey, // 0: fee payer
		payer.PublicKey,    // 1: transfer authority (required signer)
		solanaTestPubkey(), // 2: source token account
		mint,               // 3: mint
		dest,               // 4: destination token account
		solcommon.TokenProgramID,
	}
	data := make([]byte, 0, 10)
	data = append(data, solanaTransferCheckedInstruction)
	data = binary.LittleEndian.AppendUint64(data, amount)
	data = append(data, decimals)

	message := soltypes.Message{
		Header: soltypes.MessageHeader{
			NumRequireSignatures:        2,
			NumReadonlySignedAccounts:   0,
			NumReadonlyUnsignedAccounts: 1,
		},
		Accounts:        accounts,
		RecentBlockHash: base58.Encode(make([]byte, 32)),
		Instructions: []soltypes.CompiledInstruction{{
			ProgramIDIndex: 5,
			Accounts:       []int{2, 3, 4, 1},
			Data:           data,
		}},
		Version: soltypes.MessageVersionLegacy,
	}
	signatures := make([]soltypes.Signature, 2)
	signatures[0] = make(soltypes.Signature, 64) // empty fee payer signature slot
	signatures[1] = soltypes.Signature(payer.Sign(mustSerializeMessage(t, message)))
	return soltypes.Transaction{
		Signatures: signatures,
		Message:    message,
	}
}

func mustSerializeMessage(t *testing.T, message soltypes.Message) []byte {
	t.Helper()
	raw, err := message.Serialize()
	require.NoError(t, err)
	return raw
}

func solanaTestFacilitator(t *testing.T) *SolanaFacilitator {
	t.Helper()
	feePayer := solanaTestAccount(t)
	feePayerKey := hex.EncodeToString(feePayer.PrivateKey)
	f, err := NewSolanaFacilitator("solana:devnet", "https://api.devnet.solana.com", feePayerKey)
	require.NoError(t, err)
	return f
}

func solanaTestRequirements(mint solcommon.PublicKey, payTo solcommon.PublicKey, amount uint64) *types.PaymentRequirements {
	return &types.PaymentRequirements{
		Scheme:            string(types.Exact),
		Network:           "solana:devnet",
		Asset:             mint.String(),
		Amount:            strconv.FormatUint(amount, 10),
		PayTo:             payTo.String(),
		MaxTimeoutSeconds: 60,
	}
}

func solanaTestPayload(t *testing.T, tx soltypes.Transaction, req *types.PaymentRequirements) *types.PaymentPayload {
	t.Helper()
	return &types.PaymentPayload{
		X402Version: int(types.X402VersionV2),
		Payload: map[string]interface{}{
			"transaction": base64.StdEncoding.EncodeToString(mustSerializeTransaction(t, tx)),
		},
		Accepted: *req,
	}
}

func mustSerializeTransaction(t *testing.T, tx soltypes.Transaction) []byte {
	t.Helper()
	raw, err := tx.Serialize()
	require.NoError(t, err)
	return raw
}

func TestNewSolanaFacilitatorValidatesInputs(t *testing.T) {
	feePayer := solanaTestAccount(t)
	key := hex.EncodeToString(feePayer.PrivateKey)

	_, err := NewSolanaFacilitator("eip155:84532", "https://api.devnet.solana.com", key)
	require.ErrorContains(t, err, "invalid Solana network")

	_, err = NewSolanaFacilitator("solana:", "https://api.devnet.solana.com", key)
	require.ErrorContains(t, err, "invalid Solana network")

	_, err = NewSolanaFacilitator("solana:devnet", "   ", key)
	require.ErrorContains(t, err, "rpc URL is required")

	_, err = NewSolanaFacilitator("solana:devnet", "https://api.devnet.solana.com", "not-hex")
	require.ErrorContains(t, err, "invalid hex private key")

	_, err = NewSolanaFacilitator("solana:devnet", "https://api.devnet.solana.com", "aabb")
	require.ErrorContains(t, err, "invalid private key format")
}

func TestNewSolanaFacilitatorAcceptsValidConfig(t *testing.T) {
	f := solanaTestFacilitator(t)
	require.NotEqual(t, solcommon.PublicKey{}, f.feePayer.PublicKey)
	require.Equal(t, "solana:devnet", f.network)
}

func TestSupportedAdvertisesFeePayer(t *testing.T) {
	f := solanaTestFacilitator(t)
	supported := f.Supported()
	require.NotNil(t, supported)
	require.Len(t, supported.Kinds, 1)

	kind := supported.Kinds[0]
	require.Equal(t, int(types.X402VersionV2), kind.X402Version)
	require.Equal(t, string(types.Exact), kind.Scheme)
	require.Equal(t, "solana:devnet", kind.Network)
	require.Equal(t, f.feePayer.PublicKey.String(), kind.Extra["feePayer"])
	require.Equal(t, "base64", kind.Extra["transactionEncoding"])
	require.Equal(t, []string{f.feePayer.PublicKey.String()}, supported.Signers["solana:*"])
}

func TestSolanaVerifyRejectsInvalidEnvelopes(t *testing.T) {
	f := solanaTestFacilitator(t)
	payer := solanaTestAccount(t)
	mint := solanaTestPubkey()
	payTo := solanaTestPubkey()
	dest, _, err := solcommon.FindAssociatedTokenAddress(payTo, mint)
	require.NoError(t, err)

	req := solanaTestRequirements(mint, payTo, 1_000)
	tx := solanaTestTx(t, f.feePayer, payer, mint, dest, 1_000, 6)
	payload := solanaTestPayload(t, tx, req)

	tests := []struct {
		name   string
		mutate func(p *types.PaymentPayload, r *types.PaymentRequirements)
		reason string
	}{
		{"wrong version", func(p *types.PaymentPayload, r *types.PaymentRequirements) { p.X402Version = 1 }, types.ErrInvalidPayloadFormat.Error()},
		{"wrong scheme", func(p *types.PaymentPayload, r *types.PaymentRequirements) { r.Scheme = "other" }, types.ErrIncompatibleScheme.Error()},
		{"wrong network", func(p *types.PaymentPayload, r *types.PaymentRequirements) { r.Network = "solana:mainnet" }, types.ErrNetworkMismatch.Error()},
		{"accepted asset drift", func(p *types.PaymentPayload, r *types.PaymentRequirements) {
			p.Accepted.Asset = solanaTestPubkey().String()
		}, types.ErrTokenMismatch.Error()},
		{"accepted amount drift", func(p *types.PaymentPayload, r *types.PaymentRequirements) { p.Accepted.Amount = "1" }, types.ErrAmountMismatch.Error()},
		{"accepted payTo drift", func(p *types.PaymentPayload, r *types.PaymentRequirements) {
			p.Accepted.PayTo = solanaTestPubkey().String()
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

func TestSolanaVerifyRejectsInvalidTransactions(t *testing.T) {
	f := solanaTestFacilitator(t)
	payer := solanaTestAccount(t)
	mint := solanaTestPubkey()
	payTo := solanaTestPubkey()
	dest, _, err := solcommon.FindAssociatedTokenAddress(payTo, mint)
	require.NoError(t, err)
	req := solanaTestRequirements(mint, payTo, 1_000)

	buildPayload := func(rawTx string) *types.PaymentPayload {
		return &types.PaymentPayload{
			X402Version: int(types.X402VersionV2),
			Payload:     map[string]interface{}{"transaction": rawTx},
			Accepted:    *req,
		}
	}

	tests := []struct {
		name    string
		rawTx   func(t *testing.T) string
		message string
		reason  string
	}{
		{"missing transaction", func(t *testing.T) string { return "" }, "required", types.ErrInvalidPayloadFormat.Error()},
		{"not base64", func(t *testing.T) string { return "!!!" }, "base64", types.ErrInvalidPayloadFormat.Error()},
		{"not a transaction", func(t *testing.T) string { return base64.StdEncoding.EncodeToString([]byte("garbage")) }, "not a valid Solana transaction", types.ErrInvalidTransaction.Error()},
		{"wrong fee payer", func(t *testing.T) string {
			tx := solanaTestTx(t, solanaTestAccount(t), payer, mint, dest, 1_000, 6)
			return base64.StdEncoding.EncodeToString(mustSerializeTransaction(t, tx))
		}, "fee payer is not the facilitator fee payer", types.ErrInvalidTransaction.Error()},
		{"no transfer instruction", func(t *testing.T) string {
			tx := solanaTestTx(t, f.feePayer, payer, mint, dest, 1_000, 6)
			tx.Message.Instructions = nil
			return base64.StdEncoding.EncodeToString(mustSerializeTransaction(t, tx))
		}, "no SPL Token TransferChecked instruction", types.ErrInvalidTransaction.Error()},
		{"wrong mint", func(t *testing.T) string {
			tx := solanaTestTx(t, f.feePayer, payer, solanaTestPubkey(), dest, 1_000, 6)
			return base64.StdEncoding.EncodeToString(mustSerializeTransaction(t, tx))
		}, "mint does not match", types.ErrTokenMismatch.Error()},
		{"wrong amount", func(t *testing.T) string {
			tx := solanaTestTx(t, f.feePayer, payer, mint, dest, 999, 6)
			return base64.StdEncoding.EncodeToString(mustSerializeTransaction(t, tx))
		}, "does not match required amount", types.ErrAmountMismatch.Error()},
		{"destination not payTo ATA", func(t *testing.T) string {
			tx := solanaTestTx(t, f.feePayer, payer, mint, solanaTestPubkey(), 1_000, 6)
			return base64.StdEncoding.EncodeToString(mustSerializeTransaction(t, tx))
		}, "not the associated token account", types.ErrRecipientMismatch.Error()},
		{"authority did not sign", func(t *testing.T) string {
			tx := solanaTestTx(t, f.feePayer, payer, mint, dest, 1_000, 6)
			tx.Signatures[1] = make(soltypes.Signature, 64) // present but empty
			return base64.StdEncoding.EncodeToString(mustSerializeTransaction(t, tx))
		}, "did not sign", types.ErrInvalidSignature.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := f.Verify(t.Context(), buildPayload(tt.rawTx(t)), req)
			require.NoError(t, err)
			require.False(t, res.IsValid)
			require.Equal(t, tt.reason, res.InvalidReason)
			require.Contains(t, res.InvalidMessage, tt.message)
		})
	}
}

func TestSolanaVerifyAcceptsValidPayment(t *testing.T) {
	f := solanaTestFacilitator(t)
	payer := solanaTestAccount(t)
	mint := solanaTestPubkey()
	payTo := solanaTestPubkey()
	dest, _, err := solcommon.FindAssociatedTokenAddress(payTo, mint)
	require.NoError(t, err)

	req := solanaTestRequirements(mint, payTo, 1_000)
	payload := solanaTestPayload(t, solanaTestTx(t, f.feePayer, payer, mint, dest, 1_000, 6), req)

	res, err := f.Verify(t.Context(), payload, req)
	require.NoError(t, err)
	require.True(t, res.IsValid)
	require.Equal(t, payer.PublicKey.String(), res.Payer)
}

func TestSolanaSettleCoSignsAndSubmits(t *testing.T) {
	f := solanaTestFacilitator(t)
	payer := solanaTestAccount(t)
	mint := solanaTestPubkey()
	payTo := solanaTestPubkey()
	dest, _, err := solcommon.FindAssociatedTokenAddress(payTo, mint)
	require.NoError(t, err)

	submitter := &stubSubmitter{signature: "5txSignature"}
	f.client = submitter

	req := solanaTestRequirements(mint, payTo, 1_000)
	payload := solanaTestPayload(t, solanaTestTx(t, f.feePayer, payer, mint, dest, 1_000, 6), req)

	res, err := f.Settle(t.Context(), payload, req)
	require.NoError(t, err)
	require.True(t, res.Success)
	require.Equal(t, "5txSignature", res.Transaction)
	require.Equal(t, payer.PublicKey.String(), res.Payer)
	require.Equal(t, types.Network("solana:devnet"), res.Network)

	// The facilitator must have co-signed in the fee payer slot.
	require.NotNil(t, submitter.got)
	require.Len(t, submitter.got.Signatures, 2)
	require.False(t, solanaEmptySignature(submitter.got.Signatures[0]), "fee payer slot must be signed")
	require.False(t, solanaEmptySignature(submitter.got.Signatures[1]), "payer signature must be preserved")
}

func TestSolanaSettleReportsBroadcastFailure(t *testing.T) {
	f := solanaTestFacilitator(t)
	payer := solanaTestAccount(t)
	mint := solanaTestPubkey()
	payTo := solanaTestPubkey()
	dest, _, err := solcommon.FindAssociatedTokenAddress(payTo, mint)
	require.NoError(t, err)

	f.client = &stubSubmitter{err: errors.New("rpc unavailable")}

	req := solanaTestRequirements(mint, payTo, 1_000)
	payload := solanaTestPayload(t, solanaTestTx(t, f.feePayer, payer, mint, dest, 1_000, 6), req)

	res, err := f.Settle(t.Context(), payload, req)
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Equal(t, types.ErrTransactionFailed.Error(), res.ErrorReason)
	require.Contains(t, res.ErrorMessage, "rpc unavailable")
}

func TestSolanaSettleRejectsNilInputs(t *testing.T) {
	f := solanaTestFacilitator(t)

	res, err := f.Settle(t.Context(), nil, nil)
	require.NoError(t, err)
	require.False(t, res.Success)
	require.Equal(t, types.ErrInvalidPayloadFormat.Error(), res.ErrorReason)
}
