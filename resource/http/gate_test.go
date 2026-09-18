package x402http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gosuda/x402-facilitator/api/client"
	"github.com/gosuda/x402-facilitator/types"
)

// Compile-time proof that a hand-written stub and the remote facilitator
// client both plug into the gate.
var (
	_ Facilitator = (*stubFacilitator)(nil)
	_ Facilitator = (*client.Client)(nil)
)

var testRequirements = types.PaymentRequirements{
	Scheme:            string(types.Exact),
	Network:           "eip155:84532",
	Asset:             "0xasset",
	Amount:            "10000",
	PayTo:             "0xpayto",
	MaxTimeoutSeconds: 60,
}

var testSettleResponse = &types.PaymentSettleResponse{
	Success:     true,
	Transaction: "0xsettled",
	Payer:       "0xpayer",
	Network:     "eip155:84532",
}

// stubFacilitator records the settle call and returns a canned success
// response unless settleFn overrides the outcome.
type stubFacilitator struct {
	verifyCalls int
	settleCalls int
	payload     *types.PaymentPayload
	reqs        *types.PaymentRequirements

	settleFn func() (*types.PaymentSettleResponse, error)
}

func (f *stubFacilitator) Verify(context.Context, *types.PaymentPayload, *types.PaymentRequirements) (*types.PaymentVerifyResponse, error) {
	f.verifyCalls++
	return &types.PaymentVerifyResponse{IsValid: true}, nil
}

func (f *stubFacilitator) Settle(_ context.Context, payload *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentSettleResponse, error) {
	f.settleCalls++
	f.payload = payload
	f.reqs = req
	if f.settleFn != nil {
		return f.settleFn()
	}
	return testSettleResponse, nil
}

// blockingFacilitator settles only after its context is done, simulating a
// stuck facilitator.
type blockingFacilitator struct{}

func (blockingFacilitator) Verify(context.Context, *types.PaymentPayload, *types.PaymentRequirements) (*types.PaymentVerifyResponse, error) {
	return &types.PaymentVerifyResponse{IsValid: true}, nil
}

func (blockingFacilitator) Settle(ctx context.Context, _ *types.PaymentPayload, _ *types.PaymentRequirements) (*types.PaymentSettleResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

var okHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
	w.Write([]byte("ok"))
})

// challengeBody mirrors the wire shape of the 402 body.
type challengeBody struct {
	X402Version int                         `json:"x402Version"`
	Error       string                      `json:"error"`
	Resource    *types.ResourceInfo         `json:"resource"`
	Accepts     []types.PaymentRequirements `json:"accepts"`
}

func newTestGate(t *testing.T, mutate func(*Config)) (*Gate, *stubFacilitator) {
	t.Helper()
	stub := &stubFacilitator{}
	cfg := Config{
		Requirements: testRequirements,
		Facilitator:  stub,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	gate, err := New(cfg)
	require.NoError(t, err)
	return gate, stub
}

func v2Payload() types.PaymentPayload {
	return types.PaymentPayload{
		X402Version: int(types.X402VersionV2),
		Payload:     map[string]interface{}{"authorization": "0xsig"},
		Accepted:    testRequirements,
	}
}

func paidRequest(t *testing.T, payload types.PaymentPayload) *http.Request {
	t.Helper()
	raw, err := json.Marshal(payload)
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "/resource", nil)
	req.Header.Set(HeaderXPayment, string(raw))
	return req
}

func decodeChallenge(t *testing.T, rec *httptest.ResponseRecorder) challengeBody {
	t.Helper()
	var body challengeBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	return body
}

func TestWrapUnpaidRequestChallenges(t *testing.T) {
	gate, stub := newTestGate(t, nil)
	downstream := false
	handler := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { downstream = true }))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	require.False(t, downstream, "unpaid request must not reach the resource")
	require.Zero(t, stub.settleCalls)

	body := decodeChallenge(t, rec)
	require.Equal(t, int(types.X402VersionV2), body.X402Version)
	require.Equal(t, "payment required", body.Error)
	require.Len(t, body.Accepts, 1)
	require.Equal(t, string(types.Exact), body.Accepts[0].Scheme)
	require.Equal(t, "eip155:84532", body.Accepts[0].Network)

	encoded := base64.StdEncoding.EncodeToString(rec.Body.Bytes())
	require.Equal(t, encoded, rec.Header().Get(HeaderPaymentRequired))
	require.Equal(t, encoded, rec.Header().Get(HeaderXPaymentRequired))
}

