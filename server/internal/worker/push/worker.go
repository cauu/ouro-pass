package push

import (
	"context"
	"log/slog"
	"time"

	"ouro-pass/server/internal/store"
)

// Worker polls for due scheduled push jobs and runs each through the Scheduler
// until ctx is cancelled. It is the runtime driver that was previously missing,
// so admin-created PushJobs are actually delivered (p12-4).
type Worker struct {
	jobs  *store.PushJobRepo
	sched *Scheduler
	poll  time.Duration
	batch int
	now   func() time.Time
}

// NewWorker builds a push worker delivering through sender at the given options.
func NewWorker(st *store.Store, sender Sender, poll time.Duration, opt Options) *Worker {
	if poll <= 0 {
		poll = 15 * time.Second
	}
	return &Worker{jobs: st.PushJobs(), sched: NewScheduler(st, sender, opt), poll: poll, batch: 50, now: time.Now}
}

// Run drains due jobs each tick until ctx ends.
func (w *Worker) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		jobs, err := w.jobs.ListScheduled(ctx, w.now(), w.batch)
		if err != nil {
			slog.Warn("push worker: list scheduled failed", "err", err)
		}
		for _, j := range jobs {
			if ctx.Err() != nil {
				return
			}
			if res, err := w.sched.Run(ctx, j); err != nil {
				slog.Warn("push worker: job failed", "job", j.JobID, "err", err)
			} else {
				slog.Info("push worker: job done", "job", j.JobID, "sent", res.Sent, "failed", res.Failed)
			}
		}
		if !sleep(ctx, w.poll) {
			return
		}
	}
}
