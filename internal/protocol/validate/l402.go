package validate

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/google/uuid"
)

// StubL402 implements L402 challenge/response without a real Lightning node.
// In production Phase 2, replace with a real LND/CLN client.
//
// The macaroon is HMAC-SHA256(secret, tokenID) hex-encoded.
// The invoice is a signet/regtest stub that signals stub mode to callers.
type StubL402 struct {
	Secret []byte // 32-byte HMAC signing key from env
}

// IssueChallenge returns a stub macaroon and a stub invoice for the given
// token. Neither is a real Lightning artifact — callers in stub mode
// skip actual payment and call the consume endpoint directly.
func (s *StubL402) IssueChallenge(tokenID uuid.UUID, satoshis int64) (macaroon, invoice string) {
	macaroon = s.sign(tokenID)
	// Stub invoice follows lnbcrt (regtest) convention with a recognisable
	// suffix so callers can detect stub mode without an out-of-band flag.
	invoice = fmt.Sprintf("lnbcrt%dn1stub%s", satoshis, tokenID.String()[:8])
	return macaroon, invoice
}

// VerifyMacaroon returns true iff the presented macaroon matches the HMAC
// for tokenID. Uses constant-time comparison to prevent timing attacks.
func (s *StubL402) VerifyMacaroon(tokenID uuid.UUID, macaroon string) bool {
	expected := s.sign(tokenID)
	// hex.DecodeString both; fall back to byte-level compare on error.
	expectedBytes, err1 := hex.DecodeString(expected)
	providedBytes, err2 := hex.DecodeString(macaroon)
	if err1 != nil || err2 != nil {
		return false
	}
	return hmac.Equal(expectedBytes, providedBytes)
}

func (s *StubL402) sign(tokenID uuid.UUID) string {
	mac := hmac.New(sha256.New, s.Secret)
	mac.Write([]byte(tokenID.String()))
	return hex.EncodeToString(mac.Sum(nil))
}
