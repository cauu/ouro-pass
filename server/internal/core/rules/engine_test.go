package rules

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/poolops/issuer/internal/domain"
)

func rule(id, tier string, prio int, cfg string, ents ...string) domain.MembershipRule {
	return domain.MembershipRule{
		RuleID: id, Name: id, Tier: tier, Priority: prio, Status: domain.RuleActive,
		RuleConfig: json.RawMessage(cfg), Entitlements: ents,
	}
}

var ruleset = []domain.MembershipRule{
	rule("gold", "gold", 10, `{"min_active_stake_lovelace":"100000000000"}`, "read", "push", "vip"),
	rule("silver", "silver", 5, `{"min_active_stake_lovelace":"1000000"}`, "read"),
}

func TestEvaluate_PriorityAndThresholds(t *testing.T) {
	pool := "pool1abc"
	cases := []struct {
		name      string
		in        Input
		wantElig  bool
		wantTier  string
		wantMatch string
	}{
		{"whale → gold", Input{PoolID: pool, DelegatedPoolID: pool, ActiveStakeLovelace: "500000000000", EpochsDelegated: -1}, true, "gold", "gold"},
		{"small staker → silver", Input{PoolID: pool, DelegatedPoolID: pool, ActiveStakeLovelace: "2000000", EpochsDelegated: -1}, true, "silver", "silver"},
		{"below all thresholds", Input{PoolID: pool, DelegatedPoolID: pool, ActiveStakeLovelace: "100", EpochsDelegated: -1}, false, "", ""},
		{"delegates elsewhere", Input{PoolID: pool, DelegatedPoolID: "pool1other", ActiveStakeLovelace: "500000000000", EpochsDelegated: -1}, false, "", ""},
		{"not delegating", Input{PoolID: pool, DelegatedPoolID: "", ActiveStakeLovelace: "500000000000", EpochsDelegated: -1}, false, "", ""},
		{"unknown stake fails min", Input{PoolID: pool, DelegatedPoolID: pool, ActiveStakeLovelace: "", EpochsDelegated: -1}, false, "", ""},
	}
	for _, c := range cases {
		got := Evaluate(c.in, ruleset, 480)
		if got.Eligible != c.wantElig || got.Tier != c.wantTier || (c.wantMatch != "" && got.MatchedRule != c.wantMatch) {
			t.Errorf("%s: got %+v, want elig=%v tier=%s", c.name, got, c.wantElig, c.wantTier)
		}
	}
}

func TestEvaluate_DeterministicAndOrderIndependent(t *testing.T) {
	in := Input{PoolID: "p", DelegatedPoolID: "p", ActiveStakeLovelace: "500000000000", EpochsDelegated: -1}

	// Same inputs → identical output across repeated calls (pure).
	d1 := Evaluate(in, ruleset, 480)
	for i := 0; i < 50; i++ {
		if !reflect.DeepEqual(Evaluate(in, ruleset, 480), d1) {
			t.Fatal("non-deterministic output")
		}
	}

	// Highest priority wins regardless of input ordering: both rules match the
	// whale, gold (prio 10) must win even if silver is listed first.
	reordered := []domain.MembershipRule{ruleset[1], ruleset[0]}
	d2 := Evaluate(in, reordered, 480)
	if d2.MatchedRule != "gold" {
		t.Fatalf("order-dependent: matched %s, want gold", d2.MatchedRule)
	}
	if !reflect.DeepEqual(d2.Entitlements, []string{"read", "push", "vip"}) {
		t.Errorf("entitlements = %v", d2.Entitlements)
	}
}

func TestEvaluate_MinEpochsWithGrace(t *testing.T) {
	pool := "p"
	r := []domain.MembershipRule{rule("g", "gold", 1, `{"min_active_epochs":5,"grace_epochs":2}`)}
	// effective min = 5-2 = 3 epochs.
	if Evaluate(Input{PoolID: pool, DelegatedPoolID: pool, EpochsDelegated: 2}, r, 480).Eligible {
		t.Error("2 epochs < effective 3 should fail")
	}
	if !Evaluate(Input{PoolID: pool, DelegatedPoolID: pool, EpochsDelegated: 3}, r, 480).Eligible {
		t.Error("3 epochs >= effective 3 should pass")
	}
	// Unknown epoch age (-1) skips the epoch check (source can't provide it).
	if !Evaluate(Input{PoolID: pool, DelegatedPoolID: pool, EpochsDelegated: -1}, r, 480).Eligible {
		t.Error("unknown epoch age should not block on min_active_epochs")
	}
}

func TestEvaluate_DisabledRuleIgnored(t *testing.T) {
	r := rule("g", "gold", 1, `{}`)
	r.Status = domain.RuleDisabled
	d := Evaluate(Input{PoolID: "p", DelegatedPoolID: "p", EpochsDelegated: -1}, []domain.MembershipRule{r}, 480)
	if d.Eligible {
		t.Error("disabled rule must not match")
	}
}
