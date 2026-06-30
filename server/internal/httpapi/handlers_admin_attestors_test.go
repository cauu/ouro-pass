package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/domain"
)

func listAttestors(t *testing.T, c *http.Client, url string) []map[string]any {
	t.Helper()
	resp, err := c.Get(url + "/api/admin/attestors")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Attestors []map[string]any `json:"attestors"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	return body.Attestors
}

// TestAdminAttestors_CRUD: operator creates/edits/deletes attestors; validation +
// duplicate-label + unsupported-kind are rejected (S0006 p6-1).
func TestAdminAttestors_CRUD(t *testing.T) {
	srv, client, _, _, _ := adminResourceEnv(t, domain.RoleOperator)
	base := srv.URL + "/api/admin/attestors"

	// adminResourceEnv already seeds one pool_stake attestor (label "pool1").
	if got := len(listAttestors(t, client, srv.URL)); got != 1 {
		t.Fatalf("seeded attestors = %d, want 1", got)
	}

	// Create a second pool_stake attestor.
	created := postJSON(t, client, base, `{"kind":"pool_stake","label":"second","params":{"pool_id":"pool1second","network":"mainnet"}}`)
	id, _ := created["attestor_id"].(string)
	if id == "" {
		t.Fatalf("create returned no id: %v", created)
	}
	// The id travels in the update/delete path, which the client percent-encodes —
	// it must be URL-safe (no reserved chars) or those routes silently 404.
	if strings.ContainsAny(id, ":/?#[]@!$&'()*+,;=") {
		t.Fatalf("attestor_id must be URL-safe, got %q", id)
	}
	if got := len(listAttestors(t, client, srv.URL)); got != 2 {
		t.Fatalf("after create = %d, want 2", got)
	}

	// Duplicate (kind,label) → 409.
	if c := postCode(t, client, base, `{"kind":"pool_stake","label":"second","params":{"pool_id":"poolX","network":"mainnet"}}`); c != http.StatusConflict {
		t.Fatalf("duplicate label = %d, want 409", c)
	}
	// Missing network → 400 (S0014 p1-2: network required per attestor).
	if c := postCode(t, client, base, `{"kind":"pool_stake","label":"nonet","params":{"pool_id":"poolNoNet"}}`); c != http.StatusBadRequest {
		t.Fatalf("missing network = %d, want 400", c)
	}
	// Unsupported kind → 400.
	if c := postCode(t, client, base, `{"kind":"nft","label":"n","params":{"policy_id":"p"}}`); c != http.StatusBadRequest {
		t.Fatalf("nft kind = %d, want 400", c)
	}
	// Missing pool_id → 400.
	if c := postCode(t, client, base, `{"kind":"pool_stake","label":"nopool","params":{"network":"mainnet"}}`); c != http.StatusBadRequest {
		t.Fatalf("missing pool_id = %d, want 400", c)
	}

	// Update status → disabled.
	if c := postCode(t, client, base+"/"+id, `{"status":"disabled"}`); c != 200 {
		t.Fatalf("disable = %d, want 200", c)
	}
	for _, a := range listAttestors(t, client, srv.URL) {
		if a["attestor_id"] == id && a["status"] != "disabled" {
			t.Fatalf("status not updated: %v", a)
		}
	}
	// Invalid status → 400.
	if c := postCode(t, client, base+"/"+id, `{"status":"bogus"}`); c != http.StatusBadRequest {
		t.Fatalf("bad status = %d, want 400", c)
	}

	// Delete → 200; gone from list; deleting again → 404.
	req, _ := http.NewRequest(http.MethodDelete, base+"/"+id, nil)
	resp, _ := client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delete = %d, want 200", resp.StatusCode)
	}
	if got := len(listAttestors(t, client, srv.URL)); got != 1 {
		t.Fatalf("after delete = %d, want 1", got)
	}
	req2, _ := http.NewRequest(http.MethodDelete, base+"/"+id, nil)
	resp2, _ := client.Do(req2)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("delete missing = %d, want 404", resp2.StatusCode)
	}
}

// TestAdminAttestors_LegacyColonID: an attestor whose id contains a ':' (created
// before the URL-safe fix, or by the migration) must still be deletable via the
// browser's percent-encoded path (':' → %3A). Guards the decode in the handlers.
func TestAdminAttestors_LegacyColonID(t *testing.T) {
	srv, client, _, _, st := adminResourceEnv(t, domain.RoleOperator)
	params, _ := json.Marshal(attestor.PoolStakeParams{PoolID: "pool1legacy", Network: "mainnet"})
	if err := st.Attestors().Create(context.Background(), domain.AttestorConfig{
		AttestorID: "pool_stake:legacyabc", Kind: "pool_stake", Label: "legacy", Params: params,
		Status: domain.AttestorActive, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	enc := strings.ReplaceAll("pool_stake:legacyabc", ":", "%3A") // what encodeURIComponent sends
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/admin/attestors/"+enc, nil)
	resp, _ := client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("delete legacy colon-id via encoded path = %d, want 200", resp.StatusCode)
	}
}

// TestAdminAttestors_RBAC: a viewer may list but not mutate.
func TestAdminAttestors_RBAC(t *testing.T) {
	srv, client, _, _, _ := adminResourceEnv(t, domain.RoleViewer)
	base := srv.URL + "/api/admin/attestors"

	if resp, _ := client.Get(base); resp.StatusCode != 200 {
		t.Fatalf("viewer GET = %d, want 200", resp.StatusCode)
	}
	if c := postCode(t, client, base, `{"kind":"pool_stake","label":"x","params":{"pool_id":"p"}}`); c != http.StatusForbidden {
		t.Fatalf("viewer create = %d, want 403", c)
	}
	req, _ := http.NewRequest(http.MethodDelete, base+"/att-pool1", strings.NewReader(""))
	resp, _ := client.Do(req)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer delete = %d, want 403", resp.StatusCode)
	}
}
