package membership

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

// countingSource wraps a MockSource and counts per-credential Snapshot calls so a
// test can prove the cache prevented an origin fetch.
type countingSource struct {
	inner *chain.MockSource
	calls map[string]int
}

func (c *countingSource) Snapshot(ctx context.Context, sch string) (*chain.Snapshot, error) {
	c.calls[sch]++
	return c.inner.Snapshot(ctx, sch)
}
func (c *countingSource) Epoch(ctx context.Context) (uint64, error) { return c.inner.Epoch(ctx) }
func (c *countingSource) Name() string                              { return "counting" }

func newCachedTest(t *testing.T) (*CachedSource, *countingSource, func(time.Time)) {
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
	cnt := &countingSource{inner: chain.NewMockSource(0), calls: map[string]int{}}
	cs := NewCachedSource(cnt, st.SnapshotCache(), "preview", time.Second)
	clock := time.Date(2023, 2, 2, 0, 0, 0, 0, time.UTC) // preview epoch 100 (genesis + 100d)
	cur := &clock
	cs.now = func() time.Time { return *cur }
	setNow := func(tm time.Time) { *cur = tm }
	return cs, cnt, setNow
}

func TestCachedSource_ActiveHitsAndEpochRollover(t *testing.T) {
	const us = "pool1us"
	cs, cnt, setNow := newCachedTest(t)
	ctx := context.Background()

	cnt.inner.Put(&chain.Snapshot{
		StakeCredentialHash: "h1", AccountStatus: "registered",
		DelegatedPoolID: us, ActiveStakePoolID: us, ActiveStakeLovelace: "5000000",
		EpochsDelegated: 3, Source: "mock", FetchedAt: time.Now().UTC(),
	})

	// First call: miss → origin fetch + cache.
	s1, err := cs.Snapshot(ctx, "h1")
	if err != nil || DeriveState(s1, us) != StateActive || cnt.calls["h1"] != 1 {
		t.Fatalf("first: state=%v calls=%d err=%v", DeriveState(s1, us), cnt.calls["h1"], err)
	}
	// Second call same epoch: hit → no origin fetch; reconstructed active facts.
	s2, err := cs.Snapshot(ctx, "h1")
	if err != nil || cnt.calls["h1"] != 1 {
		t.Fatalf("hit should not refetch: calls=%d err=%v", cnt.calls["h1"], err)
	}
	if DeriveState(s2, us) != StateActive || s2.ActiveStakeLovelace != "5000000" || s2.EpochsDelegated != 3 {
		t.Fatalf("reconstructed snapshot wrong: %+v", s2)
	}

	// Epoch rolls over (+1 day on preview): the same-epoch guard fails → refetch.
	setNow(time.Date(2023, 2, 3, 0, 0, 0, 0, time.UTC)) // epoch 101
	if _, err := cs.Snapshot(ctx, "h1"); err != nil || cnt.calls["h1"] != 2 {
		t.Fatalf("epoch rollover should refetch: calls=%d err=%v", cnt.calls["h1"], err)
	}
}

func TestCachedSource_PendingNeverCached(t *testing.T) {
	const us = "pool1us"
	cs, cnt, _ := newCachedTest(t)
	ctx := context.Background()

	// Registered + live-delegating to us, no active stake yet → pending.
	cnt.inner.Put(&chain.Snapshot{
		StakeCredentialHash: "h2", AccountStatus: "registered", DelegatedPoolID: us, Source: "mock",
	})
	if s, _ := cs.Snapshot(ctx, "h2"); DeriveState(s, us) != StatePending {
		t.Fatalf("want pending, got %v", DeriveState(s, us))
	}
	// pending hinges on live delegation → recomputed every call (not cached).
	_, _ = cs.Snapshot(ctx, "h2")
	if cnt.calls["h2"] != 2 {
		t.Fatalf("pending must not be cached: calls=%d, want 2", cnt.calls["h2"])
	}
}

func TestCachedSource_PoolAgnosticUpdateAndBail(t *testing.T) {
	const us, other = "pool1us", "pool1other"
	cs, cnt, _ := newCachedTest(t)
	ctx := context.Background()

	cnt.inner.Put(&chain.Snapshot{
		StakeCredentialHash: "h3", AccountStatus: "registered",
		DelegatedPoolID: us, ActiveStakePoolID: us, ActiveStakeLovelace: "9", EpochsDelegated: 1, Source: "mock",
	})
	if s, _ := cs.Snapshot(ctx, "h3"); DeriveState(s, us) != StateActive {
		t.Fatal("setup: expected active in us")
	}

	// Moved to another pool (still active somewhere): the pool-agnostic cache updates
	// to the new active pool — us derives none, the new pool active. (Detected at the
	// epoch boundary; active is epoch-stable, so we roll the epoch to re-fetch.)
	cnt.inner.Put(&chain.Snapshot{StakeCredentialHash: "h3", AccountStatus: "registered", DelegatedPoolID: other, ActiveStakePoolID: other, ActiveStakeLovelace: "9", EpochsDelegated: 1, Source: "mock"})
	cs.now = func() time.Time { return time.Date(2023, 2, 3, 0, 0, 0, 0, time.UTC) } // epoch 101
	s, _ := cs.Snapshot(ctx, "h3")
	if DeriveState(s, us) != StateNone || DeriveState(s, other) != StateActive {
		t.Fatalf("after move: us=%v other=%v", DeriveState(s, us), DeriveState(s, other))
	}
	if row, err := cs.cache.Get(ctx, "h3", "preview"); err != nil || derefStr(row.DelegatedPoolID) != other {
		t.Fatalf("cache should hold the new active pool: %v %+v", err, row)
	}

	// Fully bailed (active nowhere — live-delegating but no active stake yet): the row
	// is deleted so no stale active is ever served.
	cnt.inner.Put(&chain.Snapshot{StakeCredentialHash: "h3", AccountStatus: "registered", DelegatedPoolID: other, Source: "mock"})
	cs.now = func() time.Time { return time.Date(2023, 2, 4, 0, 0, 0, 0, time.UTC) } // epoch 102
	if s, _ := cs.Snapshot(ctx, "h3"); DeriveState(s, other) != StatePending {
		t.Fatalf("expected pending after losing active stake, got %v", DeriveState(s, other))
	}
	if _, err := cs.cache.Get(ctx, "h3", "preview"); err == nil {
		t.Fatal("active-nowhere must delete the cache row")
	}
}
