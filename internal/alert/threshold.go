// internal/alert/threshold.go
//
// Threshold evaluation engine. Per-rule thresholds are stored in
// q.detection_rules.attributes as:
//
//	{"threshold_count": 3, "threshold_window_minutes": 60, "severity_overrides": {"critical": 10}}
//
// EvaluateThreshold is called by the Chirp evaluator before firing a
// detection; it returns whether the detection count in the window
// exceeds the threshold and what severity to assign.
//
// Spec: GRO-766 Phase A.2.

package alert

import (
	"encoding/json"
	"math"
)

// ThresholdConfig is the parsed shape of detection_rules.attributes
// threshold fields. Missing fields use the defaults below.
type ThresholdConfig struct {
	// ThresholdCount is the minimum number of detections in the window
	// required to fire. Default: 1 (any detection fires).
	ThresholdCount int `json:"threshold_count"`

	// ThresholdWindowMinutes is the rolling window size in minutes.
	// Default: 60.
	ThresholdWindowMinutes int `json:"threshold_window_minutes"`

	// SeverityOverrides maps count thresholds to severity escalations.
	// e.g. {"high": 5, "critical": 10} means count >=5 → high,
	// count >=10 → critical. Baselines from detection_rules.severity.
	SeverityOverrides map[string]int `json:"severity_overrides,omitempty"`
}

// DefaultThresholdConfig returns safe defaults used when the rule has
// no threshold config or the config is partial.
func DefaultThresholdConfig() ThresholdConfig {
	return ThresholdConfig{
		ThresholdCount:         1,
		ThresholdWindowMinutes: 60,
	}
}

// ParseThresholdConfig parses the JSONB attributes blob from a
// detection_rule row. Missing fields are replaced with defaults.
func ParseThresholdConfig(raw []byte) ThresholdConfig {
	cfg := DefaultThresholdConfig()
	if len(raw) == 0 {
		return cfg
	}
	// Unmarshal into a map so we can apply partial overrides only.
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return cfg
	}
	if v, ok := m["threshold_count"]; ok {
		var n int
		if json.Unmarshal(v, &n) == nil && n > 0 {
			cfg.ThresholdCount = n
		}
	}
	if v, ok := m["threshold_window_minutes"]; ok {
		var n int
		if json.Unmarshal(v, &n) == nil && n > 0 {
			cfg.ThresholdWindowMinutes = n
		}
	}
	if v, ok := m["severity_overrides"]; ok {
		var overrides map[string]int
		if json.Unmarshal(v, &overrides) == nil {
			cfg.SeverityOverrides = overrides
		}
	}
	return cfg
}

// EvaluateThreshold decides whether to fire a detection and at what
// severity. count is the number of matching detections observed in
// cfg.ThresholdWindowMinutes. baseSeverity is from detection_rules.severity.
//
// Returns (shouldFire, severity). shouldFire=false means discard the
// detection (below threshold). shouldFire=true means create the detection
// row with the returned severity.
func EvaluateThreshold(cfg ThresholdConfig, count int, baseSeverity string) (shouldFire bool, severity string) {
	if count < cfg.ThresholdCount {
		return false, ""
	}
	severity = baseSeverity
	// Apply severity overrides: the highest threshold that count meets wins.
	best := 0
	bestSev := baseSeverity
	for sev, thresh := range cfg.SeverityOverrides {
		if count >= thresh && thresh > best {
			best = thresh
			bestSev = sev
		}
	}
	if best > 0 {
		severity = bestSev
	}
	return true, severity
}

// SeverityRank maps string severity to an ordinal so callers can
// compare / pick the higher of two severities. Unknown values → 0.
func SeverityRank(s string) int {
	switch s {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	case "critical":
		return 4
	}
	return 0
}

// MaxSeverity returns whichever of a, b ranks higher.
func MaxSeverity(a, b string) string {
	if SeverityRank(a) >= SeverityRank(b) {
		return a
	}
	return b
}

// SignalToSeverity converts a 0–1 signal strength to a severity bucket.
// Used when a rule does not supply an explicit severity.
func SignalToSeverity(signal float64) string {
	switch {
	case signal >= 0.9:
		return "critical"
	case signal >= 0.7:
		return "high"
	case signal >= 0.4:
		return "medium"
	default:
		return "low"
	}
}

// clamp is a small float util used in tests.
func clamp(v, lo, hi float64) float64 {
	return math.Min(hi, math.Max(lo, v))
}
