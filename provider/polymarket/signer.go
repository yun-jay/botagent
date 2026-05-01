package polymarket

import (
	"crypto/ecdsa"
	"fmt"
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/signer/core/apitypes"
)

// Signer defines the interface for EIP-712 signing.
type Signer interface {
	Address() common.Address
	ChainID() *big.Int
	SignTypedData(domain *apitypes.TypedDataDomain, types apitypes.Types, message apitypes.TypedDataMessage, primaryType string) ([]byte, error)
}

// SignatureType indicates the wallet type for signature verification.
type SignatureType int

const (
	SignatureEOA         SignatureType = 0
	SignatureProxy       SignatureType = 1
	SignatureGnosisSafe  SignatureType = 2
)

// PrivateKeySigner implements Signer using a local ECDSA private key.
type PrivateKeySigner struct {
	key     *ecdsa.PrivateKey
	address common.Address
	chainID *big.Int
}

// NewPrivateKeySigner creates a signer from a hex-encoded private key.
func NewPrivateKeySigner(hexKey string, chainID int64) (*PrivateKeySigner, error) {
	if len(hexKey) > 2 && hexKey[:2] == "0x" {
		hexKey = hexKey[2:]
	}
	key, err := crypto.HexToECDSA(hexKey)
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}
	return &PrivateKeySigner{
		key:     key,
		address: crypto.PubkeyToAddress(key.PublicKey),
		chainID: big.NewInt(chainID),
	}, nil
}

func (s *PrivateKeySigner) Address() common.Address { return s.address }
func (s *PrivateKeySigner) ChainID() *big.Int       { return s.chainID }

func (s *PrivateKeySigner) SignTypedData(domain *apitypes.TypedDataDomain, types apitypes.Types, message apitypes.TypedDataMessage, primaryType string) ([]byte, error) {
	typedData := apitypes.TypedData{
		Types:       types,
		PrimaryType: primaryType,
		Domain:      *domain,
		Message:     message,
	}
	sighash, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		return nil, fmt.Errorf("hash typed data: %w", err)
	}
	sig, err := crypto.Sign(sighash, s.key)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	// Adjust V value to Ethereum's 27/28 convention.
	if sig[64] < 27 {
		sig[64] += 27
	}
	return sig, nil
}

// DeriveSafeWallet calculates the deterministic Gnosis Safe address for an EOA on Polygon.
func DeriveSafeWallet(eoa common.Address) common.Address {
	safeFactory := common.HexToAddress("0xaacFeEa03eb1561C4e67d661e40682Bd20E3541b")
	initCodeHash := common.FromHex("0x2bce2127ff07fb632d16c8347c4ebf501f4841168bed00d9e6ef715ddb6fcecf")
	paddedEOA := common.LeftPadBytes(eoa.Bytes(), 32)
	salt := crypto.Keccak256(paddedEOA)
	return crypto.CreateAddress2(safeFactory, common.BytesToHash(salt), initCodeHash)
}

// MakerAddress returns the correct maker address for the given signer and signature type.
// For Gnosis Safe, it derives the Safe address. For EOA, it returns the signer address.
// If funder is non-zero, it is used directly as the maker.
func MakerAddress(signer Signer, sigType SignatureType, funder common.Address) common.Address {
	if funder != (common.Address{}) {
		return funder
	}
	if sigType == SignatureGnosisSafe {
		return DeriveSafeWallet(signer.Address())
	}
	return signer.Address()
}

// saltGenerator returns a random salt fitting in 53 bits (safe for JSON numbers).
func generateSalt() (*big.Int, error) {
	max := new(big.Int).Lsh(big.NewInt(1), 53)
	salt, err := crypto.GenerateKey()
	if err != nil {
		return nil, err
	}
	saltInt := new(big.Int).SetBytes(crypto.Keccak256(salt.D.Bytes()))
	return saltInt.Mod(saltInt, max), nil
}

