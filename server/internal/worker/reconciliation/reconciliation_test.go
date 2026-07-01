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

// srcOnce adapts a single source to the per-network SourceFor/Networks the reconciler now
// takes (S0014 p1-3): one network ("mainnet") always backed by src.
func srcOnce(src chain.Source) (SourceFor, Networks) {
	return func(string) (chain.Source, error) { return src, nil },
		func(context.Context) ([]string, error) { return []string{"mainnet"}, nil }
}

// programmableState returns a per-credential membership state (and optional tier)
// set by the test. An absent credential defaults to `none` (no longer a member →
// expire). tiers is optional; an absent tier is "" (the pre-p1-1 behavior).
type programmableState struct {
	states map[string]membership.State
	tiers  map[string]string
}

func (p programmableState) Attest(_ context.Context, sch string) (membership.State, string, error) {
	if s, ok := p.states[sch]; ok {
		return s, p.tiers[sch], nil
	}
	return membership.StateNone, "", nil
}

// faultyState errors for credentials in failFor; otherwise defers to states.
type faultyState struct {
	states  map[string]membership.State
	failFor map[string]bool
}

func (f faultyState) Attest(_ context.Context, sch string) (membership.State, string, error) {
	if f.failFor[sch] {
		return membership.StateNone, "", context.DeadlineExceeded // simulate a transient chain error
	}
	if s, ok := f.states[sch]; ok {
		return s, "", nil
	}
	return membership.StateNone, "", nil
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

// TestReconcile_KeepAndGrace: members (active/pending) are kept, refreshed, and
// their informational ExpiresAt slides; a credential that is no longer a member
// (state none) enters GRACE on the first observation (not immediate expiry, S0019
// p1-2) — GraceUntil is set and status stays active until the deadline.
func TestReconcile_KeepAndGrace(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "active", "sch-active", "gold")   // active → kept, refreshed
	seed(t, st, "pending", "sch-pending", "gold") // pending → kept, refreshed
	seed(t, st, "gone", "sch-gone", "gold")       // none → grace

	elig := programmableState{states: map[string]membership.State{
		"sch-active":  membership.StateActive,
		"sch-pending": membership.StatePending,
		// sch-gone absent → none
	}}
	sf, nf := srcOnce(chain.NewMockSource(481))
	rec := New(st, elig, sf, nf, "pool1")

	res, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Checked != 3 || res.Grace != 1 || res.Expired != 0 || res.Unchanged != 2 {
		t.Fatalf("result = %+v, want Checked3/Grace1/Expired0/Unchanged2", res)
	}

	for _, id := range []string{"active", "pending"} {
		s, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-"+id)
		if s.Status != domain.SubActive || !s.LastVerifiedAt.After(s.CreatedAt) || s.GraceUntil != nil ||
			!s.ExpiresAt.After(time.Now().Add(29*24*time.Hour)) {
			t.Errorf("%s session not kept+refreshed+slid: %+v", id, s)
		}
	}
	gone, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-gone")
	if gone.Status != domain.SubActive || gone.GraceUntil == nil {
		t.Errorf("gone session should be in grace (active + GraceUntil set), got status=%s grace=%v", gone.Status, gone.GraceUntil)
	}
}

