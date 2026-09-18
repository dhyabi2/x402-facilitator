// Package x402http gates net/http handlers behind x402 payments.
//
// The gate is chain-blind and application-blind: it parses the x402 payment
// headers, settles paid requests through a Facilitator before the resource
// runs, publishes the settlement receipt, and strips all payment traffic
// before the application handler sees the request. Chain specifics live in
// the facilitator implementation, not here.
//
// Supported wire contract (phase 1):
//
//   - inbound payment header: canonical v2 PAYMENT-SIGNATURE, with the
//     legacy X-PAYMENT accepted as a fallback when the canonical header is
//     absent;
//   - payment flow: settle-before-resource only (PaymentFlowSettleBeforeResource).
//     Flows such as verify/resource/settle are out of scope and rejected at
//     configuration time rather than silently mis-orchestrated;
//   - the 402 challenge always carries resource metadata: Config.Resource
//     (with URL) is required.
//
// Which routes or HTTP methods are paid is application policy: wrap exactly
// the handlers that should be paid.
package x402http

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gosuda/x402-facilitator/types"
)

const (
	// defaultRequestTimeout bounds a settle call when Config.RequestTimeout
	// is unset, so a stuck facilitator cannot hang a paid request forever.
	defaultRequestTimeout = 30 * time.Second

	// defaultMaxTimeoutSeconds is published as maxTimeoutSeconds when the
	// configured requirements leave it unset.
	defaultMaxTimeoutSeconds = 60
)

// The only challenge reasons written to the 402 body.
const (
	reasonPaymentRequired = "payment required"
	reasonInvalidPayment  = "invalid payment payload"
	reasonSettleFailed    = "payment settlement failed"
)

// PaymentFlowSettleBeforeResource is the only payment flow this phase
// supports: the payment is fully settled before the resource handler runs.
// Flows that interleave verification and resource execution (e.g.
// verify/resource/settle) are intentionally not implemented here and are
// rejected at configuration time, so adopting them later is an additive
// change instead of a redesign of Wrap.
const PaymentFlowSettleBeforeResource = "settle-before-resource"

// Facilitator is the settlement backend the gate drives. Both the local
// facilitators and the remote api/client.Client satisfy it.
type Facilitator interface {
	Verify(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentVerifyResponse, error)
	Settle(ctx context.Context, payment *types.PaymentPayload, req *types.PaymentRequirements) (*types.PaymentSettleResponse, error)
}

// Config owns one x402 payment contract and its facilitator runtime.
type Config struct {
	// Requirements is the accepted payment contract: advertised in the 402
	// accepts list and passed to Settle. Scheme, Network, Asset, Amount, and
	// PayTo are required; a non-positive MaxTimeoutSeconds defaults to 60.
	Requirements types.PaymentRequirements

	// Facilitator settles accepted payments. Required.
	Facilitator Facilitator

	// Resource describes the paid resource and is embedded into every 402
	// challenge; a URL is required, because the v2 payment-required shape
	// advertises it. Use the public URL a paying client can reach.
	Resource *types.ResourceInfo

	// PaymentFlow selects the orchestration the gate performs. Empty
	// defaults to PaymentFlowSettleBeforeResource, the only supported flow;
	// any other value is a configuration error.
	PaymentFlow string

	// RequestTimeout bounds each Settle call. Zero defaults to 30s; a
	// negative value is a configuration error.
	RequestTimeout time.Duration
}

// Gate wraps HTTP handlers behind the configured payment contract.
type Gate struct {
	facilitator    Facilitator
	requirements   types.PaymentRequirements
	resource       types.ResourceInfo
	paymentFlow    string
	requestTimeout time.Duration
}

// New validates cfg and returns a Gate. cfg is not mutated; the gate keeps a
// normalized copy of the requirements.
func New(cfg Config) (*Gate, error) {
	if cfg.Facilitator == nil {
		return nil, errors.New("x402http: facilitator is required")
	}
	requirements := cfg.Requirements
	switch {
	case strings.TrimSpace(requirements.Scheme) == "":
		return nil, errors.New("x402http: requirements.scheme is required")
	case strings.TrimSpace(requirements.Network) == "":
		return nil, errors.New("x402http: requirements.network is required")
	case strings.TrimSpace(requirements.Asset) == "":
		return nil, errors.New("x402http: requirements.asset is required")
	case strings.TrimSpace(requirements.Amount) == "":
		return nil, errors.New("x402http: requirements.amount is required")
	case strings.TrimSpace(requirements.PayTo) == "":
		return nil, errors.New("x402http: requirements.payTo is required")
	}
	if requirements.MaxTimeoutSeconds <= 0 {
		requirements.MaxTimeoutSeconds = defaultMaxTimeoutSeconds
	}
	if cfg.Resource == nil || strings.TrimSpace(cfg.Resource.URL) == "" {
		return nil, errors.New("x402http: resource with a URL is required for the 402 challenge")
	}
	paymentFlow := cfg.PaymentFlow
	if paymentFlow == "" {
		paymentFlow = PaymentFlowSettleBeforeResource
	}
	if paymentFlow != PaymentFlowSettleBeforeResource {
		return nil, fmt.Errorf("x402http: unsupported payment flow %q: only %q is supported", paymentFlow, PaymentFlowSettleBeforeResource)
	}
	if cfg.RequestTimeout < 0 {
		return nil, errors.New("x402http: request timeout must not be negative")
	}
	requestTimeout := cfg.RequestTimeout
	if requestTimeout == 0 {
		requestTimeout = defaultRequestTimeout
	}
	return &Gate{
		facilitator:    cfg.Facilitator,
		requirements:   requirements,
		resource:       *cfg.Resource,
		paymentFlow:    paymentFlow,
		requestTimeout: requestTimeout,
	}, nil
}

