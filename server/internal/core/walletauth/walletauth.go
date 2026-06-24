// Package walletauth is the wallet-signature primitive shared by issuance,
// channel activation, and admin login: it issues one-time nonces and verifies
// CIP-30 signData (COSE) responses, mapping a stake verification key to its
// stake credential hash (the durable identity key, C8). The clock is injected
// for deterministic tests.
//
// CIP-30 reality (S0003): a wallet exposes the bare stake vkey only inside the
// COSE_Key returned by signData — never before signing. So Challenge binds the
// nonce to the stake credential hash derived from the reward address (available
// pre-signing), and Verify recovers the vkey from the COSE_Key at submit time and
// proves it both ways: the COSE_Sign1 signature must verify under it AND
// blake2b224(vkey) must equal the bound hash. The bound hash alone is only a
// claim — the stake vkey is public once it has witnessed an on-chain certificate,
// so the signature is the real authentication.
package walletauth

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
)

// nonceBytes is the entropy size of an issued nonce.
const nonceBytes = 32

// Service issues and verifies wallet-signing nonces.
type Service struct {
	nonces    *store.AuthNonceRepo
	ttl       time.Duration
	now       func() time.Time
	randToken func(int) string
}

// New builds a wallet-auth service. ttl bounds nonce validity.
func New(st *store.Store, ttl time.Duration) *Service {
	return &Service{
		nonces:    st.AuthNonces(),
		ttl:       ttl,
		now:       time.Now,
		randToken: crypto.RandomToken,
	}
}

// Challenge issues a nonce bound to the stake credential behind the given reward
// (stake) address and purpose. The address is what a CIP-30 wallet can supply
// before signing (getRewardAddresses); the nonce is then signed via signData and
// submitted back to Verify.
func (s *Service) Challenge(ctx context.Context, purpose domain.NoncePurpose, stakeAddress string) (nonce string, expiresAt time.Time, err error) {
	keyHash, err := chain.StakeHashFromRewardAddress(stakeAddress)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("walletauth: %w", err)
	}
	now := s.now()
	nonce = s.randToken(nonceBytes)
	expiresAt = now.Add(s.ttl)
	rec := domain.AuthNonce{
		Nonce: nonce, Purpose: purpose, BoundKeyHash: &keyHash,
		ExpiresAt: expiresAt, CreatedAt: now,
	}
	if err := s.nonces.Create(ctx, rec); err != nil {
		return "", time.Time{}, err
	}
	return nonce, expiresAt, nil
}

// Verify consumes the nonce (single-use, unexpired, matching purpose), recovers
// the stake vkey from the CIP-30 COSE_Key, checks the COSE_Sign1 signature over
// the nonce by that vkey, enforces the nonce↔key binding (blake2b224(vkey) ==
// bound hash), and returns the signer's stake credential hash. The payload signed
// by the wallet MUST be the nonce bytes (hex(utf8(nonce)) on the wire).
func (s *Service) Verify(ctx context.Context, purpose domain.NoncePurpose, coseKeyHex, nonce, signatureHex string) (stakeCredentialHash string, err error) {
	pub, err := crypto.StakeVkeyFromCOSEKey(coseKeyHex)
	if err != nil {
		return "", err
	}
	keyHash := hex.EncodeToString(crypto.Blake2b224(pub))

	rec, err := s.nonces.Consume(ctx, nonce, purpose, s.now())
	if err != nil {
		return "", err // ErrNotFound/ErrConsumed/ErrExpired/ErrPurpose
	}
	if rec.BoundKeyHash == nil || *rec.BoundKeyHash != keyHash {
		return "", errors.New("walletauth: nonce not bound to this key")
	}

	sig, err := hex.DecodeString(signatureHex)
	if err != nil {
		return "", fmt.Errorf("walletauth: decode signature: %w", err)
	}
	cose, err := crypto.ParseCOSESign1(sig)
	if err != nil {
		return "", err
	}
	if err := cose.Verify(pub, []byte(nonce)); err != nil {
		return "", err
	}
	return keyHash, nil
}

// PurgeExpiredNonces deletes nonces past their validity window (GC). Returns
// the number removed. Safe to call periodically from a maintenance ticker.
func (s *Service) PurgeExpiredNonces(ctx context.Context) (int64, error) {
	return s.nonces.DeleteExpired(ctx, s.now())
}
