package main

import (
	"testing"

	"github.com/evgenmay1978-del/proectmaestro-vpn/backend/internal/api"
)

func TestRQLiteAPIConfigParsesDeviceLimitKillSwitch(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "empty defaults on", raw: "", want: true},
		{name: "on stays enabled", raw: "on", want: true},
		{name: "zero is not the kill switch", raw: "0", want: true},
		{name: "off disables", raw: "off", want: false},
		{name: "off is trimmed and case insensitive", raw: "  OFF\t", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := deviceLimitEnforced(test.raw); got != test.want {
				t.Fatalf("deviceLimitEnforced(%q)=%v, want %v", test.raw, got, test.want)
			}
			t.Setenv("MAESTRO_DEVICE_LIMIT", test.raw)
			if got := rqliteAPIConfigFromEnvironment().EnforceDeviceLimit; got != test.want {
				t.Fatalf("MAESTRO_DEVICE_LIMIT=%q enabled=%v, want %v", test.raw, got, test.want)
			}
		})
	}
}

func TestRQLiteServiceBusinessConfigWiresProductionDeviceLimitPolicy(t *testing.T) {
	tests := []struct {
		name    string
		enforce bool
		login   string
		want    int
	}{
		{name: "kill switch bypasses ordinary", enforce: false, login: "customer", want: -1},
		{name: "kill switch overrides unlimited login", enforce: false, login: "wapmix", want: -1},
		{name: "ordinary default", enforce: true, login: "customer", want: 5},
		{name: "owner unlimited remains admission enabled", enforce: true, login: "WAPMIXX", want: 0},
		{name: "strogino override", enforce: true, login: "STROGINO", want: 9},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			config := rqliteServiceBusinessConfig(api.Config{EnforceDeviceLimit: test.enforce}, nil, "worker-test")
			if config.DeviceLimitFor == nil {
				t.Fatal("DeviceLimitFor resolver is nil")
			}
			if got := config.DeviceLimitFor(test.login); got != test.want {
				t.Fatalf("enabled=%v login=%q limit=%d, want %d", test.enforce, test.login, got, test.want)
			}
		})
	}
}
