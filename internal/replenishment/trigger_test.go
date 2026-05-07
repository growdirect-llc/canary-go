package replenishment

import (
	"testing"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestBelowMin(t *testing.T) {
	cases := []struct {
		soh, min float64
		want     bool
	}{
		{0, 1, true},
		{-1, 1, true},
		{0.5, 1, true},
		{1, 1, false},
		{2, 1, false},
	}
	for _, tc := range cases {
		got := belowMin(tc.soh, tc.min)
		if got != tc.want {
			t.Errorf("belowMin(%v,%v)=%v want %v", tc.soh, tc.min, got, tc.want)
		}
	}
}

func TestQuantityToPull(t *testing.T) {
	five := 5.0
	cases := []struct {
		soh  float64
		max  *float64
		min  float64
		want string
	}{
		{0, &five, 1, "5.0000"},  // max - soh = 5
		{2, &five, 1, "3.0000"},  // max - soh = 3
		{6, &five, 1, "1.0000"},  // max < soh → fallback to min
		{0, nil, 2, "2.0000"},    // no max → use min
	}
	for _, tc := range cases {
		got := quantityToPull(tc.soh, tc.max, tc.min)
		if got != tc.want {
			t.Errorf("quantityToPull(%v,%v,%v)=%q want %q", tc.soh, tc.max, tc.min, got, tc.want)
		}
	}
}

func TestParseReplenishMsg(t *testing.T) {
	tenantID := uuid.New()
	itemID := uuid.New()
	locationID := uuid.New()

	msg := redis.XMessage{
		ID: "1-0",
		Values: map[string]any{
			"tenant_id":   tenantID.String(),
			"item_id":     itemID.String(),
			"location_id": locationID.String(),
			"soh":         "-1.5",
			"emitted_at":  "2026-05-05T00:00:00Z",
		},
	}

	gotTenant, gotItem, gotLoc, gotSOH, err := parseReplenishMsg(msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotTenant != tenantID {
		t.Errorf("tenant mismatch")
	}
	if gotItem != itemID {
		t.Errorf("item mismatch")
	}
	if gotLoc != locationID {
		t.Errorf("location mismatch")
	}
	if gotSOH != -1.5 {
		t.Errorf("soh=%v want -1.5", gotSOH)
	}
}

func TestParseReplenishMsg_BadUUID(t *testing.T) {
	msg := redis.XMessage{
		ID:     "1-0",
		Values: map[string]any{"tenant_id": "not-a-uuid"},
	}
	_, _, _, _, err := parseReplenishMsg(msg)
	if err == nil {
		t.Error("expected error for bad UUID")
	}
}
