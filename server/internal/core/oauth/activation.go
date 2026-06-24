package oauth

import (
	"context"
	"time"

	"ouro-pass/server/internal/core/rules"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/crypto"
)

// activationTTL bounds how long a channel activation code is valid.
const activationTTL = 30 * time.Minute

// ActivationResult is returned to the first-party binding page (detailed §9.5).
type ActivationResult struct {
	ActivationCode string
	DeepLink       string
	ExpiresAt      time.Time
}

// CreateActivation verifies the wallet, gates on eligibility, and issues a
// one-time short activation code plus a Telegram deep link (D8). The code is
// stored hashed and consumed by the bot on /start.
func (s *Server) CreateActivation(ctx context.Context, channelType, nonce, coseKey, signature, botUsername string) (*ActivationResult, error) {
	if channelType == "" {
		channelType = "telegram"
	}
	sch, err := s.cfg.Wallet.Verify(ctx, domain.NonceActivation, coseKey, nonce, signature)
	if err != nil {
		return nil, ErrAccessDenied
	}
	eligible, _, err := s.evaluate(ctx, sch)
	if err != nil {
		return nil, err
	}
	if !eligible {
		return nil, ErrNotEligible
	}

	short := crypto.RandomToken(16) // ~22 chars, fits Telegram's 64-char start param
	now := s.now()
	exp := now.Add(activationTTL)
	if err := s.cfg.Store.ActivationCodes().Create(ctx, domain.ActivationCode{
		Code: crypto.HashToken(short), StakeCredentialHash: sch, ChannelType: channelType,
		Status: domain.ActivationActive, ExpiresAt: exp, CreatedAt: now,
	}); err != nil {
		return nil, err
	}

	deepLink := ""
	if channelType == "telegram" && botUsername != "" {
		deepLink = "https://t.me/" + botUsername + "?start=" + short
	}
	return &ActivationResult{ActivationCode: short, DeepLink: deepLink, ExpiresAt: exp}, nil
}

// Eligibility is the exported eligibility check used by the Telegram worker when
// redeeming an activation code (it re-evaluates to populate the session tier /
// entitlements; D8).
func (s *Server) Eligibility(ctx context.Context, sch string) (bool, rules.Decision, error) {
	return s.evaluate(ctx, sch)
}
