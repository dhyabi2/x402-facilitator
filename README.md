# x402-facilitator

**x402-facilitator** is a Go-based middleware that settles on-chain payments authorized via the [x402 protocol](https://x402.dev).

## Prerequisites
- Golang 1.24 or later
- Docker
- Docker Compose

## Supported schemes × networks

x402 v2 treats the payment **scheme** (the on-chain protocol used to move
funds) and the **network** (which chain that protocol runs on) as two
independent axes. This facilitator currently supports:

| Scheme  | `eip155:*` (EVM) | `solana:*` | `sui:*` | `tron:*` | `casper:*` |
|---------|:----------------:|:----------:|:-------:|:--------:|:----------:|
| `exact` |        ✅        |     ✅     |   🚧    |    🚧    |     ✅     |

Networks are specified in [CAIP-2](https://chainagnostic.org/CAIPs/caip-2)
format (e.g. `eip155:84532` for Base Sepolia, `eip155:8453` for Base
mainnet, `eip155:42161` for Arbitrum One). The `exact` scheme supports
both EIP-3009 `transferWithAuthorization` and Permit2
`PermitWitnessTransferFrom` payloads on EVM chains; see the `--method`
flag on `x402-client` to pick between them.

### Casper

Casper is addressed as `casper:casper` (mainnet) and `casper:casper-test`
(testnet). Settlement uses wCSPR, a CEP-18 token with 9 decimals, so
amounts are integer motes encoded as decimal strings; conversions that
would lose sub-mote precision fail instead of truncating.

Casper payments are authorized by the payer and broadcast by a Casper
facilitator service, so `url` points at that service rather than a node
RPC endpoint. It defaults to `https://x402-facilitator.cspr.cloud` and can
be overridden through `url` in `config.toml` or the
`CASPER_FACILITATOR_URL` environment variable.

### Solana

Solana networks are `solana:mainnet`, `solana:devnet`, and other
`solana:*` CAIP-2 identifiers; `url` is the Solana JSON-RPC endpoint (for
example `https://api.devnet.solana.com`). The configured private key is
the facilitator's fee payer, advertised to payers through the
`feePayer` extra and the `signers` map of the `/supported` response.

Settlement uses SPL Token `TransferChecked`: the payer builds and signs a
base64-encoded legacy transaction carrying a single `TransferChecked`
instruction that moves `amount` base units of the `asset` mint (a base58
public key) to the associated token account of `payTo`, naming the
facilitator's fee payer as the transaction fee payer. The facilitator
verifies the transfer against the payment requirements, co-signs as fee
payer, and submits the transaction.

The supported subset is intentionally minimal; transactions outside it are
rejected:

- legacy transaction version only (no versioned/v0 transactions)
- exactly one instruction, which must be the SPL Token `TransferChecked`
  payment (no ComputeBudget, Memo, or other extra instructions)
- exactly two required signers: the facilitator fee payer and the payer,
  whose Ed25519 signature over the serialized message is verified; no SPL
  multisig or Token-2022 support
- the fee payer key must not appear as an instruction account; the
  facilitator only ever signs to pay fees
- settlement reports success only after the RPC reports the transaction
  confirmed and successful; on-chain failures and confirmation timeouts
  are structured settlement failures

## Resource server (HTTP integration)

`resource/http` (package `x402http`) is a chain-blind, application-blind
payment gate for `net/http` services. The gate owns the x402 wire surface:
it parses the inbound `X-PAYMENT` header (with legacy `PAYMENT-SIGNATURE`
fallback), answers unpaid and undecodable requests with a `402` challenge
carrying the accepted requirements, settles paid requests through a
`Facilitator` **before** the resource handler runs, publishes the base64
settlement receipt in `PAYMENT-RESPONSE` / `X-PAYMENT-RESPONSE` headers, and
strips every payment header from the forwarded request. `Methods` lists the
paid HTTP methods; leave it empty to gate every method.

```go
import (
	"net/http"

	"github.com/gosuda/x402-facilitator/facilitator"
	x402http "github.com/gosuda/x402-facilitator/resource/http"
	"github.com/gosuda/x402-facilitator/types"
)

fac, err := facilitator.NewFacilitator(
	types.Exact, "eip155:84532", "https://sepolia.base.org", privateKeyHex)
if err != nil {
	return err
}

gate, err := x402http.New(x402http.Config{
	Requirements: types.PaymentRequirements{
		Scheme:  string(types.Exact),
		Network: "eip155:84532",
		Asset:   "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913",
		Amount:  "10000",
		PayTo:   "0xYourReceivingAddress",
	},
	Facilitator: fac,
})
if err != nil {
	return err
}

mux := http.NewServeMux()
mux.Handle("/paid-resource", gate.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	// receipt headers are already set; the settlement is also on the context
	settlement, _ := x402http.SettlementFrom(r.Context())
	w.Write([]byte("paid: " + settlement.Transaction))
})))
```

Anything satisfying the gate's two-method `Facilitator` interface works as
the settlement backend: the local facilitators in this repository (EVM,
Solana, Sui, Tron, Casper) and the remote `api/client.Client` for talking to
a separate facilitator deployment.

## How to run

### Build binary
```bash
make build
```

### Run x402-facilitator

#### 1. Run with docker compose
```bash
docker compose up
```

#### 2. Configuration
x402-facilitator is configured via `config.toml`.
```toml
# Port for HTTP server (default: 9090)
port = 9090

# Payment protocol scheme. Currently only "exact" is supported; the
# value is the x402 v2 scheme identifier, not a chain name.
scheme = "exact"

# Network in CAIP-2 format. Examples:
#   eip155:84532  — Base Sepolia
#   eip155:8453   — Base mainnet
#   eip155:42161  — Arbitrum One
network = "eip155:84532"

# RPC endpoint the facilitator uses to verify and broadcast
# transactions on the configured network.
url = "https://sepolia.base.org"

# Private key of the facilitator's fee payer (hex, no 0x prefix).
# Leave empty in the repo; inject via your deployment's secret
# management.
privateKey = ""
```

#### 3. Api Specification
After starting the service, open your browser to:
```
/swagger/index.html
```

### Run x402-client
```
Usage:
  x402-client [flags]

Flags:
  -A, --amount string    Amount to send
  -F, --from string      Sender address
  -h, --help             help for x402-client
  -m, --method string    Payment method (eip3009 or permit2) (default "eip3009")
  -n, --network string   CAIP-2 network to pay on (default "eip155:84532")
  -P, --privkey string   Sender private key
  -s, --scheme string    Payment scheme to use (default "exact")
  -T, --to string        Recipient address
  -t, --token string     token contract for sending (default "USDC")
  -u, --url string       Base URL of the facilitator server (default "http://localhost:9090")

Example:
  x402-client -n eip155:84532 -s exact -t USDC -F {0xYourSenderAddress} -T {0xRecipientAddress} -P {YourPrivateKey} -A 1000
```


## Contributing
We welcome any contributions! Feel free to open issues or submit pull requests at any time.
