package riskcontrol

import (
	"errors"
	"strings"
	"time"
)

// Static error only: configuration values are never part of this error.
var errRiskPolicyInvalid = errors.New("risk control policy is invalid")

func policyFlag(raw string) (bool, error) {
	if raw == "" {
		return false, nil // Absent/empty switches keep the legacy disabled default.
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, nil
	case "0", "false", "no", "off":
		return false, nil
	default:
		return false, errRiskPolicyInvalid
	}
}

// evaluateActionPolicy is the strict decision boundary, separate from the
// legacy permissive ParseBool/InDailyWindow helpers. Invalid explicit switches
// fail even when another switch is disabled. Clock fields are required only
// for an enabled action; an intentionally disabled policy needs no schedule.
func evaluateActionPolicy(conf map[string]string, actionKey string, now time.Time) (bool, error) {
	enabled, err := policyFlag(conf[RiskControlEnabledKey])
	if err != nil {
		return false, err
	}
	action, err := policyFlag(conf[actionKey])
	if err != nil {
		return false, err
	}
	if !enabled || !action {
		return false, nil
	}
	for _, key := range []string{RiskControlStartKey, RiskControlEndKey} {
		if _, err := time.Parse("15:04", strings.TrimSpace(conf[key])); err != nil {
			return false, errRiskPolicyInvalid
		}
	}
	return InDailyWindow(conf[RiskControlStartKey], conf[RiskControlEndKey], now), nil
}
