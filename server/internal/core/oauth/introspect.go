package oauth

import (
	"context"
	"strings"

	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/utils/crypto"
	"github.com/poolops/issuer/internal/utils/jose"
)

// IntrospectResponse is the RFC 7662 introspection result.
type IntrospectResponse struct {
	Active           bool   `json:"active"`
	Sub              string `json:"sub,omitempty"`
	Tier             string `json:"tier,omitempty"`
	Exp              int64  `json:"exp,omitempty"`
	MembershipStatus string `json:"membership_status,omitempty"`
}

// Introspect reports a token's live status (detailed §9.6). A token string is
// verified against the current JWKS and cross-checked with the ledger; a bare
// jti is checked against the ledger only. Unknown/expired/revoked → inactive.
func (s *Server) Introspect(ctx context.Context, token, jti string) (IntrospectResponse, error) {
	var sub, tier string
	if token != "" {
		jwks, err := s.currentJWKS(ctx)
		if err != nil {
			return IntrospectResponse{}, err
		}
		tok, err := jose.Verify(token, jwks)
		if err != nil {
			return IntrospectResponse{Active: false}, nil // bad signature / expired
		}
		jti = tok.JwtID()
		sub = tok.Subject()
		if v, ok := tok.Get("tier"); ok {
			tier, _ = v.(string)
		}
	}
	if jti == "" {
		return IntrospectResponse{Active: false}, nil
	}
	rec, err := s.issuedTokens.Get(ctx, jti)
	if err != nil {
		return IntrospectResponse{Active: false}, nil
	}
	active := rec.Status == domain.TokenActive && s.now().Before(rec.ExpiresAt)
	if !active {
		return IntrospectResponse{Active: false}, nil
	}
	return IntrospectResponse{
		Active: true, Sub: sub, Tier: tier, Exp: rec.ExpiresAt.Unix(), MembershipStatus: "eligible_member",
	}, nil
}

// Revoke revokes a token (RFC 7009). An access token (compact JWS) revokes its
// IssuedToken ledger row; an opaque refresh token revokes its grant. Per the
// RFC, revocation is idempotent and unknown tokens still succeed.
func (s *Server) Revoke(ctx context.Context, token, hint string) error {
	if token == "" {
		return nil
	}
	isJWS := strings.Count(token, ".") == 2 && hint != "refresh_token"
	if isJWS {
		if jti, err := jose.JTIUnverified(token); err == nil && jti != "" {
			return s.issuedTokens.Revoke(ctx, jti, s.now())
		}
		// fall through: maybe it was actually a refresh token
	}
	// Treat as opaque refresh token.
	if g, err := s.grants.Get(ctx, crypto.HashToken(token)); err == nil {
		return s.grants.SetStatus(ctx, nil, g.RefreshGrantID, domain.GrantRevoked)
	}
	return nil
}

// currentJWKS builds the JWKS document for the current published keys.
func (s *Server) currentJWKS(ctx context.Context) ([]byte, error) {
	pub, err := s.cfg.Keys.PublicJWKSKeys(ctx)
	if err != nil {
		return nil, err
	}
	return jose.BuildJWKS(pub)
}
