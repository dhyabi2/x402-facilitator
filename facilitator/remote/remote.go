// Package remote provides a chain-blind facilitator that delegates
// verification and settlement to a remote x402 facilitator deployment over
// HTTP. Resource servers that settle through a hosted facilitator (for
// example a Casper deployment) use this instead of writing their own
// adapter around the client.
//
// The package depends only on the wire types and the HTTP client: importing
// it must not pull any chain SDK into a consumer's module graph.
package remote

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gosuda/x402-facilitator/api/client"
	"github.com/gosuda/x402-facilitator/types"
)

// Facilitator satisfies the facilitator and resource-server facilitator
// interfaces while delegating Verify and Settle to the remote deployment.
// Supported is answered locally from the configured networks: the interface
// carries no context, so the advertisement is static rather than fetched.
type Facilitator struct {
	client    *client.Client
	supported *types.SupportedResponse
}

type options struct {
	httpClient *http.Client
	authHeader func() (map[string]map[string]string, error)
	networks   []string
}

// Option configures a remote facilitator.
type Option func(*options)

// WithHTTPClient sets the transport used for facilitator calls, for timeout,
// proxy, and TLS control. The client package default applies when unset.
func WithHTTPClient(httpClient *http.Client) Option {
	return func(o *options) {
		o.httpClient = httpClient
	}
}

// WithAuthHeader injects credentials per operation on every Verify and
// Settle call. The returned map is keyed by operation ("verify", "settle")
// and maps to the headers to attach, e.g.
// {"verify": {"Authorization": "Bearer tok"}, "settle": {...}}. An error
// aborts the call without a request being sent.
func WithAuthHeader(authHeader func() (map[string]map[string]string, error)) Option {
	return func(o *options) {
		o.authHeader = authHeader
	}
}

// WithNetworks lists the CAIP-2 networks the remote deployment settles, in
// advertisement order. At least one network is required.
func WithNetworks(networks ...string) Option {
	return func(o *options) {
		o.networks = append(o.networks, networks...)
	}
}

// New builds a remote facilitator backed by baseURL. It fails on an
// unusable URL and requires at least one network to advertise.
func New(baseURL string, opts ...Option) (*Facilitator, error) {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	seen := make(map[string]struct{}, len(o.networks))
	networks := make([]string, 0, len(o.networks))
	for _, network := range o.networks {
		network = strings.TrimSpace(network)
		if network == "" {
			return nil, fmt.Errorf("remote: networks must be non-blank")
		}
		if _, dup := seen[network]; dup {
			continue
		}
		seen[network] = struct{}{}
		networks = append(networks, network)
	}
	if len(networks) == 0 {
		return nil, fmt.Errorf("remote: at least one network is required")
	}

	cl, err := client.NewClient(baseURL)
	if err != nil {
		return nil, fmt.Errorf("remote: %w", err)
	}
	if o.httpClient != nil {
		cl.HTTPClient = o.httpClient
	}
	cl.CreateAuthHeader = o.authHeader

	kinds := make([]types.SupportedKind, 0, len(networks))
	for _, network := range networks {
		kinds = append(kinds, types.SupportedKind{
			X402Version: int(types.X402VersionV2),
			Scheme:      string(types.Exact),
			Network:     network,
		})
	}
	return &Facilitator{
		client: cl,
		supported: &types.SupportedResponse{
			Kinds:      kinds,
			Extensions: []string{},
			Signers:    map[string][]string{},
		},
	}, nil
}

// Verify delegates to the remote deployment's POST /verify.
func (f *Facilitator) Verify(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentVerifyResponse, error) {
	return f.client.Verify(ctx, payment, req)
}

// Settle delegates to the remote deployment's POST /settle.
func (f *Facilitator) Settle(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentSettleResponse, error) {
	return f.client.Settle(ctx, payment, req)
}

// Supported returns the static advertisement built from the configured
// networks. Extensions and Signers are always non-nil so discovery output
// stays well-formed.
func (f *Facilitator) Supported() *types.SupportedResponse {
	return f.supported
}
