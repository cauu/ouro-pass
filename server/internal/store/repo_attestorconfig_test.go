package store

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"ouro-pass/server/internal/domain"
)

func mkAttestor(id, label, pool string, now time.Time) domain.AttestorConfig {
	params, _ := json.Marshal(map[string]string{"pool_id": pool, "network": "mainnet"})
	return domain.AttestorConfig{
		AttestorID: id, Kind: "pool_stake", Label: label, Params: params,
		Status: domain.AttestorActive, CreatedAt: now, UpdatedAt: now,
	}
}

func TestAttestorConfigRepo_CRUD(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)
	now := time.Now().Truncate(time.Second)

	if err := st.Attestors().Create(ctx, mkAttestor("a1", "Alpha", "poolA", now)); err != nil {
		t.Fatalf("create a1: %v", err)
	}
	if err := st.Attestors().Create(ctx, mkAttestor("a2", "Beta", "poolB", now)); err != nil {
		t.Fatalf("create a2: %v", err)
	}
	// Duplicate (kind, label) must violate the unique constraint.
	if err := st.Attestors().Create(ctx, mkAttestor("a3", "Alpha", "poolC", now)); err == nil {
		t.Fatal("duplicate (kind,label) should fail")
	}

	list, err := st.Attestors().List(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list: %v len=%d", err, len(list))
	}

	got, err := st.Attestors().Get(ctx, "a1")
	if err != nil || got.Label != "Alpha" {
		t.Fatalf("get a1: %v %+v", err, got)
	}
	var p map[string]string
	_ = json.Unmarshal(got.Params, &p)
	if p["pool_id"] != "poolA" {
		t.Fatalf("params: %v", p)
	}

	// Update label/params/status.
	got.Label = "Alpha2"
	got.Params = json.RawMessage(`{"pool_id":"poolA","network":"preprod"}`)
	got.UpdatedAt = now.Add(time.Minute)
	if err := st.Attestors().Update(ctx, *got); err != nil {
		t.Fatalf("update: %v", err)
	}
	if g2, _ := st.Attestors().Get(ctx, "a1"); g2.Label != "Alpha2" || !strings.Contains(string(g2.Params), "preprod") {
		t.Fatalf("after update: %+v", g2)
	}

	// Disable a2 → excluded from ListActive but present in List.
	if err := st.Attestors().SetStatus(ctx, "a2", domain.AttestorDisabled, now); err != nil {
		t.Fatalf("setstatus: %v", err)
	}
	active, _ := st.Attestors().ListActive(ctx)
	if len(active) != 1 || active[0].AttestorID != "a1" {
		t.Fatalf("listactive: %+v", active)
	}

	// Delete → Get returns ErrNotFound; deleting again is ErrNotFound.
	if err := st.Attestors().Delete(ctx, "a1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Attestors().Get(ctx, "a1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("get deleted: %v", err)
	}
	if err := st.Attestors().Delete(ctx, "a1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestIssuerConfig_TierRules(t *testing.T) {
	ctx := context.Background()
	st := migratedStore(t)

	// Unset → "[]" (no opinion), never ErrNotFound.
	tr, err := st.Issuer().GetTierRules(ctx)
	if err != nil || string(tr) != "[]" {
		t.Fatalf("default: %v %s", err, tr)
	}
	rules := json.RawMessage(`[{"tier":"gold","when":{"fact":"any_active","op":"==","value":"true"}}]`)
	if err := st.Issuer().SetTierRules(ctx, rules, time.Now()); err != nil {
		t.Fatalf("set: %v", err)
	}
	if got, _ := st.Issuer().GetTierRules(ctx); string(got) != string(rules) {
		t.Fatalf("get: %s", got)
	}
	// Upsert again (singleton, no duplicate row).
	if err := st.Issuer().SetTierRules(ctx, json.RawMessage(`[]`), time.Now()); err != nil {
		t.Fatalf("re-set: %v", err)
	}
	if got, _ := st.Issuer().GetTierRules(ctx); string(got) != "[]" {
		t.Fatalf("re-get: %s", got)
	}
}

// migrateUpTo applies the real embedded migrations through maxVersion (a "NNNN"
// prefix), letting a test seed pre-existing rows before a later migration's
// backfill runs.
func migrateUpTo(t *testing.T, st *Store, maxVersion string) {
	t.Helper()
	sub, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	driver := string(st.Driver)
	entries, err := fs.ReadDir(sub, driver)
	if err != nil {
		t.Fatal(err)
	}
	m := fstest.MapFS{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || e.Name()[:4] > maxVersion {
			continue
		}
		body, err := fs.ReadFile(sub, driver+"/"+e.Name())
		if err != nil {
			t.Fatal(err)
		}
		m[driver+"/"+e.Name()] = &fstest.MapFile{Data: body}
	}
	if err := MigrateFS(context.Background(), st, m); err != nil {
		t.Fatalf("partial migrate to %s: %v", maxVersion, err)
	}
}

// TestMigration_BackfillPoolToAttestor: the 0012 migration projects a pre-existing
// pool into one pool_stake attestor and lifts its tier_rules to IssuerConfig (TC-2).
func TestMigration_BackfillPoolToAttestor(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	migrateUpTo(t, st, "0011") // schema with PoolConfig.tier_rules, no AttestorConfig yet
	now := time.Now().Truncate(time.Second)

	if err := st.PoolConfig().Upsert(ctx, domain.PoolConfig{
		PoolID: "pool1xyz", Ticker: "OURO", Name: ptr("Ouro Pool"), Network: "mainnet",
		TierRules: json.RawMessage(`[{"tier":"gold"}]`), CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed pool: %v", err)
	}

	// Apply the rest (0012 backfill runs with the pool present).
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("migrate 0012: %v", err)
	}

	list, err := st.Attestors().List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("backfilled attestors: %v len=%d", err, len(list))
	}
	a := list[0]
	if a.Kind != "pool_stake" || a.Status != domain.AttestorActive || a.Label != "OURO" {
		t.Fatalf("backfilled attestor: %+v", a)
	}
	var p map[string]string
	if err := json.Unmarshal(a.Params, &p); err != nil {
		t.Fatalf("params json: %v (%s)", err, a.Params)
	}
	if p["pool_id"] != "pool1xyz" || p["network"] != "mainnet" || p["ticker"] != "OURO" {
		t.Fatalf("backfilled params: %v", p)
	}
	if tr, _ := st.Issuer().GetTierRules(ctx); !strings.Contains(string(tr), "gold") {
		t.Fatalf("tier_rules not lifted: %s", tr)
	}
}
