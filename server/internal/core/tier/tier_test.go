package tier

import "testing"

// gold≥1M, silver≥100k (both require active stake), basic = any active.
const sampleRules = `[
	{"tier":"gold","when":{"fact":"total_active_stake","op":">=","value":"1000000"}},
	{"tier":"silver","when":{"fact":"total_active_stake","op":">=","value":"100000"}},
	{"tier":"basic","when":{"fact":"any_active","op":"==","value":"true"}}
]`

func TestEval_ThresholdsFirstMatch(t *testing.T) {
	cases := []struct {
		name  string
		facts map[string]string
		want  string
	}{
		{"gold", map[string]string{"any_active": "true", "total_active_stake": "5000000"}, "gold"},
		{"gold-boundary", map[string]string{"any_active": "true", "total_active_stake": "1000000"}, "gold"},
		{"silver", map[string]string{"any_active": "true", "total_active_stake": "500000"}, "silver"},
		{"basic", map[string]string{"any_active": "true", "total_active_stake": "0"}, "basic"},
		{"none-when-not-active", map[string]string{"any_active": "false", "total_active_stake": "0"}, ""},
		{"empty-facts", map[string]string{}, ""},
	}
	for _, c := range cases {
		if got := Eval([]byte(sampleRules), c.facts); got != c.want {
			t.Errorf("%s: tier = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestEval_BooleanCombinators(t *testing.T) {
	rules := `[
		{"tier":"vip","when":{"all":[
			{"fact":"pool:poolA.state","op":"==","value":"active"},
			{"any":[
				{"fact":"total_active_stake","op":">=","value":"5000000"},
				{"fact":"nft:policyX.count","op":">=","value":"1"}
			]}
		]}},
		{"tier":"member","when":{"not":{"fact":"any_active","op":"==","value":"false"}}}
	]`
	// vip via the stake branch of the OR.
	if got := Eval([]byte(rules), map[string]string{"pool:poolA.state": "active", "total_active_stake": "9000000", "any_active": "true"}); got != "vip" {
		t.Errorf("vip via stake: %q", got)
	}
	// vip via the NFT branch of the OR (stake below threshold).
	if got := Eval([]byte(rules), map[string]string{"pool:poolA.state": "active", "total_active_stake": "1", "nft:policyX.count": "2", "any_active": "true"}); got != "vip" {
		t.Errorf("vip via nft: %q", got)
	}
	// not in poolA, but active → falls through to member (not(any_active==false)).
	if got := Eval([]byte(rules), map[string]string{"pool:poolA.state": "none", "any_active": "true"}); got != "member" {
		t.Errorf("member via not: %q", got)
	}
	// inactive → no rule matches.
	if got := Eval([]byte(rules), map[string]string{"any_active": "false"}); got != "" {
		t.Errorf("inactive should have no tier: %q", got)
	}
}

func TestValidate(t *testing.T) {
	good := []string{
		`[]`,
		sampleRules,
		`[{"tier":"t","when":{}}]`, // empty when = catch-all
		`[{"tier":"t","when":{"not":{"fact":"x","op":"!=","value":"y"}}}]`,
	}
	for _, g := range good {
		if err := Validate([]byte(g)); err != nil {
			t.Errorf("valid rules rejected: %s: %v", g, err)
		}
	}
	bad := []string{
		`[{"when":{"fact":"x","op":"==","value":"y"}}]`,                                                       // missing tier
		`[{"tier":"t","when":{"fact":"x","op":"~=","value":"y"}}]`,                                            // bad op
		`[{"tier":"t","when":{"op":">=","value":"1"}}]`,                                                       // op/value without fact
		`[{"tier":"t","when":{"all":[{"fact":"x","op":"==","value":"y"}],"fact":"z","op":"==","value":"w"}}]`, // two forms
		`{not an array}`,
	}
	for _, b := range bad {
		if err := Validate([]byte(b)); err == nil {
			t.Errorf("invalid rules accepted: %s", b)
		}
	}
}
