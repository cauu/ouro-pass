package reconciliation

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

// programmableState returns a per-credential membership state set by the test.
// An absent credential defaults to `none` (no longer a member → expire).
type programmableState struct {
	states map[string]membership.State
}

func (p programmableState) Membership(_ context.Context, sch string) (membership.State, error) {
	if s, ok := p.states[sch]; ok {
		return s, nil
	}
	return membership.StateNone, nil
}

// faultyState errors for credentials in failFor; otherwise defers to states.
type faultyState struct {
	states  map[string]membership.State
	failFor map[string]bool
}

func (f faultyState) Membership(_ context.Context, sch string) (membership.State, error) {
	if f.failFor[sch] {
		return membership.StateNone, context.DeadlineExceeded // simulate a transient chain error
	}
	if s, ok := f.states[sch]; ok {
		return s, nil
	}
	return membership.StateNone, nil
}

func newStore(t *testing.T) *store.Store {
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

func seed(t *testing.T, st *store.Store, id, sch, tier string) {
	t.Helper()
	now := time.Now().Add(-time.Hour) // stale last_verified so we can detect refresh
	if err := st.Subscriptions().Upsert(context.Background(), domain.SubscriptionSession{
		SessionID: id, PoolID: "pool1", StakeCredentialHash: sch, ChannelType: "telegram",
		ChannelUserID: "u-" + id, Status: domain.SubActive, Tier: tier, Topics: []string{"news"},
		Entitlements: []string{"read"}, CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestReconcile_ExpireKeep: members (active/pending) are kept and refreshed; a
// credential that is no longer a member (state none) is expired. Tier is NOT
// reconciled here (it is first-party, recomputed at consumption).
func TestReconcile_ExpireKeep(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "active", "sch-active", "gold")   // active → kept, refreshed
	seed(t, st, "pending", "sch-pending", "gold") // pending → kept, refreshed
	seed(t, st, "gone", "sch-gone", "gold")       // none → expired

	elig := programmableState{states: map[string]membership.State{
		"sch-active":  membership.StateActive,
		"sch-pending": membership.StatePending,
		// sch-gone absent → none
	}}
	rec := New(st, elig, chain.NewMockSource(481), "pool1")

	res, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 3 || res.Expired != 1 || res.Unchanged != 2 {
		t.Fatalf("result = %+v, want Checked3/Expired1/Unchanged2", res)
	}

	for _, id := range []string{"active", "pending"} {
		s, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-"+id)
		if s.Status != domain.SubActive || !s.LastVerifiedAt.After(s.CreatedAt) {
			t.Errorf("%s session not kept+refreshed: %+v", id, s)
		}
	}
	gone, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-gone")
	if gone.Status != domain.SubExpired {
		t.Errorf("gone session status = %s, want expired", gone.Status)
	}
}

// TestReconcile_FaultIsolation (p12-3 + D8): one credential's chain error is
// isolated — the session is left untouched (soft fail-open, no false expiry) and
// the remaining sessions still reconcile.
func TestReconcile_FaultIsolation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "bad", "sch-bad", "gold")   // membership check errors
	seed(t, st, "gone", "sch-gone", "gold") // none → expired
	seed(t, st, "keep", "sch-keep", "gold") // active → kept

	elig := faultyState{
		states:  map[string]membership.State{"sch-keep": membership.StateActive},
		failFor: map[string]bool{"sch-bad": true},
	}
	rec := New(st, elig, chain.NewMockSource(481), "pool1")

	res, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("whole pass must not error on a single bad session: %v", err)
	}
	if res.Checked != 3 || res.Failed != 1 || res.Expired != 1 || res.Unchanged != 1 {
		t.Fatalf("result = %+v, want Checked3/Failed1/Expired1/Unchanged1", res)
	}
	// The failing session is untouched (still active); the others applied.
	bad, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-bad")
	if bad.Status != domain.SubActive {
		t.Errorf("bad session should be left active, got %s", bad.Status)
	}
	gone, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-gone")
	if gone.Status != domain.SubExpired {
		t.Errorf("gone session not expired despite earlier failure: %s", gone.Status)
	}
}

func TestReconcile_EmptyIsNoop(t *testing.T) {
	st := newStore(t)
	rec := New(st, programmableState{states: map[string]membership.State{}}, chain.NewMockSource(1), "pool1")
	res, err := rec.Reconcile(context.Background())
	if err != nil || res.Checked != 0 {
		t.Fatalf("empty reconcile: %v %+v", err, res)
	}
}

func TestRun_TriggersOnEpochAdvance(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	st := newStore(t)
	seed(t, st, "gone", "sch-gone", "gold")
	elig := programmableState{states: map[string]membership.State{}} // all none
	src := chain.NewMockSource(490)
	rec := New(st, elig, src, "pool1")

	// Run in this goroutine until the timeout; it should reconcile once on the
	// first epoch read (490 > 0) and expire the session.
	go rec.Run(ctx, time.Millisecond)
	<-ctx.Done()

	gone, _ := st.Subscriptions().GetByChannelUser(context.Background(), "pool1", "telegram", "u-gone")
	if gone.Status != domain.SubExpired {
		t.Fatalf("session not reconciled by Run: status=%s", gone.Status)
	}
}
