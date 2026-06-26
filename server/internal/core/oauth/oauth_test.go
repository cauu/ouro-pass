package oauth

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
)

const testPool = "pool1abc"

// PKCE is mandatory for every client now, so tests authorize with this fixed
// challenge and exchange the matching verifier.
const testPKCEVerifier = "test-pkce-code-verifier-0123456789"

func testPKCEChallenge() string {
	sum := sha256.Sum256([]byte(testPKCEVerifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// harness wires a Server over a fresh store with a mock chain and one client.
type harness struct {
	srv        *Server
	st         *store.Store
	chain      *chain.MockSource
	pub        ed25519.PublicKey
	priv       ed25519.PrivateKey
	rewardAddr string // CIP-30 reward address (Challenge input)
	coseKey    string // CIP-30 signData `key` carrying pub (Verify input)
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
	// S0006: one pool_stake attestor for testPool, evaluated over the mock source.
	attestorsFor := func(context.Context) (*attestor.Set, error) {
		params, _ := json.Marshal(attestor.PoolStakeParams{PoolID: testPool, Network: "preview"})
		a, err := attestor.BuildPoolStake("att-test", params, func(string) (chain.Source, error) { return mock, nil })
		if err != nil {
			return nil, err
		}
		return attestor.NewSet([]attestor.Attestor{a}), nil
	}
	srv := New(Config{
		Store: st, Wallet: walletauth.New(st, time.Minute), Keys: ks, Attestors: attestorsFor,
		Issuer: "ouropass:" + testPool, ServerSalt: []byte("salt"),
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	})

	// One active confidential client + a gold rule.
	if err := st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential,
		RedirectURIs: []string{"https://app/cb"}, AllowedAudiences: []string{"app:ouro"},
		Status: "active", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	// First-party tier mapping (S0006: issuer-global boolean DSL over aggregate
	// facts): active≥1M → gold, active≥100k → silver, any active → basic.
	if err := st.Issuer().SetTierRules(ctx, json.RawMessage(`[
			{"tier":"gold","when":{"fact":"total_active_stake","op":">=","value":"1000000"}},
			{"tier":"silver","when":{"fact":"total_active_stake","op":">=","value":"100000"}},
			{"tier":"basic","when":{"fact":"any_active","op":"==","value":"true"}}
		]`), time.Now()); err != nil {
		t.Fatal(err)
	}

	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cred := crypto.Blake2b224(pub)
	rewardAddr := hex.EncodeToString(append([]byte{0xe1}, cred...)) // mainnet reward header
	coseKeyBytes, _ := cbor.Marshal(map[int]any{1: 1, -1: 6, -2: []byte(pub), 3: -8})
	return &harness{
		srv: srv, st: st, chain: mock, pub: pub, priv: priv,
		rewardAddr: rewardAddr, coseKey: hex.EncodeToString(coseKeyBytes),
	}
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
	nonce, _, err := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.rewardAddr)
	if err != nil {
		t.Fatal(err)
	}
	return h.srv.Authorize(ctx, AuthorizeRequest{
		ClientID: "c1", RedirectURI: "https://app/cb", State: "xyz", Aud: "app:ouro",
		Scope: []string{"read"}, Nonce: nonce, CoseKey: h.coseKey, Signature: h.sign(t, nonce),
		CodeChallenge: testPKCEChallenge(),
	})
}

func TestAuthorize_EligibleIssuesCode(t *testing.T) {
	h := newHarness(t)
	sch := hex.EncodeToString(crypto.Blake2b224(h.pub))
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakePoolID: testPool, AccountStatus: "registered", ActiveStakeLovelace: "5000000"})

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
	h.chain.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakePoolID: testPool, AccountStatus: "registered", ActiveStakeLovelace: "5000000"})
	h.st.Blacklist().Add(context.Background(), domain.Blacklist{StakeCredentialHash: sch, CreatedAt: time.Now()})

	if _, err := h.authorizeAs(t, sch); err != ErrNotEligible {
		t.Fatalf("blacklisted authorize: %v, want ErrNotEligible", err)
	}
}

func TestAuthorize_ClientValidation(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	nonce, _, _ := h.srv.cfg.Wallet.Challenge(ctx, domain.NonceIssue, h.rewardAddr)
	base := AuthorizeRequest{
		ClientID: "c1", RedirectURI: "https://app/cb", Aud: "app:ouro",
		Nonce: nonce, CoseKey: h.coseKey, Signature: h.sign(t, nonce),
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
