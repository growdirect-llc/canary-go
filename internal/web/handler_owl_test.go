package web

import (
	"testing"
	"time"
)

// TestOwlPortalPeriod_Day pins a known UTC midpoint and verifies the
// helper truncates to midnight UTC.
func TestOwlPortalPeriod_Day(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)
	from, to, label := owlPortalPeriod("day", now)

	if !from.Equal(time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("day from = %v want 2026-05-06 00:00 UTC", from)
	}
	if !to.Equal(now) {
		t.Errorf("day to = %v want %v", to, now)
	}
	if label != "day" {
		t.Errorf("label = %q want %q", label, "day")
	}
}

// TestOwlPortalPeriod_Week_Wednesday — ISO week starts Monday.
func TestOwlPortalPeriod_Week_Wednesday(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC) // Wed
	from, _, _ := owlPortalPeriod("week", now)
	want := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) // prev Mon
	if !from.Equal(want) {
		t.Errorf("week from = %v want %v", from, want)
	}
}

// TestOwlPortalPeriod_Week_Sunday — Sunday rolls back to the
// preceding Monday (not forward to tomorrow's Monday).
func TestOwlPortalPeriod_Week_Sunday(t *testing.T) {
	now := time.Date(2026, 5, 10, 9, 0, 0, 0, time.UTC) // Sun
	from, _, _ := owlPortalPeriod("week", now)
	want := time.Date(2026, 5, 4, 0, 0, 0, 0, time.UTC) // prev Mon
	if !from.Equal(want) {
		t.Errorf("sun: week from = %v want %v", from, want)
	}
}

func TestOwlPortalPeriod_Month(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)
	from, _, _ := owlPortalPeriod("month", now)
	want := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	if !from.Equal(want) {
		t.Errorf("month from = %v want %v", from, want)
	}
}

// TestOwlPortalPeriod_Quarter — covers all four calendar quarters.
func TestOwlPortalPeriod_Quarter(t *testing.T) {
	cases := []struct {
		now  time.Time
		want time.Time
	}{
		{time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}, // Q1
		{time.Date(2026, 5, 6, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},  // Q2
		{time.Date(2026, 8, 30, 0, 0, 0, 0, time.UTC), time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)}, // Q3
		{time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}, // Q4
	}
	for _, c := range cases {
		from, _, _ := owlPortalPeriod("quarter", c.now)
		if !from.Equal(c.want) {
			t.Errorf("quarter(now=%v) from = %v want %v", c.now, from, c.want)
		}
	}
}

// TestOwlPortalPeriod_Default — empty + unknown both fall through to week.
func TestOwlPortalPeriod_Default(t *testing.T) {
	now := time.Date(2026, 5, 6, 14, 30, 0, 0, time.UTC)
	if _, _, label := owlPortalPeriod("", now); label != "week" {
		t.Errorf("empty kind label = %q want week", label)
	}
	if _, _, label := owlPortalPeriod("nonsense", now); label != "week" {
		t.Errorf("unknown kind label = %q want week", label)
	}
}
