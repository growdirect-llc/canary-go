package keystore

import (
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

// TestGenerateRSA_ProducesValidKey verifies GenerateRSA emits a
// SigningKey whose PrivateKeyPEM parses cleanly back into an RSA
// private key, the public JWK has the required fields, and the kid
// matches the ID.
func TestGenerateRSA_ProducesValidKey(t *testing.T) {
	sk, err := GenerateRSA()
	if err != nil {
		t.Fatalf("GenerateRSA: %v", err)
	}

	// Kid must equal the ID's string form (the rotation runbook
	// assumes this so the JWKS publishes a kid the operator can
	// trace back to a row by primary key).
	if sk.Kid != sk.ID.String() {
		t.Errorf("kid: got %q, want %q (= ID.String())", sk.Kid, sk.ID.String())
	}

	if sk.Alg != AlgRS256 {
		t.Errorf("alg: got %q, want %q", sk.Alg, AlgRS256)
	}

	if sk.Status != StatusActive {
		t.Errorf("status: got %q, want %q", sk.Status, StatusActive)
	}

	// PrivateKeyPEM round-trips through x509.
	block, _ := pem.Decode([]byte(sk.PrivateKeyPEM))
	if block == nil {
		t.Fatal("PrivateKeyPEM did not decode as PEM")
	}
	if block.Type != "RSA PRIVATE KEY" {
		t.Errorf("PEM type: got %q, want RSA PRIVATE KEY", block.Type)
	}
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKCS1PrivateKey: %v", err)
	}
	if bits := priv.N.BitLen(); bits < 2048 {
		t.Errorf("RSA modulus: got %d bits, want >= 2048", bits)
	}
}

// TestGenerateRSA_PublicJWK_HasRequiredFields verifies the JWK
// emitted matches RFC 7517 §4 minimum field set for an RSA signing
// key, plus the kid binding required by GRO-848 surface 2.
func TestGenerateRSA_PublicJWK_HasRequiredFields(t *testing.T) {
	sk, err := GenerateRSA()
	if err != nil {
		t.Fatalf("GenerateRSA: %v", err)
	}

	var jwk map[string]string
	if err := json.Unmarshal(sk.PublicJWK, &jwk); err != nil {
		t.Fatalf("PublicJWK is not valid JSON: %v", err)
	}

	for _, field := range []string{"kty", "use", "alg", "kid", "n", "e"} {
		if jwk[field] == "" {
			t.Errorf("JWK missing field %q", field)
		}
	}
	if jwk["kty"] != "RSA" {
		t.Errorf("kty: got %q, want RSA", jwk["kty"])
	}
	if jwk["use"] != "sig" {
		t.Errorf("use: got %q, want sig", jwk["use"])
	}
	if jwk["alg"] != "RS256" {
		t.Errorf("alg: got %q, want RS256", jwk["alg"])
	}
	if jwk["kid"] != sk.Kid {
		t.Errorf("kid mismatch: JWK %q vs SigningKey %q", jwk["kid"], sk.Kid)
	}
	// n is base64url-encoded modulus; for a 2048-bit key the raw
	// bytes are 256 bytes → ~342 chars in base64url. Sanity-check
	// the length is in that ballpark (no padding == RawURLEncoding).
	if len(jwk["n"]) < 300 {
		t.Errorf("n: too short (%d chars), expected ~342 for RSA-2048", len(jwk["n"]))
	}
	if strings.Contains(jwk["n"], "=") {
		t.Errorf("n contains base64 padding; should be RawURLEncoding")
	}
}

// TestGenerateRSA_DistinctKidsAcrossCalls verifies two calls produce
// different ids/kids — defense against accidentally reusing a kid in
// a rotation, which would let a stale JWKS cache verify a new token
// with an old key.
func TestGenerateRSA_DistinctKidsAcrossCalls(t *testing.T) {
	a, err := GenerateRSA()
	if err != nil {
		t.Fatalf("GenerateRSA #1: %v", err)
	}
	b, err := GenerateRSA()
	if err != nil {
		t.Fatalf("GenerateRSA #2: %v", err)
	}
	if a.Kid == b.Kid {
		t.Errorf("two calls returned same kid %q", a.Kid)
	}
	if a.ID == b.ID {
		t.Errorf("two calls returned same ID %s", a.ID)
	}
}

// TestRSAPublicJWK_StablyEncodesPublicKey verifies rsaPublicJWK
// produces deterministic output for the same key — JWKS clients
// cache by content, so the bytes have to be stable across emissions.
func TestRSAPublicJWK_StablyEncodesPublicKey(t *testing.T) {
	sk, err := GenerateRSA()
	if err != nil {
		t.Fatalf("GenerateRSA: %v", err)
	}
	block, _ := pem.Decode([]byte(sk.PrivateKeyPEM))
	priv, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParsePKCS1PrivateKey: %v", err)
	}

	pub, ok := any(&priv.PublicKey).(*rsa.PublicKey)
	if !ok {
		t.Fatal("type assertion to *rsa.PublicKey failed")
	}

	a, err := rsaPublicJWK(sk.Kid, pub)
	if err != nil {
		t.Fatalf("rsaPublicJWK #1: %v", err)
	}
	b, err := rsaPublicJWK(sk.Kid, pub)
	if err != nil {
		t.Fatalf("rsaPublicJWK #2: %v", err)
	}
	if string(a) != string(b) {
		t.Errorf("rsaPublicJWK produced different bytes for same key + kid:\na=%s\nb=%s", a, b)
	}
}
