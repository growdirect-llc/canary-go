// internal/auth/jwt.go
package auth

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	MerchantID uuid.UUID `json:"merchant_id"`
	UserID     uuid.UUID `json:"user_id"`
	Roles      []string  `json:"roles"`
	jwt.RegisteredClaims
}

// SignToken creates a signed JWT for the given principal.
func SignToken(secret string, merchantID, userID uuid.UUID, roles []string, ttl time.Duration) (string, error) {
	claims := Claims{
		MerchantID: merchantID,
		UserID:     userID,
		Roles:      roles,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// VerifyToken parses and validates a JWT. Returns Claims on success.
func VerifyToken(secret, tokenStr string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	return claims, nil
}

// TokenHash returns SHA-256 hex of a token string — used as Valkey key suffix.
func TokenHash(token string) string {
	h := sha256.Sum256([]byte(token))
	return fmt.Sprintf("%x", h)
}
