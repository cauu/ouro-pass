package oauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/utils/jose"
)

// Token-endpoint errors (OAuth codes).
var (
	ErrInvalidGrant       = errors.New("invalid_grant")
	ErrUnsupportedGrant   = errors.New("unsupported_grant_type")
	ErrInvalidClientCreds = errors.New("invalid_client")
)

// TokenRequest is the /api/oauth/token input (both grant types).
type TokenRequest struct {
	GrantType    string
	Code         string
	ClientID     string
	ClientSecret string
	CodeVerifier string
	RedirectURI  string
	RefreshToken string
	DevicePubkey string // public client PoP device key (hex)
}

// TokenResponse is the unified token response (detailed §9.4).
type TokenResponse struct {
	AccessToken  string    `json:"access_token"`
	TokenType    string    `json:"token_type"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"-"`
	Status       string    `json:"-"`
	Tier         string    `json:"-"`
}

// Token dispatches on grant_type.
func (s *Server) Token(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	switch req.GrantType {
	case "authorization_code":
		return s.tokenAuthCode(ctx, req)
	case "refresh_token":
		return s.tokenRefresh(ctx, req)
	default:
		return nil, ErrUnsupportedGrant
	}
}

// tokenRefresh rotates a refresh grant and re-evaluates eligibility. Replaying
// an already-rotated grant is treated as theft: the whole rotation chain is
// revoked (detailed §9.4).
func (s *Server) tokenRefresh(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	grant, err := s.grants.Get(ctx, crypto.HashToken(req.RefreshToken))
	if err != nil {
		return nil, ErrInvalidGrant
	}
	now := s.now()
	switch grant.Status {
	case domain.GrantActive:
		// proceed
	case domain.GrantRotated:
		// Replay of a superseded grant → revoke the entire chain.
		_ = s.grants.RevokeChain(ctx, grant.RefreshGrantID)
		return nil, ErrInvalidGrant
	default:
		return nil, ErrInvalidGrant
	}
	if grant.ExpiresAt != nil && now.After(*grant.ExpiresAt) {
		_ = s.grants.SetStatus(ctx, nil, grant.RefreshGrantID, domain.GrantExpired)
		return nil, ErrInvalidGrant
	}

	// Authenticate by client type (confidential: client_secret; public: DPoP
	// deferred per D7, device re-bound from the stored grant).
	if grant.ClientType == domain.ClientConfidential {
		if grant.ClientID == nil {
			return nil, ErrInvalidClientCreds
		}
		client, err := s.clients.Get(ctx, *grant.ClientID)
		if err != nil {
			return nil, ErrInvalidClientCreds
		}
		if client.ClientSecretHash == nil || subtle.ConstantTimeCompare([]byte(crypto.HashToken(req.ClientSecret)), []byte(*client.ClientSecretHash)) != 1 {
			return nil, ErrInvalidClientCreds
		}
	}

	// Public client: the refresh must carry the device public key the grant was
	// bound to (cnf.jkt). This is an interim possession check — full DPoP proof
	// of the device private key remains deferred (D7) — but it stops a stolen
	// refresh token from being exchanged with the binding silently dropped (p12-5).
	if grant.ClientType == domain.ClientPublic && len(grant.BoundDevicePubkey) > 0 {
		reqDev, derr := hex.DecodeString(req.DevicePubkey)
		if derr != nil || !bytes.Equal(reqDev, grant.BoundDevicePubkey) {
			return nil, ErrInvalidGrant
		}
	}

	// Re-evaluate eligibility; a lower-tier match naturally downgrades, full
	// ineligibility is denied.
	eligible, decision, err := s.evaluate(ctx, grant.StakeCredentialHash)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, ErrNotEligible
	}

	devHex := ""
	if len(grant.BoundDevicePubkey) > 0 {
		devHex = hex.EncodeToString(grant.BoundDevicePubkey)
	}
	// Read the signing key BEFORE the transaction (SQLite single-connection).
	signer, err := s.cfg.Keys.ActiveSigner(ctx)
	if err != nil {
		return nil, err
	}
	// Rotate-then-mint must be one atomic step: a compare-and-swap to rotated
	// (only one concurrent refresh of the same grant wins) and the new grant +
	// access ledger row are written in the same transaction. A mid-sequence
	// failure rolls back, so the old grant is never left rotated without a
	// successor (p12-2).
	var resp *TokenResponse
	err = s.cfg.Store.WithTx(ctx, func(tx *sql.Tx) error {
		won, err := s.grants.RotateIfActive(ctx, tx, grant.RefreshGrantID)
		if err != nil {
			return err
		}
		if !won {
			return ErrInvalidGrant // lost a concurrent rotation race / no longer active
		}
		r, err := s.mint(ctx, tx, mintParams{
			sch: grant.StakeCredentialHash, aud: grant.Audience, clientType: grant.ClientType,
			clientID: grant.ClientID, tier: decision.Tier, entitlements: decision.Entitlements,
			devicePubkey: devHex, rotatedFrom: &grant.RefreshGrantID,
			signerKID: signer.KID, signerPriv: signer.Priv,
		})
		if err != nil {
			return err
		}
		resp = r
		return nil
	})
	if err != nil {
		return nil, err
	}
	return resp, nil
}

