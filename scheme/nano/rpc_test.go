package nano

import "testing"

func TestNormalizeBlockInfoRealShape(t *testing.T) {
	// The real Nano RPC shape (block-13 lesson): confirmed is the STRING
	// "true", subtype lives at contents.type, destination at contents.destination,
	// payer at block_account, amount at top level.
	raw := map[string]interface{}{
		"block_account": "nano_3t6k35gi95xu6tergt6p69ck76ogmitsa8mnijtpxm9fkcm736xtoncuohr3",
		"amount":        "205676479000000000000000000000000000000",
		"confirmed":     "true",
		"contents": map[string]interface{}{
			"type":        "send",
			"destination": "nano_1111111111111111111111111111111111111111111111111111hifc8npp",
		},
	}
	b := NormalizeBlockInfo(raw)
	if b.Subtype != "send" {
		t.Fatalf("subtype = %q, want send", b.Subtype)
	}
	if b.Account != "nano_3t6k35gi95xu6tergt6p69ck76ogmitsa8mnijtpxm9fkcm736xtoncuohr3" {
		t.Fatalf("account = %q", b.Account)
	}
	if b.Destination != "nano_1111111111111111111111111111111111111111111111111111hifc8npp" {
		t.Fatalf("destination = %q", b.Destination)
	}
	if b.AmountRaw != "205676479000000000000000000000000000000" {
		t.Fatalf("amount = %q", b.AmountRaw)
	}
	if !b.Confirmed {
		t.Fatalf("confirmed should be true from string \"true\"")
	}
}

func TestNormalizeBlockInfoAliasKeys(t *testing.T) {
	// Some nodes expose alternate key names; the normalizer must tolerate them.
	raw := map[string]interface{}{
		"account":       "nano_1x",
		"link_as_account": "nano_2y",
		"amount":        "1",
		"confirmed":     true,
		"type":          "send",
	}
	b := NormalizeBlockInfo(raw)
	if b.Subtype != "send" || b.Account != "nano_1x" || b.Destination != "nano_2y" || !b.Confirmed {
		t.Fatalf("alias keys not normalized: %+v", b)
	}
}

func TestNormalizeBlockInfoFailClosedMissingConfirmed(t *testing.T) {
	// A block_info that cannot authoritatively report confirmation must NOT be
	// treated as confirmed.
	raw := map[string]interface{}{"amount": "1", "contents": map[string]interface{}{"type": "send"}}
	b := NormalizeBlockInfo(raw)
	if b.Confirmed {
		t.Fatalf("missing confirmed must not default to true")
	}
	if b.Destination != "" || b.Subtype != "send" {
		t.Fatalf("unexpected normalisation: %+v", b)
	}
}

func TestIsBlockHash(t *testing.T) {
	if !IsBlockHash("ECCB8CB65CD3106EDA8CE9AA893FEAD497A91BCA903890CBD7A5C59F06AB9113") {
		t.Fatalf("valid 64-hex rejected")
	}
	if IsBlockHash("") || IsBlockHash("short") || IsBlockHash("GGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGGG") {
		t.Fatalf("invalid block hash accepted")
	}
}
