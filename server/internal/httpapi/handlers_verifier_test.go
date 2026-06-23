package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/crypto"
)

func keysDeps(t *testing.T) (Deps, *keys.Service) {
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
	return Deps{Keys: ks}, ks
}

func TestJWKS_Endpoint(t *testing.T) {
	deps, ks := keysDeps(t)
	srv := httptest.NewServer(NewRouter(deps))
	defer srv.Close()

	get := func() (int, map[string]any) {
		resp, err := http.Get(srv.URL + "/.well-known/poolops/jwks.json")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return resp.StatusCode, m
	}

	// Before any key: valid empty JWKS (200), not an error.
	code, doc := get()
	if code != 200 {
		t.Fatalf("empty jwks status = %d", code)
	}

	// After rotation: one published key, OKP/Ed25519, no cert chain / no private.
	if _, err := ks.Rotate(context.Background()); err != nil {
		t.Fatal(err)
	}
	code, doc = get()
	if code != 200 {
		t.Fatalf("jwks status = %d", code)
	}
	arr, _ := doc["keys"].([]any)
	if len(arr) != 1 {
		t.Fatalf("jwks keys = %d, want 1", len(arr))
	}
	k := arr[0].(map[string]any)
	if k["kty"] != "OKP" || k["crv"] != "Ed25519" || k["status"] != "active" {
		t.Errorf("jwks key = %v", k)
	}
	for _, banned := range []string{"d", "x5c", "chain"} {
		if _, ok := k[banned]; ok {
			t.Errorf("jwks key leaked %q", banned)
		}
	}
}

func TestJWKS_DisabledWhenNoKeyService(t *testing.T) {
	srv := httptest.NewServer(NewRouter(Deps{}))
	defer srv.Close()
	resp, _ := http.Get(srv.URL + "/.well-known/poolops/jwks.json")
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}
