package membership

import "testing"

func TestTierFor(t *testing.T) {
	rules := []byte(`[
		{"tier":"gold","min_state":"active","min_active_stake":"1000000"},
		{"tier":"silver","min_state":"active","min_active_stake":"100000"},
		{"tier":"basic","min_state":"active"},
		{"tier":"prospect","min_state":"pending"}
	]`)

	cases := []struct {
		name  string
		state State
		stake string
		want  string
	}{
		{"active above gold threshold → gold", StateActive, "5000000", "gold"},
		{"active mid → silver (first match wins, top-down)", StateActive, "500000", "silver"},
		{"active below all stake thresholds → basic", StateActive, "1", "basic"},
		{"exact threshold qualifies (>=) → gold", StateActive, "1000000", "gold"},
		{"pending → prospect (active-min rules skipped)", StatePending, "9999999", "prospect"},
		{"none → no tier", StateNone, "9999999", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := TierFor(c.state, c.stake, rules); got != c.want {
				t.Fatalf("TierFor(%s,%s) = %q, want %q", c.state, c.stake, got, c.want)
			}
		})
	}

	// Empty / invalid rules → no opinion.
	if got := TierFor(StateActive, "9999999", nil); got != "" {
		t.Fatalf("empty rules: %q", got)
	}
	if got := TierFor(StateActive, "9999999", []byte("not json")); got != "" {
		t.Fatalf("invalid rules: %q", got)
	}
	// Big lovelace beyond int64 compares correctly (C4).
	big := []byte(`[{"tier":"whale","min_state":"active","min_active_stake":"45000000000000000"}]`)
	if got := TierFor(StateActive, "45000000000000001", big); got != "whale" {
		t.Fatalf("big stake: %q, want whale", got)
	}
	if got := TierFor(StateActive, "44999999999999999", big); got != "" {
		t.Fatalf("big stake below: %q, want empty", got)
	}
}
