package admin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/poolops/issuer/internal/core/walletauth"
	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/store"
	"github.com/poolops/issuer/internal/utils/crypto"
)

func newStore(t *testing.T) *store.Store {
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

func sign(t *testing.T, priv ed25519.PrivateKey, nonce string) string {
	t.Helper()
	protected, _ := cbor.Marshal(map[int]int{1: -8})
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

func newKey() (ed25519.PublicKey, ed25519.PrivateKey, string, string) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	vkey := hex.EncodeToString(pub)
	keyHash := hex.EncodeToString(crypto.Blake2b224(pub))
	return pub, priv, vkey, keyHash
}

func TestVerify_OwnerSelfBootstraps(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	_, priv, vkey, keyHash := newKey()
	svc := New(Config{Wallet: walletauth.New(st, time.Minute), Store: st, OwnerKeyHash: []string{keyHash}, PoolID: "pool1"})

	nonce, _, err := svc.Challenge(ctx, vkey)
	if err != nil {
		t.Fatal(err)
	}
	token, role, err := svc.Verify(ctx, vkey, nonce, sign(t, priv, nonce), "1.2.3.4")
	if err != nil || role != domain.RoleOwner || token == "" {
		t.Fatalf("owner verify: %v role=%s", err, role)
	}
	// Session authenticates and resolves the owner.
	user, err := svc.Authenticate(ctx, token)
	if err != nil || user.Role != domain.RoleOwner {
		t.Fatalf("authenticate: %v %+v", err, user)
	}
	// Logout invalidates it.
	if err := svc.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(ctx, token); err != ErrUnauthorized {
		t.Fatalf("post-logout: %v, want ErrUnauthorized", err)
	}
}

func TestVerify_NonOwnerNeedsSeededRecord(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	_, priv, vkey, keyHash := newKey()
	// keyHash NOT in owner allowlist.
	svc := New(Config{Wallet: walletauth.New(st, time.Minute), Store: st, OwnerKeyHash: nil, PoolID: "pool1"})

	nonce, _, _ := svc.Challenge(ctx, vkey)
	if _, _, err := svc.Verify(ctx, vkey, nonce, sign(t, priv, nonce), ""); err != ErrForbidden {
		t.Fatalf("unknown key: %v, want ErrForbidden", err)
	}

	// Owner seeds an operator with this key hash → login now works as operator.
	st.AdminUsers().Upsert(ctx, domain.AdminUser{AdminID: crypto.RandomID(), PoolID: "pool1", OwnerKeyHash: keyHash, Role: domain.RoleOperator, CreatedAt: time.Now()})
	nonce2, _, _ := svc.Challenge(ctx, vkey)
	_, role, err := svc.Verify(ctx, vkey, nonce2, sign(t, priv, nonce2), "")
	if err != nil || role != domain.RoleOperator {
		t.Fatalf("seeded operator: %v role=%s", err, role)
	}
}

func TestRBAC_AtLeast(t *testing.T) {
	if !AtLeast(domain.RoleOwner, domain.RoleViewer) || !AtLeast(domain.RoleOperator, domain.RoleOperator) {
		t.Error("higher/equal role should satisfy minimum")
	}
	if AtLeast(domain.RoleViewer, domain.RoleOperator) {
		t.Error("viewer must not satisfy operator")
	}
}

func TestStepUp(t *testing.T) {
	ctx := context.Background()
	st := newStore(t)
	_, priv, vkey, keyHash := newKey()
	svc := New(Config{Wallet: walletauth.New(st, time.Minute), Store: st, OwnerKeyHash: []string{keyHash}, PoolID: "pool1"})

	nonce, _, _ := svc.ChallengeStepUp(ctx, vkey)
	if err := svc.VerifyStepUp(ctx, vkey, nonce, sign(t, priv, nonce), keyHash); err != nil {
		t.Fatalf("step-up: %v", err)
	}
	// Step-up by a different key hash than the session owner → forbidden.
	nonce2, _, _ := svc.ChallengeStepUp(ctx, vkey)
	if err := svc.VerifyStepUp(ctx, vkey, nonce2, sign(t, priv, nonce2), "other-hash"); err != ErrForbidden {
		t.Fatalf("step-up wrong key: %v, want ErrForbidden", err)
	}
}