// TestReconcile_GraceLifecycle (S0019 p1-2 / TC-3): first none → grace deadline set,
// not expired; recovery before the deadline restores (GraceUntil cleared); none with
// now >= deadline → expired. Expiry is decided by the timestamp, not the pass count.
func TestReconcile_GraceLifecycle(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "s", "sch", "gold")

	sf, nf := srcOnce(chain.NewMockSource(481))
	none := programmableState{states: map[string]membership.State{}} // sch absent → none
	back := programmableState{states: map[string]membership.State{"sch": membership.StateActive}}

	// Pass 1: none → grace (deadline ~now+5d), still active.
	rec := New(st, none, sf, nf, "pool1")
	if res, _ := rec.Reconcile(ctx); res.Grace != 1 || res.Expired != 0 {
		t.Fatalf("pass1 = %+v, want Grace1", res)
	}
	s, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-s")
	if s.Status != domain.SubActive || s.GraceUntil == nil {
		t.Fatalf("after grace entry: status=%s grace=%v", s.Status, s.GraceUntil)
	}

	// Pass 2: membership recovers before the deadline → restore (grace cleared).
	recBack := New(st, back, sf, nf, "pool1")
	if res, _ := recBack.Reconcile(ctx); res.Unchanged != 1 || res.Grace != 0 {
		t.Fatalf("pass2 = %+v, want Unchanged1", res)
	}
	s, _ = st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-s")
	if s.Status != domain.SubActive || s.GraceUntil != nil {
		t.Fatalf("after restore: status=%s grace=%v, want active + nil grace", s.Status, s.GraceUntil)
	}

	// Re-enter grace, then force the deadline into the past and reconcile none → expired.
	if _, err := rec.Reconcile(ctx); err != nil { // pass 3: none → grace again
		t.Fatal(err)
	}
	s, _ = st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-s")
	past := time.Now().Add(-time.Minute)
	s.GraceUntil = &past
	if err := st.Subscriptions().Upsert(ctx, *s); err != nil {
		t.Fatal(err)
	}
	if res, _ := rec.Reconcile(ctx); res.Expired != 1 { // pass 4: none, deadline passed → expired
		t.Fatalf("pass4 = %+v, want Expired1", res)
	}
	s, _ = st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-s")
	if s.Status != domain.SubExpired {
		t.Fatalf("after deadline: status=%s, want expired", s.Status)
	}
}

// TestReconcile_RefreshesTier (S0019 p1-1 / TC-1): a session created while pending
// with an empty tier has its stored tier set once the credential becomes active at
// the next reconcile — no re-bind. Uses Attest (state + tier), not state-only.
func TestReconcile_RefreshesTier(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "up", "sch-up", "") // created pending, empty tier

	elig := programmableState{
		states: map[string]membership.State{"sch-up": membership.StateActive},
		tiers:  map[string]string{"sch-up": "gold"}, // now active → gold
	}
	sf, nf := srcOnce(chain.NewMockSource(481))
	rec := New(st, elig, sf, nf, "pool1")

	if _, err := rec.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-up")
	if s.Status != domain.SubActive || s.Tier != "gold" {
		t.Fatalf("tier not refreshed: status=%s tier=%q, want active/gold", s.Status, s.Tier)
	}
}

// TestReconcile_FaultIsolation (p12-3 + D8): one credential's chain error is
// isolated — the session is left untouched (soft fail-open, no false expiry) and
// the remaining sessions still reconcile.
func TestReconcile_FaultIsolation(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "bad", "sch-bad", "gold")   // membership check errors → untouched, no grace
	seed(t, st, "gone", "sch-gone", "gold") // none → grace
	seed(t, st, "keep", "sch-keep", "gold") // active → kept

	elig := faultyState{
		states:  map[string]membership.State{"sch-keep": membership.StateActive},
		failFor: map[string]bool{"sch-bad": true},
	}
	sf, nf := srcOnce(chain.NewMockSource(481))
	rec := New(st, elig, sf, nf, "pool1")

	res, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatalf("whole pass must not error on a single bad session: %v", err)
	}
	if res.Checked != 3 || res.Failed != 1 || res.Grace != 1 || res.Unchanged != 1 {
		t.Fatalf("result = %+v, want Checked3/Failed1/Grace1/Unchanged1", res)
	}
	// The failing session is untouched (still active, NOT in grace — fail-open never
	// starts grace on a chain error); the others applied.
	bad, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-bad")
	if bad.Status != domain.SubActive || bad.GraceUntil != nil {
		t.Errorf("bad session should be left active with no grace, got status=%s grace=%v", bad.Status, bad.GraceUntil)
	}
	gone, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u-gone")
	if gone.Status != domain.SubActive || gone.GraceUntil == nil {
		t.Errorf("gone session should be in grace despite the other session's failure: status=%s grace=%v", gone.Status, gone.GraceUntil)
	}
}

