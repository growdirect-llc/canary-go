package web

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestScanFlowToken_RoundTrip(t *testing.T) {
	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	state := scanFlowState{
		Barcode:       "012345678905",
		Source:        "Open Food Facts",
		Confidence:    0.85,
		PartialFields: []string{"brand"},
		Product: scanProductFields{
			Name:     "Organic Whole Milk",
			Brand:    "Clover",
			ImageURL: "https://example.test/milk.png",
		},
		Operational: scanOperationalFields{
			SKU:           "012345678905",
			UnitOfMeasure: "EA",
			Status:        "active",
		},
	}

	token, err := codec.Encode(tenantID, state)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if token == "" || strings.Count(token, ".") != 1 {
		t.Fatalf("token shape = %q, want payload.signature", token)
	}

	got, err := codec.Decode(token, tenantID)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if got.Version != scanFlowTokenVersion {
		t.Errorf("Version = %d, want %d", got.Version, scanFlowTokenVersion)
	}
	if got.Barcode != "012345678905" {
		t.Errorf("Barcode = %q", got.Barcode)
	}
	if got.Product.Name != "Organic Whole Milk" {
		t.Errorf("Product.Name = %q", got.Product.Name)
	}
	if got.ExpiresAt <= got.IssuedAt {
		t.Errorf("ExpiresAt = %d must be greater than IssuedAt = %d", got.ExpiresAt, got.IssuedAt)
	}
}

func TestScanFlowToken_RejectsWrongTenant(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	token, err := codec.Encode(uuid.MustParse("00000000-0000-0000-0000-000000000001"), scanFlowState{Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	_, err = codec.Decode(token, uuid.MustParse("00000000-0000-0000-0000-000000000002"))
	if !errors.Is(err, errScanFlowInvalid) {
		t.Fatalf("Decode err = %v, want errScanFlowInvalid", err)
	}
}

func TestScanFlowToken_RejectsExpired(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }
	codec.ttl = time.Minute

	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	token, err := codec.Encode(tenantID, scanFlowState{Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	codec.now = func() time.Time { return time.Unix(1062, 0).UTC() }
	_, err = codec.Decode(token, tenantID)
	if !errors.Is(err, errScanFlowExpired) {
		t.Fatalf("Decode err = %v, want errScanFlowExpired", err)
	}
}

func TestScanFlowToken_RejectsTamper(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	token, err := codec.Encode(tenantID, scanFlowState{Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	parts := strings.Split(token, ".")
	tampered := parts[0] + ".AAAA" + parts[1]
	_, err = codec.Decode(tampered, tenantID)
	if !errors.Is(err, errScanFlowInvalid) {
		t.Fatalf("Decode err = %v, want errScanFlowInvalid", err)
	}
}

func TestScanFlowToken_RejectsUnsupportedVersion(t *testing.T) {
	codec := newScanFlowTokenCodec([]byte("01234567890123456789012345678901"))
	codec.now = func() time.Time { return time.Unix(1000, 0).UTC() }

	tenantID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	token, err := codec.Encode(tenantID, scanFlowState{Version: 99, Barcode: "012345678905"})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	_, err = codec.Decode(token, tenantID)
	if !errors.Is(err, errScanFlowInvalid) {
		t.Fatalf("Decode err = %v, want errScanFlowInvalid", err)
	}
}
