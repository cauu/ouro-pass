package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/poolops/issuer/internal/core/keys"
	"github.com/poolops/issuer/internal/core/oauth"
	"github.com/poolops/issuer/internal/core/walletauth"
	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/store"
	"github.com/poolops/issuer/internal/utils/chain"
	"github.com/poolops/issuer/internal/utils/crypto"
)

func oauthDeps(t *testing.T) (Deps, *chain.MockSource, ed25519.PrivateKey, string) {
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
	ks.Rotate(ctx)
	mock := chain.NewMockSource(480)
	wallet := walletauth.New(st, time.Minute)
	srv := oauth.New(oauth.Config{
		Store: st, Wallet: wallet, Keys: ks, Chain: mock, PoolID: "pool1abc",
		Issuer: "poolops:pool1abc", ServerSalt: []byte("salt"), AccessTTL: time.Hour, RefreshTTL: time.Hour,
	})
	st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential, Party: domain.FirstParty,
		RedirectURIs: []string{"https://app/cb"}, AllowedAudiences: []string{"app:ouro"},
		AllowedScopes: []string{"read"}, Status: "active", CreatedAt: time.Now(),
	})
	st.Rules().Upsert(ctx, domain.MembershipRule{
		RuleID: "gold", Tier: "gold", Priority: 10, Status: domain.RuleActive,
		RuleConfig: json.RawMessage(`{"min_active_stake_lovelace":"1000000"}`), Entitlements: []string{"read"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return Deps{Wallet: wallet, Keys: ks, OAuth: srv}, mock, priv, hex.EncodeToString(pub)
}

func signNonce(t *testing.T, priv ed25519.PrivateKey, nonce string) string {
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

func TestConnect_ValidatesParams(t *testing.T) {
	deps, _, _, _ := oauthDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()
	// response_type must be code.
	resp, _ := http.Get(srv.URL + "/connect?client_id=c1&redirect_uri=https://app/cb&response_type=token")
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("bad response_type = %d, want 400", resp.StatusCode)
	}
	// Unknown client → 401.
	resp, _ = http.Get(srv.URL + "/connect?client_id=nope&redirect_uri=https://app/cb&response_type=code")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("unknown client = %d, want 401", resp.StatusCode)
	}
	// Valid → 200 HTML.
	resp, _ = http.Get(srv.URL + "/connect?client_id=c1&redirect_uri=https://app/cb&aud=app:ouro&response_type=code")
	if resp.StatusCode != 200 {
		t.Errorf("valid connect = %d, want 200", resp.StatusCode)
	}
}

func TestConnectAuthorize_RedirectsWithCode(t *testing.T) {
	deps, mock, priv, vkey := oauthDeps(t)
	ctx := context.Background()
	pubRaw, _ := hex.DecodeString(vkey)
	sch := hex.EncodeToString(crypto.Blake2b224(pubRaw))
	mock.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "pool1abc", ActiveStakeLovelace: "5000000"})

	// No-redirect client so we can inspect the 302 Location.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	nonce, _, _ := deps.Wallet.Challenge(ctx, domain.NonceIssue, vkey)
	body, _ := json.Marshal(map[string]any{
		"client_id": "c1", "redirect_uri": "https://app/cb", "state": "xyz", "aud": "app:ouro",
		"nonce": nonce, "stake_vkey": vkey, "signature": signNonce(t, priv, nonce),
	})
	resp, err := client.Post(srv.URL+"/api/connect/authorize", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want 302", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("code") == "" || loc.Query().Get("state") != "xyz" {
		t.Fatalf("redirect = %s (missing code/state)", resp.Header.Get("Location"))
	}

	// Ineligible (delegates elsewhere) → redirect with error=not_eligible.
	mock.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "other", ActiveStakeLovelace: "5000000"})
	nonce2, _, _ := deps.Wallet.Challenge(ctx, domain.NonceIssue, vkey)
	body2, _ := json.Marshal(map[string]any{
		"client_id": "c1", "redirect_uri": "https://app/cb", "state": "s2", "aud": "app:ouro",
		"nonce": nonce2, "stake_vkey": vkey, "signature": signNonce(t, priv, nonce2),
	})
	resp2, _ := client.Post(srv.URL+"/api/connect/authorize", "application/json", strings.NewReader(string(body2)))
	loc2, _ := url.Parse(resp2.Header.Get("Location"))
	if loc2.Query().Get("error") != "not_eligible" {
		t.Fatalf("ineligible redirect = %s, want error=not_eligible", resp2.Header.Get("Location"))
	}
}
