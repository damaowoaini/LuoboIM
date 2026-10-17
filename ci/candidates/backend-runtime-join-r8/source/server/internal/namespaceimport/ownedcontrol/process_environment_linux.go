//go:build linux

package ownedcontrol

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/openimsdk/open-im-server/v3/internal/namespaceinventory"
)

// This private input configures a real child; it is never an admission ticket.
// Keep the whitelist tied to actual constructor/application readers. New keys
// require a source review instead of granting an arbitrary LUOBO_* override.
type OwnedServiceEnvironment struct {
	Version string            `json:"version"`
	Role    string            `json:"role"`
	Values  map[string]string `json:"values"`
}

func ownedServiceEnvironmentKey(role, key string) bool {
	if role != "rpc-rtc" && role != "api" {
		return false
	}
	switch key {
	case "LUOBO_RTC_LIVE_URL", "LUOBO_RTC_ALLOW_LOOPBACK", "LUOBO_RTC_API_KEY", "LUOBO_RTC_API_SECRET":
		return true
	}
	return false
}
func loadOwnedServiceEnvironment(spec ServiceSpec) ([]string, error) {
	if spec.PrivateEnvironmentPath == "" && spec.PrivateEnvironmentSHA256 == "" {
		return nil, nil
	}
	if !filepath.IsAbs(spec.PrivateEnvironmentPath) || filepath.Clean(spec.PrivateEnvironmentPath) != spec.PrivateEnvironmentPath || !isSHA(spec.PrivateEnvironmentSHA256) {
		return nil, ErrOwnedEvidence
	}
	raw, e := namespaceinventory.ReadPrivateDocument(spec.PrivateEnvironmentPath, 64<<10)
	if e != nil || sha(raw) != spec.PrivateEnvironmentSHA256 {
		return nil, ErrOwnedEvidence
	}
	var v OwnedServiceEnvironment
	if namespaceinventory.DecodeStrict(raw, &v) != nil || v.Version != "owned-service-environment-v1" || v.Role != spec.Role || (!serverRole(v.Role) && v.Role != "msgtransfer-managed") || v.Values == nil || len(v.Values) > 16 {
		return nil, ErrOwnedEvidence
	}
	canonical, e := json.Marshal(v)
	if e != nil || !bytes.Equal(append(canonical, '\n'), raw) {
		return nil, ErrOwnedEvidence
	}
	var keys []string
	for key, value := range v.Values {
		if !ownedServiceEnvironmentKey(v.Role, key) || len(value) > 4096 || strings.ContainsAny(value, "\x00\r\n") {
			return nil, ErrOwnedEvidence
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+v.Values[key])
	}
	return out, nil
}
