package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/fstest"
)

// testMigrations is a controlled, dual-dialect migration set injected via
// MigrateFS to prove the runner + a real DDL round-trip on each engine (TC-2).
func testMigrations() fstest.MapFS {
	ddl := `CREATE TABLE widget (id TEXT PRIMARY KEY, name TEXT NOT NULL, meta TEXT);`
	return fstest.MapFS{
		"sqlite/0001_widget.sql":   {Data: []byte(ddl)},
		"postgres/0001_widget.sql": {Data: []byte(ddl)},
	}
}

// openTestStore returns an isolated SQLite store (a fresh temp file per test).
// Real-Postgres validation — dialect round-trips and the CAS concurrency
// invariants — lives in the dedicated internal/inttest package, which isolates
// each run; pointing these unit tests at a shared PG would collide on fixed keys
// (decision D24, supersedes the D3 shared-DSN branch).
func openTestStore(t *testing.T) *Store {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "t.db") + "?_pragma=foreign_keys(1)"
	st, err := Open(context.Background(), SQLite, dsn)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMigrate_AppliesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	fsys := testMigrations()

	if err := MigrateFS(ctx, st, fsys); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	// Re-running must be a no-op, not an error.
	if err := MigrateFS(ctx, st, fsys); err != nil {
		t.Fatalf("second migrate (idempotency): %v", err)
	}

	var applied int
	if err := st.DB.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM schema_migrations WHERE version = '0001_widget'`).Scan(&applied); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if applied != 1 {
		t.Fatalf("schema_migrations has %d rows for 0001_widget, want 1", applied)
	}
}

func TestRepository_RoundTrip(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := MigrateFS(ctx, st, testMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	// Insert + select round-trip via Querier + Rebind (exercises both engines).
	_, err := st.DB.ExecContext(ctx,
		st.Rebind(`INSERT INTO widget (id, name, meta) VALUES (?, ?, ?)`),
		"w1", "gizmo", `{"k":"v"}`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	var name, meta string
	err = st.DB.QueryRowContext(ctx,
		st.Rebind(`SELECT name, meta FROM widget WHERE id = ?`), "w1").Scan(&name, &meta)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if name != "gizmo" || meta != `{"k":"v"}` {
		t.Fatalf("round-trip mismatch: name=%q meta=%q", name, meta)
	}
}

func TestWithTx_RollbackOnError(t *testing.T) {
	ctx := context.Background()
	st := openTestStore(t)
	if err := MigrateFS(ctx, st, testMigrations()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// A tx that errors must leave no row behind.
	wantErr := context.Canceled
	err := st.WithTx(ctx, func(tx *sql.Tx) error {
		_, _ = tx.ExecContext(ctx, st.Rebind(`INSERT INTO widget (id, name) VALUES (?, ?)`), "w2", "tmp")
		return wantErr
	})
	if err != wantErr {
		t.Fatalf("WithTx returned %v, want %v", err, wantErr)
	}
	var n int
	st.DB.QueryRowContext(ctx, `SELECT COUNT(1) FROM widget WHERE id='w2'`).Scan(&n)
	if n != 0 {
		t.Fatalf("rollback failed: found %d rows for w2", n)
	}
}

func TestRebind(t *testing.T) {
	pg := &Store{Driver: Postgres}
	if got := pg.Rebind(`a=? AND b=?`); got != `a=$1 AND b=$2` {
		t.Errorf("pg rebind = %q", got)
	}
	lite := &Store{Driver: SQLite}
	if got := lite.Rebind(`a=? AND b=?`); got != `a=? AND b=?` {
		t.Errorf("sqlite rebind = %q", got)
	}
}
