package nano

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultEndpoints are public Nano nodes whose block_info response this
// facilitator parses. They are the standard building block every Nano tool
// reads settlement through; rpc.nano.to may require an API key header which
// the facilitator sets from configuration.
var DefaultEndpoints = []string{
	"https://rpc.nano.to",
	"https://rainstorm.city/api",
}

const (
	defaultRequestTimeout = 15 * time.Second
	maxResponseBytes      = 4 << 20
)

// blockInfoRequest is the Nano RPC block_info action this client issues.
type blockInfoRequest struct {
	Action string `json:"action"`
	Hash   string `json:"hash"`
	JSON   string `json:"json_block"`
	Force  string `json:"force"`
}

// BlockContents is the parsed <contents> of a Nano block.
type BlockContents struct {
	// Type is the Nano state-block subtype ("send", "receive",
	// "open", "change").
	Type string `json:"type"`
	// Destination is the send block's destination account; present on send
	// blocks as the block's link in address form.
	Destination string `json:"destination"`
	// Link is the raw block link (destination public key for a send, or the
	// source block hash for a receive).
	Link string `json:"link"`
}

// BlockInfo is the parsed, normalized shape of a Nano node's block_info
// response. Different nodes return slightly different key names; the
// facilitator reads the aliases (block_account|account,
// contents.type|subtype) through these normalized fields.
type BlockInfo struct {
	// Account is the account that authored the block.
	Account string `json:"block_account"`
	// Subtype is the block subtype when the envelope did not nest it.
	Subtype string `json:"subtype"`
	// Contents holds the parsed state-block.
	Contents *BlockContents `json:"contents"`
	// Amount is the value transferred by the block in raw (1 XNO = 10^30 raw).
	Amount string `json:"amount"`
	// Confirmed is "true"/"false" from the node when it can assert
	// cementation; some nodes omit it.
	Confirmed string `json:"confirmed"`
}

// rawBlockInfo is the raw wire shape; parsing tolerates aliases.
type rawBlockInfo struct {
	BlockAccount string          `json:"block_account"`
	Account      string          `json:"account"`
	Subtype      string          `json:"subtype"`
	Confirmed    string          `json:"confirmed"`
	Amount       string          `json:"amount"`
	Contents     json.RawMessage `json:"contents"`
}

// blockInfoResponse wraps the node response (which may carry an error).
type blockInfoResponse struct {
	Error string        `json:"error"`
	Block *rawBlockInfo `json:"block"`
	rawBlockInfo
}

// Client is the Nano node RPC client the facilitator verifies settlement
// through. It is a thin, standard-library JSON-RPC POSTer over the node's
// HTTP endpoint — no Nano SDK dependency.
type Client struct {
	endpoints []string
	apiKey    string
	http      *http.Client
}

// NewClient builds a client over the supplied endpoints with an optional API
// key header (rpc.nano.to). The apiKey is passed in by configuration and is
// only ever sent in the request header.
func NewClient(endpoints []string, apiKey string) *Client {
	return &Client{
		endpoints: endpoints,
		apiKey:    strings.TrimSpace(apiKey),
		http:      &http.Client{Timeout: defaultRequestTimeout},
	}
}

// BlockInfo fetches and normalizes the block_info response for hash.
func (c *Client) BlockInfo(ctx context.Context, hash string) (*BlockInfo, error) {
	if c == nil {
		return nil, fmt.Errorf("nano client is nil")
	}
	var lastErr error
	for _, endpoint := range c.endpoints {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" {
			continue
		}
		info, err := c.blockInfo(ctx, endpoint, hash)
		if err != nil {
			lastErr = err
			continue
		}
		return info, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no endpoints configured")
	}
	return nil, fmt.Errorf("all nano endpoints failed: %w", lastErr)
}

func (c *Client) blockInfo(ctx context.Context, endpoint, hash string) (*BlockInfo, error) {
	body, err := json.Marshal(blockInfoRequest{
		Action: "block_info",
		Hash:   hash,
		JSON:   "true",
	})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	if c.apiKey != "" {
		request.Header.Set("x-api-key", c.apiKey)
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("nano node %s: %s", response.Status, strings.TrimSpace(string(payload)))
	}
	var raw blockInfoResponse
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, fmt.Errorf("decode block_info: %w", err)
	}
	if raw.Error != "" {
		return nil, fmt.Errorf("nano node error: %s", raw.Error)
	}
	// Normalize aliases: some nodes put the block under "block" and the
	// account under "account"; the envelope may carry "subtype".
	record := raw.rawBlockInfo
	if raw.Block != nil {
		record = *raw.Block
		if record.BlockAccount == "" {
			record.BlockAccount = raw.rawBlockInfo.BlockAccount
		}
	}
	account := record.BlockAccount
	if account == "" {
		account = record.Account
	}
	contents, err := parseContents(record.Contents)
	if err != nil {
		return nil, err
	}
	subtype := contents.Type
	if subtype == "" {
		subtype = record.Subtype
	}
	return &BlockInfo{
		Account:   account,
		Subtype:   subtype,
		Contents:  contents,
		Amount:    record.Amount,
		Confirmed: record.Confirmed,
	}, nil
}

func parseContents(raw json.RawMessage) (*BlockContents, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("block has no contents")
	}
	type envelope struct {
		Type          string `json:"type"`
		Subtype       string `json:"subtype"`
		Destination   string `json:"destination"`
		Link          string `json:"link"`
		LinkAsAccount string `json:"link_as_account"`
	}
	var e envelope
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&e); err != nil {
		return nil, fmt.Errorf("decode block contents: %w", err)
	}
	blockType := e.Type
	if blockType == "" {
		blockType = e.Subtype
	}
	// Real Nano nodes report a send block's destination under
	// link_as_account (the block's link decoded to an address); some nodes
	// use destination or a raw link. Canonicalize to the address form.
	destination := e.LinkAsAccount
	if destination == "" {
		destination = e.Destination
	}
	return &BlockContents{
		Type:        blockType,
		Destination: destination,
		Link:        e.Link,
	}, nil
}
