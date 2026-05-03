package lnurl

import (
	"fmt"
	"time"

	// golang-jwt/jwt v5 — https://github.com/golang-jwt/jwt
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Claims is the JWT payload for an LNURL-auth session.
type Claims struct {
	OwnerID    string `json:"sub"`
	LinkingKey string `json:"key"`
	jwt.RegisteredClaims
}

// IssueJWT signs a 24-hour HS256 JWT for the given owner and linking key.
// secret must be the raw bytes of LNURL_JWT_SECRET.
func IssueJWT(ownerID uuid.UUID, linkingKey string, secret []byte) (string, error) {
	now := time.Now()
	claims := Claims{
		OwnerID:    ownerID.String(),
		LinkingKey: linkingKey,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   ownerID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(secret)
	if err != nil {
		return "", fmt.Errorf("lnurl session: sign jwt: %w", err)
	}
	return signed, nil
}

// ValidateJWT parses and validates a JWT, returning the embedded Claims.
func ValidateJWT(tokenStr string, secret []byte) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("lnurl session: unexpected signing method: %v", t.Header["alg"])
		}
		return secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("lnurl session: parse jwt: %w", err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("lnurl session: invalid token")
	}
	return claims, nil
}
