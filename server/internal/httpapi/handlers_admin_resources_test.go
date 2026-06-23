package httpapi

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/poolops/issuer/internal/core/admin"
	"github.com/poolops/issuer/internal/core/keys"
	"github.com/poolops/issuer/internal/core/walletauth"
	"github.com/poolops/issuer/internal/domain"
	"github.com/poolops/issuer/internal/store"
	"github.com/poolops/issuer/internal/utils/crypto"
)

// adminResourceEnv builds a full admin-capable server and logs in as the role
// seeded for the given key (owner via allowlist, or operator/viewer if seeded).
func adminResourceEnv(t *testing.T, role domain.AdminRole) (*httptest.Server, *http.Client, ed25519.PrivateKey, string, *store.Store) {
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

	var ownerKeys []string
	if role == domain.RoleOwner {
		ownerKeys = []string{keyHash}
	} else {
		st.AdminUsers().Upsert(ctx, domain.AdminUser{AdminID: crypto.RandomID(), PoolID: "pool1", OwnerKeyHash: keyHash, Role: role, CreatedAt: time.Now()})
	}
	cipher, _ := crypto.NewFieldCipher(make([]byte, 32))
	deps := Deps{
		Wallet: wallet, Store: st, PoolID: "pool1", Keys: keys.New(st, cipher),
		Admin: admin.New(admin.Config{Wallet: wallet, Store: st, OwnerKeyHash: ownerKeys, PoolID: "pool1"}),
	}
	srv := httptest.NewTLSServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := srv.Client()
	client.Jar = jar

	// Login.
	nonce := adminChallengeReq(t, client, srv.URL, vkey)
	body, _ := json.Marshal(map[string]string{"owner_vkey": vkey, "nonce": nonce, "signature": signNonce(t, priv, nonce)})
	resp, _ := client.Post(srv.URL+"/api/admin/auth/verify", "application/json", strings.NewReader(string(body)))
	if resp.StatusCode != 200 {
		t.Fatalf("login as %s = %d", role, resp.StatusCode)
	}
	return srv, client, priv, vkey, st
}

func TestAdminRBAC_Matrix(t *testing.T) {
	srv, client, _, _, _ := adminResourceEnv(t, domain.RoleViewer)

	// viewer can read members but not write rules or register clients.
	if code := getCode(t, client, srv.URL+"/api/admin/members"); code != 200 {
		t.Errorf("viewer GET members = %d, want 200", code)
	}
	if code := postCode(t, client, srv.URL+"/api/admin/rules", `{"rule_id":"r","tier":"gold"}`); code != http.StatusForbidden {
		t.Errorf("viewer POST rules = %d, want 403", code)
	}
	if code := getCode(t, client, srv.URL+"/api/admin/oauth-clients"); code != http.StatusForbidden {
		t.Errorf("viewer GET clients = %d, want 403 (owner only)", code)
	}
}

