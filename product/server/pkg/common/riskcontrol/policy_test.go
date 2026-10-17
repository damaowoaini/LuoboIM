package riskcontrol

import (
	"testing"
	"time"
)

func TestRiskPolicyStrictDecisionPreservesValidAndRejectsMalformedConfiguration(t *testing.T) {
	// Existing callers of these public helpers keep their old permissive API.
	if ParseBool("invalid") || InDailyWindow("invalid", "12:00", time.Now()) {
		t.Fatal("legacy helper behavior changed")
	}
	for _, tc := range []struct {
		name, enabled, action, start, end, clock string
		blocked, invalid                         bool
	}{
		{name: "missing-switches"},
		{name: "missing-action", enabled: "true"},
		{name: "missing-master", action: "true"},
		{name: "disabled-no-schedule", enabled: "off", action: "true"},
		{name: "action-disabled-no-schedule", enabled: "true", action: "NO"},
		{name: "inside", enabled: " TRUE ", action: "yes", start: "09:00", end: "17:00", clock: "12:00", blocked: true},
		{name: "end-exclusive", enabled: "1", action: "on", start: "09:00", end: "17:00", clock: "17:00"},
		{name: "overnight-late", enabled: "true", action: "true", start: "22:00", end: "06:00", clock: "23:30", blocked: true},
		{name: "overnight-early", enabled: "true", action: "true", start: "22:00", end: "06:00", clock: "05:30", blocked: true},
		{name: "overnight-outside", enabled: "true", action: "true", start: "22:00", end: "06:00", clock: "12:00"},
		{name: "equal-is-full-day", enabled: "true", action: "true", start: "08:00", end: "08:00", clock: "03:00", blocked: true},
		{name: "invalid-master", enabled: "tru", action: "true", invalid: true},
		{name: "invalid-action-despite-disabled-master", enabled: "false", action: "unknown", invalid: true},
		{name: "nonempty-whitespace-is-invalid", enabled: " ", invalid: true},
		{name: "missing-start", enabled: "true", action: "true", end: "12:00", invalid: true},
		{name: "missing-end", enabled: "true", action: "true", start: "09:00", invalid: true},
		{name: "out-of-range-time", enabled: "true", action: "true", start: "24:00", end: "12:00", invalid: true},
		{name: "malformed-time", enabled: "true", action: "true", start: "09:00", end: "no-clock", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
			if tc.clock != "" {
				clock, err := time.Parse("15:04", tc.clock)
				if err != nil {
					t.Fatal("invalid synthetic test clock")
				}
				now = time.Date(2026, 10, 17, clock.Hour(), clock.Minute(), 0, 0, time.UTC)
			}
			conf := map[string]string{}
			for key, value := range map[string]string{RiskControlEnabledKey: tc.enabled, RiskDisableGroupActionKey: tc.action, RiskControlStartKey: tc.start, RiskControlEndKey: tc.end} {
				if value != "" {
					conf[key] = value
				}
			}
			blocked, err := evaluateActionPolicy(conf, RiskDisableGroupActionKey, now)
			if blocked != tc.blocked || (err != nil) != tc.invalid || err != nil && err != errRiskPolicyInvalid {
				t.Fatal("strict policy result did not match expected safety/compatibility outcome")
			}
		})
	}
}
