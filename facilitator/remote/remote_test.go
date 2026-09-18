package remote

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/gosuda/x402-facilitator/facilitator"
	x402http "github.com/gosuda/x402-facilitator/resource/http"
	"github.com/gosuda/x402-facilitator/types"
)

// Compile-time proof that the remote adapter plugs into both the local
// facilitator registry interface and the resource-server gate interface.
// These imports live here so consumers of this package inherit no chain SDK.
var (
	_ facilitator.Facilitator = (*Facilitator)(nil)
	_ x402http.Facilitator    = (*Facilitator)(nil)
)

var testRequirements = types.PaymentRequirements{
	Scheme:            string(types.Exact),
	Network:           "casper:casper-test",
	Asset:             "casper-asset",
	Amount:            "10000000",
	PayTo:             "0xpayto",
	MaxTimeoutSeconds: 60,
}

func testPayload() *types.PaymentPayload {
	return &types.PaymentPayload{
		X402Version: int(types.X402VersionV2),
		Payload:     map[string]interface{}{"signature": "deadbeef"},
		Accepted:    testRequirements,
	}
}

func TestNewValidation(t *testing.T) {
	cases := []struct {
		name   string
		setups []Option
	}{
		{"no networks", nil},
		{"blank network", []Option{WithNetworks("casper:casper-test", "  ")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := append([]Option{WithNetworks()}, tc.setups...)
			f, err := New("http://localhost:1", opts...)
			require.Error(t, err)
			require.Nil(t, f)
		})
	}

	t.Run("invalid base url", func(t *testing.T) {
		f, err := New("://not-a-url", WithNetworks("casper:casper-test"))
		require.Error(t, err)
		require.Nil(t, f)
	})

	t.Run("duplicate networks deduplicate", func(t *testing.T) {
		f, err := New("http://localhost:1",
			WithNetworks("casper:casper-test"),
			WithNetworks("casper:casper-test", "casper:casper"))
		require.NoError(t, err)
		require.Len(t, f.Supported().Kinds, 2)
	})
}

func TestSupportedAdvertisesConfiguredNetworks(t *testing.T) {
	f, err := New("http://localhost:1", WithNetworks("casper:casper", "casper:casper-test"))
	require.NoError(t, err)

	supported := f.Supported()
	require.NotNil(t, supported)
	require.Len(t, supported.Kinds, 2)
	for i, network := range []string{"casper:casper", "casper:casper-test"} {
		kind := supported.Kinds[i]
		require.Equal(t, int(types.X402VersionV2), kind.X402Version)
		require.Equal(t, string(types.Exact), kind.Scheme)
		require.Equal(t, network, kind.Network)
	}
	require.NotNil(t, supported.Extensions)
	require.NotNil(t, supported.Signers)
}

func TestVerifyDelegatesWireAndAuth(t *testing.T) {
	var gotPath, gotAuth, gotContentType string
	var gotBody types.PaymentVerifyRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(types.PaymentVerifyResponse{
			IsValid: true,
			Payer:   "0xpayer",
		}))
	}))
	defer server.Close()

	f, err := New(server.URL,
		WithNetworks("casper:casper-test"),
		WithAuthHeader(func() (map[string]map[string]string, error) {
			return map[string]map[string]string{
				"verify": {"Authorization": "Bearer verify-tok"},
				"settle": {"Authorization": "Bearer settle-tok"},
			}, nil
		}))
	require.NoError(t, err)

	res, err := f.Verify(t.Context(), testPayload(), &testRequirements)
	require.NoError(t, err)
	require.True(t, res.IsValid)
	require.Equal(t, "0xpayer", res.Payer)

	require.Equal(t, "/verify", gotPath)
	require.Equal(t, "Bearer verify-tok", gotAuth)
	require.Equal(t, "application/json", gotContentType)
	require.Equal(t, *testPayload(), gotBody.PaymentPayload)
	require.Equal(t, testRequirements, gotBody.PaymentRequirements)
}

func TestSettleDelegatesWireAndAuth(t *testing.T) {
	var gotPath, gotAuth string
	var gotBody types.PaymentSettleRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(types.PaymentSettleResponse{
			Success:     true,
			Transaction: "0xsettled",
			Payer:       "0xpayer",
			Network:     "casper:casper-test",
		}))
	}))
	defer server.Close()

	f, err := New(server.URL,
		WithNetworks("casper:casper-test"),
		WithAuthHeader(func() (map[string]map[string]string, error) {
			return map[string]map[string]string{
				"verify": {"Authorization": "Bearer verify-tok"},
				"settle": {"Authorization": "Bearer settle-tok"},
			}, nil
		}))
	require.NoError(t, err)

	res, err := f.Settle(t.Context(), testPayload(), &testRequirements)
	require.NoError(t, err)
	require.True(t, res.Success)
	require.Equal(t, "0xsettled", res.Transaction)
	require.Equal(t, "0xpayer", res.Payer)
	require.Equal(t, types.Network("casper:casper-test"), res.Network)

	require.Equal(t, "/settle", gotPath)
	require.Equal(t, "Bearer settle-tok", gotAuth)
	require.Equal(t, *testPayload(), gotBody.PaymentPayload)
	require.Equal(t, testRequirements, gotBody.PaymentRequirements)
}

func TestFacilitatorErrorsPropagate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	f, err := New(server.URL, WithNetworks("casper:casper-test"))
	require.NoError(t, err)

	_, err = f.Verify(t.Context(), testPayload(), &testRequirements)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")

	_, err = f.Settle(t.Context(), testPayload(), &testRequirements)
	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
}

func TestAuthHeaderErrorAbortsCall(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))
	defer server.Close()

	f, err := New(server.URL,
		WithNetworks("casper:casper-test"),
		WithAuthHeader(func() (map[string]map[string]string, error) {
			return nil, context.DeadlineExceeded
		}))
	require.NoError(t, err)

	_, err = f.Verify(t.Context(), testPayload(), &testRequirements)
	require.Error(t, err)
	_, err = f.Settle(t.Context(), testPayload(), &testRequirements)
	require.Error(t, err)
	require.Zero(t, calls, "auth failures must abort before any request is sent")
}

func TestWithoutAuthHeaderNoAuthorizationSent(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(types.PaymentVerifyResponse{IsValid: true})
	}))
	defer server.Close()

	f, err := New(server.URL, WithNetworks("casper:casper-test"))
	require.NoError(t, err)

	res, err := f.Verify(t.Context(), testPayload(), &testRequirements)
	require.NoError(t, err)
	require.True(t, res.IsValid)
	require.Empty(t, gotAuth)
}