// tokenAuthCode redeems an authorization code for tokens.
func (s *Server) tokenAuthCode(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	code, err := s.authCodes.Consume(ctx, crypto.HashToken(req.Code), s.now())
	if err != nil {
		return nil, ErrInvalidGrant // not found / consumed / expired
	}
	if code.ClientID != req.ClientID || code.RedirectURI != req.RedirectURI {
		return nil, ErrInvalidGrant
	}
	client, err := s.clients.Get(ctx, req.ClientID)
	if err != nil {
		return nil, ErrInvalidClientCreds
	}
	if err := s.authenticateClient(client, code, req); err != nil {
		return nil, err
	}

	// Re-evaluate eligibility at token time (single rule path with refresh).
	eligible, decision, err := s.evaluate(ctx, code.StakeCredentialHash)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, ErrNotEligible
	}

	signer, err := s.cfg.Keys.ActiveSigner(ctx)
	if err != nil {
		return nil, err
	}
	return s.mint(ctx, nil, mintParams{
		sch: code.StakeCredentialHash, aud: code.Aud, clientType: client.ClientType,
		clientID: &client.ClientID, tier: decision.Tier, entitlements: decision.Entitlements,
		devicePubkey: req.DevicePubkey, signerKID: signer.KID, signerPriv: signer.Priv,
	})
}

// authenticateClient enforces confidential client_secret or public PKCE.
func (s *Server) authenticateClient(client *domain.OAuthClient, code *domain.AuthorizationCode, req TokenRequest) error {
	if client.ClientType == domain.ClientConfidential {
		if client.ClientSecretHash == nil || crypto.HashToken(req.ClientSecret) != *client.ClientSecretHash {
			return ErrInvalidClientCreds
		}
		return nil
	}
	// Public client → PKCE S256 (D7).
	if code.CodeChallenge == nil || req.CodeVerifier == "" {
		return ErrInvalidGrant
	}
	if pkceS256(req.CodeVerifier) != *code.CodeChallenge {
		return ErrInvalidGrant
	}
	return nil
}

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// mintParams bundles the inputs for issuing a token + refresh grant.
type mintParams struct {
	sch          string
	aud          string
	clientType   domain.ClientType
	clientID     *string
	tier         string
	entitlements []string
	devicePubkey string
	rotatedFrom  *string // set on refresh rotation
	signerKID    string  // active signing key, fetched by the caller before any tx
	signerPriv   ed25519.PrivateKey
}

// mint signs an access token, records the IssuedToken ledger row, and creates a
// fresh refresh grant. Shared by the authorization_code and refresh_token paths.
// q is the transaction to write within (nil → autocommit on the shared pool).
func (s *Server) mint(ctx context.Context, q store.Querier, p mintParams) (*TokenResponse, error) {
	// The signing key is read by the caller BEFORE any transaction is opened: on
	// SQLite (MaxOpenConns=1) reading it here, while q holds the only connection,
	// would deadlock (p12-2 fix follow-up).
	schBytes, err := hex.DecodeString(p.sch)
	if err != nil {
		return nil, err
	}
	sub := crypto.DeriveSub(s.cfg.ServerSalt, schBytes)

	now := s.now()
	exp := now.Add(s.cfg.AccessTTL)
	jti := crypto.RandomID()

	claims := jose.AccessClaims{
		Issuer: s.cfg.Issuer, Subject: sub, Audience: p.aud,
		IssuedAt: now, NotBefore: now, Expiry: exp, JTI: jti,
		Tier: p.tier, Entitlements: p.entitlements,
	}
	var boundDevice []byte
	if p.clientType == domain.ClientPublic && p.devicePubkey != "" {
		// A provided device key must be a well-formed 32-byte ed25519 public key;
		// a malformed one is rejected rather than silently dropped (which would
		// mint an unbound bearer token without cnf.jkt) (p12-5).
		dev, err := hex.DecodeString(p.devicePubkey)
		if err != nil || len(dev) != ed25519.PublicKeySize {
			return nil, ErrInvalidRequest
		}
		boundDevice = dev
		thumb := sha256.Sum256(dev)
		claims.Cnf = map[string]string{"jkt": base64.RawURLEncoding.EncodeToString(thumb[:])}
	}

	accessToken, err := jose.SignAccessToken(p.signerKID, p.signerPriv, claims)
	if err != nil {
		return nil, err
	}

	if err := s.issuedTokens.Create(ctx, q, domain.IssuedToken{
		JTI: jti, StakeCredentialHash: p.sch, Kind: domain.TokenAccess, Audience: p.aud,
		KID: p.signerKID, ClientID: p.clientID, Status: domain.TokenActive, IssuedAt: now, ExpiresAt: exp,
	}); err != nil {
		return nil, err
	}

	refreshPlain := crypto.RandomToken(32)
	refreshExp := now.Add(s.cfg.RefreshTTL)
	if err := s.grants.Create(ctx, q, domain.RefreshGrant{
		RefreshGrantID: crypto.HashToken(refreshPlain), StakeCredentialHash: p.sch, Audience: p.aud,
		ClientType: p.clientType, BoundDevicePubkey: boundDevice, ClientID: p.clientID,
		Status: domain.GrantActive, RotatedFrom: p.rotatedFrom, CreatedAt: now, ExpiresAt: &refreshExp,
	}); err != nil {
		return nil, err
	}

	return &TokenResponse{
		AccessToken: accessToken, TokenType: "Bearer", RefreshToken: refreshPlain,
		ExpiresAt: exp, Status: "eligible_member", Tier: p.tier,
	}, nil
}
