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
	// flags propagated, mock chain selected.
	full := &config.Config{
		Network: "preview", DBDriver: "sqlite", ChainKind: "mock", Issuer: "ouropass:p", Scope: "p",
		FieldKeyHex: hex.EncodeToString(make([]byte, 32)), ServerSaltHex: hex.EncodeToString([]byte("salt")),
		TrustedProxy: true, TLS: false,
	}
	deps, err := buildServices(full, st)
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
	degraded := &config.Config{Network: "preview", DBDriver: "sqlite", ChainKind: "mock", Issuer: "ouropass:p", Scope: "p", TLS: true}
	d2, err := buildServices(degraded, st)
	if err != nil {
		t.Fatalf("degraded: %v", err)
	}
	if d2.OAuth != nil || d2.Keys != nil {
		t.Error("degraded config must leave OAuth/Keys nil")
	}
	if d2.Admin == nil || d2.Wallet == nil {
		t.Error("wallet + admin must always be present")
	}

	// db_sync fails fast at construction (p12-10).
	dbsync := *full
	dbsync.ChainKind = "db_sync"
	if _, err := buildServices(&dbsync, st); err == nil {
		t.Error("db_sync must fail fast in the default build")
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
