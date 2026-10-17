package config

import "testing"

func TestCheckSecretStrength(t *testing.T) {
	for _, v := range []string{"", "CHANGE_ME", "openIM123", "short-secret"} {
		if checkSecretStrength("secret", v) == nil {
			t.Fatalf("%q should be rejected", v)
		}
	}
	if err := checkSecretStrength("secret", "pF3k9Qz7Lw2Xn8Rt5Vb1"); err != nil {
		t.Fatalf("strong secret rejected: %v", err)
	}
}
