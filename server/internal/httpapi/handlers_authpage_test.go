package httpapi

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/worker/telegram"
)

// TestConnectPage_RendersHTML covers p2-1/TC-4: GET /connect returns the full
// issuer-served Authorization Page (not a placeholder), carrying the OAuth params
// as data-* attributes and referencing the same-origin script, with a CSP. An
// invalid client fails fast; a non-code response_type is rejected.
func TestConnectPage_RendersHTML(t *testing.T) {
	deps, _, _, _ := oauthDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/connect?response_type=code&client_id=c1&redirect_uri=https://app/cb&aud=app:ouro&state=s1")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("valid connect = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
	if resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatal("missing Content-Security-Policy header")
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	for _, want := range []string{`data-mode="authorize"`, `data-client-id="c1"`, `data-aud="app:ouro"`, "/assets/ouropass-auth.js"} {
		if !strings.Contains(s, want) {
			t.Errorf("page missing %q", want)
		}
	}

	// Unknown client → not the page.
	bad, _ := http.Get(srv.URL + "/connect?response_type=code&client_id=nope&redirect_uri=https://x/cb")
	if bad.StatusCode == 200 {
		t.Fatal("unknown client should not render the page")
	}
	// Wrong response_type → 400.
	rt, _ := http.Get(srv.URL + "/connect?response_type=token&client_id=c1")
	if rt.StatusCode != 400 {
		t.Fatalf("bad response_type = %d, want 400", rt.StatusCode)
	}
}

// TestBindPage_RendersHTML covers p2-2/TC-5: GET /bind returns the binding page
// in activate mode.
func TestBindPage_RendersHTML(t *testing.T) {
	deps, _, _, _ := oauthDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/bind")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("bind = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	for _, want := range []string{`data-mode="activate"`, `data-channel-type="telegram"`, "/assets/ouropass-auth.js"} {
		if !strings.Contains(s, want) {
			t.Errorf("bind page missing %q", want)
		}
	}
}

// TestAuthAsset_ServesJS covers the same-origin script asset.
func TestAuthAsset_ServesJS(t *testing.T) {
	deps, _, _, _ := oauthDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/assets/ouropass-auth.js")
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("asset = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Fatalf("content-type = %q", ct)
	}
	body, _ := io.ReadAll(resp.Body)
	for _, want := range []string{"signData", "getRewardAddresses", "stake_address", "cose_key"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("asset missing %q", want)
		}
	}
}

// TestActivationCreate_UsesInstanceBot covers S0016 TC-1/TC-2: when the activation
// request carries channel_id, the deep link uses THAT instance's bot username; with
// no channel_id it falls back to the deployment-wide default bot.
func TestActivationCreate_UsesInstanceBot(t *testing.T) {
	deps, mock, priv, vkey := oauthDeps(t)
	ctx := context.Background()
	pubRaw, _ := hex.DecodeString(vkey)
	sch := hex.EncodeToString(crypto.Blake2b224(pubRaw))
	mock.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "pool1abc", ActiveStakePoolID: "pool1abc", AccountStatus: "registered", ActiveStakeLovelace: "5000000"})

	cfgBlob, err := telegram.EncodeConfig(deps.Cipher, "123:token", "my_real_bot")
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.Store.Channels().Upsert(ctx, domain.ChannelConfig{
		ChannelID: "tg1", PoolID: deps.PoolID, ChannelType: "telegram", Name: "Main",
		Config: cfgBlob, Status: "active", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	deepLink := func(channelID string) string {
		nonce, _, _ := deps.Wallet.Challenge(ctx, domain.NonceActivation, rewardAddrOf(vkey))
		body, _ := json.Marshal(map[string]string{
			"channel_type": "telegram", "channel_id": channelID,
			"nonce": nonce, "cose_key": coseKeyOf(vkey), "signature": signNonce(t, priv, nonce),
		})
		resp, err := http.Post(srv.URL+"/api/activation/create", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("activation(%q) = %d: %s", channelID, resp.StatusCode, b)
		}
		var out struct {
			DeepLink string `json:"deep_link"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		return out.DeepLink
	}

	// With channel_id → the instance's own bot.
	if dl := deepLink("tg1"); !strings.HasPrefix(dl, "https://t.me/my_real_bot?start=") {
		t.Errorf("deep_link with channel_id = %q, want my_real_bot", dl)
	}
	// Without channel_id → the deployment default bot (unchanged behavior).
	if dl := deepLink(""); !strings.HasPrefix(dl, "https://t.me/ouro_default_bot?start=") {
		t.Errorf("deep_link without channel_id = %q, want ouro_default_bot", dl)
	}
}

// TestConnectAuthorize_FormPost covers the hidden-form submission path the page
// uses so the browser natively follows the 302 to redirect_uri (TC-4).
func TestConnectAuthorize_FormPost(t *testing.T) {
	deps, mock, priv, vkey := oauthDeps(t)
	ctx := context.Background()
	pubRaw, _ := hex.DecodeString(vkey)
	sch := hex.EncodeToString(crypto.Blake2b224(pubRaw))
	mock.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "pool1abc", ActiveStakePoolID: "pool1abc", AccountStatus: "registered", ActiveStakeLovelace: "5000000"})

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	nonce, _, _ := deps.Wallet.Challenge(ctx, domain.NonceIssue, rewardAddrOf(vkey))
	form := url.Values{
		"client_id": {"c1"}, "redirect_uri": {"https://app/cb"}, "state": {"xyz"}, "aud": {"app:ouro"},
		"scope": {"read"}, "nonce": {nonce}, "cose_key": {coseKeyOf(vkey)}, "signature": {signNonce(t, priv, nonce)},
		"code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, // PKCE mandatory for all
	}
	resp, err := client.PostForm(srv.URL+"/api/connect/authorize", form)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("form authorize = %d, want 302", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Query().Get("code") == "" || loc.Query().Get("state") != "xyz" {
		t.Fatalf("redirect = %s (missing code/state)", resp.Header.Get("Location"))
	}
}
