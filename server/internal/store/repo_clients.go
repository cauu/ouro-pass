package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/poolops/issuer/internal/domain"
)

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---- OAuthClient (§5.1) ----

// OAuthClientRepo persists registered clients.
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

// ---- ChannelConfig (§6.1) ----

// ChannelConfigRepo persists channel instances.
type ChannelConfigRepo struct{ s *Store }

// Channels returns a repo bound to this store.
func (s *Store) Channels() *ChannelConfigRepo { return &ChannelConfigRepo{s} }

// Upsert inserts or replaces a channel config (secret subfields pre-encrypted).
func (r *ChannelConfigRepo) Upsert(ctx context.Context, c domain.ChannelConfig) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO ChannelConfig (channel_id, pool_id, channel_type, config, status, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (channel_id) DO UPDATE SET
			config=excluded.config, status=excluded.status, updated_at=excluded.updated_at`),
		c.ChannelID, c.PoolID, c.ChannelType, string(c.Config), c.Status, ts(c.CreatedAt), ts(c.UpdatedAt))
	return err
}

// GetByType returns the active channel config for a channel type, if any.
func (r *ChannelConfigRepo) GetByType(ctx context.Context, channelType string) (*domain.ChannelConfig, error) {
	var c domain.ChannelConfig
	var config, created, updated string
	err := r.s.DB.QueryRowContext(ctx, r.s.Rebind(`
		SELECT channel_id, pool_id, channel_type, config, status, created_at, updated_at
		FROM ChannelConfig WHERE channel_type = ? AND status = 'active' LIMIT 1`), channelType).
		Scan(&c.ChannelID, &c.PoolID, &c.ChannelType, &config, &c.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.Config = []byte(config)
	if c.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if c.UpdatedAt, err = parseTS(updated); err != nil {
		return nil, err
	}
	return &c, nil
}

// ---- SubscriptionSession (§6.2) ----

// SubscriptionRepo persists channel subscriptions.
type SubscriptionRepo struct{ s *Store }

// Subscriptions returns a repo bound to this store.
func (s *Store) Subscriptions() *SubscriptionRepo { return &SubscriptionRepo{s} }

// Upsert inserts or replaces a session keyed by (pool, channel_type, channel_user_id).
func (r *SubscriptionRepo) Upsert(ctx context.Context, x domain.SubscriptionSession) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`
		INSERT INTO SubscriptionSession (session_id, pool_id, stake_credential_hash, channel_type, channel_user_id, channel_account_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at, cancelled_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (pool_id, channel_type, channel_user_id) DO UPDATE SET
			stake_credential_hash=excluded.stake_credential_hash, status=excluded.status, tier=excluded.tier,
			topics=excluded.topics, entitlements=excluded.entitlements, last_verified_at=excluded.last_verified_at,
			expires_at=excluded.expires_at, cancelled_at=excluded.cancelled_at`),
		x.SessionID, x.PoolID, x.StakeCredentialHash, x.ChannelType, x.ChannelUserID, nullStr(x.ChannelAccountID),
		string(x.Status), x.Tier, encodeStrings(x.Topics), encodeStrings(x.Entitlements),
		ts(x.CreatedAt), ts(x.LastVerifiedAt), ts(x.ExpiresAt), tsPtr(x.CancelledAt))
	return err
}

// GetByChannelUser loads a session by its channel-user unique key (bot lookups).
func (r *SubscriptionRepo) GetByChannelUser(ctx context.Context, poolID, channelType, channelUserID string) (*domain.SubscriptionSession, error) {
	return r.scanOne(r.s.DB.QueryRowContext(ctx, r.s.Rebind(subscriptionCols+
		` WHERE pool_id = ? AND channel_type = ? AND channel_user_id = ?`), poolID, channelType, channelUserID))
}

// ListActiveByChannel returns all active sessions for a pool's channel (the
// push-scheduler candidate set; tier/topic/entitlement filtering is applied by
// the scheduler in code over the JSON array columns).
func (r *SubscriptionRepo) ListActiveByChannel(ctx context.Context, poolID, channelType string) ([]domain.SubscriptionSession, error) {
	rows, err := r.s.DB.QueryContext(ctx, r.s.Rebind(subscriptionCols+
		` WHERE pool_id = ? AND channel_type = ? AND status = ?`), poolID, channelType, string(domain.SubActive))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.SubscriptionSession
	for rows.Next() {
		x, err := scanSubscription(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
	}
	return out, rows.Err()
}

// SetStatus transitions a session's status (downgrade/cancel/expire).
func (r *SubscriptionRepo) SetStatus(ctx context.Context, sessionID string, status domain.SubscriptionStatus) error {
	_, err := r.s.DB.ExecContext(ctx, r.s.Rebind(`UPDATE SubscriptionSession SET status = ? WHERE session_id = ?`),
		string(status), sessionID)
	return err
}

const subscriptionCols = `SELECT session_id, pool_id, stake_credential_hash, channel_type, channel_user_id, channel_account_id, status, tier, topics, entitlements, created_at, last_verified_at, expires_at, cancelled_at FROM SubscriptionSession`

func (r *SubscriptionRepo) scanOne(row rowScanner) (*domain.SubscriptionSession, error) {
	x, err := scanSubscription(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrNotFound
	}
	return x, err
}

func scanSubscription(row rowScanner) (*domain.SubscriptionSession, error) {
	var x domain.SubscriptionSession
	var status, tier, topics, ents, created, lastVer, expires string
	var acct, cancelled sql.NullString
	if err := row.Scan(&x.SessionID, &x.PoolID, &x.StakeCredentialHash, &x.ChannelType, &x.ChannelUserID,
		&acct, &status, &tier, &topics, &ents, &created, &lastVer, &expires, &cancelled); err != nil {
		return nil, err
	}
	x.ChannelAccountID, x.Status, x.Tier = strPtr(acct), domain.SubscriptionStatus(status), tier
	var err error
	if x.Topics, err = decodeStrings(topics); err != nil {
		return nil, err
	}
	if x.Entitlements, err = decodeStrings(ents); err != nil {
		return nil, err
	}
	if x.CreatedAt, err = parseTS(created); err != nil {
		return nil, err
	}
	if x.LastVerifiedAt, err = parseTS(lastVer); err != nil {
		return nil, err
	}
	if x.ExpiresAt, err = parseTS(expires); err != nil {
		return nil, err
	}
	if x.CancelledAt, err = scanTS(cancelled); err != nil {
		return nil, err
	}
	return &x, nil
}
