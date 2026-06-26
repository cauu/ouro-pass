package store

import (
	"context"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

// sqliteMigrationsBelow builds a MapFS of the embedded sqlite migrations whose
// version sorts before cutoff, so a test can stop at a pre-S0005 schema state.
func sqliteMigrationsBelow(t *testing.T, cutoff string) fstest.MapFS {
	t.Helper()
	out := fstest.MapFS{}
	entries, err := fs.ReadDir(embeddedMigrations, "migrations/sqlite")
	if err != nil {
		t.Fatalf("read embedded sqlite migrations: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") || e.Name() >= cutoff {
			continue
		}
		body, err := fs.ReadFile(embeddedMigrations, "migrations/sqlite/"+e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out["sqlite/"+e.Name()] = &fstest.MapFile{Data: body}
	}
	return out
}

// TestMigrate_ChannelInstancesBackfill proves S0005 p1-1 / TC-1: applying 0014
// over legacy single-telegram data backfills the 'default' instance name, the
// subscription/activation channel_id, and the new (channel_id, channel_user_id)
// unique key admits the same channel user on a second instance.
func TestMigrate_ChannelInstancesBackfill(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)

	// 1. Reach the pre-0014 schema (no name / channel_id columns yet).
	if err := MigrateFS(ctx, st, sqliteMigrationsBelow(t, "0014")); err != nil {
		t.Fatalf("pre-0014 migrate: %v", err)
	}

	// 2. Seed legacy rows using the old column shape.
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.DB.ExecContext(ctx, st.Rebind(q), args...); err != nil {
			t.Fatalf("seed %q: %v", q, err)
		}
	}
	exec(`INSERT INTO ChannelConfig (channel_id, pool_id, channel_type, config, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, "ch-legacy", "pool1", "telegram", `{}`, "active", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z")
	exec(`INSERT INTO SubscriptionSession (session_id, pool_id, stake_credential_hash, channel_type, channel_user_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"sub-legacy", "pool1", "sch-abc", "telegram", "user-1", "active", "gold", "[]", "[]", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z")
	exec(`INSERT INTO ActivationCode (code, stake_credential_hash, channel_type, status, expires_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, "code-legacy", "sch-abc", "telegram", "active", "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z")

	// 3. Apply the full embedded set (idempotent through 0013, then runs 0014).
	sub, err := fs.Sub(embeddedMigrations, "migrations")
	if err != nil {
		t.Fatalf("sub embedded: %v", err)
	}
	if err := MigrateFS(ctx, st, sub); err != nil {
		t.Fatalf("apply 0014: %v", err)
	}

	// 4a. ChannelConfig.name backfilled to 'default'.
	var name string
	if err := st.DB.QueryRowContext(ctx, st.Rebind(`SELECT name FROM ChannelConfig WHERE channel_id = ?`), "ch-legacy").Scan(&name); err != nil {
		t.Fatalf("read channel name: %v", err)
	}
	if name != "default" {
		t.Fatalf("ChannelConfig.name = %q, want default", name)
	}

	// 4b. Subscription + activation channel_id backfilled to the instance.
	var subCh, actCh string
	if err := st.DB.QueryRowContext(ctx, st.Rebind(`SELECT channel_id FROM SubscriptionSession WHERE session_id = ?`), "sub-legacy").Scan(&subCh); err != nil {
		t.Fatalf("read sub channel_id: %v", err)
	}
	if err := st.DB.QueryRowContext(ctx, st.Rebind(`SELECT channel_id FROM ActivationCode WHERE code = ?`), "code-legacy").Scan(&actCh); err != nil {
		t.Fatalf("read activation channel_id: %v", err)
	}
	if subCh != "ch-legacy" || actCh != "ch-legacy" {
		t.Fatalf("backfilled channel_id sub=%q act=%q, want ch-legacy", subCh, actCh)
	}

	// 4c. The same channel user can subscribe to a *second* instance (new unique
	// key is (channel_id, channel_user_id), not (pool, type, user)).
	if _, err := st.DB.ExecContext(ctx, st.Rebind(`INSERT INTO SubscriptionSession (session_id, pool_id, stake_credential_hash, channel_id, channel_type, channel_user_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		"sub-2nd", "pool1", "sch-abc", "ch-second", "telegram", "user-1", "active", "gold", "[]", "[]", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z"); err != nil {
		t.Fatalf("second-instance subscription for same user should be allowed: %v", err)
	}

	// 4d. ...but a duplicate on the SAME (channel_id, channel_user_id) is rejected.
	if _, err := st.DB.ExecContext(ctx, st.Rebind(`INSERT INTO SubscriptionSession (session_id, pool_id, stake_credential_hash, channel_id, channel_type, channel_user_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`),
		"sub-dup", "pool1", "sch-abc", "ch-legacy", "telegram", "user-1", "active", "gold", "[]", "[]", "2026-01-01T00:00:00Z", "2026-01-01T00:00:00Z", "2026-02-01T00:00:00Z"); err == nil {
		t.Fatal("duplicate (channel_id, channel_user_id) should violate the unique key")
	}
}
