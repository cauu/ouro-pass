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

// Reconciler maintains subscription sessions against current membership state.
type Reconciler struct {
	poolID string
	subs   *store.SubscriptionRepo
	elig   StateEvaluator
	source chain.Source
	now    func() time.Time
}

// New builds a reconciler.
func New(st *store.Store, elig StateEvaluator, source chain.Source, poolID string) *Reconciler {
	return &Reconciler{poolID: poolID, subs: st.Subscriptions(), elig: elig, source: source, now: time.Now}
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

// Run triggers Reconcile whenever the chain epoch advances, until ctx is done.
func (r *Reconciler) Run(ctx context.Context, pollInterval time.Duration) {
	lastEpoch := uint64(0)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		epoch, err := r.source.Epoch(ctx)
		if err != nil {
			slog.Warn("reconciliation: epoch query failed", "err", err)
		} else if epoch > lastEpoch {
			if res, err := r.Reconcile(ctx); err != nil {
				// Whole-pass failure (couldn't list sessions): do not advance the
				// cursor, retry next poll. Per-session failures are isolated inside
				// Reconcile and do NOT block the epoch advancing.
				slog.Warn("reconciliation failed", "err", err)
			} else {
				lastEpoch = epoch
				slog.Info("reconciliation complete", "epoch", epoch,
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
