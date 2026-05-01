package polymarket

import (
	"testing"
)

func TestHMACAuth_Headers(t *testing.T) {
	auth := NewHMACAuth("test-key", "dGVzdC1zZWNyZXQ=", "test-passphrase")
	headers := auth.Headers("GET", "/book?token_id=123", "")

	if headers["POLY_API_KEY"] != "test-key" {
		t.Fatalf("expected api key 'test-key', got '%s'", headers["POLY_API_KEY"])
	}
	if headers["POLY_PASSPHRASE"] != "test-passphrase" {
		t.Fatalf("expected passphrase 'test-passphrase', got '%s'", headers["POLY_PASSPHRASE"])
	}
	if headers["POLY_TIMESTAMP"] == "" {
		t.Fatal("timestamp should not be empty")
	}
	if headers["POLY_SIGNATURE"] == "" {
		t.Fatal("signature should not be empty")
	}
}

func TestHMACAuth_DifferentMethodsProduceDifferentSignatures(t *testing.T) {
	auth := NewHMACAuth("key", "c2VjcmV0", "pass")
	get := auth.Headers("GET", "/path", "")
	post := auth.Headers("POST", "/path", `{"foo":"bar"}`)

	if get["POLY_SIGNATURE"] == post["POLY_SIGNATURE"] {
		t.Fatal("GET and POST with different bodies should produce different signatures")
	}
}

func TestHMACAuth_DifferentPathsProduceDifferentSignatures(t *testing.T) {
	auth := NewHMACAuth("key", "c2VjcmV0", "pass")
	h1 := auth.Headers("GET", "/path1", "")
	h2 := auth.Headers("GET", "/path2", "")

	if h1["POLY_SIGNATURE"] == h2["POLY_SIGNATURE"] {
		t.Fatal("different paths should produce different signatures")
	}
}

func TestHMACAuth_ConsistentSignature(t *testing.T) {
	auth := NewHMACAuth("key", "c2VjcmV0", "pass")
	// Same inputs within the same second should produce the same signature.
	h1 := auth.Headers("GET", "/path", "")
	h2 := auth.Headers("GET", "/path", "")

	if h1["POLY_TIMESTAMP"] == h2["POLY_TIMESTAMP"] {
		// Only check signature equality if timestamps match.
		if h1["POLY_SIGNATURE"] != h2["POLY_SIGNATURE"] {
			t.Fatal("same inputs and timestamp should produce the same signature")
		}
	}
}

func TestHMACAuth_RawSecret(t *testing.T) {
	// Non-base64 secret should still work (uses raw bytes).
	auth := NewHMACAuth("key", "not-base64!!!", "pass")
	headers := auth.Headers("GET", "/path", "")
	if headers["POLY_SIGNATURE"] == "" {
		t.Fatal("signature should not be empty even with raw secret")
	}
}

func TestBuildL2HeaderValue(t *testing.T) {
	val := BuildL2HeaderValue("my-key", 12345, "my-sig")
	expected := "my-key:12345:my-sig"
	if val != expected {
		t.Fatalf("expected '%s', got '%s'", expected, val)
	}
}
