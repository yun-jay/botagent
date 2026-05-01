package polymarket

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/common/math"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// V2 Exchange contract addresses on Polygon.
const (
	ExchangeV2        = "0xE111180000d2663C0091e4f400237545B87B996B"
	NegRiskExchangeV2 = "0xe2222d279d744050d28e00520010520000310F59"
)

// OrderV2 is the EIP-712 signable order for CLOB V2.
type OrderV2 struct {
	Salt          *big.Int
	Maker         common.Address
	Signer        common.Address
	TokenID       *big.Int
	MakerAmount   *big.Int
	TakerAmount   *big.Int
	Expiration    *big.Int
	Side          string // "BUY" or "SELL"
	SignatureType int    // 0=EOA, 1=Proxy, 2=GnosisSafe
	Timestamp     int64  // ms epoch
	Metadata      [32]byte
	Builder       [32]byte
}

// SignedOrderV2 is a signed order ready for submission.
type SignedOrderV2 struct {
	Order     *OrderV2
	Signature string
	Owner     string // API key
	OrderType string // GTC, FOK, GTD
}

var orderV2Types = apitypes.Types{
	"EIP712Domain": {
		{Name: "name", Type: "string"},
		{Name: "version", Type: "string"},
		{Name: "chainId", Type: "uint256"},
		{Name: "verifyingContract", Type: "address"},
	},
	"Order": {
		{Name: "salt", Type: "uint256"},
		{Name: "maker", Type: "address"},
		{Name: "signer", Type: "address"},
		{Name: "tokenId", Type: "uint256"},
		{Name: "makerAmount", Type: "uint256"},
		{Name: "takerAmount", Type: "uint256"},
		{Name: "side", Type: "uint8"},
		{Name: "signatureType", Type: "uint8"},
		{Name: "timestamp", Type: "uint256"},
		{Name: "metadata", Type: "bytes32"},
		{Name: "builder", Type: "bytes32"},
	},
}

// SignOrderV2 builds and signs a V2 order using EIP-712.
// The negRisk flag selects the correct verifyingContract address.
func SignOrderV2(signer Signer, apiKey string, order *OrderV2, negRisk bool) (*SignedOrderV2, error) {
	if signer == nil {
		return nil, fmt.Errorf("signer is required")
	}
	if order == nil {
		return nil, fmt.Errorf("order is required")
	}

	// Generate salt if not set.
	if order.Salt == nil || order.Salt.Sign() == 0 {
		salt, err := generateSalt()
		if err != nil {
			return nil, fmt.Errorf("generate salt: %w", err)
		}
		order.Salt = salt
	}

	// Set timestamp if not set.
	if order.Timestamp == 0 {
		order.Timestamp = time.Now().UnixMilli()
	}

	// Set signer address.
	order.Signer = signer.Address()

	// Select exchange address based on neg-risk.
	exchange := ExchangeV2
	if negRisk {
		exchange = NegRiskExchangeV2
	}

	domain := &apitypes.TypedDataDomain{
		Name:              "Polymarket CTF Exchange",
		Version:           "2",
		ChainId:           (*math.HexOrDecimal256)(signer.ChainID()),
		VerifyingContract: exchange,
	}

	// Encode side as uint8: 0=BUY, 1=SELL.
	sideInt := 0
	if strings.ToUpper(order.Side) == "SELL" {
		sideInt = 1
	}

	expiration := big.NewInt(0)
	if order.Expiration != nil {
		expiration = order.Expiration
	}
	_ = expiration // expiration is not in V2 signing type but kept on struct for GTD orders

	message := apitypes.TypedDataMessage{
		"salt":          (*math.HexOrDecimal256)(order.Salt),
		"maker":         order.Maker.String(),
		"signer":        order.Signer.String(),
		"tokenId":       (*math.HexOrDecimal256)(order.TokenID),
		"makerAmount":   (*math.HexOrDecimal256)(order.MakerAmount),
		"takerAmount":   (*math.HexOrDecimal256)(order.TakerAmount),
		"side":          (*math.HexOrDecimal256)(big.NewInt(int64(sideInt))),
		"signatureType": (*math.HexOrDecimal256)(big.NewInt(int64(order.SignatureType))),
		"timestamp":     (*math.HexOrDecimal256)(big.NewInt(order.Timestamp)),
		"metadata":      order.Metadata[:],
		"builder":       order.Builder[:],
	}

	sig, err := signer.SignTypedData(domain, orderV2Types, message, "Order")
	if err != nil {
		return nil, fmt.Errorf("sign order: %w", err)
	}

	owner := apiKey
	if owner == "" {
		owner = signer.Address().String()
	}

	return &SignedOrderV2{
		Order:     order,
		Signature: hexutil.Encode(sig),
		Owner:     owner,
	}, nil
}

