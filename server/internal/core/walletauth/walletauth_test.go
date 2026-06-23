package walletauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

func testStore(t *testing.T) *store.Store {
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
	return st
}

// signNonce builds a CIP-8 COSE_Sign1 over the nonce, as a CIP-30 wallet would.
func signNonce(t *testing.T, priv ed25519.PrivateKey, nonce string) string {
	t.Helper()
	protected, _ := cbor.Marshal(map[int]int{1: -8}) // alg EdDSA
	toSign, _ := cbor.Marshal(struct {
		_       struct{} `cbor:",toarray"`
		Ctx     string
		Body    []byte
		AAD     []byte
		Payload []byte
	}{Ctx: "Signature1", Body: protected, AAD: []byte{}, Payload: []byte(nonce)})
	sig := ed25519.Sign(priv, toSign)
	msg, _ := cbor.Marshal([]any{protected, map[int]int{}, []byte(nonce), sig})
	return hex.EncodeToString(msg)
}

func TestChallengeVerify_RoundTrip(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	vkeyHex := hex.EncodeToString(pub)
	wantHash := hex.EncodeToString(crypto.Blake2b224(pub))

	nonce, exp, err := svc.Challenge(ctx, domain.NonceIssue, vkeyHex)
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if nonce == "" || !exp.After(time.Now()) {
		t.Fatalf("bad challenge: nonce=%q exp=%v", nonce, exp)
	}

	hash, err := svc.Verify(ctx, domain.NonceIssue, vkeyHex, nonce, signNonce(t, priv, nonce))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if hash != wantHash {
		t.Fatalf("credential hash = %s, want %s", hash, wantHash)
	}

	// Replay of a consumed nonce → ErrConsumed.
	if _, err := svc.Verify(ctx, domain.NonceIssue, vkeyHex, nonce, signNonce(t, priv, nonce)); err != domain.ErrConsumed {
		t.Fatalf("replay: %v, want ErrConsumed", err)
	}
}

func TestVerify_RejectsWrongKeyAndTamper(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	vkeyHex := hex.EncodeToString(pub)

	// Signature by a different key than the nonce was bound to.
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	nonce, _, _ := svc.Challenge(ctx, domain.NonceIssue, vkeyHex)
	// Present the other key's vkey → nonce binding mismatch (nonce bound to pub).
	if _, err := svc.Verify(ctx, domain.NonceIssue, hex.EncodeToString(otherPub), nonce, signNonce(t, otherPriv, nonce)); err == nil {
		t.Fatal("verify must reject signature from unbound key")
	}

	// Correct key but tampered signature.
	nonce2, _, _ := svc.Challenge(ctx, domain.NonceIssue, vkeyHex)
	sig := signNonce(t, priv, nonce2)
	bad := []byte(sig)
	bad[len(bad)-2] ^= 0x0f // flip a hex nibble in the signature
	if _, err := svc.Verify(ctx, domain.NonceIssue, vkeyHex, nonce2, string(bad)); err == nil {
		t.Fatal("verify must reject tampered signature")
	}
}

func TestPurgeExpiredNonces(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	svc := New(st, time.Minute)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)
	vkey := hex.EncodeToString(pub)

	// Issue a nonce, then advance the service clock past its TTL.
	if _, _, err := svc.Challenge(ctx, domain.NonceIssue, vkey); err != nil {
		t.Fatal(err)
	}
	svc.now = func() time.Time { return time.Now().Add(2 * time.Minute) } // past the 1m TTL
	n, err := svc.PurgeExpiredNonces(ctx)
	if err != nil || n != 1 {
		t.Fatalf("purge removed %d (err %v), want 1", n, err)
	}
}

func TestVerify_WrongPurposeRejected(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	vkeyHex := hex.EncodeToString(pub)
	nonce, _, _ := svc.Challenge(ctx, domain.NonceIssue, vkeyHex)
	// Issued for "issue", verified as "activation" → ErrPurpose.
	if _, err := svc.Verify(ctx, domain.NonceActivation, vkeyHex, nonce, signNonce(t, priv, nonce)); err != domain.ErrPurpose {
		t.Fatalf("purpose: %v, want ErrPurpose", err)
	}
}
