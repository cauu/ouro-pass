package telegram

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
)

// Runner is the per-instance unit the Supervisor manages; *Worker satisfies it.
type Runner interface{ Run(ctx context.Context) }

// Factory builds a Runner for one channel instance — it decrypts the instance
// token and binds a transport + instance-scoped processor. Returning an error
// (e.g. an unconfigured or undecryptable token) makes the supervisor skip the
// instance this tick and retry on the next.
type Factory func(inst domain.ChannelConfig) (Runner, error)

// Supervisor reconciles the set of active Telegram channel instances against the
// set of running per-instance workers (S0005 p2-1, D3): it starts a worker for
// each new or re-tokened instance and cancels the worker of any instance that is
// removed, disabled, or changed. A single supervisor goroutine owns the running
// map, so an instance is never run twice (C4) and every child worker is bound to
// a child context that is cancelled on stop or shutdown (no goroutine leak).
type Supervisor struct {
	channels *store.ChannelConfigRepo
	poolID   string
	factory  Factory
	interval time.Duration
}

type runningWorker struct {
	cancel      context.CancelFunc
	fingerprint string
}

// NewSupervisor builds a supervisor reconciling poolID's telegram instances
// every interval through factory. All instances come from the admin DB (S0017:
// no env-token fallback instance).
func NewSupervisor(st *store.Store, factory Factory, poolID string, interval time.Duration) *Supervisor {
	if interval <= 0 {
		interval = time.Second
	}
	return &Supervisor{channels: st.Channels(), poolID: poolID, factory: factory, interval: interval}
}

// Run reconciles on every tick until ctx is cancelled, then cancels and joins
// all child workers so shutdown is clean.
func (s *Supervisor) Run(ctx context.Context) {
	running := map[string]runningWorker{}
	var wg sync.WaitGroup
	defer func() {
		for _, rw := range running {
			rw.cancel()
		}
		wg.Wait()
	}()
	for {
		s.reconcile(ctx, running, &wg)
		if !sleep(ctx, s.interval) {
			return
		}
	}
}

// desired computes the instances that should be running now. Returns nil on a
// transient store error so the caller keeps the current set unchanged.
func (s *Supervisor) desired(ctx context.Context) map[string]domain.ChannelConfig {
	active, err := s.channels.ListActive(ctx, s.poolID, "telegram")
	if err != nil {
		slog.Warn("telegram supervisor: list active failed", "err", err)
		return nil
	}
	out := make(map[string]domain.ChannelConfig, len(active))
	for _, inst := range active {
		out[inst.ChannelID] = inst
	}
	return out
}

func (s *Supervisor) reconcile(ctx context.Context, running map[string]runningWorker, wg *sync.WaitGroup) {
	want := s.desired(ctx)
	if want == nil {
		return // transient error: leave the running set as-is
	}
	// Stop workers whose instance is gone, disabled, or changed (token/status).
	for id, rw := range running {
		inst, ok := want[id]
		if !ok || fingerprintOf(inst) != rw.fingerprint {
			rw.cancel()
			delete(running, id)
			slog.Info("telegram supervisor: worker stopped", "instance", id, "reason", stopReason(ok))
		}
	}
	// Start workers for instances not currently running (includes the just-stopped
	// changed ones, which restart with the new fingerprint).
	for id, inst := range want {
		if _, ok := running[id]; ok {
			continue
		}
		runner, err := s.factory(inst)
		if err != nil {
			slog.Warn("telegram supervisor: build worker failed", "instance", id, "name", inst.Name, "err", err)
			continue
		}
		cctx, cancel := context.WithCancel(ctx)
		running[id] = runningWorker{cancel: cancel, fingerprint: fingerprintOf(inst)}
		wg.Add(1)
		go func(r Runner) {
			defer wg.Done()
			r.Run(cctx)
		}(runner)
		slog.Info("telegram supervisor: worker started", "instance", id, "name", inst.Name)
	}
}

// fingerprintOf changes whenever the instance's status or stored config changes,
// so the supervisor restarts a worker after an admin edits its token live.
func fingerprintOf(inst domain.ChannelConfig) string {
	return inst.Status + "|" + inst.UpdatedAt.UTC().Format(time.RFC3339Nano)
}

// stopReason labels why a worker was cancelled: gone (removed/disabled) vs
// changed (token/config edit → restart next loop).
func stopReason(stillWanted bool) string {
	if stillWanted {
		return "changed"
	}
	return "removed-or-disabled"
}
