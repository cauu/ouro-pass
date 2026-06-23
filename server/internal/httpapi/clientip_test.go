package httpapi

import (
	"net/http/httptest"
	"testing"
)

// TestClientIP_TrustedProxyResolution covers the spoofable-IP control (p14-4,
// TC-18): X-Forwarded-For is honored ONLY when a trusted proxy is configured;
// otherwise the transport RemoteAddr host is used.
func TestClientIP_TrustedProxyResolution(t *testing.T) {
	cases := []struct {
		name    string
		trusted bool
		xff     string
		remote  string
		want    string
	}{
		{"untrusted ignores XFF", false, "1.2.3.4", "10.0.0.9:5555", "10.0.0.9"},
		{"untrusted no XFF", false, "", "10.0.0.9:5555", "10.0.0.9"},
		{"trusted takes rightmost hop", true, "1.2.3.4, 10.0.0.1", "10.0.0.9:5555", "10.0.0.1"},
		{"trusted no XFF falls back", true, "", "10.0.0.9:5555", "10.0.0.9"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := &apiHandlers{d: Deps{TrustedProxy: tc.trusted}}
			req := httptest.NewRequest("GET", "/", nil)
			req.RemoteAddr = tc.remote
			if tc.xff != "" {
				req.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := h.clientIP(req); got != tc.want {
				t.Errorf("clientIP = %q, want %q", got, tc.want)
			}
		})
	}
}
