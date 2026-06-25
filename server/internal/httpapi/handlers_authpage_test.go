package httpapi

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
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

// TestConnectAuthorize_FormPost covers the hidden-form submission path the page
// uses so the browser natively follows the 302 to redirect_uri (TC-4).
func TestConnectAuthorize_FormPost(t *testing.T) {
	deps, mock, priv, vkey := oauthDeps(t)
	ctx := context.Background()
	pubRaw, _ := hex.DecodeString(vkey)
	sch := hex.EncodeToString(crypto.Blake2b224(pubRaw))
	mock.Put(&chain.Snapshot{StakeCredentialHash: sch, Epoch: 480, DelegatedPoolID: "pool1abc", ActiveStakeLovelace: "5000000"})

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
