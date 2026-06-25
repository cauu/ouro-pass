// Package oauth is the OAuth2 authorization server: it gates wallet-proven,
// stake-eligible users into one-time authorization codes (Authorize) and
// exchanges those codes — and refresh grants — for signed access tokens (Token,
// added in later items). Issuance is unified here (no standalone license
// endpoints — C2). Eligibility is recomputed from the live snapshot at both
// authorize and token time so a single rule path governs both.
package oauth

import (
	"context"
	"errors"
	"slices"
	"time"

	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/rules"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
)

// Errors surfaced to handlers as OAuth error codes.
var (
	ErrInvalidClient   = errors.New("invalid_client")
	ErrInvalidRequest  = errors.New("invalid_request")
	ErrNotEligible     = errors.New("not_eligible")
	ErrAccessDenied    = errors.New("access_denied")
)

// Config bundles the server's collaborators and parameters.
type Config struct {
	Store      *store.Store
	Wallet     *walletauth.Service
	Keys       *keys.Service
	Chain      chain.Source
	PoolID     string
	Issuer     string // token `iss`
	ServerSalt []byte // HMAC salt for `sub` derivation
	AccessTTL  time.Duration
	RefreshTTL time.Duration
	CodeTTL    time.Duration
}

// Server is the authorization server.
type Server struct {
	cfg          Config
	clients      *store.OAuthClientRepo
	authCodes    *store.AuthCodeRepo
	rules        *store.MembershipRuleRepo
	issuedTokens *store.IssuedTokenRepo
	grants       *store.RefreshGrantRepo
	blacklist    *store.BlacklistRepo
	now          func() time.Time
}

// New builds an authorization server.
func New(cfg Config) *Server {
	if cfg.CodeTTL == 0 {
		cfg.CodeTTL = 60 * time.Second
	}
	return &Server{
		cfg:          cfg,
		clients:      cfg.Store.OAuthClients(),
		authCodes:    cfg.Store.AuthCodes(),
		rules:        cfg.Store.Rules(),
		issuedTokens: cfg.Store.IssuedTokens(),
		grants:       cfg.Store.RefreshGrants(),
		blacklist:    cfg.Store.Blacklist(),
		now:          time.Now,
	}
}

// AuthorizeRequest is the /api/connect/authorize input.
type AuthorizeRequest struct {
	ClientID      string
	RedirectURI   string
	State         string
	Aud           string
	Scope         []string
	Nonce         string
	CoseKey       string // CIP-30 signData `key` (COSE_Key); issuer recovers the vkey
	Signature     string
	CodeChallenge string
	DevicePubkey  string
}

// ValidateClient checks a client_id/redirect_uri/aud combination (used by both
// GET /connect and POST /api/connect/authorize).
func (s *Server) ValidateClient(ctx context.Context, clientID, redirectURI, aud string) (*domain.OAuthClient, error) {
	c, err := s.clients.Get(ctx, clientID)
	if errors.Is(err, domain.ErrNotFound) {
		return nil, ErrInvalidClient
	}
	if err != nil {
		return nil, err
	}
	if c.Status != "active" {
		return nil, ErrInvalidClient
	}
	if !contains(c.RedirectURIs, redirectURI) {
		return nil, ErrInvalidRequest
	}
	if aud != "" && !contains(c.AllowedAudiences, aud) {
		return nil, ErrInvalidRequest
	}
	return c, nil
}

// Authorize verifies the wallet signature, recomputes eligibility from the live
// snapshot, and on success issues a one-time authorization code bound to the
// request context. The returned code is plaintext (stored hashed).
func (s *Server) Authorize(ctx context.Context, req AuthorizeRequest) (code string, err error) {
	if _, err := s.ValidateClient(ctx, req.ClientID, req.RedirectURI, req.Aud); err != nil {
		return "", err
	}
	// PKCE is mandatory for every client (OAuth 2.1): the authorization code is
	// bound to a code_challenge that must be proven at token exchange.
	if req.CodeChallenge == "" {
		return "", ErrInvalidRequest
	}

	sch, err := s.cfg.Wallet.Verify(ctx, domain.NonceIssue, req.CoseKey, req.Nonce, req.Signature)
	if err != nil {
		return "", ErrAccessDenied
	}

	eligible, _, err := s.evaluate(ctx, sch)
	if err != nil {
		return "", err
	}
	if !eligible {
		return "", ErrNotEligible
	}

	plain := crypto.RandomToken(32)
	now := s.now()
	rec := domain.AuthorizationCode{
		Code: crypto.HashToken(plain), ClientID: req.ClientID, StakeCredentialHash: sch,
		Aud: req.Aud, Scope: req.Scope, RedirectURI: req.RedirectURI,
		CodeChallenge: &req.CodeChallenge, // always set — PKCE is mandatory
		ExpiresAt:     now.Add(s.cfg.CodeTTL), CreatedAt: now,
	}
	if err := s.authCodes.Create(ctx, rec); err != nil {
		return "", err
	}
	return plain, nil
}

// evaluate recomputes eligibility for a stake credential from the live snapshot
// and active rules (shared by authorize and token). Blacklisted credentials are
// never eligible.
func (s *Server) evaluate(ctx context.Context, sch string) (bool, rules.Decision, error) {
	if blocked, err := s.blacklist.Has(ctx, sch); err != nil {
		return false, rules.Decision{}, err
	} else if blocked {
		return false, rules.Decision{Reason: "blacklisted"}, nil
	}
	snap, err := s.cfg.Chain.Snapshot(ctx, sch)
	if err != nil {
		return false, rules.Decision{}, err
	}
	ruleset, err := s.rules.ListActive(ctx)
	if err != nil {
		return false, rules.Decision{}, err
	}
	in := rules.InputFromSnapshot(s.cfg.PoolID, snap)
	d := rules.Evaluate(in, ruleset, snap.Epoch)
	return d.Eligible, d, nil
}

func contains(xs []string, v string) bool { return slices.Contains(xs, v) }
