package oauth

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
)

// TestCreateActivation_BlacklistedRejected covers blacklist gating on the
// activation face (p14-5): an otherwise-eligible credential on the blacklist is
// denied (blacklist was only previously tested on the authorize face).
func TestCreateActivation_BlacklistedRejected(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})
	if err := h.st.Blacklist().Add(ctx, domain.Blacklist{StakeCredentialHash: sch, CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceActivation, h.rewardAddr)
	if _, err := h.srv.CreateActivation(ctx, "telegram", nonce, h.coseKey, h.sign(t, nonce), "PaoBot"); err != ErrNotEligible {
		t.Fatalf("blacklisted activation: %v, want ErrNotEligible", err)
	}
}

func TestCreateActivation_EligibleIssuesCodeAndLink(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})

	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceActivation, h.rewardAddr)
	res, err := h.srv.CreateActivation(ctx, "telegram", nonce, h.coseKey, h.sign(t, nonce), "PaoBot")
	if err != nil {
		t.Fatalf("create activation: %v", err)
	}
	if res.ActivationCode == "" || len(res.ActivationCode) > 64 {
		t.Fatalf("activation code unsuitable for deep link: %q (len %d)", res.ActivationCode, len(res.ActivationCode))
	}
	if !strings.HasPrefix(res.DeepLink, "https://t.me/PaoBot?start=") {
		t.Fatalf("deep link = %s", res.DeepLink)
	}

	// The stored ActivationCode (hashed) is consumable once for telegram.
	got, err := h.st.ActivationCodes().Consume(ctx, crypto.HashToken(res.ActivationCode), "telegram", h.srv.now())
	if err != nil || got.StakeCredentialHash != sch {
		t.Fatalf("stored activation: %v %+v", err, got)
	}
}

func TestCreateActivation_Ineligible(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "other", ActiveStakeLovelace: "5000000"})
	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceActivation, h.rewardAddr)
	if _, err := h.srv.CreateActivation(ctx, "telegram", nonce, h.coseKey, h.sign(t, nonce), "PaoBot"); err != ErrNotEligible {
		t.Fatalf("ineligible: %v, want ErrNotEligible", err)
	}
}

func TestCreateActivation_BadSignature(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})
	// Use an issue-purpose nonce → activation verify should fail (purpose mismatch).
	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.rewardAddr)
	if _, err := h.srv.CreateActivation(ctx, "telegram", nonce, h.coseKey, h.sign(t, nonce), "PaoBot"); err != ErrAccessDenied {
		t.Fatalf("wrong purpose nonce: %v, want ErrAccessDenied", err)
	}
}
