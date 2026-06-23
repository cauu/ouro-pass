package chain

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMockSource(t *testing.T) {
	m := NewMockSource(480)
	m.Put(&Snapshot{StakeCredentialHash: "h1", Epoch: 480, DelegatedPoolID: "pool1abc", ActiveStakeLovelace: "1000000"})
	s, err := m.Snapshot(context.Background(), "h1")
	if err != nil || s.DelegatedPoolID != "pool1abc" {
		t.Fatalf("known: %v %+v", err, s)
	}
	// Unknown credential → zero-stake snapshot, not an error.
	s, err = m.Snapshot(context.Background(), "unknown")
	if err != nil || s.DelegatedPoolID != "" {
		t.Fatalf("unknown: %v %+v", err, s)
	}
	if ep, _ := m.Epoch(context.Background()); ep != 480 {
		t.Errorf("epoch = %d", ep)
	}
}

func TestKoiosSource_ParsesAccountInfoAndTip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tip":
			_, _ = w.Write([]byte(`[{"epoch_no":481}]`))
		case "/account_info":
			_, _ = w.Write([]byte(`[{"stake_address":"stake1xyz","status":"registered","delegated_pool":"pool1abc","total_balance":"45000000000000000","rewards_available":"123"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	k := NewKoiosSource(srv.URL, "")
	if k.Name() != "koios" {
		t.Errorf("name = %s", k.Name())
	}
	ep, err := k.Epoch(context.Background())
	if err != nil || ep != 481 {
		t.Fatalf("epoch: %v %d", err, ep)
	}
	s, err := k.Snapshot(context.Background(), "stake1xyz")
	if err != nil {
		t.Fatal(err)
	}
	// Big lovelace preserved exactly (C4).
	if s.ActiveStakeLovelace != "45000000000000000" || s.DelegatedPoolID != "pool1abc" || s.Epoch != 481 {
		t.Fatalf("snapshot: %+v", s)
	}
}

func TestKoiosToSnapshot_UnregisteredClearsPool(t *testing.T) {
	s := koiosToSnapshot("h1", 480, koiosAccountInfo{Status: "not_registered", DelegatedPool: "pool1abc"})
	if s.DelegatedPoolID != "" {
		t.Errorf("unregistered account should have no delegated pool, got %q", s.DelegatedPoolID)
	}
}

func TestNodeLSQ_ParseStakeAddressInfoAndTip(t *testing.T) {
	snap, err := parseStakeAddressInfo("h1", 480, []byte(
		`[{"address":"stake1xyz","stakeDelegation":"pool1abc","rewardAccountBalance":98765}]`))
	if err != nil {
		t.Fatal(err)
	}
	if snap.DelegatedPoolID != "pool1abc" || snap.RewardsLovelace != "98765" || snap.Source != "node_lsq" {
		t.Fatalf("snapshot: %+v", snap)
	}
	// Empty array (undelegated) → no pool, no error.
	snap, err = parseStakeAddressInfo("h1", 480, []byte(`[]`))
	if err != nil || snap.DelegatedPoolID != "" {
		t.Fatalf("empty: %v %+v", err, snap)
	}
	ep, err := parseTip([]byte(`{"epoch":482,"slot":1}`))
	if err != nil || ep != 482 {
		t.Fatalf("tip: %v %d", err, ep)
	}
}

func TestNodeLSQ_InjectableRunner(t *testing.T) {
	n := NewNodeLSQSource("cardano-cli", "/tmp/socket", "preview")
	n.run = func(ctx context.Context, args ...string) ([]byte, error) {
		if args[1] == "tip" {
			return []byte(`{"epoch":490}`), nil
		}
		return []byte(`[{"stakeDelegation":"pool1z","rewardAccountBalance":5}]`), nil
	}
	s, err := n.Snapshot(context.Background(), "stakeAddr")
	if err != nil {
		t.Fatal(err)
	}
	if s.Epoch != 490 || s.DelegatedPoolID != "pool1z" {
		t.Fatalf("snapshot via injected runner: %+v", s)
	}
}

func TestNewSource_Factory(t *testing.T) {
	for _, kind := range []string{"mock", "koios", "node_lsq", "db_sync"} {
		s, err := NewSource(Config{Kind: kind})
		if err != nil || s == nil {
			t.Fatalf("kind %s: %v", kind, err)
		}
	}
	if _, err := NewSource(Config{Kind: "bogus"}); err == nil {
		t.Error("unknown kind should error")
	}
	// db_sync default build is not implemented.
	d, _ := NewSource(Config{Kind: "db_sync"})
	if _, err := d.Epoch(context.Background()); err != ErrNotImplemented {
		t.Errorf("db_sync should be ErrNotImplemented, got %v", err)
	}
}
