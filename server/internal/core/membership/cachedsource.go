package membership

import (
	"context"
	"time"

	"golang.org/x/sync/singleflight"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
)

// CachedSource wraps a chain.Source with the active-only membership cache (S0004
// §2.3, D4). It implements chain.Source, so the eligibility path is unchanged.
//
// Only `active` snapshots are cached — `active` derives solely from epoch-stable
// active-stake history, so it is safe to reuse within an epoch. A hit requires
// the cached row's epoch to equal the *locally computed* current epoch (D7) — no
// chain round-trip to learn the epoch. pending/none are never cached: they hinge
// on live delegation, so they are recomputed every call, making onboarding
// (none→pending) and bail (pending→none) immediate and symmetric.
//
// Failure policy (D8) is the caller's: CachedSource propagates origin errors
// unchanged. The login/issue path treats them as fail-closed; the reconciler
// treats them as soft fail-open.
type CachedSource struct {
	inner   chain.Source
	cache   *store.SnapshotCacheRepo
	network string
	timeout time.Duration
	now     func() time.Time
	sf      singleflight.Group
}

// NewCachedSource wraps src (a single network's source) with the active-membership
// cache (S0006 p5-1: pool-agnostic — caches the credential's real active pool, so
// every pool_stake attestor on the network shares one snapshot). A non-positive
// timeout defaults to 10s.
func NewCachedSource(src chain.Source, cache *store.SnapshotCacheRepo, network string, timeout time.Duration) *CachedSource {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &CachedSource{inner: src, cache: cache, network: network, timeout: timeout, now: time.Now}
}

// Name identifies the wrapped source.
func (c *CachedSource) Name() string { return c.inner.Name() + "+cache" }

// Epoch passes through to the underlying source (the live chain tip).
func (c *CachedSource) Epoch(ctx context.Context) (uint64, error) { return c.inner.Epoch(ctx) }

// Delegators forwards the optional delegator-enumeration capability to the inner
// source (S0004 §2.7) — a cold admin query, never cached. Returns
// chain.ErrNotImplemented if the wrapped source can't enumerate delegators.
func (c *CachedSource) Delegators(ctx context.Context, poolID string, page int) ([]string, error) {
	dl, ok := c.inner.(chain.DelegatorLister)
	if !ok {
		return nil, chain.ErrNotImplemented
	}
	return dl.Delegators(ctx, poolID, page)
}

// Snapshot serves the active cache on a same-epoch hit, else fetches live
// (single-flighted) and caches iff the credential is active with our pool.
func (c *CachedSource) Snapshot(ctx context.Context, sch string) (*chain.Snapshot, error) {
	e, eok := chain.CurrentEpoch(c.network, c.now())
	if eok {
		if row, err := c.cache.Get(ctx, sch, c.network); err == nil && row.SnapshotEpoch == int64(e) {
			return c.cachedToSnapshot(sch, e, row), nil // hit: zero chain I/O, active by construction
		}
	}
	// Miss / stale / unknown epoch → single-flight the origin fetch so a thundering
	// herd on one credential collapses to a single chain call.
	v, err, _ := c.sf.Do(sch, func() (any, error) {
		fctx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()
		return c.inner.Snapshot(fctx, sch)
	})
	if err != nil {
		return nil, err // D8: caller decides fail-closed vs soft fail-open
	}
	snap := v.(*chain.Snapshot)
	if snap.ActiveStakePoolID != "" {
		// Active somewhere → epoch-stable, cache the real active pool (pool-agnostic).
		epoch := snap.Epoch
		if eok {
			epoch = e // label with our local epoch so the row hits for the rest of it
		}
		_ = c.cache.Upsert(ctx, snapshotToCache(snap, c.network, int64(epoch)))
	} else {
		// Active nowhere (pending/none): drop any stale active row so we never serve it.
		_ = c.cache.Delete(ctx, sch, c.network)
	}
	return snap, nil
}

// cachedToSnapshot reconstructs a full active snapshot from a same-epoch cache
// row. The row only ever means "active with DelegatedPoolID", so both delegation
// fields are that pool and the account is registered by construction.
func (c *CachedSource) cachedToSnapshot(sch string, epoch uint64, row *domain.StakeSnapshotCache) *chain.Snapshot {
	pool := derefStr(row.DelegatedPoolID) // the credential's real active pool
	return &chain.Snapshot{
		StakeCredentialHash: sch,
		Epoch:               epoch,
		DelegatedPoolID:     pool, // cached rows are active; live delegation tracked as the active pool
		ActiveStakePoolID:   pool, // the credential's real active pool
		ActiveStakeLovelace: derefStr(row.ActiveStakeLovelace),
		RewardsLovelace:     derefStr(row.RewardsLovelace),
		EpochsDelegated:     int(row.EpochsActive),
		AccountStatus:       "registered",
		Source:              row.Source + "+cache",
		FetchedAt:           row.FetchedAt,
	}
}

// snapshotToCache projects an active snapshot into a cache row stamped with the
// given (local) epoch and network, storing the credential's REAL active pool.
func snapshotToCache(snap *chain.Snapshot, network string, epoch int64) domain.StakeSnapshotCache {
	return domain.StakeSnapshotCache{
		StakeCredentialHash: snap.StakeCredentialHash,
		Network:             network,
		SnapshotEpoch:       epoch,
		DelegatedPoolID:     strPtrOrNil(snap.ActiveStakePoolID),
		ActiveStakeLovelace: strPtrOrNil(snap.ActiveStakeLovelace),
		RewardsLovelace:     strPtrOrNil(snap.RewardsLovelace),
		EpochsActive:        int64(snap.EpochsDelegated),
		Source:              snap.Source,
		FetchedAt:           snap.FetchedAt,
	}
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func strPtrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
