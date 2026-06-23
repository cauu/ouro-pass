package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
)

// AdminUserRepo persists admin users (§8.1).
type AdminUserRepo struct{ s *Store }

// AdminUsers returns a repo bound to this store.
func (s *Store) AdminUsers() *AdminUserRepo { return &AdminUserRepo{s} }

// Upsert inserts or updates an admin keyed by owner_key_hash.
func (r *AdminUserRepo) Upsert(ctx context.Context, u domain.AdminUser) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO AdminUser (admin_id, pool_id, owner_key_hash, role, last_login_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (owner_key_hash) DO UPDATE SET role=excluded.role`),
		u.AdminID, u.PoolID, u.OwnerKeyHash, string(u.Role), tsPtr(u.LastLoginAt), ts(u.CreatedAt))
	return err
}

// GetByOwnerKeyHash loads an admin by their login key hash.
func (r *AdminUserRepo) GetByOwnerKeyHash(ctx context.Context, ownerKeyHash string) (*domain.AdminUser, error) {
	return r.scanAdmin(r.s.DB.QueryRowContext(ctx, r.s.Rebind(adminUserCols+` WHERE owner_key_hash = ?`), ownerKeyHash))
}

// GetByID loads an admin by id (session → role lookup).
func (r *AdminUserRepo) GetByID(ctx context.Context, adminID string) (*domain.AdminUser, error) {
	return r.scanAdmin(r.s.DB.QueryRowContext(ctx, r.s.Rebind(adminUserCols+` WHERE admin_id = ?`), adminID))
}

// TouchLogin stamps last_login_at.
func (r *AdminUserRepo) TouchLogin(ctx context.Context, adminID string, at time.Time) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`UPDATE AdminUser SET last_login_at = ? WHERE admin_id = ?`), ts(at), adminID)
	return err
}

const adminUserCols = `SELECT admin_id, pool_id, owner_key_hash, role, last_login_at, created_at FROM AdminUser`

func (r *AdminUserRepo) scanAdmin(row rowScanner) (*domain.AdminUser, error) {
	var u domain.AdminUser
	var role, created string
	var lastLogin sql.NullString
	err := row.Scan(&u.AdminID, &u.PoolID, &u.OwnerKeyHash, &role, &lastLogin, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.Role = domain.AdminRole(role)
	if u.LastLoginAt, err = scanTS(lastLogin); err != nil {
		return nil, err
	}
	if u.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &u, nil
}
