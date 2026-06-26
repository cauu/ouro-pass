// Package push is the broadcast scheduler (detailed §7/§9.7): it selects the
// active subscriptions matching a job's tier/topic/entitlement target, sends to
// each recipient through a rate-limited, retrying Sender, and records one
// DeliveryLog row per recipient. The Sender is an interface so delivery is
// unit-tested without a live channel.
package push

import (
	"context"
	"log/slog"
	"slices"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
	"golang.org/x/time/rate"
)

// Sender delivers one message to a channel user.
type Sender interface {
	SendMessage(ctx context.Context, chatID, text string) error
}

// Scheduler runs push jobs.
type Scheduler struct {
	jobs        *store.PushJobRepo
	subs        *store.SubscriptionRepo
	deliveries  *store.DeliveryLogRepo
	sender      Sender
	route       func(domain.PushJob) (Sender, error)
	limiter     *rate.Limiter
	maxAttempts int
	backoff     time.Duration
	now         func() time.Time
}

// Options configures the scheduler.
type Options struct {
	RatePerSec  float64 // global send rate (≈30/s for Telegram)
	Burst       int
	MaxAttempts int
	Backoff     time.Duration
	// Route resolves the Sender for a job — S0005 p3-1 per-instance delivery. When
	// nil the scheduler uses the single fixed Sender (legacy type-level fan-out).
	Route func(domain.PushJob) (Sender, error)
}

// NewScheduler builds a push scheduler.
func NewScheduler(st *store.Store, sender Sender, opt Options) *Scheduler {
	if opt.RatePerSec <= 0 {
		opt.RatePerSec = 30
	}
	if opt.Burst <= 0 {
		opt.Burst = 30
	}
	if opt.MaxAttempts <= 0 {
		opt.MaxAttempts = 3
	}
	if opt.Backoff <= 0 {
		opt.Backoff = 200 * time.Millisecond
	}
	return &Scheduler{
		jobs: st.PushJobs(), subs: st.Subscriptions(), deliveries: st.DeliveryLogs(),
		sender: sender, route: opt.Route,
		limiter:     rate.NewLimiter(rate.Limit(opt.RatePerSec), opt.Burst),
		maxAttempts: opt.MaxAttempts, backoff: opt.Backoff, now: time.Now,
	}
}

// Result summarizes a job run.
type Result struct {
	Sent    int
	Failed  int
	Skipped int
}

// Run delivers a job: it marks the job running, sends to each matching
// recipient with rate limiting and backoff retry, logs every delivery, and
// finalizes the job status (done, or failed if every send failed).
func (s *Scheduler) Run(ctx context.Context, job domain.PushJob) (Result, error) {
	if err := s.jobs.SetStatus(ctx, job.JobID, domain.PushRunning); err != nil {
		return Result{}, err
	}
	// Resolve the per-job sender (S0005 p3-1): a channel-scoped job sends through
	// its instance's transport. A routing failure (e.g. unconfigured token) fails
	// the job rather than silently delivering through the wrong channel.
	sender := s.sender
	if s.route != nil {
		sr, err := s.route(job)
		if err != nil {
			slog.Warn("push: route failed", "job", job.JobID, "err", err)
			_ = s.jobs.SetStatus(ctx, job.JobID, domain.PushFailed)
			return Result{}, err
		}
		sender = sr
	}

	// A channel-scoped job targets exactly that instance's subscribers; an unscoped
	// job keeps the legacy type-level fan-out.
	var candidates []domain.SubscriptionSession
	var err error
	if job.ChannelID != nil && *job.ChannelID != "" {
		candidates, err = s.subs.ListActiveByInstance(ctx, *job.ChannelID)
	} else {
		candidates, err = s.subs.ListActiveByChannel(ctx, job.PoolID, job.ChannelType)
	}
	if err != nil {
		return Result{}, err
	}

	var res Result
	for _, sess := range candidates {
		if ctx.Err() != nil {
			break // shutting down: stop sending promptly
		}
		if !matches(job, sess) {
			continue
		}
		attempts, sendErr := s.deliver(ctx, sender, sess.ChannelUserID, job.Title+"\n"+job.Content)
		status := domain.DeliverySent
		var errMsg *string
		if sendErr != nil {
			status = domain.DeliveryFailed
			m := sendErr.Error()
			errMsg = &m
			res.Failed++
		} else {
			res.Sent++
		}
		// The DeliveryLog is the audit record (TC-10); a write failure must be
		// surfaced, not silently dropped (p12-4).
		if err := s.deliveries.Append(ctx, domain.DeliveryLog{
			DeliveryID: crypto.RandomID(), JobID: job.JobID, SessionID: sess.SessionID,
			ChannelType: job.ChannelType, ChannelUserID: sess.ChannelUserID, Status: status,
			RetryCount: attempts - 1, ErrorMessage: errMsg, SentAt: ptrTime(s.now()),
		}); err != nil {
			slog.Warn("push: delivery-log write failed", "job", job.JobID, "session", sess.SessionID, "err", err)
		}
	}

	final := domain.PushDone
	if res.Sent == 0 && res.Failed > 0 {
		final = domain.PushFailed
	}
	if err := s.jobs.SetStatus(ctx, job.JobID, final); err != nil {
		return res, err
	}
	return res, nil
}

// deliver sends with rate limiting + bounded backoff retry; returns attempts.
// One rate-limit token is spent per recipient (not per retry), so a flaky
// recipient can't drain the shared send budget for everyone else (p12-4).
func (s *Scheduler) deliver(ctx context.Context, sender Sender, chatID, text string) (int, error) {
	if err := s.limiter.Wait(ctx); err != nil {
		return 0, err
	}
	var lastErr error
	for attempt := 1; attempt <= s.maxAttempts; attempt++ {
		if lastErr = sender.SendMessage(ctx, chatID, text); lastErr == nil {
			return attempt, nil
		}
		if attempt < s.maxAttempts {
			if !sleep(ctx, s.backoff*time.Duration(attempt)) {
				return attempt, ctx.Err()
			}
		}
	}
	return s.maxAttempts, lastErr
}

// matches applies the job's tier/topic/entitlement target filter to a session
// (three-way combinable filter, detailed §7.1).
func matches(job domain.PushJob, sess domain.SubscriptionSession) bool {
	if job.TargetTier != nil && *job.TargetTier != "" && sess.Tier != *job.TargetTier {
		return false
	}
	if job.RequiredEntitlement != nil && *job.RequiredEntitlement != "" && !slices.Contains(sess.Entitlements, *job.RequiredEntitlement) {
		return false
	}
	if job.TargetTopic != nil && *job.TargetTopic != "" && !slices.Contains(sess.Topics, *job.TargetTopic) {
		return false
	}
	return true
}

func ptrTime(t time.Time) *time.Time { return &t }

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
