// Package keys manages the issuer's rotatable Ed25519 signing-key lifecycle
// (no certificate chain — C9). It is a stateful service (not pure — C10): key
// generation, encrypted persistence, and JWKS publication are inherent side
// effects, kept in a thin shell. Rotation is zero-downtime via JWKS overlap
// (detailed §3.5/§9.8): a new key becomes active, the prior active key moves to
// `rotating` and stays published until its short-lived tokens expire.
package keys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"time"

	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/store"
	"github.com/poolops/issuer/internal/utils/crypto"
	"github.com/poolops/issuer/internal/utils/jose"
)

// Signer is an active signing key ready to mint tokens.
type Signer struct {
	KID  string
	Priv ed25519.PrivateKey
}

// Service manages signing keys.
type Service struct {
	repo   *store.IssuerKeyRepo
	cipher *crypto.FieldCipher
	now    func() time.Time
	newKID func(time.Time) string
}

// New builds a key service. cipher encrypts private keys at rest (C5).
func New(st *store.Store, cipher *crypto.FieldCipher) *Service {
	return &Service{
		repo:   st.IssuerKeys(),
		cipher: cipher,
		now:    time.Now,
		newKID: defaultKID,
	}
}

func defaultKID(now time.Time) string {
	return "pao-issuer-" + now.UTC().Format("2006-01") + "-" + crypto.RandomToken(3)
}

// Rotate generates a new active signing key and demotes any currently-active
// keys to `rotating` (overlap). It also serves bootstrap: the first call with
// no prior key simply creates the initial active key. Returns the new kid.
func (s *Service) Rotate(ctx context.Context) (string, error) {
	now := s.now()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", err
	}
	encPriv, err := s.cipher.Encrypt(priv)
	if err != nil {
		return "", err
	}
	// Demote existing active keys to rotating before activating the new one.
	actives, err := s.repo.ListByStatus(ctx, domain.KeyActive)
	if err != nil {
		return "", err
	}
	for _, k := range actives {
		if err := s.repo.SetStatus(ctx, k.KID, domain.KeyRotating, nil); err != nil {
			return "", err
		}
	}
	kid := s.newKID(now)
	if err := s.repo.Create(ctx, domain.IssuerKey{
		KID: kid, PublicKey: pub, EncryptedPrivateKey: encPriv,
		Status: domain.KeyActive, ValidFrom: &now, CreatedAt: now,
	}); err != nil {
		return "", err
	}
	return kid, nil
}

// ActiveSigner returns the current active signing key with its decrypted private
// key, for minting new tokens. Errors if no active key exists.
func (s *Service) ActiveSigner(ctx context.Context) (*Signer, error) {
	actives, err := s.repo.ListByStatus(ctx, domain.KeyActive)
	if err != nil {
		return nil, err
	}
	if len(actives) == 0 {
		return nil, errors.New("keys: no active signing key (call Rotate first)")
	}
	// Newest active wins if more than one ever coexists.
	k := actives[len(actives)-1]
	privBytes, err := s.cipher.Decrypt(k.EncryptedPrivateKey)
	if err != nil {
		return nil, err
	}
	return &Signer{KID: k.KID, Priv: ed25519.PrivateKey(privBytes)}, nil
}

// PublicJWKSKeys returns the public keys to publish in JWKS: active and rotating
// keys (the overlap set), so verifiers accept tokens from both during rotation.
func (s *Service) PublicJWKSKeys(ctx context.Context) ([]jose.PublicKey, error) {
	var out []jose.PublicKey
	for _, status := range []domain.IssuerKeyStatus{domain.KeyActive, domain.KeyRotating} {
		ks, err := s.repo.ListByStatus(ctx, status)
		if err != nil {
			return nil, err
		}
		for _, k := range ks {
			out = append(out, jose.PublicKey{KID: k.KID, Public: ed25519.PublicKey(k.PublicKey), Status: string(k.Status)})
		}
	}
	return out, nil
}

// RetireRotating moves rotating keys created before `olderThan` to `retired`
// (called once their short-lived tokens have all expired). Returns retired kids.
func (s *Service) RetireRotating(ctx context.Context, olderThan time.Time) ([]string, error) {
	rotating, err := s.repo.ListByStatus(ctx, domain.KeyRotating)
	if err != nil {
		return nil, err
	}
	now := s.now()
	var retired []string
	for _, k := range rotating {
		if k.ValidFrom != nil && k.ValidFrom.After(olderThan) {
			continue
		}
		if err := s.repo.SetStatus(ctx, k.KID, domain.KeyRetired, &now); err != nil {
			return nil, err
		}
		retired = append(retired, k.KID)
	}
	return retired, nil
}

// Revoke marks a key revoked (emergency compromise response, §3.5). Its tokens
// are invalidated; affected members re-sign under the new kid on next refresh.
func (s *Service) Revoke(ctx context.Context, kid string) error {
	now := s.now()
	return s.repo.SetStatus(ctx, kid, domain.KeyRevoked, &now)
}
