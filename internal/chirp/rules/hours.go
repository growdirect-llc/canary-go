package rules

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/ruptiv/canary/internal/chirp"
)

// AfterHoursParams binds the rule_definition.parameters block.
//
//	{ "rule_type": "after_hours_transaction",
//	 "parameters": { "tolerance_minutes": 15 } }
//
// tolerance_minutes pads the operating-hours window before flagging,
// so a register that closes 2 minutes after posted close doesn't fire
// every night.
type AfterHoursParams struct {
	ToleranceMinutes int `json:"tolerance_minutes"`
}

// AfterHoursTransaction fires when t.transactions.started_at falls
// outside the location's configured operating_hours block.
//
// operating_hours JSONB shape (per 03_l_s_locations.sql):
//
//	{
//	 "monday": [{"open":"07:00","close":"22:00"}],
//	 "tuesday": [{"open":"07:00","close":"22:00"}],
//	 ...
//	}
//
// A day with no entry is assumed closed all day. A day with an empty
// array is also closed all day. Multiple intervals per day are
// supported (e.g., split breakfast/dinner shifts).
//
// Timezone handling: the transaction's started_at is
// converted from UTC to the location's IANA timezone (per RFC 6557 /
// tzdata, sourced from l.locations.timezone) before comparing against
// the operating_hours values. operating_hours are interpreted in the
// store's local time — that's how merchants think about "we open at
// 7am." Locations with a missing or invalid timezone fall back to UTC
// comparison and emit a detection — false positives are preferred to
// silent skips when the store config is broken.
type AfterHoursTransaction struct{}

func (AfterHoursTransaction) RuleType() string { return "after_hours_transaction" }

type opInterval struct {
	Open  string `json:"open"`
	Close string `json:"close"`
}

func (AfterHoursTransaction) Evaluate(_ context.Context, rule *chirp.Rule, ec *chirp.EvalContext) ([]chirp.MatchedDetection, error) {
	var p AfterHoursParams
	if err := chirp.Params(rule, &p); err != nil {
		return nil, err
	}

	if len(ec.LocationOperatingHours) == 0 {
		// No operating hours configured — can't decide. Skip silently.
		return nil, nil
	}

	var hoursByDay map[string][]opInterval
	if err := json.Unmarshal(ec.LocationOperatingHours, &hoursByDay); err != nil {
		// Malformed JSON in the config column — log via the engine, not fatal.
		return nil, nil
	}

	// Convert UTC → location-local using IANA tz id. Fallback to UTC if
	// timezone is missing/invalid; the after-hours rule is conservative
	// (would rather flag a false positive on a misconfigured store than
	// silently swallow a real after-hours signal).
	loc := time.UTC
	if ec.LocationTimezone != "" {
		if z, err := time.LoadLocation(ec.LocationTimezone); err == nil {
			loc = z
		}
	}
	t := ec.Transaction.StartedAt.In(loc)
	dayKey := strings.ToLower(t.Weekday().String())
	intervals, ok := hoursByDay[dayKey]
	if !ok || len(intervals) == 0 {
		// Closed all day → after-hours by definition.
		return matchedAfterHours(rule, ec, "closed_all_day", dayKey, p.ToleranceMinutes), nil
	}

	tolerance := time.Duration(p.ToleranceMinutes) * time.Minute
	for _, iv := range intervals {
		open, ok1 := parseClock(iv.Open, t, loc)
		close, ok2 := parseClock(iv.Close, t, loc)
		if !ok1 || !ok2 {
			continue
		}
		// Allow a tolerance pad on both ends.
		if !t.Before(open.Add(-tolerance)) && !t.After(close.Add(tolerance)) {
			return nil, nil
		}
	}
	return matchedAfterHours(rule, ec, "outside_intervals", dayKey, p.ToleranceMinutes), nil
}

// parseClock turns "HH:MM" into a time.Time on the same calendar day
// as ref, in the supplied location. ref's calendar day is used (which
// is the location-local calendar day after ref = startedAt.In(loc)).
func parseClock(s string, ref time.Time, loc *time.Location) (time.Time, bool) {
	parsed, err := time.Parse("15:04", s)
	if err != nil {
		return time.Time{}, false
	}
	return time.Date(ref.Year(), ref.Month(), ref.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc), true
}

func matchedAfterHours(rule *chirp.Rule, ec *chirp.EvalContext, reason, dayKey string, tol int) []chirp.MatchedDetection {
	evidence, _ := json.Marshal(map[string]any{
		"reason":           reason,
		"weekday":          dayKey,
		"started_at":       ec.Transaction.StartedAt,
		"tolerance_minutes": tol,
	})
	signal := "0.7000"
	return []chirp.MatchedDetection{{
		Severity:       rule.Severity,
		SignalStrength: &signal,
		Evidence:       evidence,
	}}
}