// TestReconcile_NotifiesOnceOnGrace (S0019 p1-3 / TC-4): entering grace fires the
// notifier exactly once with the session + grace message; a notifier error does not
// fail the pass; a second consecutive none (still in grace) does NOT notify again.
func TestReconcile_NotifiesOnceOnGrace(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seed(t, st, "s", "sch", "gold")

	var calls []domain.SubscriptionSession
	var msgs []string
	notifier := func(_ context.Context, sess domain.SubscriptionSession, msg string) error {
		calls = append(calls, sess)
		msgs = append(msgs, msg)
		return context.DeadlineExceeded // send error must not fail reconcile
	}
	sf, nf := srcOnce(chain.NewMockSource(481))
	none := programmableState{states: map[string]membership.State{}} // sch → none
	rec := New(st, none, sf, nf, "pool1").WithNotifier(notifier)

	// Pass 1: grace entry → notify once.
	if res, err := rec.Reconcile(ctx); err != nil || res.Grace != 1 {
		t.Fatalf("pass1 = %+v err=%v, want Grace1 and no error despite notifier failure", res, err)
	}
	if len(calls) != 1 || calls[0].SessionID != "s" || msgs[0] != graceMessage {
		t.Fatalf("notifier calls = %+v msgs=%v, want one call for session s with graceMessage", calls, msgs)
	}
	// Pass 2: still none, already in grace → no second notification.
	if res, err := rec.Reconcile(ctx); err != nil || res.Grace != 0 {
		t.Fatalf("pass2 = %+v err=%v, want Grace0 (already in grace)", res, err)
	}
	if len(calls) != 1 {
		t.Fatalf("notifier called %d times, want exactly 1 (no repeat while still none)", len(calls))
	}
}

func TestReconcile_EmptyIsNoop(t *testing.T) {
	st := newStore(t)
	sf, nf := srcOnce(chain.NewMockSource(1))
	rec := New(st, programmableState{states: map[string]membership.State{}}, sf, nf, "pool1")
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
	sf, nf := srcOnce(chain.NewMockSource(490))
	rec := New(st, elig, sf, nf, "pool1")

	// Run in this goroutine until the timeout; it should reconcile once on the first
	// epoch read (490 > 0), which puts the no-longer-member session into grace.
	go rec.Run(ctx, time.Millisecond)
	<-ctx.Done()

	gone, _ := st.Subscriptions().GetByChannelUser(context.Background(), "pool1", "telegram", "u-gone")
	if gone.GraceUntil == nil {
		t.Fatalf("session not reconciled by Run: status=%s grace=%v", gone.Status, gone.GraceUntil)
	}
}

// TestRun_TriggersOnSecondNetworkEpoch covers S0014 p1-3 / TC-3: with two networks in use,
// an epoch advance on the NON-primary network triggers the reconcile pass (per-network
// epoch watching, not a single global source).
func TestRun_TriggersOnSecondNetworkEpoch(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	st := newStore(t)
	seed(t, st, "gone", "sch-gone", "gold")
	elig := programmableState{states: map[string]membership.State{}} // all none → expire
	sources := map[string]chain.Source{
		"mainnet": chain.NewMockSource(0),   // epoch 0 — never advances past the initial cursor
		"preprod": chain.NewMockSource(500), // advances → must trigger reconcile
	}
	sf := func(net string) (chain.Source, error) { return sources[net], nil }
	nf := func(context.Context) ([]string, error) { return []string{"mainnet", "preprod"}, nil }
	rec := New(st, elig, sf, nf, "pool1")

	go rec.Run(ctx, time.Millisecond)
	<-ctx.Done()

	gone, _ := st.Subscriptions().GetByChannelUser(context.Background(), "pool1", "telegram", "u-gone")
	if gone.GraceUntil == nil {
		t.Fatalf("preprod epoch advance did not trigger reconcile: status=%s grace=%v", gone.Status, gone.GraceUntil)
	}
}
