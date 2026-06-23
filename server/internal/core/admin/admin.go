// Package admin is the admin-plane identity service: owner-key wallet-signature
// login (no passwords), server-side sessions, RBAC role resolution, and step-up
// re-authentication for sensitive operations (detailed §9.8). The `owner` role
// is anchored to the pool's on-chain owners, approximated by a configured
// allowlist in this build (decision D9).
package admin

import (
	"context"
	"errors"
	"time"

	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

// Errors surfaced to handlers.
var (
	ErrUnauthorized = errors.New("unauthorized")
	ErrForbidden    = errors.New("forbidden")
)

// Service implements admin authentication and session management.
type Service struct {
	wallet     *walletauth.Service
	users      *store.AdminUserRepo
	sessions   *store.AdminSessionRepo
	ownerKeys  map[string]bool // configured on-chain pool owner key hashes (D9)
	poolID     string
	sessionTTL time.Duration
	now        func() time.Time
	randToken  func(int) string
	newID      func() string
}

// Config configures the admin service.
type Config struct {
	Wallet        *walletauth.Service
	Store         *store.Store
	OwnerKeyHash  []string // on-chain pool owner key hashes (D9)
	PoolID        string
	SessionTTL    time.Duration
}

// New builds an admin service.
func New(cfg Config) *Service {
	owners := make(map[string]bool, len(cfg.OwnerKeyHash))
	for _, h := range cfg.OwnerKeyHash {
		if h != "" {
			owners[h] = true
		}
	}
	ttl := cfg.SessionTTL
	if ttl == 0 {
		ttl = 12 * time.Hour
	}
	return &Service{
		wallet: cfg.Wallet, users: cfg.Store.AdminUsers(), sessions: cfg.Store.AdminSessions(),
		ownerKeys: owners, poolID: cfg.PoolID, sessionTTL: ttl,
		now: time.Now, randToken: crypto.RandomToken, newID: crypto.RandomID,
	}
}

// Challenge issues an admin-login nonce bound to the owner key.
func (s *Service) Challenge(ctx context.Context, ownerVkeyHex string) (string, time.Time, error) {
	return s.wallet.Challenge(ctx, domain.NonceAdminLogin, ownerVkeyHex)
}

// Verify validates the signed login nonce, resolves the admin (owner role for a
// configured on-chain owner key; otherwise an existing operator/viewer), and
// creates a session. Returns the plaintext session token (set as a cookie).
func (s *Service) Verify(ctx context.Context, ownerVkeyHex, nonce, signature, ip string) (token string, role domain.AdminRole, err error) {
	keyHash, err := s.wallet.Verify(ctx, domain.NonceAdminLogin, ownerVkeyHex, nonce, signature)
	if err != nil {
		return "", "", ErrUnauthorized
	}

	var user *domain.AdminUser
	if s.ownerKeys[keyHash] {
		// On-chain owner → ensure an owner AdminUser exists (self-bootstrap).
		now := s.now()
		u := domain.AdminUser{AdminID: s.newID(), PoolID: s.poolID, OwnerKeyHash: keyHash, Role: domain.RoleOwner, CreatedAt: now}
		if err := s.users.Upsert(ctx, u); err != nil {
			return "", "", err
		}
		if user, err = s.users.GetByOwnerKeyHash(ctx, keyHash); err != nil {
			return "", "", err
		}
	} else {
		// Operator/viewer must have been added by an owner.
		user, err = s.users.GetByOwnerKeyHash(ctx, keyHash)
		if errors.Is(err, domain.ErrNotFound) {
			return "", "", ErrForbidden
		}
		if err != nil {
			return "", "", err
		}
	}

	plain := s.randToken(32)
	now := s.now()
	var ipPtr *string
	if ip != "" {
		ipPtr = &ip
	}
	if err := s.sessions.Create(ctx, domain.AdminSession{
		SessionToken: crypto.HashToken(plain), AdminID: user.AdminID,
		ExpiresAt: now.Add(s.sessionTTL), IP: ipPtr, CreatedAt: now,
	}); err != nil {
		return "", "", err
	}
	_ = s.users.TouchLogin(ctx, user.AdminID, now)
	return plain, user.Role, nil
}

// Authenticate resolves a session token to its admin user, or ErrUnauthorized.
func (s *Service) Authenticate(ctx context.Context, sessionToken string) (*domain.AdminUser, error) {
	sess, err := s.sessions.GetValid(ctx, crypto.HashToken(sessionToken), s.now())
	if err != nil {
		return nil, ErrUnauthorized
	}
	user, err := s.users.GetByID(ctx, sess.AdminID)
	if err != nil {
		return nil, ErrUnauthorized
	}
	return user, nil
}

// Logout deletes a session.
func (s *Service) Logout(ctx context.Context, sessionToken string) error {
	return s.sessions.Delete(ctx, crypto.HashToken(sessionToken))
}

// VerifyStepUp re-checks a fresh owner signature for a sensitive operation
// (detailed §9.8). The step-up key must match the session admin's owner key.
func (s *Service) VerifyStepUp(ctx context.Context, ownerVkeyHex, nonce, signature, expectedKeyHash string) error {
	keyHash, err := s.wallet.Verify(ctx, domain.NonceStepUp, ownerVkeyHex, nonce, signature)
	if err != nil {
		return ErrUnauthorized
	}
	if keyHash != expectedKeyHash {
		return ErrForbidden
	}
	return nil
}

// ChallengeStepUp issues a step-up nonce bound to the owner key.
func (s *Service) ChallengeStepUp(ctx context.Context, ownerVkeyHex string) (string, time.Time, error) {
	return s.wallet.Challenge(ctx, domain.NonceStepUp, ownerVkeyHex)
}

// RoleRank orders roles for RBAC comparisons (owner > operator > viewer).
func RoleRank(r domain.AdminRole) int {
	switch r {
	case domain.RoleOwner:
		return 3
	case domain.RoleOperator:
		return 2
	case domain.RoleViewer:
		return 1
	default:
		return 0
	}
}

// AtLeast reports whether `have` satisfies the `min` role.
func AtLeast(have, min domain.AdminRole) bool { return RoleRank(have) >= RoleRank(min) }
