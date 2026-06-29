package chain

import "testing"

// TestNewKoiosSourceTrimsBaseURL covers S0014 p2-1 / TC-5: a trailing slash or
// surrounding whitespace must not survive into the base URL (it would produce
// "…/v1//account_info", which koios 404s as "Query not found").
func TestNewKoiosSourceTrimsBaseURL(t *testing.T) {
	cases := map[string]string{
		"https://api.koios.rest/api/v1":    "https://api.koios.rest/api/v1",
		"https://api.koios.rest/api/v1/":   "https://api.koios.rest/api/v1",
		"https://api.koios.rest/api/v1///": "https://api.koios.rest/api/v1",
		"  https://api.koios.rest/api/v1 ": "https://api.koios.rest/api/v1",
		"":                                 "https://api.koios.rest/api/v1", // empty → mainnet default
	}
	for in, want := range cases {
		if got := NewKoiosSource(in, "", "mainnet").baseURL; got != want {
			t.Errorf("NewKoiosSource(%q).baseURL = %q, want %q", in, got, want)
		}
	}
}