func TestAdminRevokeMember_BlacklistAndCascade(t *testing.T) {
	srv, client, _, _, st := adminResourceEnv(t, domain.RoleOperator)
	ctx := context.Background()
	const sch = "sch-deadbeef"

	// Seed an active token + grant + session for the member.
	st.IssuedTokens().Create(ctx, nil, domain.IssuedToken{
		JTI: "jti1", StakeCredentialHash: sch, Kind: domain.TokenAccess, Audience: "app",
		KID: "k1", Status: domain.TokenActive, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	st.RefreshGrants().Create(ctx, nil, domain.RefreshGrant{
		RefreshGrantID: "g1", StakeCredentialHash: sch, Audience: "app",
		ClientType: domain.ClientPublic, Status: domain.GrantActive, CreatedAt: time.Now(),
	})
	st.Subscriptions().Upsert(ctx, domain.SubscriptionSession{
		SessionID: "s1", PoolID: "pool1", StakeCredentialHash: sch, ChannelType: "telegram",
		ChannelUserID: "u1", Status: domain.SubActive, Tier: "gold",
		CreatedAt: time.Now(), LastVerifiedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})

	if code := postCode(t, client, srv.URL+"/api/admin/members/"+sch+"/revoke", ``); code != 200 {
		t.Fatalf("revoke = %d, want 200", code)
	}

	// Blacklisted by the same key evaluate() checks (D1 fix).
	if has, _ := st.Blacklist().Has(ctx, sch); !has {
		t.Fatal("member not blacklisted by sch")
	}
	// Cascade revoke happened (D2 fix).
	tok, _ := st.IssuedTokens().Get(ctx, "jti1")
	g, _ := st.RefreshGrants().Get(ctx, "g1")
	sub, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u1")
	if tok.Status != domain.TokenRevoked || g.Status != domain.GrantRevoked || sub.Status != domain.SubCancelled {
		t.Fatalf("cascade incomplete: token=%s grant=%s sub=%s", tok.Status, g.Status, sub.Status)
	}
}

func TestAdminF2_RejectsInvalidEnums(t *testing.T) {
	// operator can hit rules/push/channels; owner needed for clients.
	srvOp, op, _, _, _ := adminResourceEnv(t, domain.RoleOperator)
	if c := postCode(t, op, srvOp.URL+"/api/admin/rules", `{"rule_id":"r","tier":"gold","status":"bogus"}`); c != http.StatusBadRequest {
		t.Errorf("bad rule status = %d, want 400", c)
	}
	if c := postCode(t, op, srvOp.URL+"/api/admin/push/jobs", `{"title":"hi","channel_type":"carrier-pigeon"}`); c != http.StatusBadRequest {
		t.Errorf("bad push channel_type = %d, want 400", c)
	}
	if c := postCode(t, op, srvOp.URL+"/api/admin/channels/carrier-pigeon/configure", `{"config":{}}`); c != http.StatusBadRequest {
		t.Errorf("bad channel type = %d, want 400", c)
	}

	srvOwn, own, _, _, _ := adminResourceEnv(t, domain.RoleOwner)
	if c := postCode(t, own, srvOwn.URL+"/api/admin/oauth-clients", `{"client_id":"c","client_type":"weird","party":"first_party"}`); c != http.StatusBadRequest {
		t.Errorf("bad client_type = %d, want 400", c)
	}
	if c := postCode(t, own, srvOwn.URL+"/api/admin/oauth-clients", `{"client_id":"c","client_type":"public","party":"nobody"}`); c != http.StatusBadRequest {
		t.Errorf("bad party = %d, want 400", c)
	}
	// Valid values still pass.
	if c := postCode(t, op, srvOp.URL+"/api/admin/rules", `{"rule_id":"ok","tier":"gold","status":"disabled"}`); c != 200 {
		t.Errorf("valid rule = %d, want 200", c)
	}
}

func TestServerError_GenericNoLeak(t *testing.T) {
	// A realistic raw DB error that must never reach the client.
	rawErr := errors.New("sql: Scan error on column index 3: SELECT * FROM AdminUser; database is locked")
	rr := httptest.NewRecorder()
	serverError(rr, httptest.NewRequest(http.MethodGet, "/api/admin/members", nil), rawErr)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	var body map[string]string
	json.Unmarshal(rr.Body.Bytes(), &body)
	if body["error"] != "server_error" || body["error_description"] != "internal error" {
		t.Fatalf("body = %v, want generic envelope", body)
	}
	// The raw error text must not appear anywhere in the response.
	for _, leak := range []string{"sql", "database", "AdminUser", "SELECT", "Scan"} {
		if strings.Contains(rr.Body.String(), leak) {
			t.Errorf("response leaks %q: %s", leak, rr.Body.String())
		}
	}
}

func TestAdminOperator_RulesAndPush(t *testing.T) {
	srv, client, _, _, st := adminResourceEnv(t, domain.RoleOperator)
	// Upsert a rule.
	if code := postCode(t, client, srv.URL+"/api/admin/rules", `{"rule_id":"gold","tier":"gold","priority":10,"entitlements":["read"],"rule_config":{"min_active_stake_lovelace":"1000000"}}`); code != 200 {
		t.Fatalf("operator POST rules = %d", code)
	}
	rules, _ := st.Rules().ListActive(context.Background())
	if len(rules) != 1 || rules[0].Tier != "gold" {
		t.Fatalf("rule not stored: %+v", rules)
	}
	// Create a push job → audit recorded.
	if code := postCode(t, client, srv.URL+"/api/admin/push/jobs", `{"title":"hi","content":"x","channel_type":"telegram","target":{"tier":"gold"}}`); code != 200 {
		t.Fatalf("operator POST push = %d", code)
	}
	entries, _ := st.Audit().Recent(context.Background(), 10)
	if len(entries) < 2 {
		t.Fatalf("expected audit entries for rule + push, got %d", len(entries))
	}
}

func TestAdminOwner_RegisterClientAndRotateKey(t *testing.T) {
	srv, client, priv, vkey, st := adminResourceEnv(t, domain.RoleOwner)

	// Register a confidential client → secret returned once.
	resp := postJSON(t, client, srv.URL+"/api/admin/oauth-clients", `{"client_id":"c1","name":"App","client_type":"confidential","party":"first_party","redirect_uris":["https://app/cb"],"allowed_audiences":["app:ouro"],"allowed_scopes":["read"]}`)
	if resp["client_secret"] == "" || resp["client_secret"] == nil {
		t.Fatalf("expected one-time client_secret: %v", resp)
	}

	// Rotate key WITHOUT step-up → unauthorized.
	if code := postCode(t, client, srv.URL+"/api/admin/keys/issuer/rotate", `{}`); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("rotate without step-up = %d, want 401/403", code)
	}

	// Rotate WITH a valid step-up signature → success.
	suNonce := stepUpNonce(t, st, vkey)
	body, _ := json.Marshal(map[string]string{"owner_vkey": vkey, "step_up_nonce": suNonce, "step_up_signature": signNonce(t, priv, suNonce)})
	r, _ := client.Post(srv.URL+"/api/admin/keys/issuer/rotate", "application/json", strings.NewReader(string(body)))
	if r.StatusCode != 200 {
		t.Fatalf("rotate with step-up = %d, want 200", r.StatusCode)
	}
	var rr map[string]any
	json.NewDecoder(r.Body).Decode(&rr)
	if rr["new_kid"] == "" || rr["new_kid"] == nil {
		t.Fatalf("rotate response: %v", rr)
	}

	// Audit captured the rotation.
	entries, _ := st.Audit().Recent(context.Background(), 10)
	found := false
	for _, e := range entries {
		if e.Action == "issuer_key.rotate" {
			found = true
		}
	}
	if !found {
		t.Error("issuer_key.rotate not audited")
	}
}

// stepUpNonce issues a step-up nonce directly through the wallet repo path by
// asking the admin service via a fresh challenge.
func stepUpNonce(t *testing.T, st *store.Store, vkey string) string {
	t.Helper()
	svc := admin.New(admin.Config{Wallet: walletauth.New(st, time.Minute), Store: st, PoolID: "pool1"})
	nonce, _, err := svc.ChallengeStepUp(context.Background(), vkey)
	if err != nil {
		t.Fatal(err)
	}
	return nonce
}

func getCode(t *testing.T, c *http.Client, url string) int {
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func postCode(t *testing.T, c *http.Client, url, body string) int {
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func postJSON(t *testing.T, c *http.Client, url, body string) map[string]any {
	resp, err := c.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	json.NewDecoder(resp.Body).Decode(&m)
	return m
}
