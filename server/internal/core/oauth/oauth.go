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
	"ouro-pass/server/internal/core/membership"
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
	Network    string // mainnet | preprod | preview (for epoch→time, member_since)
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

	// Thin issuer gate (S0004 §2.5): only pool members (pending/active) get a code;
	// `none` is denied. Business policy (thresholds → access) is the RP's.
	state, _, err := s.classify(ctx, sch)
	if err != nil {
		return "", err
	}
	if state == membership.StateNone {
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


// attestation is the issuance-time view of a credential: membership state, the
// exact staking facts for claims, and the optional first-party tier. Tier is
// transitionally sourced from the rules engine; p4-1 moves it to
// PoolConfig.tier_rules and deletes rules.
type attestation struct {
	state        membership.State
	activeStake  string
	epochsActive int
	memberSince  time.Time
	tier         string
}

// classify resolves a credential's membership state from the live snapshot, with
// blacklisted credentials forced to `none` (snap is nil then).
func (s *Server) classify(ctx context.Context, sch string) (membership.State, *chain.Snapshot, error) {
	if blocked, err := s.blacklist.Has(ctx, sch); err != nil {
		return membership.StateNone, nil, err
	} else if blocked {
		return membership.StateNone, nil, nil
	}
	snap, err := s.cfg.Chain.Snapshot(ctx, sch)
	if err != nil {
		return membership.StateNone, nil, err
	}
	return membership.DeriveState(snap, s.cfg.PoolID), snap, nil
}

// Membership derives a credential's current pool-membership state (S0004 §2.2):
// the reconciler's signal. Blacklisted credentials are `none`.
func (s *Server) Membership(ctx context.Context, sch string) (membership.State, error) {
	st, _, err := s.classify(ctx, sch)
	return st, err
}

// attest builds the issuance-time attestation (state + staking facts + first-party
// tier). The thin issuer gate is the caller's: state `none` → deny.
func (s *Server) attest(ctx context.Context, sch string) (*attestation, error) {
	st, snap, err := s.classify(ctx, sch)
	if err != nil {
		return nil, err
	}
	a := &attestation{state: st}
	if snap != nil {
		a.activeStake = snap.ActiveStakeLovelace
		a.epochsActive = snap.EpochsDelegated
		a.memberSince = s.memberSince(snap)
	}
	if st != membership.StateNone {
		a.tier = s.firstPartyTier(ctx, st, snap)
	}
	return a, nil
}

// Attest exposes the issuance-time state + first-party tier for the issuer's own
// channels (Telegram). External relying parties never call this — they read the
// raw token facts and apply their own policy.
func (s *Server) Attest(ctx context.Context, sch string) (membership.State, string, error) {
	a, err := s.attest(ctx, sch)
	if err != nil {
		return membership.StateNone, "", err
	}
	return a.state, a.tier, nil
}

// firstPartyTier maps (state, active stake) to the issuer's thin first-party tier
// via PoolConfig.tier_rules (S0004 §2.6). Errors / no match degrade to no tier
// (the optional opinion is simply absent).
func (s *Server) firstPartyTier(ctx context.Context, state membership.State, snap *chain.Snapshot) string {
	pc, err := s.cfg.Store.PoolConfig().Get(ctx, s.cfg.PoolID)
	if err != nil {
		return ""
	}
	return membership.TierFor(state, snap.ActiveStakeLovelace, pc.TierRules)
}

// memberSince stamps when the credential's current active run began: the start of
// epoch (snapshotEpoch - epochsActive + 1). Zero for non-active / unknown network.
func (s *Server) memberSince(snap *chain.Snapshot) time.Time {
	if snap.EpochsDelegated <= 0 {
		return time.Time{}
	}
	start := snap.Epoch - uint64(snap.EpochsDelegated) + 1
	t, ok := chain.EpochStart(s.cfg.Network, start)
	if !ok {
		return time.Time{}
	}
	return t
}

func contains(xs []string, v string) bool { return slices.Contains(xs, v) }
