package polymarket

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
)

func TestSignOrderV2_Basic(t *testing.T) {
	// Use a deterministic test key.
	signer, err := NewPrivateKeySigner("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", 137)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	maker := MakerAddress(signer, SignatureEOA, common.Address{})

	order := &OrderV2{
		Salt:          big.NewInt(12345),
		Maker:         maker,
		TokenID:       big.NewInt(100),
		MakerAmount:   big.NewInt(1_000_000),
		TakerAmount:   big.NewInt(2_000_000),
		Side:          "BUY",
		SignatureType: int(SignatureEOA),
		Timestamp:     1713398400000,
	}

	signed, err := SignOrderV2(signer, "test-api-key", order, false)
	if err != nil {
		t.Fatalf("sign order: %v", err)
	}

	if signed.Signature == "" {
		t.Fatal("expected non-empty signature")
	}
	if signed.Owner != "test-api-key" {
		t.Errorf("owner = %q, want test-api-key", signed.Owner)
	}
	// Signature should be 0x-prefixed hex, 65 bytes = 132 hex chars + 2 for "0x"
	if len(signed.Signature) != 132 {
		t.Errorf("signature length = %d, want 132", len(signed.Signature))
	}

	t.Logf("signature: %s", signed.Signature)
}

func TestSignOrderV2_NegRisk(t *testing.T) {
	signer, err := NewPrivateKeySigner("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", 137)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	order := &OrderV2{
		Salt:          big.NewInt(99999),
		Maker:         signer.Address(),
		TokenID:       big.NewInt(200),
		MakerAmount:   big.NewInt(500_000),
		TakerAmount:   big.NewInt(1_000_000),
		Side:          "SELL",
		SignatureType: int(SignatureEOA),
		Timestamp:     1713398400000,
	}

	signedRegular, err := SignOrderV2(signer, "key", order, false)
	if err != nil {
		t.Fatalf("sign regular: %v", err)
	}

	// Reset salt so we get the same order content.
	order.Salt = big.NewInt(99999)
	signedNegRisk, err := SignOrderV2(signer, "key", order, true)
	if err != nil {
		t.Fatalf("sign neg-risk: %v", err)
	}

	// Different verifyingContract should produce different signatures.
	if signedRegular.Signature == signedNegRisk.Signature {
		t.Error("regular and neg-risk signatures should differ (different verifyingContract)")
	}
}

func TestSignOrderV2_GnosisSafe(t *testing.T) {
	signer, err := NewPrivateKeySigner("ac0974bec39a17e36ba4a6b4d238ff944bacb478cbed5efcae784d7bf4f2ff80", 137)
	if err != nil {
		t.Fatalf("create signer: %v", err)
	}

	funder := common.HexToAddress("0x9a478522b655DF2032C499A35402daff06b7F420")
	maker := MakerAddress(signer, SignatureGnosisSafe, funder)

	if maker != funder {
		t.Errorf("maker = %s, want funder %s", maker.Hex(), funder.Hex())
	}

	order := &OrderV2{
		Salt:          big.NewInt(42),
		Maker:         maker,
		TokenID:       big.NewInt(300),
		MakerAmount:   big.NewInt(1_000_000),
		TakerAmount:   big.NewInt(2_000_000),
		Side:          "BUY",
		SignatureType: int(SignatureGnosisSafe),
		Timestamp:     1713398400000,
	}

	signed, err := SignOrderV2(signer, "api-key", order, true)
	if err != nil {
		t.Fatalf("sign order: %v", err)
	}

	if signed.Signature == "" {
		t.Fatal("expected non-empty signature")
	}
}

func TestBuildOrderPayload(t *testing.T) {
	order := &OrderV2{
		Salt:          big.NewInt(12345),
		Maker:         common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678"),
		Signer:        common.HexToAddress("0xabcdefabcdefabcdefabcdefabcdefabcdefabcd"),
		TokenID:       big.NewInt(100),
		MakerAmount:   big.NewInt(1_000_000),
		TakerAmount:   big.NewInt(2_000_000),
		Side:          "BUY",
		SignatureType: 2,
		Timestamp:     1713398400000,
	}

	signed := &SignedOrderV2{
		Order:     order,
		Signature: "0xdeadbeef",
		Owner:     "my-api-key",
		OrderType: "FOK",
	}

	payload := BuildOrderPayload(signed)
	if payload["owner"] != "my-api-key" {
		t.Errorf("owner = %v, want my-api-key", payload["owner"])
	}
	if payload["orderType"] != "FOK" {
		t.Errorf("orderType = %v, want FOK", payload["orderType"])
	}

	orderMap := payload["order"].(map[string]interface{})
	if orderMap["side"] != "BUY" {
		t.Errorf("side = %v, want BUY", orderMap["side"])
	}
	if orderMap["signature"] != "0xdeadbeef" {
		t.Errorf("signature = %v, want 0xdeadbeef", orderMap["signature"])
	}
	if orderMap["timestamp"] != "1713398400000" {
		t.Errorf("timestamp = %v, want 1713398400000", orderMap["timestamp"])
	}
}

func TestBuildSimpleOrderV2(t *testing.T) {
	maker := common.HexToAddress("0x1234567890abcdef1234567890abcdef12345678")
	tokenID := "55672014635283889802989278540843249274560731895658659537716021118792377922815"

	order, err := BuildSimpleOrderV2(tokenID, 0.55, 100, "BUY", SignatureGnosisSafe, maker)
	if err != nil {
		t.Fatalf("build order: %v", err)
	}

	if order.Side != "BUY" {
		t.Errorf("side = %s, want BUY", order.Side)
	}
	if order.SignatureType != int(SignatureGnosisSafe) {
		t.Errorf("signatureType = %d, want %d", order.SignatureType, SignatureGnosisSafe)
	}
	if order.MakerAmount.Int64() != 55_000_000 { // 100 * 0.55 * 1_000_000
		t.Errorf("makerAmount = %d, want 55000000", order.MakerAmount.Int64())
	}
	if order.TakerAmount.Int64() != 100_000_000 { // 100 * 1_000_000
		t.Errorf("takerAmount = %d, want 100000000", order.TakerAmount.Int64())
	}
	if order.Maker != maker {
		t.Errorf("maker = %s, want %s", order.Maker.Hex(), maker.Hex())
	}
}

func TestDeriveSafeWallet(t *testing.T) {
	// Known EOA -> Safe derivation (same logic as GoPolymarket SDK).
	eoa := common.HexToAddress("0xf39Fd6e51aad88F6F4ce6aB8827279cffFb92266")
	safe := DeriveSafeWallet(eoa)
	if safe == (common.Address{}) {
		t.Fatal("derived safe address is zero")
	}
	t.Logf("EOA %s -> Safe %s", eoa.Hex(), safe.Hex())
}
