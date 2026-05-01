package polymarket

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"time"
)

// HMACAuth generates CLOB API authentication headers.
type HMACAuth struct {
	address    string // wallet address (POLY_ADDRESS)
	apiKey     string
	secretKey  []byte // pre-decoded from base64 at init
	passphrase string
}

func NewHMACAuth(apiKey, secret, passphrase string) *HMACAuth {
	return NewHMACAuthWithAddress("", apiKey, secret, passphrase)
}

func NewHMACAuthWithAddress(address, apiKey, secret, passphrase string) *HMACAuth {
	// Polymarket uses URL-safe base64 for secrets
	secretBytes, err := base64.URLEncoding.DecodeString(secret)
	if err != nil {
		// Try standard base64 as fallback
		secretBytes, err = base64.StdEncoding.DecodeString(secret)
		if err != nil {
			secretBytes = []byte(secret)
		}
	}
	return &HMACAuth{
		address:    address,
		apiKey:     apiKey,
		secretKey:  secretBytes,
		passphrase: passphrase,
	}
}

// Headers returns the auth headers for a CLOB API request.
// The path should be the full request path; query parameters are stripped for signing.
func (a *HMACAuth) Headers(method, path string, body string) map[string]string {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)

	// Strip query parameters from path for HMAC signing
	signPath := path
	if idx := indexOf(signPath, '?'); idx >= 0 {
		signPath = signPath[:idx]
	}

	message := timestamp + method + signPath + body
	sig := a.sign(message)

	headers := map[string]string{
		"POLY_API_KEY":        a.apiKey,
		"POLY_SIGNATURE":      sig,
		"POLY_TIMESTAMP":      timestamp,
		"POLY_PASSPHRASE":     a.passphrase,
	}
	if a.address != "" {
		headers["POLY_ADDRESS"] = a.address
	}
	return headers
}

func (a *HMACAuth) sign(message string) string {
	mac := hmac.New(sha256.New, a.secretKey)
	mac.Write([]byte(message))
	// Polymarket expects URL-safe base64 encoded signatures
	return base64.URLEncoding.EncodeToString(mac.Sum(nil))
}

func indexOf(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

// BuildL2HeaderValue builds the header value for L2 auth (used in order placement).
func BuildL2HeaderValue(apiKey string, nonce int64, signature string) string {
	return fmt.Sprintf("%s:%d:%s", apiKey, nonce, signature)
}
