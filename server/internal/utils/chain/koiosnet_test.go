package chain

import "testing"

// TestDefaultKoiosBaseURL covers S0014 p1-1 / TC-1: each network resolves to its own public
// koios endpoint; empty/unknown defaults to mainnet.
func TestDefaultKoiosBaseURL(t *testing.T) {
	cases := map[string]string{
		"mainnet": "https://api.koios.rest/api/v1",
		"preprod": "https://preprod.koios.rest/api/v1",
		"preview": "https://preview.koios.rest/api/v1",
		"":        "https://api.koios.rest/api/v1",
		"moon":    "https://api.koios.rest/api/v1",
	}
	for net, want := range cases {
		if got := DefaultKoiosBaseURL(net); got != want {
			t.Errorf("DefaultKoiosBaseURL(%q) = %q, want %q", net, got, want)
		}
	}
}
