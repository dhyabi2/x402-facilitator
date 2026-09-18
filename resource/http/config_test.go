package x402http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/gosuda/x402-facilitator/types"
)

func TestNewValidation(t *testing.T) {
	valid := func() Config {
		return Config{
			Requirements: testRequirements,
			Facilitator:  &stubFacilitator{},
		}
	}
	cases := []struct {
		name   string
		mutate func(*Config)
	}{
		{"nil facilitator", func(c *Config) { c.Facilitator = nil }},
		{"blank scheme", func(c *Config) { c.Requirements.Scheme = "  " }},
		{"blank network", func(c *Config) { c.Requirements.Network = "" }},
		{"blank asset", func(c *Config) { c.Requirements.Asset = " " }},
		{"blank amount", func(c *Config) { c.Requirements.Amount = " " }},
		{"blank payTo", func(c *Config) { c.Requirements.PayTo = "\t" }},
		{"nil resource", func(c *Config) { c.Resource = nil }},
		{"blank resource url", func(c *Config) { c.Resource = &types.ResourceInfo{URL: "  "} }},
		{"unsupported payment flow", func(c *Config) { c.PaymentFlow = "verify-resource-settle" }},
		{"negative request timeout", func(c *Config) { c.RequestTimeout = -time.Second }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := valid()
			tc.mutate(&cfg)
			gate, err := New(cfg)
			require.Error(t, err)
			require.Nil(t, gate)
		})
	}
}

func TestNewAcceptsExplicitSupportedFlow(t *testing.T) {
	cfg := Config{
		Requirements: testRequirements,
		Facilitator:  &stubFacilitator{},
		Resource:     testResource,
		PaymentFlow:  PaymentFlowSettleBeforeResource,
	}
	gate, err := New(cfg)
	require.NoError(t, err)
	require.NotNil(t, gate)
}

func TestNewDefaultsAndNoCfgMutation(t *testing.T) {
	cfg := Config{
		Requirements: testRequirements,
		Facilitator:  &stubFacilitator{},
		Resource:     testResource,
	}
	cfg.Requirements.MaxTimeoutSeconds = 0

	gate, err := New(cfg)
	require.NoError(t, err)
	require.Zero(t, cfg.Requirements.MaxTimeoutSeconds, "New must not mutate cfg")

	downstream := false
	handler := gate.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { downstream = true }))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/resource", nil))

	require.Equal(t, http.StatusPaymentRequired, rec.Code)
	require.False(t, downstream)
	body := decodeChallenge(t, rec)
	require.Len(t, body.Accepts, 1)
	require.Equal(t, 60, body.Accepts[0].MaxTimeoutSeconds, "unset maxTimeoutSeconds defaults to 60")

	payload, err := json.Marshal(body.Accepts[0])
	require.NoError(t, err)
	require.Contains(t, string(payload), `"maxTimeoutSeconds":60`)
}
