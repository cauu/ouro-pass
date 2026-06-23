package store

import (
	"context"

	"github.com/poolops/issuer/internal/domain"
)

// BlacklistRepo persists the sparse manual deny-list (§3.2).
type BlacklistRepo struct{ s *Store }

// Blacklist returns a repo bound to this store.
func (s *Store) Blacklist() *BlacklistRepo { return &BlacklistRepo{s} }

// Add inserts (or refreshes) a blacklist entry.
func (r *BlacklistRepo) Add(ctx context.Context, b domain.Blacklist) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO Blacklist (stake_credential_hash, reason, created_at) VALUES (?, ?, ?)
		ON CONFLICT (stake_credential_hash) DO UPDATE SET reason=excluded.reason`),
		b.StakeCredentialHash, nullStr(b.Reason), ts(b.CreatedAt))
	return err
}

// Has reports whether a stake credential is blacklisted.
func (r *BlacklistRepo) Has(ctx context.Context, stakeCredentialHash string) (bool, error) {
	var n int
	err := r.s.DB.QueryRowContext(ctx,
		r.s.Rebind(`SELECT COUNT(1) FROM Blacklist WHERE stake_credential_hash = ?`),
		stakeCredentialHash).Scan(&n)
	return n > 0, err
}
