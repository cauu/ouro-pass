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

	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/core/tier"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

// Errors surfaced to handlers as OAuth error codes.
var (
	ErrInvalidClient  = errors.New("invalid_client")
	ErrInvalidRequest = errors.New("invalid_request")
	ErrNotEligible    = errors.New("not_eligible")
	ErrAccessDenied   = errors.New("access_denied")
)

// Config bundles the server's collaborators and parameters.
type Config struct {
	Store  *store.Store
	Wallet *walletauth.Service
	Keys   *keys.Service
	// Attestors resolves the active attestor set to evaluate, per call, so admin
	// config changes take effect immediately (S0006). Injected: tests supply a
	// fixed set; production resolves from the store (ListActive → BuildSet).
	Attestors  func(ctx context.Context) (*attestor.Set, error)
	Issuer     string // token `iss`: the issuer's deployment identity (S0006 D3)
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

	// Thin issuer gate (S0004 §2.5, generalized to ANY-of in S0006): only holders
	// of at least one configured credential (pending/active) get a code; held-by-
	// nothing is denied. Business policy (thresholds → access) is the RP's.
	res, err := s.evaluate(ctx, sch)
	if err != nil {
		return "", err
	}
	if representativeState(res) == membership.StateNone {
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

// attestation is the issuance-time view of a credential over the WHOLE attestor
// set (S0006): the held credentials' self-describing claims (→ token
// credentials[]), the aggregate facts (→ tier DSL), a representative
// pool-membership state (gate + first-party consumers), and the first-party tier.
type attestation struct {
	state       membership.State  // representative: active if any held-active, else pending if any held, else none
	tier        string            // first-party opinion (tier DSL over facts)
	credentials []map[string]any  // held credentials' self-describing claims → token credentials[]
	facts       map[string]string // aggregate named facts (tier DSL input)
}

// evaluate runs the configured attestor set for a subject, with blacklisted
// credentials forced to an empty (not-held) result.
func (s *Server) evaluate(ctx context.Context, sch string) (*attestor.Result, error) {
	if blocked, err := s.blacklist.Has(ctx, sch); err != nil {
		return nil, err
	} else if blocked {
		return &attestor.Result{Facts: map[string]string{}}, nil
	}
	set, err := s.cfg.Attestors(ctx)
	if err != nil {
		return nil, err
	}
	return set.Evaluate(ctx, sch)
}

// representativeState collapses a multi-attestor result to one pool-membership
// state for the thin gate and first-party consumers (reconciler / Telegram):
// active if any credential is active, else pending if any is held, else none.
func representativeState(res *attestor.Result) membership.State {
	if res.Facts[attestor.FactAnyActive] == "true" {
		return membership.StateActive
	}
	if res.Held {
		return membership.StatePending
	}
	return membership.StateNone
}

// Membership derives a credential's current representative pool-membership state
// (S0004 §2.2 generalized to ANY-of, S0006): the reconciler's signal. Blacklisted
// or not-held-by-any credentials are `none`.
func (s *Server) Membership(ctx context.Context, sch string) (membership.State, error) {
	res, err := s.evaluate(ctx, sch)
	if err != nil {
		return membership.StateNone, err
	}
	return representativeState(res), nil
}

// attest builds the issuance-time attestation over the attestor set. The thin
// issuer gate is the caller's: representative state `none` (held by nothing) → deny.
func (s *Server) attest(ctx context.Context, sch string) (*attestation, error) {
	res, err := s.evaluate(ctx, sch)
	if err != nil {
		return nil, err
	}
	a := &attestation{
		state:       representativeState(res),
		facts:       res.Facts,
		credentials: claimsOf(res.Attestations),
	}
	if a.state != membership.StateNone {
		if a.tier, err = s.firstPartyTier(ctx, a.facts); err != nil {
			return nil, err
		}
	}
	return a, nil
}

// Attest exposes the issuance-time representative state + first-party tier for the
// issuer's own channels (Telegram). External relying parties never call this —
// they read the raw token facts and apply their own policy.
func (s *Server) Attest(ctx context.Context, sch string) (membership.State, string, error) {
	a, err := s.attest(ctx, sch)
	if err != nil {
		return membership.StateNone, "", err
	}
	return a.state, a.tier, nil
}

// firstPartyTier maps the aggregate facts to the issuer's thin first-party tier
// via the issuer-global tier_rules boolean DSL (S0006 §2.4). A rules-read error is
// PROPAGATED (not swallowed to ""), so callers apply the D8 policy correctly:
// issue/activation fail-closed, the reconciler fail-opens (keeps the stored tier)
// — a transient tier_rules read failure must never be mistaken for "no tier" and
// wipe a member's tier (S0019 p3-1). A genuine no-match still returns ("", nil).
func (s *Server) firstPartyTier(ctx context.Context, facts map[string]string) (string, error) {
	rules, err := s.cfg.Store.Issuer().GetTierRules(ctx)
	if err != nil {
		return "", err
	}
	return tier.Eval(rules, facts), nil
}

func contains(xs []string, v string) bool { return slices.Contains(xs, v) }

// claimsOf projects the HELD attestations' self-describing claims into the token
// credentials[] payload (S0006 §2.3) — the token attests what the subject holds,
// so not-held (none) credentials are omitted.
func claimsOf(atts []*attestor.Attestation) []map[string]any {
	out := make([]map[string]any, 0, len(atts))
	for _, a := range atts {
		if a.Held {
			out = append(out, a.Claim)
		}
	}
	return out
}
