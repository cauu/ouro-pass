package reconciliation

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"ouro-pass/server/internal/core/rules"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

// programmableElig returns a per-credential verdict set by the test.
type programmableElig struct {
	verdicts map[string]rules.Decision
}

func (p programmableElig) Eligibility(_ context.Context, sch string) (bool, rules.Decision, error) {
	d, ok := p.verdicts[sch]
	if !ok {
		return false, rules.Decision{}, nil
	}
	return d.Eligible, d, nil
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

func TestReconcile_DowngradeExpireKeep(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "keep", "sch-keep", "gold")     // stays gold
	seed(t, st, "down", "sch-down", "gold")     // gold → silver
	seed(t, st, "gone", "sch-gone", "gold")     // ineligible → expired

	elig := programmableElig{verdicts: map[string]rules.Decision{
		"sch-keep": {Eligible: true, Tier: "gold", Entitlements: []string{"read", "vip"}},
		"sch-down": {Eligible: true, Tier: "silver", Entitlements: []string{"read"}},
		// sch-gone absent → ineligible
	}}
	rec := New(st, elig, chain.NewMockSource(481), "pool1")

	res, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 3 || res.Downgraded != 1 || res.Expired != 1 || res.Unchanged != 1 {
		t.Fatalf("result = %+v", res)
	}

	keep, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-keep")
	if keep.Status != domain.SubActive || keep.Tier != "gold" || !keep.LastVerifiedAt.After(keep.CreatedAt) {
		t.Errorf("keep session: %+v", keep)
	}
	down, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-down")
	if down.Tier != "silver" || down.Status != domain.SubActive {
		t.Errorf("down session: tier=%s status=%s", down.Tier, down.Status)
	}
	gone, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-gone")
	if gone.Status != domain.SubExpired {
		t.Errorf("gone session status = %s, want expired", gone.Status)
	}
}

// faultyElig errors for credentials in failFor; otherwise defers to verdicts.
type faultyElig struct {
	verdicts map[string]rules.Decision
	failFor  map[string]bool
}

func (f faultyElig) Eligibility(_ context.Context, sch string) (bool, rules.Decision, error) {
	if f.failFor[sch] {
		return false, rules.Decision{}, context.DeadlineExceeded // simulate a transient chain error
	}
	d := f.verdicts[sch]
	return d.Eligible, d, nil
}

// TestReconcile_FaultIsolation verifies p12-3: one credential's eligibility
// error is isolated (logged, counted) and the remaining sessions are still
// reconciled, instead of the whole pass aborting on the first error.
func TestReconcile_FaultIsolation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "bad", "sch-bad", "gold")   // eligibility errors
	seed(t, st, "gone", "sch-gone", "gold") // ineligible → expired
	seed(t, st, "keep", "sch-keep", "gold") // stays gold

	elig := faultyElig{
		verdicts: map[string]rules.Decision{
			"sch-keep": {Eligible: true, Tier: "gold", Entitlements: []string{"read"}},
			// sch-gone absent → ineligible
		},
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
	rec := New(st, programmableElig{verdicts: map[string]rules.Decision{}}, chain.NewMockSource(1), "pool1")
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
	elig := programmableElig{verdicts: map[string]rules.Decision{}} // all ineligible
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
