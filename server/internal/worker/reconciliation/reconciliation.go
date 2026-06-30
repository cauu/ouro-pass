// Package reconciliation re-derives pool-membership at each epoch boundary
// (S0004 §2.3, C7): the three-state membership is recomputed from the live
// snapshot and subscription sessions are expired once the credential is no longer
// a member (state `none`). Tier is first-party and recomputed at consumption
// (token issue / channel activation), not reconciled here. It runs as a
// long-lived worker that triggers when the chain epoch advances; Reconcile is
// directly callable and unit-tested.
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

// StateEvaluator re-derives a credential's current pool-membership state.
type StateEvaluator interface {
	Membership(ctx context.Context, stakeCredentialHash string) (membership.State, error)
}

// SourceFor resolves a read-only chain source for a network. Networks resolves the set of
// networks currently in use by active attestors (S0014 p1-3): the reconciler triggers when
// ANY of those networks' epoch advances, since epoch is per-network.
type (
	SourceFor func(network string) (chain.Source, error)
	Networks  func(ctx context.Context) ([]string, error)
)

// Reconciler maintains subscription sessions against current membership state.
type Reconciler struct {
	poolID   string
	subs     *store.SubscriptionRepo
	elig     StateEvaluator
	srcFor   SourceFor
	networks Networks
	now      func() time.Time
}

// New builds a reconciler. Reconcile is network-agnostic (it re-derives via elig, which
// evaluates each attestor on its own network); srcFor+networks are used only to watch
// per-network epoch advances as the trigger (S0014 p1-3).
func New(st *store.Store, elig StateEvaluator, srcFor SourceFor, networks Networks, poolID string) *Reconciler {
	return &Reconciler{poolID: poolID, subs: st.Subscriptions(), elig: elig, srcFor: srcFor, networks: networks, now: time.Now}
}

// Result summarizes a reconciliation pass.
type Result struct {
	Checked   int
	Expired   int
	Unchanged int
	Failed    int // per-session errors that were isolated (logged, skipped)
}

// Reconcile re-derives every active session's membership once: state `none` →
// expired; still a member (active/pending) → the verification timestamp is
// refreshed. A per-session error is fault-isolated — logged, counted, and the
// session left untouched (D8 soft fail-open: never expire a member on a transient
// chain error) — so one bad credential never blocks revocation for the rest of
// the pool (p12-3). Only a whole-pass failure (listing sessions) returns an error.
func (r *Reconciler) Reconcile(ctx context.Context) (Result, error) {
	sessions, err := r.subs.ListActive(ctx, r.poolID)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, sess := range sessions {
		res.Checked++
		state, err := r.elig.Membership(ctx, sess.StakeCredentialHash)
		if err != nil {
			slog.Warn("reconciliation: membership check failed, keeping session",
				"session", sess.SessionID, "err", err)
			res.Failed++
			continue
		}
		if state == membership.StateNone {
			if err := r.subs.SetStatus(ctx, sess.SessionID, domain.SubExpired); err != nil {
				slog.Warn("reconciliation: expire failed, skipping session", "session", sess.SessionID, "err", err)
				res.Failed++
				continue
			}
			res.Expired++
			continue
		}
		// Still a member (active or pending): refresh the verification timestamp.
		sess.LastVerifiedAt = r.now()
		if err := r.subs.Upsert(ctx, sess); err != nil {
			slog.Warn("reconciliation: touch failed, skipping session", "session", sess.SessionID, "err", err)
			res.Failed++
			continue
		}
		res.Unchanged++
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
					"checked", res.Checked, "expired", res.Expired,
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
