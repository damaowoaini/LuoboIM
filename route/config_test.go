package main

import (
	"strings"
	"testing"
)

func TestConfigNeedsExplicitSecretsAndValidOrigins(t *testing.T) {
	t.Setenv("ROUTE_ADMIN_TOKEN", "")
	t.Setenv("ROUTE_MYSQL_PASSWORD", "")
	if _, err := loadConfig(); err == nil {
		t.Fatal("empty credentials accepted")
	}
	t.Setenv("ROUTE_ADMIN_TOKEN", strings.Repeat("a", 40))
	t.Setenv("ROUTE_MYSQL_PASSWORD", "test-only")
	t.Setenv("ROUTE_ALLOWED_ORIGINS", "https://app.example")
	if _, err := loadConfig(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ROUTE_ALLOWED_ORIGINS", "*")
	if _, err := loadConfig(); err == nil {
		t.Fatal("wildcard credential origin accepted")
	}
}

func TestConfigListenHostPreservesLegacyDefaultAndAcceptsOnlyExplicitIP(t *testing.T) {
	t.Setenv("ROUTE_ADMIN_TOKEN", strings.Repeat("t", 40))
	t.Setenv("ROUTE_MYSQL_PASSWORD", "isolated-test-only")
	t.Setenv("ROUTE_ALLOWED_ORIGINS", "https://app.example")
	t.Setenv("ROUTE_PORT", "10010")
	for _, tc := range []struct {
		host, address string
		valid         bool
	}{
		{"", ":10010", true}, {"127.0.0.1", "127.0.0.1:10010", true}, {"::1", "[::1]:10010", true},
		{"localhost", "localhost:10010", true}, {"0.0.0.0", "0.0.0.0:10010", true},
		{"https://127.0.0.1", "", false}, {"untrusted.example", "", false}, {"127.0.0.1:10010", "", false},
		{"127.0.0.1/8", "", false}, {" 127.0.0.1", "", false},
	} {
		t.Run(tc.host, func(t *testing.T) {
			t.Setenv("ROUTE_LISTEN_HOST", tc.host)
			config, err := loadConfig()
			if !tc.valid {
				if err == nil {
					t.Fatal("invalid listen host accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if config.Address != tc.address {
				t.Fatalf("address = %q, want %q", config.Address, tc.address)
			}
		})
	}
}
