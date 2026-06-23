package store

import (
	"context"
	"testing"
	"time"

	"github.com/poolops/issuer/internal/domain"
)

func TestPushJobAndDelivery(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now()

	j := domain.PushJob{
		JobID: "job1", PoolID: "pool1", Title: "Hi", Content: "body", ChannelType: "telegram",
		TargetTier: ptr("gold"), Status: domain.PushScheduled, CreatedBy: "admin1", CreatedAt: now,
	}
	if err := st.PushJobs().Create(ctx, j); err != nil {
		t.Fatal(err)
	}
	got, err := st.PushJobs().Get(ctx, "job1")
	if err != nil || got.TargetTier == nil || *got.TargetTier != "gold" || got.Status != domain.PushScheduled {
		t.Fatalf("get: %v %+v", err, got)
	}
	if err := st.PushJobs().SetStatus(ctx, "job1", domain.PushRunning); err != nil {
		t.Fatal(err)
	}

	for i, s := range []domain.DeliveryStatus{domain.DeliverySent, domain.DeliverySent, domain.DeliveryFailed} {
		_ = i
		if err := st.DeliveryLogs().Append(ctx, domain.DeliveryLog{
			DeliveryID: "d" + string(rune('a'+i)), JobID: "job1", SessionID: "s1",
			ChannelType: "telegram", ChannelUserID: "tg-1", Status: s, RetryCount: 0,
		}); err != nil {
			t.Fatal(err)
		}
	}
	sent, _ := st.DeliveryLogs().CountByStatus(ctx, "job1", domain.DeliverySent)
	failed, _ := st.DeliveryLogs().CountByStatus(ctx, "job1", domain.DeliveryFailed)
	if sent != 2 || failed != 1 {
		t.Fatalf("delivery counts sent=%d failed=%d, want 2/1", sent, failed)
	}
}

func TestAdminUserSessionAudit(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Millisecond)

	u := domain.AdminUser{
		AdminID: "admin1", PoolID: "pool1", OwnerKeyHash: "okh1", Role: domain.RoleOwner, CreatedAt: now,
	}
	if err := st.AdminUsers().Upsert(ctx, u); err != nil {
		t.Fatal(err)
	}
	got, err := st.AdminUsers().GetByOwnerKeyHash(ctx, "okh1")
	if err != nil || got.Role != domain.RoleOwner {
		t.Fatalf("get admin: %v %+v", err, got)
	}
	if err := st.AdminUsers().TouchLogin(ctx, "admin1", now); err != nil {
		t.Fatal(err)
	}

	// Session: valid then expired.
	sess := domain.AdminSession{SessionToken: "thash", AdminID: "admin1", ExpiresAt: now.Add(time.Hour), CreatedAt: now}
	if err := st.AdminSessions().Create(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AdminSessions().GetValid(ctx, "thash", now); err != nil {
		t.Fatalf("valid session: %v", err)
	}
	if _, err := st.AdminSessions().GetValid(ctx, "thash", now.Add(2*time.Hour)); err != domain.ErrExpired {
		t.Fatalf("expired session: %v, want ErrExpired", err)
	}
	st.AdminSessions().Delete(ctx, "thash")
	if _, err := st.AdminSessions().GetValid(ctx, "thash", now); err != domain.ErrNotFound {
		t.Fatalf("deleted session: %v, want ErrNotFound", err)
	}

	// Audit append + recent.
	for _, act := range []string{"issuer_key.rotate", "rule.update", "push.create"} {
		if err := st.Audit().Append(ctx, domain.AuditLog{
			AuditID: "a-" + act, Actor: "admin1", Action: act, Target: "x", CreatedAt: now.Add(time.Duration(len(act)) * time.Second),
		}); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := st.Audit().Recent(ctx, 10)
	if err != nil || len(recent) != 3 {
		t.Fatalf("recent audit: %v len=%d", err, len(recent))
	}
}
