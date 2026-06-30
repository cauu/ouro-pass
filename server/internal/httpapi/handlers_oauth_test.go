package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"
	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
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
	attestorsFor := func(context.Context) (*attestor.Set, error) {
		params, _ := json.Marshal(attestor.PoolStakeParams{PoolID: "pool1abc", Network: "preview"})
		a, err := attestor.BuildPoolStake("att-test", params, func(string) (chain.Source, error) { return mock, nil })
		if err != nil {
			return nil, err
		}
		return attestor.NewSet([]attestor.Attestor{a}), nil
	}
	srv := oauth.New(oauth.Config{
		Store: st, Wallet: wallet, Keys: ks, Attestors: attestorsFor,
		Issuer: "ouropass:pool1abc", ServerSalt: []byte("salt"), AccessTTL: time.Hour, RefreshTTL: time.Hour,
	})
	st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential,
		RedirectURIs: []string{"https://app/cb"}, AllowedAudiences: []string{"app:ouro"},
		Status: "active", CreatedAt: time.Now(),
	})
	st.Issuer().SetTierRules(ctx,
		json.RawMessage(`[{"tier":"gold","when":{"fact":"total_active_stake","op":">=","value":"1000000"}}]`), time.Now())
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	return Deps{Wallet: wallet, Keys: ks, OAuth: srv, Store: st, Cipher: cipher}, mock, priv, hex.EncodeToString(pub)
}

// rewardAddrOf / coseKeyOf derive the S0003 wallet-signature wire forms from a
// vkey hex: the reward (stake) address for /challenge and the COSE_Key (signData
// `key`) for verify/authorize/activation. Shared by the httpapi handler tests.
func rewardAddrOf(vkeyHex string) string {
	pub, _ := hex.DecodeString(vkeyHex)
	return hex.EncodeToString(append([]byte{0xe1}, crypto.Blake2b224(pub)...))
}

func coseKeyOf(vkeyHex string) string {
	pub, _ := hex.DecodeString(vkeyHex)
	b, _ := cbor.Marshal(map[int]any{1: 1, -1: 6, -2: pub, 3: -8})
	return hex.EncodeToString(b)
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

// TestBind_ChannelID covers S0016 p1-1 / TC-1..TC-3: /bind validates an optional
// channel_id (active telegram instance) and renders it into data-channel-id so the
// activation request can target that instance's bot; a bad id is rejected and the
// no-id form is unchanged.
func TestBind_ChannelID(t *testing.T) {
	deps, _, _, _ := oauthDeps(t)
	ctx := context.Background()
	// Seed one active telegram instance and one disabled one.
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(deps.Store.Channels().Upsert(ctx, domain.ChannelConfig{
		ChannelID: "tg-active", PoolID: deps.PoolID, ChannelType: "telegram", Name: "Main",
		Status: "active", CreatedAt: time.Now(),
	}))
	must(deps.Store.Channels().Upsert(ctx, domain.ChannelConfig{
		ChannelID: "tg-off", PoolID: deps.PoolID, ChannelType: "telegram", Name: "Old",
		Status: "disabled", CreatedAt: time.Now(),
	}))

	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	get := func(path string) (int, string) {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// TC-1: valid active telegram id → 200 with data-channel-id + name passed through.
	if code, body := get("/bind?channel_id=tg-active"); code != 200 ||
		!strings.Contains(body, `data-channel-id="tg-active"`) || !strings.Contains(body, `data-channel-name="Main"`) {
		t.Errorf("valid channel_id: code=%d body=%q", code, body)
	}
	// TC-2: no channel_id → 200 with empty data-channel-id (unchanged behavior).
	if code, body := get("/bind"); code != 200 || !strings.Contains(body, `data-channel-id=""`) {
		t.Errorf("no channel_id: code=%d, body has empty attr=%v", code, strings.Contains(body, `data-channel-id=""`))
	}
	// TC-3: unknown / inactive id → 400, no page rendered.
	if code, _ := get("/bind?channel_id=nope"); code != http.StatusBadRequest {
		t.Errorf("unknown channel_id = %d, want 400", code)
	}
	if code, _ := get("/bind?channel_id=tg-off"); code != http.StatusBadRequest {
		t.Errorf("disabled channel_id = %d, want 400", code)
	}
}

func TestConnectAuthorize_RedirectsWithCode(t *testing.T) {
	deps, mock, priv, vkey := oauthDeps(t)
	ctx := context.Background()
	pubRaw, _ := hex.DecodeString(vkey)
	sch := hex.EncodeToString(crypto.Blake2b224(pubRaw))
	mock.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "pool1abc", ActiveStakePoolID: "pool1abc", AccountStatus: "registered", ActiveStakeLovelace: "5000000"})

	// No-redirect client so we can inspect the 302 Location.
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	nonce, _, _ := deps.Wallet.Challenge(ctx, domain.NonceIssue, rewardAddrOf(vkey))
	body, _ := json.Marshal(map[string]any{
		"client_id": "c1", "redirect_uri": "https://app/cb", "state": "xyz", "aud": "app:ouro",
		"nonce": nonce, "cose_key": coseKeyOf(vkey), "signature": signNonce(t, priv, nonce),
		"code_challenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", // PKCE mandatory for all
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
	nonce2, _, _ := deps.Wallet.Challenge(ctx, domain.NonceIssue, rewardAddrOf(vkey))
	body2, _ := json.Marshal(map[string]any{
		"client_id": "c1", "redirect_uri": "https://app/cb", "state": "s2", "aud": "app:ouro",
		"nonce": nonce2, "cose_key": coseKeyOf(vkey), "signature": signNonce(t, priv, nonce2),
		"code_challenge": "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM", // PKCE mandatory for all
	})
	resp2, _ := client.Post(srv.URL+"/api/connect/authorize", "application/json", strings.NewReader(string(body2)))
	loc2, _ := url.Parse(resp2.Header.Get("Location"))
	if loc2.Query().Get("error") != "not_eligible" {
		t.Fatalf("ineligible redirect = %s, want error=not_eligible", resp2.Header.Get("Location"))
	}
}
