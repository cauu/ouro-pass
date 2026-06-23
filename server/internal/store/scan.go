package store

// rowScanner is satisfied by both *sql.Row and *sql.Rows, letting scan helpers
// serve single-row and multi-row queries alike.
type rowScanner interface {
	Scan(dest ...any) error
}
