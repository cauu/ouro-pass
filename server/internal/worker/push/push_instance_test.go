package push

import (
	"context"
	"errors"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
)

func seedInstanceSession(t *testing.T, st *store.Store, id, channelID, user string) {
	t.Helper()
	now := time.Now()
	if err := st.Subscriptions().Upsert(context.Background(), domain.SubscriptionSession{
		SessionID: id, PoolID: "pool1", StakeCredentialHash: "h-" + id, ChannelID: channelID,
		ChannelType: "telegram", ChannelUserID: user, Status: domain.SubActive, Tier: "gold",
		CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

// TestRun_ChannelScopedRoutesToInstance proves S0005 p3-1 / TC-5: a channel-
// scoped job delivers only to that instance's subscribers and sends through that
// instance's transport — no cross-instance leakage.
func TestRun_ChannelScopedRoutesToInstance(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	// Two instances; the same human even appears under both (u-shared).
	seedInstanceSession(t, st, "s1", "c1", "u1")
	seedInstanceSession(t, st, "s2", "c1", "u-shared")
	seedInstanceSession(t, st, "s3", "c2", "u2")
	seedInstanceSession(t, st, "s4", "c2", "u-shared")

	senderA, senderB := newSender(), newSender()
	route := func(job domain.PushJob) (Sender, error) {
		if job.ChannelID != nil && *job.ChannelID == "c1" {
			return senderA, nil
		}
		return senderB, nil
	}
	sch := NewScheduler(st, senderA, Options{RatePerSec: 100000, Burst: 1000, MaxAttempts: 3, Backoff: time.Millisecond, Route: route})

	cid := "c1"
	job := domain.PushJob{
		JobID: "job-c1", PoolID: "pool1", Title: "Hi", Content: "b", ChannelID: &cid, ChannelType: "telegram",
		Status: domain.PushScheduled, CreatedBy: "admin1", CreatedAt: time.Now(),
	}
	if err := st.PushJobs().Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	res, err := sch.Run(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	// Only c1's two subscribers, via senderA; senderB (c2) untouched.
	if res.Sent != 2 {
		t.Fatalf("sent = %d, want 2 (only c1 subscribers)", res.Sent)
	}
	if senderA.sent["u1"] != 1 || senderA.sent["u-shared"] != 1 {
		t.Fatalf("senderA misses c1 subscribers: %+v", senderA.sent)
	}
	if senderB.sent["u2"] != 0 || senderB.sent["u-shared"] != 0 {
		t.Fatalf("senderB (c2) should receive nothing for a c1 job: %+v", senderB.sent)
	}
}

// TestRun_RouteFailureFailsJob proves a routing error (e.g. unconfigured instance
// token) fails the job instead of misdelivering through a fallback channel.
func TestRun_RouteFailureFailsJob(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seedInstanceSession(t, st, "s1", "c1", "u1")

	sender := newSender()
	route := func(job domain.PushJob) (Sender, error) { return nil, errors.New("instance token missing") }
	sch := NewScheduler(st, sender, Options{RatePerSec: 100000, Burst: 1000, MaxAttempts: 3, Backoff: time.Millisecond, Route: route})

	cid := "c1"
	job := domain.PushJob{
		JobID: "job-bad", PoolID: "pool1", Title: "Hi", Content: "b", ChannelID: &cid, ChannelType: "telegram",
		Status: domain.PushScheduled, CreatedBy: "admin1", CreatedAt: time.Now(),
	}
	if err := st.PushJobs().Create(ctx, job); err != nil {
		t.Fatal(err)
	}
	if _, err := sch.Run(ctx, job); err == nil {
		t.Fatal("expected route failure to surface as an error")
	}
	if len(sender.sent) != 0 {
		t.Fatalf("no message should be sent on route failure, got %+v", sender.sent)
	}
	got, _ := st.PushJobs().Get(ctx, "job-bad")
	if got.Status != domain.PushFailed {
		t.Fatalf("job status = %s, want failed", got.Status)
	}
}
