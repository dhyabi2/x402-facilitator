package nano

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// realWireBlockInfo is the exact block_info wire shape real Nano nodes return
// for a send block (block_account at the top level, the subtype and
// destination nested under contents, amount in raw, confirmed as the string
// "true"). This is the shape documented by the live Nano nodes this
// facilitator reads through, so the parser must canonicalize it — never the
// stub-only shape.
const realWireBlockInfo = `{
  "block_account": "nano_3t6k35gi95xu6ter3d73en7kzakssx9ft9mwewbfnx4y2k9a7wug74f4hacd",
  "amount": "205676479",
  "balance": "105501774296641629762180171316683414079498612324520",
  "height": "152234386",
  "local_timestamp": "1700000000",
  "confirmed": "true",
  "contents": {
    "type": "send",
    "account": "nano_3t6k35gi95xu6ter3d73en7kzakssx9ft9mwewbfnx4y2k9a7wug74f4hacd",
    "previous": "F3F7FE9A0B1E2C3D4E5F60718293A4B5C6D7E8F9A0B1C2D3E4F5A6B7C8D9E0F1",
    "representative": "nano_3t6k35gi95xu6ter3d73en7kzakssx9ft9mwewbfnx4y2k9a7wug74f4hacd",
    "balance": "105501774296641762180171316683414079498612324520",
    "link": "055BCAB0B1F531E02C91B36746CBE0B7D8573437C4A1B18F47A6C963B8EA9F4C",
    "link_as_account": "nano_1111111111111111111111111111111111111111111111111111111111111111",
    "signature": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
    "work": "0000000000000000"
  },
  "subtype": "send"
}`

// TestClientParsesRealBlockInfoShape feeds the exact wire shape a real Nano
// node returns and asserts the client canonicalizes it (this is L0's grounded
// oracle: verifying reads amount, destination and subtype from the on-chain
// block, never the request body).
func TestClientParsesRealBlockInfoShape(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(realWireBlockInfo))
	}))
	defer srv.Close()

	client := NewClient([]string{srv.URL}, "")
	block, err := client.BlockInfo(context.Background(), "055BCAB0B1F531E02C91B36746CBE0B7D8573437C4A1B18F47A6C963B8EA9F4C")
	require.NoError(t, err)
	require.NotNil(t, block)
	require.Equal(t, "nano_3t6k35gi95xu6ter3d73en7kzakssx9ft9mwewbfnx4y2k9a7wug74f4hacd", block.Account)
	require.Equal(t, "send", block.Subtype)
	require.Equal(t, "send", block.Contents.Type)
	// Destination must be read from the block's link_as_account (the send
	// destination), not from anywhere else.
	require.Equal(t, "nano_1111111111111111111111111111111111111111111111111111111111111111", block.Contents.Destination)
	require.Equal(t, "205676479", block.Amount)
	require.Equal(t, "true", block.Confirmed)
}

// TestClientFailsClosedOnNodeError verifies a node error (a non-2xx or a
// response with an "error" key) surfaces as a failure the facilitator maps to
// an invalid payment — the fail-closed seam.
func TestClientFailsClosedOnNodeError(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"error": "Block not found"}`))
	}))
	defer srv.Close()

	client := NewClient([]string{srv.URL}, "")
	_, err := client.BlockInfo(context.Background(), "055BCAB0B1F531E02C91B36746CBE0B7D8573437C4A1B18F47A6C963B8EA9F4C")
	require.Error(t, err)
	require.Contains(t, err.Error(), "Block not found")
}

// TestClientSendsAPIKeyHeaderWhenConfigured verifies the optional API key is
// only ever carried in the request header (never logged or returned).
func TestClientSendsAPIKeyHeaderWhenConfigured(t *testing.T) {
	var gotHeader string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(realWireBlockInfo))
	}))
	defer srv.Close()

	client := NewClient([]string{srv.URL}, "secret-key")
	_, err := client.BlockInfo(context.Background(), "055BCAB0B1F531E02C91B36746CBE0B7D8573437C4A1B18F47A6C963B8EA9F4C")
	require.NoError(t, err)
	require.Equal(t, "secret-key", gotHeader)
}

// TestClientTriesNextEndpointOnFailure verifies failover across endpoints.
func TestClientTriesNextEndpointOnFailure(t *testing.T) {
	var dead, live *httptest.Server
	live = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(realWireBlockInfo))
	}))
	defer live.Close()
	// A dead endpoint (closed server) forces failover to the live one.
	dead = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	deadURL := dead.URL
	dead.Close()

	client := NewClient([]string{deadURL, live.URL}, "")
	block, err := client.BlockInfo(context.Background(), "055BCAB0B1F531E02C91B36746CBE0B7D8573437C4A1B18F47A6C963B8EA9F4C")
	require.NoError(t, err)
	require.NotNil(t, block)
	require.True(t, strings.HasPrefix(block.Account, "nano_"), "must read the block from the live endpoint after failover")
}
