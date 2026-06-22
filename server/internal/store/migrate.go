package store

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

//go:embed migrations/sqlite/*.sql migrations/postgres/*.sql
var embeddedMigrations embed.FS

// Migrate applies all pending migrations for the store's driver from the
// embedded migration set. Idempotent: already-applied files are skipped.
func (s *Store) Migrate(ctx context.Context) error {
	return MigrateFS(ctx, s, embeddedMigrations)
}

// MigrateFS applies migrations from an arbitrary fs.FS (tests inject their own
// set). Files live under "<driver>/NNNN_name.sql" and run in lexical order;
// each applied version is recorded in schema_migrations within the same tx.
func MigrateFS(ctx context.Context, s *Store, fsys fs.FS) error {
	if _, err := s.DB.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP)`,
	); err != nil {
		return fmt.Errorf("ensure schema_migrations: %w", err)
	}

	dir := string(s.Driver)
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return fmt.Errorf("read migrations dir %q: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".sql") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		version := strings.TrimSuffix(name, ".sql")
		applied, err := s.migrationApplied(ctx, version)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := fs.ReadFile(fsys, dir+"/"+name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", name, err)
		}
		if err := s.applyOne(ctx, version, string(body)); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrationApplied(ctx context.Context, version string) (bool, error) {
	var n int
	err := s.DB.QueryRowContext(ctx,
		s.Rebind(`SELECT COUNT(1) FROM schema_migrations WHERE version = ?`), version,
	).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("check migration %s: %w", version, err)
	}
	return n > 0, nil
}

func (s *Store) applyOne(ctx context.Context, version, body string) error {
	return s.WithTx(ctx, func(tx *sql.Tx) error {
		for _, stmt := range splitStatements(body) {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("apply %s: %w", version, err)
			}
		}
		if _, err := tx.ExecContext(ctx,
			s.Rebind(`INSERT INTO schema_migrations (version) VALUES (?)`), version,
		); err != nil {
			return fmt.Errorf("record %s: %w", version, err)
		}
		return nil
	})
}

// splitStatements strips `--` line comments and splits the file into individual
// statements on `;`. This suits the project's plain DDL migrations (no
// PL/pgSQL bodies with embedded semicolons in the MVP scope).
func splitStatements(body string) []string {
	var clean strings.Builder
	for _, line := range strings.Split(body, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		clean.WriteString(line)
		clean.WriteByte('\n')
	}
	var out []string
	for _, s := range strings.Split(clean.String(), ";") {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}
