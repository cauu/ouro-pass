package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/cookiejar"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

func adminDeps(t *testing.T) (Deps, ed25519.PrivateKey, string) {
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
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	vkey := hex.EncodeToString(pub)
	keyHash := hex.EncodeToString(crypto.Blake2b224(pub))
	wallet := walletauth.New(st, time.Minute)
	svc := admin.New(admin.Config{Wallet: wallet, Store: st, OwnerKeyHash: []string{keyHash}, PoolID: "pool1"})
	return Deps{Wallet: wallet, Admin: svc}, priv, vkey
}

func TestAdminLogin_CookieFlowAndRBAC(t *testing.T) {
	deps, priv, vkey := adminDeps(t)
	srv := httptest.NewTLSServer(NewRouter(deps))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	client := srv.Client()
	client.Jar = jar

	// Unauthenticated /me → 401.
	resp, _ := client.Get(srv.URL + "/api/admin/me")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauth /me = %d, want 401", resp.StatusCode)
	}

	// challenge → sign → verify (sets cookie via jar).
	nonce := adminChallengeReq(t, client, srv.URL, vkey)
	body, _ := json.Marshal(map[string]string{"owner_vkey": vkey, "nonce": nonce, "signature": signNonce(t, priv, nonce)})
	resp, err := client.Post(srv.URL+"/api/admin/auth/verify", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("verify = %d, want 200", resp.StatusCode)
	}
	var vr map[string]string
	json.NewDecoder(resp.Body).Decode(&vr)
	if vr["role"] != "owner" {
		t.Fatalf("role = %s, want owner", vr["role"])
	}

	// Authenticated /me → 200 with the cookie from the jar.
	resp, _ = client.Get(srv.URL + "/api/admin/me")
	if resp.StatusCode != 200 {
		t.Fatalf("auth /me = %d, want 200", resp.StatusCode)
	}
	var me map[string]string
	json.NewDecoder(resp.Body).Decode(&me)
	if me["role"] != "owner" {
		t.Errorf("me role = %s", me["role"])
	}

	// Logout → /me 401 again.
	client.Post(srv.URL+"/api/admin/auth/logout", "application/json", nil)
	resp, _ = client.Get(srv.URL + "/api/admin/me")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("post-logout /me = %d, want 401", resp.StatusCode)
	}
}

func adminChallengeReq(t *testing.T, client *http.Client, base, vkey string) string {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"owner_vkey": vkey})
	resp, err := client.Post(base+"/api/admin/auth/challenge", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]string
	json.NewDecoder(resp.Body).Decode(&m)
	if m["nonce"] == "" {
		t.Fatal("no nonce from challenge")
	}
	return m["nonce"]
}
