// Package nano declares the shared types for the Nano (XNO) exact x402
// scheme in the facilitator.
package nano

// NanoPayload is the scheme-specific payment material a payer sends in the
// x402 PaymentPayload. For Nano the payer broadcasts their own signed send
// block directly to the ledger (publication is finality), then presents the
// resulting 64-hex block hash as the proof of payment. The facilitator reads
// the block back off the chain and checks amount, destination and subtype
// against the requirements — nothing is trusted from this payload except the
// hash that names the block.
type NanoPayload struct {
	// BlockHash is the 64-hex hash of the payer's send block, as returned by
	// the Nano network's process RPC.
	BlockHash string `json:"blockHash"`
}
