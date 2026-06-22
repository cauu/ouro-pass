// Package store is the persistence boundary. It hides the PostgreSQL/SQLite
// duality (spec C4/D2/D3) behind a small Store wrapper and a Querier interface
// so repositories run unchanged against either engine and inside or outside a
// transaction. Repositories are hand-written database/sql (no ORM, no sqlc —
// see decision D2).
package store

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"

	// Pure-Go SQLite driver (no CGO), registers driver name "sqlite".
	_ "modernc.org/sqlite"
	// pgx stdlib driver, registers driver name "pgx".
	_ "github.com/jackc/pgx/v5/stdlib"
)

// Driver identifies the active SQL dialect.
type Driver string

const (
	SQLite   Driver = "sqlite"
	Postgres Driver = "postgres"
)

// Querier is satisfied by both *sql.DB and *sql.Tx, letting a repository method
// accept either an autocommit handle or a transaction.
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Store owns the connection pool and remembers the driver so it can rebind
// placeholders for the active dialect.
type Store struct {
	DB     *sql.DB
	Driver Driver
}

// Open connects using the given driver ("sqlite" | "postgres") and DSN, and
// verifies connectivity. The sql driver name differs from our Driver label:
// sqlite→"sqlite", postgres→"pgx".
func Open(ctx context.Context, driver Driver, dsn string) (*Store, error) {
	sqlDriver, err := sqlDriverName(driver)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open(sqlDriver, dsn)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", driver, err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping %s: %w", driver, err)
	}
	// SQLite is single-writer; serialize to avoid "database is locked".
	if driver == SQLite {
		db.SetMaxOpenConns(1)
	}
	return &Store{DB: db, Driver: driver}, nil
}

func sqlDriverName(d Driver) (string, error) {
	switch d {
	case SQLite:
		return "sqlite", nil
	case Postgres:
		return "pgx", nil
	default:
		return "", fmt.Errorf("unknown driver %q", d)
	}
}

// Close releases the pool.
func (s *Store) Close() error { return s.DB.Close() }

// WithTx runs fn inside a transaction, committing on success and rolling back
// on error or panic.
func (s *Store) WithTx(ctx context.Context, fn func(tx *sql.Tx) error) (err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()
	if err = fn(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// Rebind rewrites the portable `?` placeholder style to the active dialect.
// SQLite accepts `?` as-is; PostgreSQL needs positional `$1,$2,...`. Repositories
// write `?` everywhere and call Rebind, so one SQL string serves both engines.
func (s *Store) Rebind(query string) string {
	if s.Driver != Postgres {
		return query
	}
	var b strings.Builder
	b.Grow(len(query) + 8)
	n := 0
	for i := 0; i < len(query); i++ {
		if query[i] == '?' {
			n++
			b.WriteByte('$')
			b.WriteString(strconv.Itoa(n))
			continue
		}
		b.WriteByte(query[i])
	}
	return b.String()
}
