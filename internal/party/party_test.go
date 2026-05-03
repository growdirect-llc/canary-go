package party

import (
	"testing"

	"github.com/google/uuid"
)

func TestHashIdentifierDeterministic(t *testing.T) {
	tenantID := uuid.New()
	a := hashIdentifier(tenantID, IdentifierTypeEmployeeID, "abc-123")
	b := hashIdentifier(tenantID, IdentifierTypeEmployeeID, "abc-123")
	if a != b {
		t.Errorf("hash not deterministic: %s vs %s", a, b)
	}
}

func TestHashIdentifierTenantScoped(t *testing.T) {
	a := hashIdentifier(uuid.New(), IdentifierTypeEmployeeID, "shared-value")
	b := hashIdentifier(uuid.New(), IdentifierTypeEmployeeID, "shared-value")
	if a == b {
		t.Errorf("hash collided across tenants: %s", a)
	}
}

func TestHashIdentifierTypeDistinct(t *testing.T) {
	tenantID := uuid.New()
	a := hashIdentifier(tenantID, IdentifierTypeEmployeeID, "shared-value")
	b := hashIdentifier(tenantID, IdentifierTypeCustomerID, "shared-value")
	if a == b {
		t.Errorf("hash collided across identifier types: %s", a)
	}
}

func TestQualityScoreFor(t *testing.T) {
	cases := map[string]string{
		IdentifierTypeEmployeeID: "0.95",
		IdentifierTypeLoyaltyID:  "0.95",
		IdentifierTypeCustomerID: "0.50",
		"random_unknown_type":    "0.30",
	}
	for typ, want := range cases {
		if got := qualityScoreFor(typ); got != want {
			t.Errorf("qualityScoreFor(%q) = %s, want %s", typ, got, want)
		}
	}
}
