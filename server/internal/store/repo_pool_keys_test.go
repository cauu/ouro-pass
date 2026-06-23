package store

import (
	"context"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
)

// migratedStore opens a test store and applies the real embedded migrations,
// exercising the production schema on SQLite (TC-2).
func migratedStore(t *testing.T) *Store {
	t.Helper()
	st := openTestStore(t)
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatalf("embedded migrate: %v", err)
	}
	return st
}

func ptr[T any](v T) *T { return &v }

func TestPoolConfigRepo_RoundTrip(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Millisecond)

	in := domain.PoolConfig{
		PoolID: "pool1abc", Ticker: "PAO", Name: ptr("Ouro Pass"),
		Network: "preview", CreatedAt: now, UpdatedAt: now,
	}
	if err := st.PoolConfig().Upsert(ctx, in); err != nil {
		t.Fatalf("upsert: %v", err)
	}
	got, err := st.PoolConfig().Get(ctx, "pool1abc")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Ticker != "PAO" || got.Name == nil || *got.Name != "Ouro Pass" || got.Network != "preview" {
		t.Fatalf("mismatch: %+v", got)
	}
	if !got.CreatedAt.Equal(now.UTC()) {
		t.Errorf("created_at = %v, want %v", got.CreatedAt, now.UTC())
	}

	// Upsert updates in place.
	in.Ticker = "PAO2"
	in.UpdatedAt = now.Add(time.Hour)
	if err := st.PoolConfig().Upsert(ctx, in); err != nil {
		t.Fatalf("re-upsert: %v", err)
	}
	got, _ = st.PoolConfig().Get(ctx, "pool1abc")
	if got.Ticker != "PAO2" {
		t.Errorf("ticker after update = %q", got.Ticker)
	}

	if _, err := st.PoolConfig().Get(ctx, "nope"); err != domain.ErrNotFound {
		t.Errorf("missing pool: err = %v, want ErrNotFound", err)
	}
}

func TestIssuerKeyRepo_LifecycleAndQueries(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Millisecond)

	k := domain.IssuerKey{
		KID: "op-issuer-2026-08", PublicKey: []byte{1, 2, 3},
		EncryptedPrivateKey: []byte{4, 5, 6}, Status: domain.KeyActive,
		ValidFrom: ptr(now), CreatedAt: now,
	}
	if err := st.IssuerKeys().Create(ctx, k); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := st.IssuerKeys().Get(ctx, k.KID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if string(got.PublicKey) != string(k.PublicKey) || got.Status != domain.KeyActive {
		t.Fatalf("mismatch: %+v", got)
	}
	if got.ValidFrom == nil || !got.ValidFrom.Equal(now.UTC()) {
		t.Errorf("valid_from = %v", got.ValidFrom)
	}
	if got.RetiredAt != nil {
		t.Errorf("retired_at should be nil, got %v", got.RetiredAt)
	}

	// Status query.
	active, err := st.IssuerKeys().ListByStatus(ctx, domain.KeyActive)
	if err != nil || len(active) != 1 {
		t.Fatalf("list active: %v len=%d", err, len(active))
	}

	// Transition to retired with timestamp.
	retiredAt := now.Add(48 * time.Hour)
	if err := st.IssuerKeys().SetStatus(ctx, k.KID, domain.KeyRetired, &retiredAt); err != nil {
		t.Fatalf("set status: %v", err)
	}
	got, _ = st.IssuerKeys().Get(ctx, k.KID)
	if got.Status != domain.KeyRetired || got.RetiredAt == nil || !got.RetiredAt.Equal(retiredAt.UTC()) {
		t.Fatalf("after retire: status=%s retired=%v", got.Status, got.RetiredAt)
	}
	if active, _ := st.IssuerKeys().ListByStatus(ctx, domain.KeyActive); len(active) != 0 {
		t.Errorf("active after retire = %d, want 0", len(active))
	}
}
