// internal/auth/jwt_test.go
package auth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/growdirect-llc/rapidpos/internal/auth"
)

func TestSignAndVerifyClaims(t *testing.T) {
	secret := "test-secret-at-least-32-bytes-long!!"
	merchantID := uuid.New()
	userID := uuid.New()
	roles := []string{"owner", "manager"}

	token, err := auth.SignToken(secret, merchantID, userID, roles, 8*time.Hour)
	if err != nil {
		t.Fatalf("SignToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	claims, err := auth.VerifyToken(secret, token)
	if err != nil {
		t.Fatalf("VerifyToken: %v", err)
	}
	if claims.MerchantID != merchantID {
		t.Errorf("MerchantID: got %v want %v", claims.MerchantID, merchantID)
	}
	if claims.UserID != userID {
		t.Errorf("UserID: got %v want %v", claims.UserID, userID)
	}
	if len(claims.Roles) != 2 {
		t.Errorf("Roles: got %v want %v", claims.Roles, roles)
	}
}

func TestExpiredToken(t *testing.T) {
	secret := "test-secret-at-least-32-bytes-long!!"
	token, _ := auth.SignToken(secret, uuid.New(), uuid.New(), []string{"owner"}, -1*time.Second)

	_, err := auth.VerifyToken(secret, token)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestInvalidSignature(t *testing.T) {
	token, _ := auth.SignToken("secret-a-at-least-32-bytes-long!!", uuid.New(), uuid.New(), nil, time.Hour)
	_, err := auth.VerifyToken("secret-b-at-least-32-bytes-long!!", token)
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}
