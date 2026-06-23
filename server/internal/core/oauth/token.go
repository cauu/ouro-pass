package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/utils/crypto"
	"github.com/poolops/issuer/internal/utils/jose"
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

// tokenRefresh is implemented in p5-3 (refresh.go); stubbed here so the
// dispatch compiles.
func (s *Server) tokenRefresh(ctx context.Context, req TokenRequest) (*TokenResponse, error) {
	return nil, ErrUnsupportedGrant
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

	return s.mint(ctx, mintParams{
		sch: code.StakeCredentialHash, aud: code.Aud, clientType: client.ClientType,
		clientID: &client.ClientID, tier: decision.Tier, entitlements: decision.Entitlements,
		devicePubkey: req.DevicePubkey,
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
}

// mint signs an access token, records the IssuedToken ledger row, and creates a
// fresh refresh grant. Shared by the authorization_code and refresh_token paths.
func (s *Server) mint(ctx context.Context, p mintParams) (*TokenResponse, error) {
	signer, err := s.cfg.Keys.ActiveSigner(ctx)
	if err != nil {
		return nil, err
	}
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
		if dev, err := hex.DecodeString(p.devicePubkey); err == nil {
			boundDevice = dev
			thumb := sha256.Sum256(dev)
			claims.Cnf = map[string]string{"jkt": base64.RawURLEncoding.EncodeToString(thumb[:])}
		}
	}

	accessToken, err := jose.SignAccessToken(signer.KID, signer.Priv, claims)
	if err != nil {
		return nil, err
	}

	if err := s.issuedTokens.Create(ctx, nil, domain.IssuedToken{
		JTI: jti, StakeCredentialHash: p.sch, Kind: domain.TokenAccess, Audience: p.aud,
		KID: signer.KID, ClientID: p.clientID, Status: domain.TokenActive, IssuedAt: now, ExpiresAt: exp,
	}); err != nil {
		return nil, err
	}

	refreshPlain := crypto.RandomToken(32)
	refreshExp := now.Add(s.cfg.RefreshTTL)
	if err := s.grants.Create(ctx, nil, domain.RefreshGrant{
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
