// Package reconciliation re-derives pool-membership at each epoch boundary
// (S0004 §2.3, C7): the three-state membership is recomputed from the live
// snapshot and subscription sessions are expired once the credential is no longer
// a member (state `none`). The first-party tier is recomputed each pass too
// (S0019 p1-1): the reconciler evaluates `Attest` (state + tier) and writes the
// fresh tier back, so a `pending→active` upgrade or a stake-driven tier change is
// reflected without a re-bind. It runs as a long-lived worker that triggers when
// the chain epoch advances; Reconcile is directly callable and unit-tested.
package reconciliation

import (
	"context"
	"log/slog"
	"time"

	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

// subscriptionTTL / subscriptionGrace are the shared lifecycle policy consts (a
// single source of truth in `domain`, so the reconcile slide and the activation
// display cannot drift — S0019 p3-5).
const (
	subscriptionTTL   = domain.SubscriptionTTL
	subscriptionGrace = domain.SubscriptionGrace
)

// StateEvaluator re-derives a credential's current pool-membership state and the
// issuer's first-party tier for it (S0019 p1-1: was state-only `Membership`; now
// `Attest` so the reconciler can refresh a member's tier each pass). `oauth.Server`
// implements it.
type StateEvaluator interface {
	Attest(ctx context.Context, stakeCredentialHash string) (membership.State, string, error)
}

// SourceFor resolves a read-only chain source for a network. Networks resolves the set of
// networks currently in use by active attestors (S0014 p1-3): the reconciler triggers when
// ANY of those networks' epoch advances, since epoch is per-network.
type (
	SourceFor func(network string) (chain.Source, error)
	Networks  func(ctx context.Context) ([]string, error)
)

// Notifier delivers a best-effort message to a subscription's channel user (S0019
// p1-3: the grace-entry warning). It is invoked once, on grace entry; a send error
// is logged and never blocks reconcile. nil → notifications skipped.
type Notifier func(ctx context.Context, sess domain.SubscriptionSession, msg string) error

// Reconciler maintains subscription sessions against current membership state.
type Reconciler struct {
	poolID   string
	subs     *store.SubscriptionRepo
	elig     StateEvaluator
	srcFor   SourceFor
	networks Networks
	notifier Notifier
	now      func() time.Time
}

// New builds a reconciler. Reconcile is network-agnostic (it re-derives via elig, which
// evaluates each attestor on its own network); srcFor+networks are used only to watch
// per-network epoch advances as the trigger (S0014 p1-3).
func New(st *store.Store, elig StateEvaluator, srcFor SourceFor, networks Networks, poolID string) *Reconciler {
	return &Reconciler{poolID: poolID, subs: st.Subscriptions(), elig: elig, srcFor: srcFor, networks: networks, now: time.Now}
}

// WithNotifier attaches the grace-entry notifier (S0019 p1-3) and returns the
// reconciler for chaining. nil (the default) → notifications are skipped.
func (r *Reconciler) WithNotifier(n Notifier) *Reconciler {
	r.notifier = n
	return r
}

// graceMessage is the one-shot warning sent when a subscription enters grace.
const graceMessage = "Your subscription is expiring: we no longer see an active delegation for this account. Re-delegate to your pool to keep your membership."

// notifyGrace best-effort delivers the grace warning once (on grace entry). A nil
// notifier or a send error is logged and never affects reconcile (S0019 p1-3).
func (r *Reconciler) notifyGrace(ctx context.Context, sess domain.SubscriptionSession) {
	if r.notifier == nil {
		return
	}
	if err := r.notifier(ctx, sess, graceMessage); err != nil {
		slog.Warn("reconciliation: grace notification failed", "session", sess.SessionID, "err", err)
	}
}

// Result summarizes a reconciliation pass.
type Result struct {
	Checked   int
	Expired   int // terminally expired (state==none past the grace deadline)
	Grace     int // entered grace this pass (first observed state==none)
	Unchanged int
	Failed    int // per-session errors that were isolated (logged, skipped)
}

// Reconcile re-derives every active session's membership once. Membership is
// re-derived FIRST, then expiry is decided (S0019 p1-2):
//
//   - member (active/pending): refresh LastVerifiedAt + first-party Tier, slide the
//     informational ExpiresAt = now + TTL, and clear any grace (GraceUntil = nil).
//     This restores a session that had entered grace but recovered before the deadline.
//   - state == none, not yet in grace (GraceUntil == nil): record the grace deadline
//     GraceUntil = now + GRACE and notify the user once. The session stays active.
//   - state == none, already in grace: expire terminally only once now >= *GraceUntil
//     (a pure timestamp comparison — reconcile frequency changes only how quickly
//     expiry is noticed, never when it is due); otherwise keep waiting.
//
// A per-session error is fault-isolated — logged, counted, and the session left
// untouched (D8 soft fail-open: a transient chain error never expires a member and
// never starts grace or notifies) — so one bad credential never blocks the rest of
// the pool (p12-3). Only a whole-pass failure (listing sessions) returns an error.
func (r *Reconciler) Reconcile(ctx context.Context) (Result, error) {
	sessions, err := r.subs.ListActive(ctx, r.poolID)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, sess := range sessions {
		res.Checked++
		state, tier, err := r.elig.Attest(ctx, sess.StakeCredentialHash)
		if err != nil {
			slog.Warn("reconciliation: membership check failed, keeping session",
				"session", sess.SessionID, "err", err)
			res.Failed++
			continue
		}
		now := r.now()

		if state != membership.StateNone {
			// Still a member (active or pending): refresh verification + tier (p1-1),
			// slide the informational ExpiresAt, and clear grace (restore, p1-2).
			sess.LastVerifiedAt = now
			sess.Tier = tier
			sess.ExpiresAt = now.Add(subscriptionTTL)
			sess.GraceUntil = nil
			if err := r.subs.Upsert(ctx, sess); err != nil {
				slog.Warn("reconciliation: touch failed, skipping session", "session", sess.SessionID, "err", err)
				res.Failed++
				continue
			}
			res.Unchanged++
			continue
		}

		// state == none: membership lost.
		if sess.GraceUntil == nil {
			// First observed loss → open the grace window and notify once.
			deadline := now.Add(subscriptionGrace)
			sess.GraceUntil = &deadline
			if err := r.subs.Upsert(ctx, sess); err != nil {
				slog.Warn("reconciliation: grace-entry failed, skipping session", "session", sess.SessionID, "err", err)
				res.Failed++
				continue
			}
			r.notifyGrace(ctx, sess)
			res.Grace++
			continue
		}
		if now.Before(*sess.GraceUntil) {
			// In grace, deadline not yet reached: keep waiting for recovery.
			res.Unchanged++
			continue
		}
		// In grace, deadline passed → terminal expiry.
		if err := r.subs.SetStatus(ctx, sess.SessionID, domain.SubExpired); err != nil {
			slog.Warn("reconciliation: expire failed, skipping session", "session", sess.SessionID, "err", err)
			res.Failed++
			continue
		}
		res.Expired++
	}
	return res, nil
}

// Run triggers Reconcile whenever ANY in-use network's chain epoch advances, until ctx is
// done. Epoch is per-network (S0014 p1-3): with a mainnet and a preprod attestor, a new
// epoch on either network re-runs the (network-agnostic) reconcile pass.
func (r *Reconciler) Run(ctx context.Context, pollInterval time.Duration) {
	lastEpoch := map[string]uint64{}
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		nets, err := r.networks(ctx)
		if err != nil {
			slog.Warn("reconciliation: network set query failed", "err", err)
			if !sleep(ctx, pollInterval) {
				return
			}
			continue
		}
		// Probe each in-use network's epoch; remember the new values but only commit them
		// to the cursor after a successful reconcile (so a failed pass retries next poll).
		current := map[string]uint64{}
		advanced := false
		for _, net := range nets {
			src, err := r.srcFor(net)
			if err != nil {
				slog.Warn("reconciliation: source for network failed", "network", net, "err", err)
				continue
			}
			epoch, err := src.Epoch(ctx)
			if err != nil {
				slog.Warn("reconciliation: epoch query failed", "network", net, "err", err)
				continue
			}
			current[net] = epoch
			if epoch > lastEpoch[net] {
				advanced = true
			}
		}
		if advanced {
			if res, err := r.Reconcile(ctx); err != nil {
				slog.Warn("reconciliation failed", "err", err) // keep cursor; retry next poll
			} else {
				for net, e := range current {
					lastEpoch[net] = e
				}
				slog.Info("reconciliation complete", "epochs", current,
					"checked", res.Checked, "expired", res.Expired, "grace", res.Grace,
					"unchanged", res.Unchanged, "failed", res.Failed)
			}
		}
		if !sleep(ctx, pollInterval) {
			return
		}
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