// BuildOrderPayload converts a SignedOrderV2 into the wire format for POST /order.
func BuildOrderPayload(signed *SignedOrderV2) map[string]interface{} {
	order := signed.Order

	sideStr := strings.ToUpper(order.Side)

	expiration := "0"
	if order.Expiration != nil {
		expiration = order.Expiration.String()
	}

	orderMap := map[string]interface{}{
		"salt":          order.Salt.Uint64(),
		"maker":         order.Maker.Hex(),
		"signer":        order.Signer.Hex(),
		"tokenId":       order.TokenID.String(),
		"makerAmount":   order.MakerAmount.String(),
		"takerAmount":   order.TakerAmount.String(),
		"side":          sideStr,
		"expiration":    expiration,
		"signatureType": order.SignatureType,
		"signature":     signed.Signature,
		"timestamp":     fmt.Sprintf("%d", order.Timestamp),
		"metadata":      hexutil.Encode(order.Metadata[:]),
		"builder":       hexutil.Encode(order.Builder[:]),
	}

	orderType := signed.OrderType
	if orderType == "" {
		orderType = "GTC"
	}

	return map[string]interface{}{
		"order":     orderMap,
		"owner":     signed.Owner,
		"orderType": orderType,
	}
}

// bigIntFromInt64 wraps an int64 in a big.Int.
func bigIntFromInt64(v int64) *big.Int { return big.NewInt(v) }

// decodeBytes32Hex parses a 0x-prefixed 32-byte hex string into a [32]byte.
func decodeBytes32Hex(s string) ([32]byte, error) {
	var out [32]byte
	if !strings.HasPrefix(s, "0x") && !strings.HasPrefix(s, "0X") {
		s = "0x" + s
	}
	b, err := hexutil.Decode(s)
	if err != nil {
		return out, err
	}
	if len(b) != 32 {
		return out, fmt.Errorf("builder code must be 32 bytes, got %d", len(b))
	}
	copy(out[:], b)
	return out, nil
}

// BuildSimpleOrderV2 creates an OrderV2 from high-level parameters (price, size, side).
// This handles the maker/taker amount calculation that the GoPolymarket SDK does internally.
func BuildSimpleOrderV2(tokenID string, price, size float64, side string, sigType SignatureType, maker common.Address) (*OrderV2, error) {
	tokenIDBig, ok := new(big.Int).SetString(tokenID, 10)
	if !ok {
		return nil, fmt.Errorf("invalid token ID: %s", tokenID)
	}

	// Calculate maker/taker amounts in USDC units (6 decimals).
	// BUY: makerAmount = size * price (USDC you pay), takerAmount = size (shares you get)
	// SELL: makerAmount = size (shares you give), takerAmount = size * price (USDC you get)
	sideUpper := strings.ToUpper(side)
	var makerAmount, takerAmount *big.Int

	scale := 1_000_000.0 // USDC has 6 decimals
	if sideUpper == "BUY" {
		makerAmount = big.NewInt(int64(size * price * scale))
		takerAmount = big.NewInt(int64(size * scale))
	} else {
		makerAmount = big.NewInt(int64(size * scale))
		takerAmount = big.NewInt(int64(size * price * scale))
	}

	return &OrderV2{
		Maker:         maker,
		TokenID:       tokenIDBig,
		MakerAmount:   makerAmount,
		TakerAmount:   takerAmount,
		Side:          sideUpper,
		SignatureType: int(sigType),
		Timestamp:     time.Now().UnixMilli(),
	}, nil
}
