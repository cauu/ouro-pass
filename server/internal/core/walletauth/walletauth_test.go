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

// rewardAddr builds a raw (hex) CIP-19 mainnet reward address for pub — the form
// a CIP-30 getRewardAddresses can return, accepted by Challenge.
func rewardAddr(pub ed25519.PublicKey) string {
	cred := crypto.Blake2b224(pub)
	return hex.EncodeToString(append([]byte{0xe1}, cred...))
}

// coseKey builds the COSE_Key (signData `key` field) carrying pub, as a wallet
// would, for Verify.
func coseKey(t *testing.T, pub ed25519.PublicKey) string {
	t.Helper()
	b, err := cbor.Marshal(map[int]any{1: 1, -1: 6, -2: []byte(pub), 3: -8})
	if err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// signPayload builds a CIP-8 COSE_Sign1 over payload, as a CIP-30 wallet would.
func signPayload(t *testing.T, priv ed25519.PrivateKey, payload []byte) string {
	t.Helper()
	protected, _ := cbor.Marshal(map[int]int{1: -8}) // alg EdDSA
	toSign, _ := cbor.Marshal(struct {
		_       struct{} `cbor:",toarray"`
		Ctx     string
		Body    []byte
		AAD     []byte
		Payload []byte
	}{Ctx: "Signature1", Body: protected, AAD: []byte{}, Payload: payload})
	sig := ed25519.Sign(priv, toSign)
	msg, _ := cbor.Marshal([]any{protected, map[int]int{}, payload, sig})
	return hex.EncodeToString(msg)
}

func signNonce(t *testing.T, priv ed25519.PrivateKey, nonce string) string {
	return signPayload(t, priv, []byte(nonce))
}

func TestChallengeVerify_RoundTrip(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	wantHash := hex.EncodeToString(crypto.Blake2b224(pub))

	nonce, exp, err := svc.Challenge(ctx, domain.NonceIssue, rewardAddr(pub))
	if err != nil {
		t.Fatalf("challenge: %v", err)
	}
	if nonce == "" || !exp.After(time.Now()) {
		t.Fatalf("bad challenge: nonce=%q exp=%v", nonce, exp)
	}

	hash, err := svc.Verify(ctx, domain.NonceIssue, coseKey(t, pub), nonce, signNonce(t, priv, nonce))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if hash != wantHash {
		t.Fatalf("credential hash = %s, want %s", hash, wantHash)
	}

	// Replay of a consumed nonce → ErrConsumed.
	if _, err := svc.Verify(ctx, domain.NonceIssue, coseKey(t, pub), nonce, signNonce(t, priv, nonce)); err != domain.ErrConsumed {
		t.Fatalf("replay: %v, want ErrConsumed", err)
	}
}

func TestVerify_RejectsWrongKeyAndTamper(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)

	// Signature by a different key than the nonce was bound to → hash mismatch.
	otherPub, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
	nonce, _, _ := svc.Challenge(ctx, domain.NonceIssue, rewardAddr(pub))
	if _, err := svc.Verify(ctx, domain.NonceIssue, coseKey(t, otherPub), nonce, signNonce(t, otherPriv, nonce)); err == nil {
		t.Fatal("verify must reject a COSE_Key whose hash != bound hash")
	}

	// Correct key but tampered signature.
	nonce2, _, _ := svc.Challenge(ctx, domain.NonceIssue, rewardAddr(pub))
	sig := signNonce(t, priv, nonce2)
	bad := []byte(sig)
	bad[len(bad)-2] ^= 0x0f // flip a hex nibble in the signature
	if _, err := svc.Verify(ctx, domain.NonceIssue, coseKey(t, pub), nonce2, string(bad)); err == nil {
		t.Fatal("verify must reject tampered signature")
	}
}

// TestVerify_RejectsPayloadMismatch covers the nonce↔payload binding (TC-2): a
// signature over some other payload must not verify against the nonce.
func TestVerify_RejectsPayloadMismatch(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	nonce, _, _ := svc.Challenge(ctx, domain.NonceIssue, rewardAddr(pub))

	badSig := signPayload(t, priv, []byte("not-the-nonce"))
	if _, err := svc.Verify(ctx, domain.NonceIssue, coseKey(t, pub), nonce, badSig); err != crypto.ErrCOSEPayload {
		t.Fatalf("payload mismatch: %v, want ErrCOSEPayload", err)
	}
}

// TestVerify_RejectsBadCOSEKey covers a malformed/wrong-curve COSE_Key (TC-2).
func TestVerify_RejectsBadCOSEKey(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	nonce, _, _ := svc.Challenge(ctx, domain.NonceIssue, rewardAddr(pub))

	bad, _ := cbor.Marshal(map[int]any{1: 1, -1: 4 /* not Ed25519 */, -2: []byte(pub)})
	if _, err := svc.Verify(ctx, domain.NonceIssue, hex.EncodeToString(bad), nonce, signNonce(t, priv, nonce)); err == nil {
		t.Fatal("verify must reject a malformed COSE_Key")
	}
}

// TestChallenge_RejectsBadAddress covers reward-address parse failure (TC-2).
func TestChallenge_RejectsBadAddress(t *testing.T) {
	ctx := context.Background()
	svc := New(testStore(t), time.Minute)
	if _, _, err := svc.Challenge(ctx, domain.NonceIssue, "not-an-address"); err == nil {
		t.Fatal("challenge must reject an unparseable reward address")
	}
}

func TestPurgeExpiredNonces(t *testing.T) {
	ctx := context.Background()
	st := testStore(t)
	svc := New(st, time.Minute)
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	// Issue a nonce, then advance the service clock past its TTL.
	if _, _, err := svc.Challenge(ctx, domain.NonceIssue, rewardAddr(pub)); err != nil {
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
	nonce, _, _ := svc.Challenge(ctx, domain.NonceIssue, rewardAddr(pub))
	// Issued for "issue", verified as "activation" → ErrPurpose.
	if _, err := svc.Verify(ctx, domain.NonceActivation, coseKey(t, pub), nonce, signNonce(t, priv, nonce)); err != domain.ErrPurpose {
		t.Fatalf("purpose: %v, want ErrPurpose", err)
	}
}
