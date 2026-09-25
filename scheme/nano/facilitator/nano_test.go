package facilitator

import (
	"context"
	"testing"

	nanoscheme "github.com/gosuda/x402-facilitator/scheme/nano"
	"github.com/gosuda/x402-facilitator/types"
)

const (
	goodBlock = "ECCB8CB65CD3106EDA8CE9AA893FEAD497A91BCA903890CBD7A5C59F06AB9113"
	payTo     = "nano_1111111111111111111111111111111111111111111111111111hifc8npp"
	payer     = "nano_3t6k35gi95xu6tergt6p69ck76ogmitsa8mnijtpxm9fkcm736xtoncuohr3"
	amount    = "205676479000000000000000000000000000000"
)

func realBlock(endpoint string, _ map[string]interface{}) (map[string]interface{}, error) {
	return map[string]interface{}{
		"block_account": payer,
		"amount":        amount,
		"confirmed":     "true",
		"contents": map[string]interface{}{
			"type":        "send",
			"destination": payTo,
		},
	}, nil
}

func newTestFac(cb nanoscheme.Callback) (*NanoFacilitator, error) {
	return NewNanoFacilitatorWithOptions(nanoscheme.NetworkMainnet, "", "",
		NanoFacilitatorOptions{
			Endpoints: []string{"https://a", "https://b"},
			Client:    nanoscheme.NewClientWithCallback(cb),
		})
}

func req() *types.PaymentRequirements {
	return &types.PaymentRequirements{
		Scheme:  "exact",
		Network: nanoscheme.NetworkMainnet,
		Asset:   "XNO",
		Amount:  amount,
		PayTo:   payTo,
	}
}

func payload() *types.PaymentPayload {
	r := req()
	return &types.PaymentPayload{
		X402Version: 2,
		Payload:     map[string]interface{}{"blockHash": goodBlock},
		Accepted:    *r,
	}
}

func TestVerifyAllGood(t *testing.T) {
	fac, err := newTestFac(realBlock)
	if err != nil {
		t.Fatal(err)
	}
	v, err := fac.Verify(context.Background(), payload(), req())
	if err != nil {
		t.Fatal(err)
	}
	if !v.IsValid {
		t.Fatalf("expected valid, got %+v", v)
	}
	if v.Payer != payer {
		t.Fatalf("payer = %q", v.Payer)
	}
}

func TestVerifyFailClosed(t *testing.T) {
	// A broken second endpoint must fail the whole verify.
	broken := func(endpoint string, body map[string]interface{}) (map[string]interface{}, error) {
		if endpoint == "https://b" {
			return nil, &nanoscheme.RPCError{Endpoint: endpoint, Status: 500, Body: "down"}
		}
		return realBlock(endpoint, body)
	}
	fac, err := newTestFac(broken)
	if err != nil {
		t.Fatal(err)
	}
	v, err := fac.Verify(context.Background(), payload(), req())
	if err != nil {
		t.Fatal(err)
	}
	if v.IsValid {
		t.Fatalf("verify must fail closed on one bad endpoint")
	}
}

func TestVerifyRejectsNonSend(t *testing.T) {
	cb := func(endpoint string, _ map[string]interface{}) (map[string]interface{}, error) {
		b, _ := realBlock(endpoint, nil)
		b["contents"] = map[string]interface{}{"type": "receive", "destination": payTo}
		return b, nil
	}
	fac, err := newTestFac(cb)
	if err != nil {
		t.Fatal(err)
	}
	v, err := fac.Verify(context.Background(), payload(), req())
	if err != nil {
		t.Fatal(err)
	}
	if v.IsValid {
		t.Fatalf("non-send must be rejected")
	}
}

func TestVerifyRejectsBadBlockHash(t *testing.T) {
	fac, err := newTestFac(realBlock)
	if err != nil {
		t.Fatal(err)
	}
	p := payload()
	p.Payload = map[string]interface{}{"blockHash": "not-a-real-hash"}
	v, err := fac.Verify(context.Background(), p, req())
	if err != nil {
		t.Fatal(err)
	}
	if v.IsValid {
		t.Fatalf("bad block hash must be rejected")
	}
}

func TestVerifyRejectsAmountMismatch(t *testing.T) {
	fac, err := newTestFac(realBlock)
	if err != nil {
		t.Fatal(err)
	}
	r := req()
	r.Amount = "999"
	v, err := fac.Verify(context.Background(), payload(), r)
	if err != nil {
		t.Fatal(err)
	}
	if v.IsValid {
		t.Fatalf("amount mismatch must be rejected")
	}
}

func TestSettleReVerifies(t *testing.T) {
	// Settle must re-verify the block (fresh RPC read), not trust a prior
	// verify. A block that currently fails (broken endpoint) cannot settle.
	broken := func(endpoint string, body map[string]interface{}) (map[string]interface{}, error) {
		if endpoint == "https://b" {
			return nil, &nanoscheme.RPCError{Endpoint: endpoint, Status: 500, Body: "down"}
		}
		return realBlock(endpoint, body)
	}
	fac, err := newTestFac(broken)
	if err != nil {
		t.Fatal(err)
	}
	s, err := fac.Settle(context.Background(), payload(), req())
	if err != nil {
		t.Fatal(err)
	}
	if s.Success {
		t.Fatalf("settle must not succeed when the current verify fails")
	}
}

func TestSettleRejectsReplay(t *testing.T) {
	fac, err := newTestFac(realBlock)
	if err != nil {
		t.Fatal(err)
	}
	s1, err := fac.Settle(context.Background(), payload(), req())
	if err != nil {
		t.Fatal(err)
	}
	if !s1.Success {
		t.Fatalf("first settle should succeed: %+v", s1)
	}
	if s1.Transaction != goodBlock {
		t.Fatalf("transaction should be the block hash, got %q", s1.Transaction)
	}
	// Replay the SAME proof: must be refused.
	s2, err := fac.Settle(context.Background(), payload(), req())
	if err != nil {
		t.Fatal(err)
	}
	if s2.Success {
		t.Fatalf("replay must be refused")
	}
}
