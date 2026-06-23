package config

import (
	"testing"
)

// withEnv sets env vars for one test and restores them after.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		t.Setenv(k, v)
	}
}

func TestLoad_DefaultsAndRequired(t *testing.T) {
	// A minimal valid config: only POOL_ID is required beyond defaults.
	withEnv(t, map[string]string{"OUROPASS_POOL_ID": "pool1abc"})
	c, err := Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if c.Addr != defaultAddr || c.Network != defaultNetwork || c.DBDriver != defaultDBDriver {
		t.Errorf("defaults not applied: %+v", c)
	}
	if c.Issuer != "ouropass:pool1abc" {
		t.Errorf("issuer = %q, want ouropass:pool1abc", c.Issuer)
	}
	if c.TrustedProxy || !c.TLS { // TrustedProxy defaults false, TLS defaults true
		t.Errorf("edge defaults: TrustedProxy=%v TLS=%v", c.TrustedProxy, c.TLS)
	}
	if c.ChainKind != "mock" {
		t.Errorf("chain kind default = %q", c.ChainKind)
	}
}

func TestLoad_Validation(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		ok   bool
	}{
		{"missing pool id", map[string]string{}, false},
		{"bad network", map[string]string{"OUROPASS_POOL_ID": "p", "OUROPASS_NETWORK": "moon"}, false},
		{"bad driver", map[string]string{"OUROPASS_POOL_ID": "p", "OUROPASS_DB_DRIVER": "mysql"}, false},
		{"empty dsn", map[string]string{"OUROPASS_POOL_ID": "p", "OUROPASS_DB_DSN": "   "}, false},
		{"bad shutdown duration", map[string]string{"OUROPASS_POOL_ID": "p", "OUROPASS_SHUTDOWN_TIMEOUT": "nope"}, false},
		{"valid mainnet pg", map[string]string{"OUROPASS_POOL_ID": "p", "OUROPASS_NETWORK": "mainnet", "OUROPASS_DB_DRIVER": "postgres", "OUROPASS_DB_DSN": "postgres://x"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withEnv(t, tc.env)
			_, err := Load()
			if tc.ok && err != nil {
				t.Errorf("want ok, got %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("want error, got nil")
			}
		})
	}
}

func TestEnvBoolAndSplitCSV(t *testing.T) {
	for _, tc := range []struct {
		in  string
		def bool
		out bool
	}{{"1", false, true}, {"true", false, true}, {"YES", false, true}, {"off", true, false}, {"0", true, false}, {"garbage", true, true}, {"", false, false}} {
		t.Setenv("OUROPASS_X", tc.in)
		if got := envBool("OUROPASS_X", tc.def); got != tc.out {
			t.Errorf("envBool(%q, %v) = %v, want %v", tc.in, tc.def, got, tc.out)
		}
	}
	if got := splitCSV(" a , ,b,c "); len(got) != 3 || got[0] != "a" || got[2] != "c" {
		t.Errorf("splitCSV = %v", got)
	}
	if splitCSV("   ") != nil {
		t.Error("splitCSV blank should be nil")
	}
}
