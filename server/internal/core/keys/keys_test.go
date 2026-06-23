package keys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"path/filepath"
	"testing"
	"time"

	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

func testService(t *testing.T) *Service {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, store.SQLite, "file:"+filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	key := make([]byte, 32)
	rand.Read(key)
	cipher, _ := crypto.NewFieldCipher(key)
	return New(st, cipher)
}

func TestRotate_BootstrapThenOverlap(t *testing.T) {
	ctx := context.Background()
	s := testService(t)

	// Bootstrap: first rotate creates the sole active key.
	kid1, err := s.Rotate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys, _ := s.PublicJWKSKeys(ctx)
	if len(keys) != 1 || keys[0].KID != kid1 || keys[0].Status != "active" {
		t.Fatalf("after bootstrap: %+v", keys)
	}

	// Rotate again: new active, prior → rotating, both published (overlap).
	time.Sleep(2 * time.Millisecond)
	kid2, err := s.Rotate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if kid2 == kid1 {
		t.Fatal("rotation produced duplicate kid")
	}
	keys, _ = s.PublicJWKSKeys(ctx)
	if len(keys) != 2 {
		t.Fatalf("overlap should publish 2 keys, got %d", len(keys))
	}
	statusByKID := map[string]string{}
	for _, k := range keys {
		statusByKID[k.KID] = k.Status
	}
	if statusByKID[kid1] != "rotating" || statusByKID[kid2] != "active" {
		t.Fatalf("overlap statuses wrong: %v", statusByKID)
	}
}

func TestActiveSigner_SignsVerifiably(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	if _, err := s.ActiveSigner(ctx); err == nil {
		t.Fatal("expected error with no active key")
	}
	kid, _ := s.Rotate(ctx)

	signer, err := s.ActiveSigner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if signer.KID != kid {
		t.Fatalf("signer kid = %s, want %s", signer.KID, kid)
	}
	// The decrypted private key must produce signatures verifiable by the
	// published public key (proves encrypt/decrypt round-trip).
	msg := []byte("hello")
	sig := ed25519.Sign(signer.Priv, msg)
	keys, _ := s.PublicJWKSKeys(ctx)
	if !ed25519.Verify(keys[0].Public, msg, sig) {
		t.Fatal("signature not verifiable by published public key")
	}
}

func TestRetireRotating(t *testing.T) {
	ctx := context.Background()
	s := testService(t)
	kid1, _ := s.Rotate(ctx)
	time.Sleep(2 * time.Millisecond)
	s.Rotate(ctx) // kid1 → rotating

	// Retire rotating keys older than "now" → kid1 retired, dropped from JWKS.
	retired, err := s.RetireRotating(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(retired) != 1 || retired[0] != kid1 {
		t.Fatalf("retired = %v, want [%s]", retired, kid1)
	}
	keys, _ := s.PublicJWKSKeys(ctx)
	if len(keys) != 1 {
		t.Fatalf("after retire, JWKS should have 1 key, got %d", len(keys))
	}
}
