package store

import (
	"context"
	"database/sql"
	"errors"

	"ouro-pass/server/internal/domain"
)

// OAuthClientRepo persists registered clients (§5.1).
type OAuthClientRepo struct{ s *Store }

// OAuthClients returns a repo bound to this store.
func (s *Store) OAuthClients() *OAuthClientRepo { return &OAuthClientRepo{s} }

// Upsert inserts or replaces a client registration.
func (r *OAuthClientRepo) Upsert(ctx context.Context, c domain.OAuthClient) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO OAuthClient (client_id, name, client_type, client_secret_hash, redirect_uris, allowed_audiences, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (client_id) DO UPDATE SET
			name=excluded.name, client_type=excluded.client_type, client_secret_hash=excluded.client_secret_hash,
			redirect_uris=excluded.redirect_uris, allowed_audiences=excluded.allowed_audiences,
			status=excluded.status`),
		c.ClientID, c.Name, string(c.ClientType), nullStr(c.ClientSecretHash),
		encodeStrings(c.RedirectURIs), encodeStrings(c.AllowedAudiences),
		c.Status, ts(c.CreatedAt))
	return err
}

// Get loads a client by id.
func (r *OAuthClientRepo) Get(ctx context.Context, clientID string) (*domain.OAuthClient, error) {
	var c domain.OAuthClient
	var clientType, redirects, auds, status, created string
	var secretHash sql.NullString
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT client_id, name, client_type, client_secret_hash, redirect_uris, allowed_audiences, status, created_at
		FROM OAuthClient WHERE client_id = ?`), clientID).
		Scan(&c.ClientID, &c.Name, &clientType, &secretHash, &redirects, &auds, &status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.ClientType, c.Status = domain.ClientType(clientType), status
	c.ClientSecretHash = strPtr(secretHash)
	if c.RedirectURIs, err = decodeStrings(redirects); err != nil {
		return nil, err
	}
	if c.AllowedAudiences, err = decodeStrings(auds); err != nil {
		return nil, err
	}
	if c.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &c, nil
}

// List returns all registered clients (secret hashes omitted by callers).
func (r *OAuthClientRepo) List(ctx context.Context) ([]domain.OAuthClient, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(`
		SELECT client_id, name, client_type, client_secret_hash, redirect_uris, allowed_audiences, status, created_at
		FROM OAuthClient ORDER BY created_at`))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OAuthClient
	for rows.Next() {
		var c domain.OAuthClient
		var clientType, redirects, auds, status, created string
		var secretHash sql.NullString
		if err := rows.Scan(&c.ClientID, &c.Name, &clientType, &secretHash, &redirects, &auds, &status, &created); err != nil {
			return nil, err
		}
		c.ClientType, c.Status = domain.ClientType(clientType), status
		// Propagate decode errors instead of silently yielding empty fields
		// (consistency with Get, p12-9).
		if c.RedirectURIs, err = decodeStrings(redirects); err != nil {
			return nil, err
		}
		if c.AllowedAudiences, err = decodeStrings(auds); err != nil {
			return nil, err
		}
		if c.CreatedAt, err = parseTS(created); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
