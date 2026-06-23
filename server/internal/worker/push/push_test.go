package push

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/store"
)

// recordingSender records sends and can fail the first N attempts per chat.
type recordingSender struct {
	mu       sync.Mutex
	sent     map[string]int
	failFor  map[string]int // chatID → remaining failures
}

func newSender() *recordingSender {
	return &recordingSender{sent: map[string]int{}, failFor: map[string]int{}}
}

func (s *recordingSender) SendMessage(_ context.Context, chatID, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failFor[chatID] > 0 {
		s.failFor[chatID]--
		return errors.New("transient send failure")
	}
	s.sent[chatID]++
	return nil
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

func seedSession(t *testing.T, st *store.Store, id, user, tier string, topics, ents []string) {
	t.Helper()
	now := time.Now()
	if err := st.Subscriptions().Upsert(context.Background(), domain.SubscriptionSession{
		SessionID: id, PoolID: "pool1", StakeCredentialHash: "h-" + id, ChannelType: "telegram",
		ChannelUserID: user, Status: domain.SubActive, Tier: tier, Topics: topics, Entitlements: ents,
		CreatedAt: now, LastVerifiedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func fastScheduler(st *store.Store, sender Sender) *Scheduler {
	return NewScheduler(st, sender, Options{RatePerSec: 100000, Burst: 1000, MaxAttempts: 3, Backoff: time.Millisecond})
}

func TestRun_TierTargetingAndDeliveryLog(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seedSession(t, st, "s1", "u1", "gold", []string{"news"}, []string{"read"})
	seedSession(t, st, "s2", "u2", "silver", []string{"news"}, []string{"read"})
	seedSession(t, st, "s3", "u3", "gold", []string{"news"}, []string{"read"})

	sender := newSender()
	sch := fastScheduler(st, sender)
	job := domain.PushJob{
		JobID: "job1", PoolID: "pool1", Title: "Hi", Content: "body", ChannelType: "telegram",
		TargetTier: ptrStr("gold"), Status: domain.PushScheduled, CreatedBy: "admin1", CreatedAt: time.Now(),
	}
	st.PushJobs().Create(ctx, job)

	res, err := sch.Run(ctx, job)
	if err != nil {
		t.Fatal(err)
	}
	// Only the two gold members receive it.
	if res.Sent != 2 || res.Failed != 0 {
		t.Fatalf("result = %+v, want sent=2", res)
	}
	if sender.sent["u1"] != 1 || sender.sent["u3"] != 1 || sender.sent["u2"] != 0 {
		t.Fatalf("sends = %v", sender.sent)
	}
	sent, _ := st.DeliveryLogs().CountByStatus(ctx, "job1", domain.DeliverySent)
	if sent != 2 {
		t.Fatalf("delivery log sent = %d, want 2", sent)
	}
	j, _ := st.PushJobs().Get(ctx, "job1")
	if j.Status != domain.PushDone {
		t.Fatalf("job status = %s, want done", j.Status)
	}
}

func TestRun_EntitlementAndTopicFilter(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seedSession(t, st, "s1", "u1", "gold", []string{"news", "alerts"}, []string{"read", "vip"})
	seedSession(t, st, "s2", "u2", "gold", []string{"news"}, []string{"read"})

	sender := newSender()
	sch := fastScheduler(st, sender)
	job := domain.PushJob{
		JobID: "j2", PoolID: "pool1", Title: "VIP", Content: "x", ChannelType: "telegram",
		RequiredEntitlement: ptrStr("vip"), TargetTopic: ptrStr("alerts"),
		Status: domain.PushScheduled, CreatedBy: "a", CreatedAt: time.Now(),
	}
	st.PushJobs().Create(ctx, job)
	res, _ := sch.Run(ctx, job)
	// Only u1 has both vip entitlement and alerts topic.
	if res.Sent != 1 || sender.sent["u1"] != 1 || sender.sent["u2"] != 0 {
		t.Fatalf("filtered result=%+v sends=%v", res, sender.sent)
	}
}

func TestDeliver_RetriesThenSucceeds(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seedSession(t, st, "s1", "u1", "gold", nil, nil)
	sender := newSender()
	sender.failFor["u1"] = 2 // fail twice, succeed on 3rd attempt
	sch := fastScheduler(st, sender)
	job := domain.PushJob{JobID: "j3", PoolID: "pool1", Title: "t", Content: "c", ChannelType: "telegram", Status: domain.PushScheduled, CreatedBy: "a", CreatedAt: time.Now()}
	st.PushJobs().Create(ctx, job)

	res, _ := sch.Run(ctx, job)
	if res.Sent != 1 {
		t.Fatalf("expected eventual success: %+v", res)
	}
	// retry_count recorded = attempts-1 = 2.
	if sender.sent["u1"] != 1 {
		t.Fatalf("sends = %v", sender.sent)
	}
}

func TestDeliver_ExhaustsRetriesAndFails(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	seedSession(t, st, "s1", "u1", "gold", nil, nil)
	sender := newSender()
	sender.failFor["u1"] = 99 // always fails
	sch := fastScheduler(st, sender)
	job := domain.PushJob{JobID: "j4", PoolID: "pool1", Title: "t", Content: "c", ChannelType: "telegram", Status: domain.PushScheduled, CreatedBy: "a", CreatedAt: time.Now()}
	st.PushJobs().Create(ctx, job)

	res, _ := sch.Run(ctx, job)
	if res.Failed != 1 || res.Sent != 0 {
		t.Fatalf("result = %+v, want failed=1", res)
	}
	failed, _ := st.DeliveryLogs().CountByStatus(ctx, "j4", domain.DeliveryFailed)
	if failed != 1 {
		t.Fatalf("failed deliveries = %d", failed)
	}
	j, _ := st.PushJobs().Get(ctx, "j4")
	if j.Status != domain.PushFailed {
		t.Fatalf("job status = %s, want failed", j.Status)
	}
}

func ptrStr(s string) *string { return &s }
