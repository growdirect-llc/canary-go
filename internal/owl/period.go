package owl

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ParsePeriod resolves a (period kind, optional from/to, timezone) into
// a concrete UTC [from, to) window.
//
// Inputs come straight off url.Values. Timezone is a Postgres-loaded
// IANA string (e.g., America/Los_Angeles) — defaults to UTC if the
// merchant has no app.merchant_settings row.
//
// Conventions:
//   - "today" → midnight local TZ → now (UTC).
//   - "wtd"   → Monday 00:00 local TZ → now. ISO-8601 week (Mon start).
//   - "mtd"   → 1st of month 00:00 local TZ → now.
//   - "range" → from + to required, ISO-8601 (RFC3339). Both UTC after parse.
//
// Now is injected so tests can pin time without monkeypatching.
func ParsePeriod(q url.Values, tz string, now time.Time) (Period, error) {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return Period{}, fmt.Errorf("owl: invalid timezone %q: %w", tz, err)
	}

	kind := PeriodKind(strings.ToLower(strings.TrimSpace(q.Get("period"))))
	if kind == "" {
		kind = PeriodToday
	}

	nowUTC := now.UTC()
	nowLocal := nowUTC.In(loc)

	switch kind {
	case PeriodToday:
		startLocal := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
		return Period{Kind: kind, From: startLocal.UTC(), To: nowUTC, Timezone: tz}, nil

	case PeriodWTD:
		// Monday-anchored week. Go's Weekday() returns Sunday=0..Saturday=6.
		// Map to Monday=0..Sunday=6.
		offset := (int(nowLocal.Weekday()) + 6) % 7
		startLocal := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day()-offset, 0, 0, 0, 0, loc)
		return Period{Kind: kind, From: startLocal.UTC(), To: nowUTC, Timezone: tz}, nil

	case PeriodMTD:
		startLocal := time.Date(nowLocal.Year(), nowLocal.Month(), 1, 0, 0, 0, 0, loc)
		return Period{Kind: kind, From: startLocal.UTC(), To: nowUTC, Timezone: tz}, nil

	case PeriodCustom:
		fromStr := strings.TrimSpace(q.Get("from"))
		toStr := strings.TrimSpace(q.Get("to"))
		if fromStr == "" || toStr == "" {
			return Period{}, errors.New("owl: period=range requires from and to (RFC3339)")
		}
		from, err := time.Parse(time.RFC3339, fromStr)
		if err != nil {
			return Period{}, fmt.Errorf("owl: parse from: %w", err)
		}
		to, err := time.Parse(time.RFC3339, toStr)
		if err != nil {
			return Period{}, fmt.Errorf("owl: parse to: %w", err)
		}
		if !from.Before(to) {
			return Period{}, errors.New("owl: from must be strictly before to")
		}
		return Period{Kind: kind, From: from.UTC(), To: to.UTC(), Timezone: tz}, nil

	default:
		return Period{}, fmt.Errorf("owl: unknown period %q (want today|wtd|mtd|range)", kind)
	}
}
