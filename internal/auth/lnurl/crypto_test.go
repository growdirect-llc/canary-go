package lnurl_test

import (
	"crypto/rand"
	"encoding/hex"
	"testing"

	// secp256k1 — https://github.com/btcsuite/btcd/tree/master/btcec/v2
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"

	"github.com/growdirect-llc/rapidpos/internal/auth/lnurl"
)

func TestVerifySignature(t *testing.T) {
	// Generate a fresh secp256k1 key pair.
	privKey, err := btcec.NewPrivateKey()
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	pubKey := privKey.PubKey()

	// Generate a random 32-byte k1 challenge.
	k1raw := make([]byte, 32)
	if _, err := rand.Read(k1raw); err != nil {
		t.Fatalf("rand k1: %v", err)
	}
	k1hex := hex.EncodeToString(k1raw)

	// Sign k1 with the private key.
	sig := ecdsa.Sign(privKey, k1raw)
	sigHex := hex.EncodeToString(sig.Serialize())

	// Compressed public key hex.
	keyHex := hex.EncodeToString(pubKey.SerializeCompressed())

	// Valid signature must verify.
	ok, err := lnurl.VerifySignature(k1hex, sigHex, keyHex)
	if err != nil {
		t.Fatalf("VerifySignature error on valid sig: %v", err)
	}
	if !ok {
		t.Error("VerifySignature returned false for a valid signature")
	}

	// Mutate the signature (flip one byte) — must return false, not an error.
	sigBytes := sig.Serialize()
	sigBytes[len(sigBytes)-1] ^= 0xff
	badSigHex := hex.EncodeToString(sigBytes)

	ok, err = lnurl.VerifySignature(k1hex, badSigHex, keyHex)
	// A mutated DER signature may fail to parse (err) or verify as false.
	// Both are acceptable; what is NOT acceptable is (true, nil).
	if ok {
		t.Error("VerifySignature returned true for a mutated signature")
	}
	_ = err // parse error on malformed DER is acceptable
}
