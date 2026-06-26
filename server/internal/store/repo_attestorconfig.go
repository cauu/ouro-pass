package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"ouro-pass/server/internal/domain"
)

// AttestorConfigRepo persists the issuer's set of on-chain credential sources
// (S0006 §2.2). It is by-id CRUD: the generalization of the single PoolConfig.
type AttestorConfigRepo struct{ s *Store }

// Attestors returns a repo bound to this store.
func (s *Store) Attestors() *AttestorConfigRepo { return &AttestorConfigRepo{s} }

const attestorCols = `attestor_id, kind, label, params, status, created_at, updated_at`

// Create inserts a new attestor config. A duplicate (kind, label) violates the
// unique constraint and surfaces the driver error.
func (r *AttestorConfigRepo) Create(ctx context.Context, a domain.AttestorConfig) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO AttestorConfig (`+attestorCols+`) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		a.AttestorID, a.Kind, a.Label, paramsOrEmpty(a.Params), a.Status, ts(a.CreatedAt), ts(a.UpdatedAt))
	return err
}

// Update replaces a config's label/params/status by id (kind is immutable).
func (r *AttestorConfigRepo) Update(ctx context.Context, a domain.AttestorConfig) error {
	res, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		UPDATE AttestorConfig SET label=?, params=?, status=?, updated_at=? WHERE attestor_id=?`),
		a.Label, paramsOrEmpty(a.Params), a.Status, ts(a.UpdatedAt), a.AttestorID)
	if err != nil {
		return err
	}
	return notFoundIfNoRows(res)
}

// SetStatus flips a config's status (active|disabled) by id.
func (r *AttestorConfigRepo) SetStatus(ctx context.Context, id, status string, now time.Time) error {
	res, err := r.s.DB.ExecContext(ctx, r.s.Rebind(
		`UPDATE AttestorConfig SET status=?, updated_at=? WHERE attestor_id=?`), status, ts(now), id)
	if err != nil {
		return err
	}
	return notFoundIfNoRows(res)
}

// Delete removes a config by id.
func (r *AttestorConfigRepo) Delete(ctx context.Context, id string) error {
	res, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`DELETE FROM AttestorConfig WHERE attestor_id=?`), id)
	if err != nil {
		return err
	}
	return notFoundIfNoRows(res)
}

// Get loads one config by id.
func (r *AttestorConfigRepo) Get(ctx context.Context, id string) (*domain.AttestorConfig, error) {
	row := r.s.DB.QueryRowContext(ctx, r.s.Rebind(
		`SELECT `+attestorCols+` FROM AttestorConfig WHERE attestor_id = ?`), id)
	a, err := scanAttestorConfig(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &a, nil
}

// List returns all attestor configs ordered by creation.
func (r *AttestorConfigRepo) List(ctx context.Context) ([]domain.AttestorConfig, error) {
	return r.list(ctx, "")
}

// ListActive returns only the active attestor configs (the evaluated set).
func (r *AttestorConfigRepo) ListActive(ctx context.Context) ([]domain.AttestorConfig, error) {
	return r.list(ctx, "WHERE status = 'active'")
}

func (r *AttestorConfigRepo) list(ctx context.Context, where string) ([]domain.AttestorConfig, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(
		`SELECT `+attestorCols+` FROM AttestorConfig `+where+` ORDER BY created_at, attestor_id`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.AttestorConfig
	for rows.Next() {
		a, err := scanAttestorConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func scanAttestorConfig(sc rowScanner) (domain.AttestorConfig, error) {
	var a domain.AttestorConfig
	var params, created, updated string
	if err := sc.Scan(&a.AttestorID, &a.Kind, &a.Label, &params, &a.Status, &created, &updated); err != nil {
		return a, err
	}
	a.Params = json.RawMessage(params)
	var err error
	if a.CreatedAt, err = parseTS(created); err != nil {
		return a, err
	}
	if a.UpdatedAt, err = parseTS(updated); err != nil {
		return a, err
	}
	return a, nil
}

func paramsOrEmpty(p json.RawMessage) string {
	if len(p) == 0 {
		return "{}"
	}
	return string(p)
}

// notFoundIfNoRows maps a zero-row write to domain.ErrNotFound.
func notFoundIfNoRows(res sql.Result) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return domain.ErrNotFound
	}
	return nil
}
