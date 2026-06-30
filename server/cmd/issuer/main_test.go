package main

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"ouro-pass/server/internal/config"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

func testStore(t *testing.T) *store.Store {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestBuildServices_FullVsDegraded(t *testing.T) {
	st := testStore(t)

	// Full config: field key + server salt present → OAuth + Keys wired, edge
	// flags propagated. The chain source is injected via the buildServices seam
	// (S0015): a deterministic MockSource, never selected via env.
	full := &config.Config{
		DBDriver: "sqlite", Issuer: "ouropass:p", Scope: "p",
		FieldKeyHex: hex.EncodeToString(make([]byte, 32)), ServerSaltHex: hex.EncodeToString([]byte("salt")),
		TrustedProxy: true, TLS: false,
	}
	deps, err := buildServices(full, st, chain.NewMockSource(0))
	if err != nil {
		t.Fatalf("full: %v", err)
	}
	if deps.OAuth == nil || deps.Keys == nil {
		t.Fatal("full config must wire OAuth + Keys")
	}
	if !deps.TrustedProxy || deps.SecureCookies {
		t.Errorf("edge flags not propagated: TrustedProxy=%v SecureCookies=%v", deps.TrustedProxy, deps.SecureCookies)
	}
	// p5-1: the per-network fallback source is wrapped with the active-membership cache.
	if deps.Chain == nil || deps.Chain.Name() != "mock+cache" {
		t.Errorf("chain source = %v (want mock+cache)", deps.Chain)
	}

	// Degraded: no field key → OAuth/Keys nil (routes degrade to 501) but the
	// server still builds with wallet + admin present.
	degraded := &config.Config{DBDriver: "sqlite", Issuer: "ouropass:p", Scope: "p", TLS: true}
	d2, err := buildServices(degraded, st, chain.NewMockSource(0))
	if err != nil {
		t.Fatalf("degraded: %v", err)
	}
	if d2.OAuth != nil || d2.Keys != nil {
		t.Error("degraded config must leave OAuth/Keys nil")
	}
	if d2.Admin == nil || d2.Wallet == nil {
		t.Error("wallet + admin must always be present")
	}
}

// TestBuildServices_ProductionKoiosPerNetwork covers S0015 TC-2 for the production
// path (nil chainOverride): buildServices must wire a per-network Koios origin
// wrapped by the active-membership cache, with no "kind" selection. Constructing a
// KoiosSource makes no network call, so this is deterministic and offline.
func TestBuildServices_ProductionKoiosPerNetwork(t *testing.T) {
	st := testStore(t)
	cfg := &config.Config{DBDriver: "sqlite", Issuer: "ouropass:p", Scope: "p", TLS: true}

	deps, err := buildServices(cfg, st, nil) // nil → production Koios path
	if err != nil {
		t.Fatalf("buildServices: %v", err)
	}
	if deps.SrcFor == nil {
		t.Fatal("SrcFor must be wired")
	}

	// Each network resolves to a Koios source behind the membership cache.
	for _, network := range []string{"mainnet", "preprod", "preview"} {
		s, err := deps.SrcFor(network)
		if err != nil {
			t.Fatalf("SrcFor(%q): %v", network, err)
		}
		if s.Name() != "koios+cache" {
			t.Errorf("SrcFor(%q).Name() = %q, want koios+cache", network, s.Name())
		}
	}

	// Empty network defaults to mainnet and is the same cached instance (S0014 p1-2).
	def, err := deps.SrcFor("")
	if err != nil {
		t.Fatalf(`SrcFor(""): %v`, err)
	}
	main, _ := deps.SrcFor("mainnet")
	if def != main {
		t.Error(`SrcFor("") must resolve to the mainnet source instance`)
	}

	// The fallback deps.Chain (admin delegator roster) is the mainnet Koios source.
	if deps.Chain == nil || deps.Chain.Name() != "koios+cache" {
		t.Errorf("deps.Chain = %v, want koios+cache", deps.Chain)
	}
}

func TestRunNonceGC_StopsOnCancel(t *testing.T) {
	st := testStore(t)
	wallet := walletauth.New(st, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { runNonceGC(ctx, wallet); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runNonceGC did not return on ctx cancel")
	}
}