func TestWrapPaidRequestSettlesAndForwards(t *testing.T) {
	gate, stub := newTestGate(t, nil)
	var forwarded http.Header
	var settlement Settlement
	var settlementOK bool
	handler := gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = r.Header.Clone()
		settlement, settlementOK = SettlementFrom(r.Context())
		// Forge payment headers downstream; the trusted receipt must win.
		w.Header().Set(HeaderPaymentResponse, "forged")
		w.Header().Set(HeaderPaymentRequired, "forged")
		w.Write([]byte("ok")) // no WriteHeader: exercises the auto-200
	}))

	req := paidRequest(t, v2Payload())
	req.Header.Set("X-Trace", "keep")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code, "downstream never called WriteHeader")
	require.Equal(t, "ok", rec.Body.String())
	require.Equal(t, 1, stub.settleCalls)

	require.Equal(t, int(types.X402VersionV2), stub.payload.X402Version)
	require.Equal(t, map[string]interface{}{"authorization": "0xsig"}, stub.payload.Payload)
	require.Equal(t, testRequirements, *stub.reqs, "settle receives the configured requirements")

	for _, name := range paymentHeaders {
		require.Empty(t, forwarded.Get(name), "%s must be stripped before forwarding", name)
	}
	require.Equal(t, "keep", forwarded.Get("X-Trace"), "unrelated headers survive")

	receipt, err := json.Marshal(testSettleResponse)
	require.NoError(t, err)
	encoded := base64.StdEncoding.EncodeToString(receipt)
	require.Equal(t, encoded, rec.Header().Get(HeaderPaymentResponse))
	require.Equal(t, encoded, rec.Header().Get(HeaderXPaymentResponse))
	require.Empty(t, rec.Header().Get(HeaderPaymentRequired), "forged challenge header must be stripped")

	require.True(t, settlementOK)
	require.Equal(t, "0xsettled", settlement.Transaction)
	require.Equal(t, types.Network("eip155:84532"), settlement.Network)
	require.Equal(t, "0xpayer", settlement.Payer)
}

func TestWrapMethodFilterPassesOthersThrough(t *testing.T) {
	gate, stub := newTestGate(t, func(cfg *Config) { cfg.Methods = []string{"POST"} })
	var forwardedHeader string
	handler := gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwardedHeader = r.Header.Get(HeaderXPayment)
		w.Write([]byte(r.Method))
	}))

	get := httptest.NewRequest(http.MethodGet, "/resource", nil)
	get.Header.Set(HeaderXPayment, "!!!not-a-payment!!!")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, get)

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, http.MethodGet, rec.Body.String())
	require.Equal(t, "!!!not-a-payment!!!", forwardedHeader, "unpaid method passes through untouched")
	require.Zero(t, stub.settleCalls)

	post := httptest.NewRequest(http.MethodPost, "/resource", nil)
	post.Header.Set(HeaderXPayment, "!!!not-a-payment!!!")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, post)

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	body := decodeChallenge(t, rec)
	require.Equal(t, "invalid payment payload", body.Error)
	require.Zero(t, stub.settleCalls)
}

func TestWrapSettleTimeoutChallenges(t *testing.T) {
	gate, err := New(Config{
		Requirements:   testRequirements,
		Facilitator:    blockingFacilitator{},
		RequestTimeout: 20 * time.Millisecond,
	})
	require.NoError(t, err)
	downstream := false
	handler := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { downstream = true }))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, paidRequest(t, v2Payload()))

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	require.False(t, downstream, "timed-out settlement must not reach the resource")
	body := decodeChallenge(t, rec)
	require.Equal(t, "payment settlement failed", body.Error)
}

func TestWrapRejectedSettlementChallenges(t *testing.T) {
	cases := map[string]func(*stubFacilitator){
		"facilitator error": func(f *stubFacilitator) {
			f.settleFn = func() (*types.PaymentSettleResponse, error) { return nil, errors.New("broadcast failed") }
		},
		"nil response": func(f *stubFacilitator) {
			f.settleFn = func() (*types.PaymentSettleResponse, error) { return nil, nil }
		},
		"unsuccessful response": func(f *stubFacilitator) {
			f.settleFn = func() (*types.PaymentSettleResponse, error) {
				return &types.PaymentSettleResponse{Success: false, ErrorReason: "transaction_failed"}, nil
			}
		},
	}
	for name, poison := range cases {
		t.Run(name, func(t *testing.T) {
			gate, stub := newTestGate(t, nil)
			poison(stub)
			downstream := false
			handler := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { downstream = true }))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, paidRequest(t, v2Payload()))

			require.Equal(t, http.StatusPaymentRequired, rec.Code)
			require.False(t, downstream, "rejected settlement must not reach the resource")
			body := decodeChallenge(t, rec)
			require.Equal(t, "payment settlement failed", body.Error)
			require.Empty(t, rec.Header().Get(HeaderPaymentResponse))
		})
	}
}

func TestWrapForwardsRequirementsAndAcceptedRoundTrip(t *testing.T) {
	gate, stub := newTestGate(t, nil)
	payload := v2Payload()
	payload.Payload = map[string]interface{}{
		"permit": map[string]interface{}{"amount": "10000"},
	}
	handler := gate.Wrap(okHandler)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, paidRequest(t, payload))

	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, stub.settleCalls)
	require.Equal(t, payload.Accepted, stub.payload.Accepted, "accepted requirements survive the header round-trip")
	require.Equal(t, testRequirements.Amount, stub.reqs.Amount)
	require.Equal(t, testRequirements.Network, stub.reqs.Network)
	require.Equal(t, testRequirements, *stub.reqs, "settle receives the configured contract, not the client's")
}
