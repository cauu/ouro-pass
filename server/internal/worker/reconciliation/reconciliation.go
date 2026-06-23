// Package reconciliation re-evaluates membership at each epoch boundary
// (detailed §3.3/§6, C7): eligibility is recomputed from the live snapshot and
// subscription sessions are downgraded (tier change) or expired (no longer
// eligible) accordingly. It runs as a long-lived worker that triggers when the
// chain epoch advances; Reconcile is directly callable and unit-tested.
package reconciliation

import (
	"context"
	"log/slog"
	"time"

	"ouro-pass/server/internal/core/rules"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

// Eligibilizer re-evaluates a credential's current eligibility.
type Eligibilizer interface {
	Eligibility(ctx context.Context, stakeCredentialHash string) (bool, rules.Decision, error)
}

// Reconciler maintains subscription sessions against current eligibility.
type Reconciler struct {
	poolID string
	subs   *store.SubscriptionRepo
	elig   Eligibilizer
	source chain.Source
	now    func() time.Time
}

// New builds a reconciler.
func New(st *store.Store, elig Eligibilizer, source chain.Source, poolID string) *Reconciler {
	return &Reconciler{poolID: poolID, subs: st.Subscriptions(), elig: elig, source: source, now: time.Now}
}

// Result summarizes a reconciliation pass.
type Result struct {
	Checked    int
	Downgraded int
	Expired    int
	Unchanged  int
	Failed     int // per-session errors that were isolated (logged, skipped)
}

// Reconcile re-evaluates every active session once: ineligible → expired;
// tier change → downgrade/upgrade with refreshed entitlements; otherwise the
// verification timestamp is refreshed. A per-session error (transient chain
// query or store write) is fault-isolated — logged, counted, and skipped — so
// one bad credential never blocks revocation/downgrade for the rest of the pool
// (p12-3). Only a whole-pass failure (listing active sessions) returns an error.
func (r *Reconciler) Reconcile(ctx context.Context) (Result, error) {
	sessions, err := r.subs.ListActive(ctx, r.poolID)
	if err != nil {
		return Result{}, err
	}
	var res Result
	for _, sess := range sessions {
		res.Checked++
		eligible, decision, err := r.elig.Eligibility(ctx, sess.StakeCredentialHash)
		if err != nil {
			slog.Warn("reconciliation: eligibility failed, skipping session",
				"session", sess.SessionID, "err", err)
			res.Failed++
			continue
		}
		if !eligible {
			if err := r.subs.SetStatus(ctx, sess.SessionID, domain.SubExpired); err != nil {
				slog.Warn("reconciliation: expire failed, skipping session", "session", sess.SessionID, "err", err)
				res.Failed++
				continue
			}
			res.Expired++
			continue
		}
		now := r.now()
		if decision.Tier != sess.Tier {
			sess.Tier = decision.Tier
			sess.Entitlements = decision.Entitlements
			sess.Topics = decision.Entitlements
			sess.LastVerifiedAt = now
			if err := r.subs.Upsert(ctx, sess); err != nil {
				slog.Warn("reconciliation: downgrade failed, skipping session", "session", sess.SessionID, "err", err)
				res.Failed++
				continue
			}
			res.Downgraded++
			continue
		}
		sess.LastVerifiedAt = now
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
					"checked", res.Checked, "downgraded", res.Downgraded,
					"expired", res.Expired, "failed", res.Failed)
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
