package x402http

import "net/http"

// x402 wire headers. The X-PAYMENT-REQUIRED and X-PAYMENT-RESPONSE names are
// legacy twins published alongside PAYMENT-REQUIRED and PAYMENT-RESPONSE;
// PAYMENT-SIGNATURE is a legacy inbound fallback for X-PAYMENT.
const (
	HeaderXPayment         = "X-PAYMENT"
	HeaderPaymentSignature = "PAYMENT-SIGNATURE"
	HeaderPaymentRequired  = "PAYMENT-REQUIRED"
	HeaderXPaymentRequired = "X-PAYMENT-REQUIRED"
	HeaderPaymentResponse  = "PAYMENT-RESPONSE"
	HeaderXPaymentResponse = "X-PAYMENT-RESPONSE"
)

var paymentHeaders = [...]string{
	HeaderXPayment,
	HeaderPaymentSignature,
	HeaderPaymentRequired,
	HeaderXPaymentRequired,
	HeaderPaymentResponse,
	HeaderXPaymentResponse,
}

// StripPaymentHeaders deletes every x402 payment header from h.
func StripPaymentHeaders(h http.Header) {
	for _, name := range paymentHeaders {
		h.Del(name)
	}
}
