package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"ouro-pass/server/internal/domain"
)

// IssuerKeyRepo persists rotatable signing keys (§2.2).
type IssuerKeyRepo struct{ s *Store }

// IssuerKeys returns a repo bound to this store.
func (s *Store) IssuerKeys() *IssuerKeyRepo { return &IssuerKeyRepo{s} }

// Create inserts a new signing key.
func (r *IssuerKeyRepo) Create(ctx context.Context, k domain.IssuerKey) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO IssuerKey (kid, public_key, encrypted_private_key, status, valid_from, valid_until, created_at, retired_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`),
		k.KID, k.PublicKey, k.EncryptedPrivateKey, string(k.Status),
		tsPtr(k.ValidFrom), tsPtr(k.ValidUntil), ts(k.CreatedAt), tsPtr(k.RetiredAt))
	return err
}

// SetStatus transitions a key's lifecycle status, optionally stamping retired_at.
func (r *IssuerKeyRepo) SetStatus(ctx context.Context, kid string, status domain.IssuerKeyStatus, retiredAt *time.Time) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(
		`UPDATE IssuerKey SET status = ?, retired_at = ? WHERE kid = ?`),
		string(status), tsPtr(retiredAt), kid)
	return err
}

// Get loads a signing key by kid.
func (r *IssuerKeyRepo) Get(ctx context.Context, kid string) (*domain.IssuerKey, error) {
	return scanIssuerKey(r.s.DB.QueryRowContext(ctx, r.s.Rebind(issuerKeyCols+` WHERE kid = ?`), kid))
}

// ListByStatus returns all keys in the given status.
func (r *IssuerKeyRepo) ListByStatus(ctx context.Context, status domain.IssuerKeyStatus) ([]domain.IssuerKey, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(issuerKeyCols+` WHERE status = ? ORDER BY created_at`), string(status))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.IssuerKey
	for rows.Next() {
		k, err := scanIssuerKeyRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *k)
	}
	return out, rows.Err()
}

const issuerKeyCols = `SELECT kid, public_key, encrypted_private_key, status, valid_from, valid_until, created_at, retired_at FROM IssuerKey`

func scanIssuerKey(row rowScanner) (*domain.IssuerKey, error) {
	k, err := scanIssuerKeyRows(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return k, err
}

func scanIssuerKeyRows(row rowScanner) (*domain.IssuerKey, error) {
	var k domain.IssuerKey
	var status, created string
	var validFrom, validUntil, retired sql.NullString
	if err := row.Scan(&k.KID, &k.PublicKey, &k.EncryptedPrivateKey, &status, &validFrom, &validUntil, &created, &retired); err != nil {
		return nil, err
	}
	k.Status = domain.IssuerKeyStatus(status)
	var err error
	if k.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if k.ValidFrom, err = scanTS(validFrom); err != nil {
		return nil, err
	}
	if k.ValidUntil, err = scanTS(validUntil); err != nil {
		return nil, err
	}
	if k.RetiredAt, err = scanTS(retired); err != nil {
		return nil, err
	}
	return &k, nil
}
