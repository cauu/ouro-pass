package telegram

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
)

// fakeFleet is a stub Factory whose Runners record start/stop and build counts,
// so a test can assert the supervisor's lifecycle decisions without a live bot.
type fakeFleet struct {
	mu      sync.Mutex
	running map[string]int // channel_id -> live worker count
	builds  map[string]int // channel_id -> times the factory was invoked
}

func newFleet() *fakeFleet {
	return &fakeFleet{running: map[string]int{}, builds: map[string]int{}}
}

func (f *fakeFleet) factory(inst domain.ChannelConfig) (Runner, error) {
	f.mu.Lock()
	f.builds[inst.ChannelID]++
	f.mu.Unlock()
	return &fakeRunner{fleet: f, id: inst.ChannelID}, nil
}

func (f *fakeFleet) isRunning(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running[id] > 0
}

func (f *fakeFleet) buildCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.builds[id]
}

func (f *fakeFleet) liveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.running {
		n += c
	}
	return n
}

type fakeRunner struct {
	fleet *fakeFleet
	id    string
}

func (r *fakeRunner) Run(ctx context.Context) {
	r.fleet.mu.Lock()
	r.fleet.running[r.id]++
	r.fleet.mu.Unlock()
	<-ctx.Done() // a real worker also returns only on ctx cancel
	r.fleet.mu.Lock()
	r.fleet.running[r.id]--
	r.fleet.mu.Unlock()
}

func newSupStore(t *testing.T) *store.Store {
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

func mkInstance(t *testing.T, st *store.Store, id, name string) {
	t.Helper()
	now := time.Now().UTC()
	if err := st.Channels().Create(context.Background(), domain.ChannelConfig{
		ChannelID: id, PoolID: "pool1", ChannelType: "telegram", Name: name,
		Config: []byte(`{}`), Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("create instance %s: %v", id, err)
	}
}

// waitFor polls cond up to 2s; the supervisor tick here is 10ms so this is ample.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for: %s", what)
}

// TestSupervisor_Lifecycle proves S0005 p2-1 / TC-3: per-instance workers start
// on add, stop on disable/delete within bounded time, never double-start, and
// all child workers are cancelled when the supervisor's context ends (no leak).
func TestSupervisor_Lifecycle(t *testing.T) {
	st := newSupStore(t)
	fleet := newFleet()
	mkInstance(t, st, "c1", "members")

	sup := NewSupervisor(st, fleet.factory, "pool1", 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { sup.Run(ctx); close(done) }()

	// c1 starts.
	waitFor(t, "c1 worker start", func() bool { return fleet.isRunning("c1") })

	// Add c2 → starts; c1 keeps running and is not rebuilt.
	mkInstance(t, st, "c2", "announce")
	waitFor(t, "c2 worker start", func() bool { return fleet.isRunning("c2") })

	// Disable c1 → its worker stops in bounded time; c2 stays up.
	if err := st.Channels().SetStatus(context.Background(), "c1", "disabled", time.Now()); err != nil {
		t.Fatalf("disable c1: %v", err)
	}
	waitFor(t, "c1 worker stop on disable", func() bool { return !fleet.isRunning("c1") })
	if !fleet.isRunning("c2") {
		t.Fatal("c2 should still be running after c1 disabled")
	}

	// Delete c2 → its worker stops.
	if err := st.Channels().Delete(context.Background(), "c2"); err != nil {
		t.Fatalf("delete c2: %v", err)
	}
	waitFor(t, "c2 worker stop on delete", func() bool { return !fleet.isRunning("c2") })

	// No double-start: each instance was built exactly once across its lifetime.
	if n := fleet.buildCount("c1"); n != 1 {
		t.Fatalf("c1 built %d times, want 1 (no double-start)", n)
	}
	if n := fleet.buildCount("c2"); n != 1 {
		t.Fatalf("c2 built %d times, want 1", n)
	}

	// Shutdown: ctx cancel drains all workers (no leak).
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("supervisor did not return after ctx cancel")
	}
	if n := fleet.liveCount(); n != 0 {
		t.Fatalf("%d workers still live after shutdown, want 0", n)
	}
}

// TestSupervisor_RestartOnTokenChange proves a live config edit (new fingerprint)
// stops the old worker and starts a fresh one — no process restart.
func TestSupervisor_RestartOnTokenChange(t *testing.T) {
	st := newSupStore(t)
	fleet := newFleet()
	mkInstance(t, st, "c1", "members")

	sup := NewSupervisor(st, fleet.factory, "pool1", 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)
	waitFor(t, "c1 first start", func() bool { return fleet.buildCount("c1") == 1 })

	// Re-token c1 (Upsert bumps updated_at + config) → supervisor restarts it.
	now := time.Now().UTC().Add(time.Second)
	if err := st.Channels().Upsert(context.Background(), domain.ChannelConfig{
		ChannelID: "c1", PoolID: "pool1", ChannelType: "telegram", Name: "members",
		Config: []byte(`{"bot_token_enc":"deadbeef"}`), Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("re-token c1: %v", err)
	}
	waitFor(t, "c1 rebuild after token change", func() bool { return fleet.buildCount("c1") == 2 })
	waitFor(t, "c1 running after rebuild", func() bool { return fleet.isRunning("c1") })
}

// TestSupervisor_NoInstancesNoWorkers proves S0017: with no DB telegram instance,
// the supervisor starts no worker (there is no env-token fallback instance).
func TestSupervisor_NoInstancesNoWorkers(t *testing.T) {
	st := newSupStore(t)
	fleet := newFleet()

	sup := NewSupervisor(st, fleet.factory, "pool1", 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sup.Run(ctx)

	// Several reconcile ticks pass (interval is 10ms); nothing should start.
	time.Sleep(50 * time.Millisecond)
	if n := fleet.liveCount(); n != 0 {
		t.Fatalf("%d workers running with no DB instance, want 0 (no env fallback)", n)
	}

	// Adding a DB instance starts exactly that one.
	mkInstance(t, st, "c1", "members")
	waitFor(t, "c1 start", func() bool { return fleet.isRunning("c1") })
}
