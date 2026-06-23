package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/store"
)

func walletDeps(t *testing.T) Deps {
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
	return Deps{Wallet: walletauth.New(st, time.Minute)}
}

func TestAuthChallenge_Handler(t *testing.T) {
	srv := httptest.NewServer(NewRouter(walletDeps(t)))
	defer srv.Close()
	pub, _, _ := ed25519.GenerateKey(rand.Reader)

	post := func(body string) (int, map[string]any) {
		resp, err := http.Post(srv.URL+"/api/auth/challenge", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}

	code, m := post(`{"purpose":"issue","stake_vkey":"` + hex.EncodeToString(pub) + `"}`)
	if code != 200 || m["nonce"] == "" || m["nonce"] == nil {
		t.Fatalf("valid challenge: %d %v", code, m)
	}
	if code, m := post(`{"purpose":"bogus","stake_vkey":"ab"}`); code != 400 || m["error"] != "invalid_request" {
		t.Fatalf("bad purpose: %d %v", code, m)
	}
	if code, _ := post(`{"purpose":"issue","stake_vkey":"zz"}`); code != 400 {
		t.Fatalf("bad vkey hex: %d", code)
	}
	if code, _ := post(`not json`); code != 400 {
		t.Fatalf("malformed: %d", code)
	}
}
