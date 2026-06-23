// Package walletauth is the wallet-signature primitive shared by issuance,
// channel activation, and admin login: it issues one-time nonces and verifies
// CIP-30 signData (COSE) responses, mapping a stake verification key to its
// stake credential hash (the durable identity key, C8). The clock is injected
// for deterministic tests.
package walletauth

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/store"
	"github.com/poolops/issuer/internal/utils/crypto"
)

// nonceBytes is the entropy size of an issued nonce.
const nonceBytes = 32

// Service issues and verifies wallet-signing nonces.
type Service struct {
	nonces   *store.AuthNonceRepo
	ttl      time.Duration
	now      func() time.Time
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

// Challenge issues a nonce bound to the given stake vkey and purpose. The
// returned nonce is signed by the wallet and submitted back to Verify.
func (s *Service) Challenge(ctx context.Context, purpose domain.NoncePurpose, stakeVkeyHex string) (nonce string, expiresAt time.Time, err error) {
	pub, err := decodeVkey(stakeVkeyHex)
	if err != nil {
		return "", time.Time{}, err
	}
	keyHash := hex.EncodeToString(crypto.Blake2b224(pub))
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

// Verify consumes the nonce (single-use, unexpired, matching purpose), checks
// the COSE_Sign1 signature over the nonce by stakeVkey, enforces the nonce↔key
// binding, and returns the signer's stake credential hash.
func (s *Service) Verify(ctx context.Context, purpose domain.NoncePurpose, stakeVkeyHex, nonce, signatureHex string) (stakeCredentialHash string, err error) {
	pub, err := decodeVkey(stakeVkeyHex)
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

// decodeVkey parses a hex-encoded 32-byte Ed25519 stake verification key.
func decodeVkey(hexStr string) (ed25519.PublicKey, error) {
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("walletauth: decode stake_vkey: %w", err)
	}
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("walletauth: stake_vkey must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	return ed25519.PublicKey(raw), nil
}
