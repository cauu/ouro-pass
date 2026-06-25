package chain

import (
	"context"
	"errors"
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
			_, _ = w.Write([]byte(`[{"stake_address":"stake1xyz","status":"registered","delegated_pool":"pool1abc","rewards_available":"123"}]`))
		case "/account_stake_history":
			// Active stake comes from history now, not total_balance: 3 consecutive
			// epochs with pool1abc, latest = 479 = exact active stake (C4).
			_, _ = w.Write([]byte(`[{"pool_id":"pool1abc","epoch_no":477,"active_stake":"44000000000000000"},{"pool_id":"pool1abc","epoch_no":478,"active_stake":"44500000000000000"},{"pool_id":"pool1abc","epoch_no":479,"active_stake":"45000000000000000"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	k := NewKoiosSource(srv.URL, "", "preview")
	if k.Name() != "koios" {
		t.Errorf("name = %s", k.Name())
	}
	ep, err := k.Epoch(context.Background())
	if err != nil || ep != 481 {
		t.Fatalf("epoch: %v %d", err, ep)
	}
	// 28-byte hex stake credential (the mock server ignores the derived address).
	s, err := k.Snapshot(context.Background(), "00112233445566778899aabbccddeeff00112233445566778899aabb")
	if err != nil {
		t.Fatal(err)
	}
	// Active stake = latest history entry (exact, big value preserved, C4); the
	// active pool + trailing epoch count come from history; live pool from info.
	if s.ActiveStakeLovelace != "45000000000000000" || s.ActiveStakePoolID != "pool1abc" ||
		s.DelegatedPoolID != "pool1abc" || s.EpochsDelegated != 3 || s.Epoch != 481 {
		t.Fatalf("snapshot: %+v", s)
	}
}

func TestKoiosToSnapshot_UnregisteredClearsPool(t *testing.T) {
	s := koiosToSnapshot("h1", 480, koiosAccountInfo{Status: "not_registered", DelegatedPool: "pool1abc"}, nil)
	if s.DelegatedPoolID != "" {
		t.Errorf("unregistered account should have no delegated pool, got %q", s.DelegatedPoolID)
	}
}

func TestKoiosToSnapshot_Vectors(t *testing.T) {
	// Pending: registered + live delegation to us, but no active-stake history yet
	// (entered, ~2 epochs from effective). No ActiveStakePoolID; 0 epochs active.
	pending := koiosToSnapshot("h", 500, koiosAccountInfo{Status: "registered", DelegatedPool: "poolUS"}, nil)
	if pending.DelegatedPoolID != "poolUS" || pending.ActiveStakePoolID != "" ||
		pending.ActiveStakeLovelace != "" || pending.EpochsDelegated != 0 {
		t.Fatalf("pending: %+v", pending)
	}

	// Leaving tail: live delegation already moved to poolOTHER, but active stake
	// still counts for poolUS (history latest). ActiveStakePoolID stays poolUS.
	leavingHist := []koiosStakeHistory{
		{PoolID: "poolUS", Epoch: 498, ActiveStake: "100"},
		{PoolID: "poolUS", Epoch: 499, ActiveStake: "100"},
	}
	leaving := koiosToSnapshot("h", 500, koiosAccountInfo{Status: "registered", DelegatedPool: "poolOTHER"}, leavingHist)
	if leaving.DelegatedPoolID != "poolOTHER" || leaving.ActiveStakePoolID != "poolUS" ||
		leaving.ActiveStakeLovelace != "100" || leaving.EpochsDelegated != 2 {
		t.Fatalf("leaving: %+v", leaving)
	}

	// Switched pools: trailing count resets at the pool change (poolUS for the
	// latest 2 epochs after an earlier poolOLD epoch).
	switched := []koiosStakeHistory{
		{PoolID: "poolOLD", Epoch: 497, ActiveStake: "1"},
		{PoolID: "poolUS", Epoch: 498, ActiveStake: "2"},
		{PoolID: "poolUS", Epoch: 499, ActiveStake: "3"},
	}
	s := koiosToSnapshot("h", 500, koiosAccountInfo{Status: "registered", DelegatedPool: "poolUS"}, switched)
	if s.ActiveStakePoolID != "poolUS" || s.EpochsDelegated != 2 {
		t.Fatalf("switched: %+v", s)
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
	s, err := n.Snapshot(context.Background(), "00112233445566778899aabbccddeeff00112233445566778899aabb")
	if err != nil {
		t.Fatal(err)
	}
	if s.Epoch != 490 || s.DelegatedPoolID != "pool1z" || s.AccountStatus != "registered" {
		t.Fatalf("snapshot via injected runner: %+v", s)
	}
}

func TestNewSource_Factory(t *testing.T) {
	for _, kind := range []string{"mock", "koios", "node_lsq"} {
		s, err := NewSource(Config{Kind: kind})
		if err != nil || s == nil {
			t.Fatalf("kind %s: %v", kind, err)
		}
	}
	if _, err := NewSource(Config{Kind: "bogus"}); err == nil {
		t.Error("unknown kind should error")
	}
	// db_sync fails fast at construction in the default build (p12-10).
	if _, err := NewSource(Config{Kind: "db_sync"}); !errors.Is(err, ErrNotImplemented) {
		t.Errorf("db_sync should fail fast with ErrNotImplemented, got %v", err)
	}
}