// Wrap gates next behind the payment: requests without a verifiable payment
// receive the 402 challenge, paid requests are settled before being
// forwarded, payment headers are stripped from the forwarded request, and the
// settlement receipt is published as trusted response headers. Which routes
// and methods are paid is application policy: wrap exactly the handlers that
// should be paid.
func (g *Gate) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		payload, ok := g.paymentPayloadFromRequest(w, r)
		if !ok {
			return
		}
		if payload.X402Version != int(types.X402VersionV2) {
			g.write402(w, reasonInvalidPayment)
			return
		}

		settleCtx, cancel := context.WithTimeout(r.Context(), g.requestTimeout)
		requirements := g.requirements
		settled, err := g.facilitator.Settle(settleCtx, payload, &requirements)
		cancel()
		if err != nil || settled == nil || !settled.Success {
			g.write402(w, reasonSettleFailed)
			return
		}
		receipt, err := receiptOf(settled)
		if err != nil {
			g.write402(w, reasonSettleFailed)
			return
		}
		setPaymentResponseHeaders(w.Header(), receipt)

		settlement := Settlement{
			Transaction: strings.TrimSpace(settled.Transaction),
			Network:     settled.Network,
			Payer:       strings.TrimSpace(settled.Payer),
		}
		// Forward on the inbound request context: the settle deadline must
		// not abort resource execution.
		forwarded := r.Clone(context.WithValue(r.Context(), settlementKey{}, settlement))
		StripPaymentHeaders(forwarded.Header)
		response := &gateResponseWriter{ResponseWriter: w, receipt: receipt}
		next.ServeHTTP(response, forwarded)
		if !response.wroteHeader {
			response.WriteHeader(http.StatusOK)
		}
	})
}

// paymentPayloadFromRequest parses the inbound payment, writing the 402
// challenge and returning false when no usable payload is present.
func (g *Gate) paymentPayloadFromRequest(w http.ResponseWriter, r *http.Request) (*types.PaymentPayload, bool) {
	raw := ""
	// PAYMENT-SIGNATURE is the canonical v2 request header; X-PAYMENT is the
	// legacy compatibility form and must not override it.
	for _, name := range []string{HeaderPaymentSignature, HeaderXPayment} {
		if value := strings.TrimSpace(r.Header.Get(name)); value != "" {
			raw = value
			break
		}
	}
	if raw == "" {
		g.write402(w, reasonPaymentRequired)
		return nil, false
	}
	payload, ok := decodePaymentPayload(raw)
	if !ok {
		g.write402(w, reasonInvalidPayment)
		return nil, false
	}
	return payload, true
}

// decodePaymentPayload decodes a raw payment header value: direct JSON first,
// then base64 (Std, RawStd, URL, RawURL) wrapping JSON.
func decodePaymentPayload(raw string) (*types.PaymentPayload, bool) {
	var direct types.PaymentPayload
	if err := json.Unmarshal([]byte(raw), &direct); err == nil {
		return &direct, true
	}
	for _, encoding := range []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	} {
		decoded, err := encoding.DecodeString(raw)
		if err != nil {
			continue
		}
		var payload types.PaymentPayload
		if err := json.Unmarshal(decoded, &payload); err == nil {
			return &payload, true
		}
	}
	return nil, false
}

// receiptOf encodes the settlement response as the base64 receipt carried in
// the payment response headers.
func receiptOf(settled *types.PaymentSettleResponse) (string, error) {
	raw, err := json.Marshal(settled)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
}

// setPaymentResponseHeaders publishes the trusted receipt ahead of any
// headers the downstream handler writes.
func setPaymentResponseHeaders(header http.Header, receipt string) {
	header.Set(HeaderPaymentResponse, receipt)
	header.Set(HeaderXPaymentResponse, receipt)
}

// write402 answers with the x402 challenge: the JSON body carries the
// protocol version, the failure reason, the optional resource, and the
// accepted requirements; both challenge headers carry the same body base64
// encoded.
func (g *Gate) write402(w http.ResponseWriter, reason string) {
	body := struct {
		X402Version int                         `json:"x402Version"`
		Error       string                      `json:"error,omitempty"`
		Resource    types.ResourceInfo          `json:"resource"`
		Accepts     []types.PaymentRequirements `json:"accepts"`
	}{
		X402Version: int(types.X402VersionV2),
		Error:       reason,
		Resource:    g.resource,
		Accepts:     []types.PaymentRequirements{g.requirements},
	}
	raw, err := json.Marshal(body)
	if err != nil {
		http.Error(w, "encode x402 payment requirements", http.StatusInternalServerError)
		return
	}
	encoded := base64.StdEncoding.EncodeToString(raw)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(HeaderPaymentRequired, encoded)
	w.Header().Set(HeaderXPaymentRequired, encoded)
	w.WriteHeader(http.StatusPaymentRequired)
	_, _ = w.Write(raw)
}

// gateResponseWriter keeps the trusted settlement receipt ahead of any
// headers written by the downstream handler.
type gateResponseWriter struct {
	http.ResponseWriter
	receipt     string
	wroteHeader bool
}

func (w *gateResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	StripPaymentHeaders(w.Header())
	if w.receipt != "" {
		setPaymentResponseHeaders(w.Header(), w.receipt)
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *gateResponseWriter) Write(body []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

func (w *gateResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gateResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
