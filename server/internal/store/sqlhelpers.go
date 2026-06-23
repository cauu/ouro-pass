package store

import (
	"database/sql"
	"time"
)

// Portable encodings (decision D6): times are RFC3339Nano UTC strings so SQLite
// and PostgreSQL behave identically regardless of native time handling.

// ts encodes a time as a storage string.
func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

// tsPtr encodes an optional time as a nullable storage value.
func tsPtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return ts(*t)
}

// parseTS decodes a storage time string.
func parseTS(s string) (time.Time, error) { return time.Parse(time.RFC3339Nano, s) }

// scanTS decodes a nullable time column into *time.Time.
func scanTS(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid || ns.String == "" {
		return nil, nil
	}
	t, err := parseTS(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// strPtr decodes a nullable text column into *string.
func strPtr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

// nullStr encodes an optional string as a nullable storage value.
func nullStr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}
