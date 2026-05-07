package lnurl

import (
	"encoding/hex"
	"fmt"

	// secp256k1 ECDSA — https://github.com/btcsuite/btcd/tree/master/btcec/v2
	"github.com/btcsuite/btcd/btcec/v2"
	"github.com/btcsuite/btcd/btcec/v2/ecdsa"
)

// VerifySignature verifies a secp256k1 ECDSA signature per the LNURL-auth spec.
//
//   - k1hex:  32-byte challenge, hex-encoded (64 chars)
//   - sighex: DER-encoded signature, hex-encoded
//   - keyhex: 33-byte compressed public key, hex-encoded
//
// Returns (true, nil) on success. Returns (false, nil) when the signature is
// syntactically valid but does not verify. Returns (false, err) when the
// inputs cannot be decoded or parsed.
func VerifySignature(k1hex, sighex, keyhex string) (bool, error) {
	k1bytes, err := hex.DecodeString(k1hex)
	if err != nil {
		return false, fmt.Errorf("lnurl verify: decode k1: %w", err)
	}
	if len(k1bytes) != 32 {
		return false, fmt.Errorf("lnurl verify: k1 must be 32 bytes, got %d", len(k1bytes))
	}

	sigBytes, err := hex.DecodeString(sighex)
	if err != nil {
		return false, fmt.Errorf("lnurl verify: decode sig: %w", err)
	}

	keyBytes, err := hex.DecodeString(keyhex)
	if err != nil {
		return false, fmt.Errorf("lnurl verify: decode key: %w", err)
	}

	pubKey, err := btcec.ParsePubKey(keyBytes)
	if err != nil {
		return false, fmt.Errorf("lnurl verify: parse pubkey: %w", err)
	}

	sig, err := ecdsa.ParseDERSignature(sigBytes)
	if err != nil {
		return false, fmt.Errorf("lnurl verify: parse DER signature: %w", err)
	}

	return sig.Verify(k1bytes, pubKey), nil
}
