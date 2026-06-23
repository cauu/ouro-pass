package oauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
)

const testPool = "pool1abc"

// harness wires a Server over a fresh store with a mock chain and one client.
type harness struct {
	srv   *Server
	st    *store.Store
	chain *chain.MockSource
	pub   ed25519.PublicKey
	priv  ed25519.PrivateKey
	vkey  string
}

func newHarness(t *testing.T) *harness {
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
	ks := keys.New(st, cipher)
	if _, err := ks.Rotate(ctx); err != nil {
		t.Fatal(err)
	}

	mock := chain.NewMockSource(480)
	srv := New(Config{
		Store: st, Wallet: walletauth.New(st, time.Minute), Keys: ks, Chain: mock,
		PoolID: testPool, Issuer: "poolops:" + testPool, ServerSalt: []byte("salt"),
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	})

	// One active confidential client + a gold rule.
	if err := st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential, Party: domain.FirstParty,
		RedirectURIs: []string{"https://app/cb"}, AllowedAudiences: []string{"app:ouro"},
		AllowedScopes: []string{"read"}, Status: "active", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Rules().Upsert(ctx, domain.MembershipRule{
		RuleID: "gold", Name: "gold", Tier: "gold", Priority: 10, Status: domain.RuleActive,
		RuleConfig: json.RawMessage(`{"min_active_stake_lovelace":"1000000"}`), Entitlements: []string{"read"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return &harness{srv: srv, st: st, chain: mock, pub: pub, priv: priv, vkey: hex.EncodeToString(pub)}
}

func (h *harness) sign(t *testing.T, nonce string) string {
	t.Helper()
	protected, _ := cbor.Marshal(map[int]int{1: -8})
	toSign, _ := cbor.Marshal(struct {
		_       struct{} `cbor:",toarray"`
		Ctx     string
		Body    []byte
		AAD     []byte
		Payload []byte
	}{Ctx: "Signature1", Body: protected, AAD: []byte{}, Payload: []byte(nonce)})
	sig := ed25519.Sign(h.priv, toSign)
	msg, _ := cbor.Marshal([]any{protected, map[int]int{}, []byte(nonce), sig})
	return hex.EncodeToString(msg)
}

// authorizeAs runs challenge → sign → Authorize and returns (code, err).
func (h *harness) authorizeAs(t *testing.T, sch string) (string, error) {
	ctx := context.Background()
	nonce, _, err := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.vkey)
	if err != nil {
		t.Fatal(err)
	}
	return h.srv.Authorize(ctx, AuthorizeRequest{
		ClientID: "c1", RedirectURI: "https://app/cb", State: "xyz", Aud: "app:ouro",
		Scope: []string{"read"}, Nonce: nonce, StakeVkey: h.vkey, Signature: h.sign(t, nonce),
	})
}

func TestAuthorize_EligibleIssuesCode(t *testing.T) {
	h := newHarness(t)
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})

	code, err := h.authorizeAs(t, sch)
	if err != nil || code == "" {
		t.Fatalf("authorize: %v code=%q", err, code)
	}
	// The stored code is the hash of the returned plaintext (one-time, ≤60s).
	rec, err := h.st.AuthCodes().Consume(context.Background(), crypto.HashToken(code), time.Now())
	if err != nil || rec.ClientID != "c1" || rec.StakeCredentialHash != sch {
		t.Fatalf("stored code: %v %+v", err, rec)
	}
}

func TestAuthorize_IneligibleRejected(t *testing.T) {
	h := newHarness(t)
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	// Delegates to a different pool → not eligible.
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "pool1other", ActiveStakeLovelace: "5000000"})

	if _, err := h.authorizeAs(t, sch); err != ErrNotEligible {
		t.Fatalf("ineligible authorize: %v, want ErrNotEligible", err)
	}
}

func TestAuthorize_BlacklistedRejected(t *testing.T) {
	h := newHarness(t)
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakeLovelace: "5000000"})
	h.st.Blacklist().Add(context.Background(), domain.Blacklist{StakeCredentialHash: sch, CreatedAt: time.Now()})

	if _, err := h.authorizeAs(t, sch); err != ErrNotEligible {
		t.Fatalf("blacklisted authorize: %v, want ErrNotEligible", err)
	}
}

func TestAuthorize_ClientValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.vkey)
	base := AuthorizeRequest{
		ClientID: "c1", RedirectURI: "https://app/cb", Aud: "app:ouro",
		Nonce: nonce, StakeVkey: h.vkey, Signature: h.sign(t, nonce),
	}
	// Unknown client.
	bad := base
	bad.ClientID = "nope"
	if _, err := h.srv.Authorize(ctx, bad); err != ErrInvalidClient {
		t.Errorf("unknown client: %v", err)
	}
	// redirect_uri not in allowlist.
	bad = base
	bad.RedirectURI = "https://evil/cb"
	if _, err := h.srv.Authorize(ctx, bad); err != ErrInvalidRequest {
		t.Errorf("bad redirect: %v", err)
	}
}
