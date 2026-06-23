package store

import (
	"context"
	"database/sql"
	"errors"

	"ouro-pass/server/internal/domain"
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// OAuthClientRepo persists registered clients (§5.1).
type OAuthClientRepo struct{ s *Store }

// OAuthClients returns a repo bound to this store.
func (s *Store) OAuthClients() *OAuthClientRepo { return &OAuthClientRepo{s} }

// Upsert inserts or replaces a client registration.
func (r *OAuthClientRepo) Upsert(ctx context.Context, c domain.OAuthClient) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO OAuthClient (client_id, name, client_type, client_secret_hash, party, redirect_uris, allowed_audiences, allowed_scopes, pkce_required, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (client_id) DO UPDATE SET
			name=excluded.name, client_type=excluded.client_type, client_secret_hash=excluded.client_secret_hash,
			party=excluded.party, redirect_uris=excluded.redirect_uris, allowed_audiences=excluded.allowed_audiences,
			allowed_scopes=excluded.allowed_scopes, pkce_required=excluded.pkce_required, status=excluded.status`),
		c.ClientID, c.Name, string(c.ClientType), nullStr(c.ClientSecretHash), string(c.Party),
		encodeStrings(c.RedirectURIs), encodeStrings(c.AllowedAudiences), encodeStrings(c.AllowedScopes),
		boolToInt(c.PKCERequired), c.Status, ts(c.CreatedAt))
	return err
}

// Get loads a client by id.
func (r *OAuthClientRepo) Get(ctx context.Context, clientID string) (*domain.OAuthClient, error) {
	var c domain.OAuthClient
	var clientType, party, redirects, auds, scopes, status, created string
	var secretHash sql.NullString
	var pkce int
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT client_id, name, client_type, client_secret_hash, party, redirect_uris, allowed_audiences, allowed_scopes, pkce_required, status, created_at
		FROM OAuthClient WHERE client_id = ?`), clientID).
		Scan(&c.ClientID, &c.Name, &clientType, &secretHash, &party, &redirects, &auds, &scopes, &pkce, &status, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.ClientType, c.Party, c.Status = domain.ClientType(clientType), domain.ClientParty(party), status
	c.ClientSecretHash, c.PKCERequired = strPtr(secretHash), pkce != 0
	if c.RedirectURIs, err = decodeStrings(redirects); err != nil {
		return nil, err
	}
	if c.AllowedAudiences, err = decodeStrings(auds); err != nil {
		return nil, err
	}
	if c.AllowedScopes, err = decodeStrings(scopes); err != nil {
		return nil, err
	}
	if c.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	return &c, nil
}

// List returns all registered clients (secret hashes omitted by callers).
func (r *OAuthClientRepo) List(ctx context.Context) ([]domain.OAuthClient, error) {
	rows, err := r.s.DB.QueryContext(ctx, `
		SELECT client_id, name, client_type, client_secret_hash, party, redirect_uris, allowed_audiences, allowed_scopes, pkce_required, status, created_at
		FROM OAuthClient ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.OAuthClient
	for rows.Next() {
		var c domain.OAuthClient
		var clientType, party, redirects, auds, scopes, status, created string
		var secretHash sql.NullString
		var pkce int
		if err := rows.Scan(&c.ClientID, &c.Name, &clientType, &secretHash, &party, &redirects, &auds, &scopes, &pkce, &status, &created); err != nil {
			return nil, err
		}
		c.ClientType, c.Party, c.Status = domain.ClientType(clientType), domain.ClientParty(party), status
		c.PKCERequired = pkce != 0
		c.RedirectURIs, _ = decodeStrings(redirects)
		c.AllowedAudiences, _ = decodeStrings(auds)
		c.AllowedScopes, _ = decodeStrings(scopes)
		c.CreatedAt, _ = parseTS(created)
		out = append(out, c)
	}
	return out, rows.Err()
}
