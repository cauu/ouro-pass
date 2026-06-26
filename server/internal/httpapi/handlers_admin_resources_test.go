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

	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
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
	// S0006: the served pool is configured as a pool_stake attestor; the delegator
	// roster resolves the real pool_id from it.
	atParams, _ := json.Marshal(attestor.PoolStakeParams{PoolID: "pool1", Network: "preview"})
	st.Attestors().Create(ctx, domain.AttestorConfig{
		AttestorID: "att-pool1", Kind: attestor.KindPoolStake, Label: "pool1", Params: atParams,
		Status: domain.AttestorActive, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	cipher, _ := crypto.NewFieldCipher(make([]byte, 32))
	deps := Deps{
		Wallet: wallet, Store: st, PoolID: "pool1", Keys: keys.New(st, cipher), Cipher: cipher,
		Chain: &chain.MockSource{DelegatorsByPool: map[string][]string{"pool1": {"sch-aaa", "sch-bbb"}}},
		Admin: admin.New(admin.Config{Wallet: wallet, Store: st, OwnerKeyHash: ownerKeys, PoolID: "pool1"}),
	}
	srv := httptest.NewTLSServer(NewRouter(deps))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := srv.Client()
	client.Jar = jar

	// Login.
	nonce := adminChallengeReq(t, client, srv.URL, vkey)
	body, _ := json.Marshal(map[string]string{"cose_key": coseKeyOf(vkey), "nonce": nonce, "signature": signNonce(t, priv, nonce)})
	resp, _ := client.Post(srv.URL+"/api/admin/auth/verify", "application/json", strings.NewReader(string(body)))
	if resp.StatusCode != 200 {
		t.Fatalf("login as %s = %d", role, resp.StatusCode)
	}
	return srv, client, priv, vkey, st
}

func TestAdminRBAC_Matrix(t *testing.T) {
	srv, client, _, _, _ := adminResourceEnv(t, domain.RoleViewer)

	// viewer can read members but not register clients.
	if code := getCode(t, client, srv.URL+"/api/admin/members"); code != 200 {
		t.Errorf("viewer GET members = %d, want 200", code)
	}
	if code := postCode(t, client, srv.URL+"/api/admin/push/jobs", `{"title":"x"}`); code != http.StatusForbidden {
		t.Errorf("viewer POST push = %d, want 403", code)
	}
	if code := getCode(t, client, srv.URL+"/api/admin/oauth-clients"); code != http.StatusForbidden {
		t.Errorf("viewer GET clients = %d, want 403 (owner only)", code)
	}
}

// TestAdminConfigureChannel: configuring the Telegram bot token stores it
// ENCRYPTED (never plaintext) and surfaces a "configured" status (S0004 p8-1).
func TestAdminConfigureChannel(t *testing.T) {
	srv, client, _, _, st := adminResourceEnv(t, domain.RoleOperator)
	const token = "987654:XYZ-secret-bot-token"

	if c := postCode(t, client, srv.URL+"/api/admin/channels/telegram/configure",
		`{"config":{"bot_token":"`+token+`"}}`); c != 200 {
		t.Fatalf("configure telegram = %d, want 200", c)
	}

	// Stored config must be encrypted (no plaintext token in the row).
	cfg, err := st.Channels().GetByType(context.Background(), "telegram")
	if err != nil {
		t.Fatalf("channel not stored: %v", err)
	}
	if strings.Contains(string(cfg.Config), token) {
		t.Fatalf("bot token stored in PLAINTEXT: %s", cfg.Config)
	}

	// Status endpoint reports telegram configured.
	resp, _ := client.Get(srv.URL + "/api/admin/channels")
	defer resp.Body.Close()
	var body struct {
		Channels []struct {
			ChannelType string `json:"channel_type"`
			Configured  bool   `json:"configured"`
		} `json:"channels"`
	}
	json.NewDecoder(resp.Body).Decode(&body)
	if len(body.Channels) != 1 || body.Channels[0].ChannelType != "telegram" || !body.Channels[0].Configured {
		t.Fatalf("status = %+v, want telegram configured", body.Channels)
	}

	// Missing bot_token → 400.
	if c := postCode(t, client, srv.URL+"/api/admin/channels/telegram/configure", `{"config":{}}`); c != http.StatusBadRequest {
		t.Fatalf("empty bot_token = %d, want 400", c)
	}
}

// TestAdminTierRules: operator sets the first-party tier mapping; GET reflects it;
// invalid rules are rejected (S0004 p7-1).
func TestAdminTierRules(t *testing.T) {
	srv, client, _, _, _ := adminResourceEnv(t, domain.RoleOperator)

	getPool := func() map[string]any {
		resp, err := client.Get(srv.URL + "/api/admin/pool")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	if tr, _ := json.Marshal(getPool()["tier_rules"]); string(tr) != "[]" {
		t.Fatalf("default tier_rules = %s, want []", tr)
	}

	// Invalid (bad op in the boolean DSL) → 400.
	if c := postCode(t, client, srv.URL+"/api/admin/pool/tier-rules", `{"tier_rules":[{"tier":"x","when":{"fact":"any_active","op":"~=","value":"true"}}]}`); c != http.StatusBadRequest {
		t.Fatalf("bad op = %d, want 400", c)
	}

	// Valid → stored and reflected by GET.
	rules := `{"tier_rules":[{"tier":"gold","when":{"fact":"total_active_stake","op":">=","value":"1000000"}},{"tier":"basic","when":{"fact":"any_active","op":"==","value":"true"}}]}`
	if c := postCode(t, client, srv.URL+"/api/admin/pool/tier-rules", rules); c != 200 {
		t.Fatalf("set tier_rules = %d, want 200", c)
	}
	tr, _ := json.Marshal(getPool()["tier_rules"])
	if !strings.Contains(string(tr), `"gold"`) || !strings.Contains(string(tr), `"1000000"`) {
		t.Fatalf("tier_rules not persisted: %s", tr)
	}
}

// TestAdminDelegators lists the pool's full on-chain delegator set (S0004 §2.7):
// a viewer-readable roster served from the chain source (mocked here).
func TestAdminDelegators(t *testing.T) {
	srv, client, _, _, _ := adminResourceEnv(t, domain.RoleViewer)
	resp, err := client.Get(srv.URL + "/api/admin/delegators")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Delegators []string `json:"delegators"`
		Page       int      `json:"page"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Delegators) != 2 || body.Delegators[0] != "sch-aaa" || body.Delegators[1] != "sch-bbb" {
		t.Fatalf("delegators = %v (want full set sch-aaa/sch-bbb)", body.Delegators)
	}
}

func TestAdminRevokeMember_BlacklistAndCascade(t *testing.T) {
	srv, client, priv, vkey, st := adminResourceEnv(t, domain.RoleOperator)
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

	// Without a step-up signature → rejected (p12-11).
	if code := postCode(t, client, srv.URL+"/api/admin/members/"+sch+"/revoke", `{}`); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("revoke without step-up = %d, want 401/403", code)
	}
	// With a fresh step-up signature → success.
	suNonce := stepUpNonce(t, st, vkey)
	revokeBody, _ := json.Marshal(map[string]string{"cose_key": coseKeyOf(vkey), "step_up_nonce": suNonce, "step_up_signature": signNonce(t, priv, suNonce)})
	if code := postCode(t, client, srv.URL+"/api/admin/members/"+sch+"/revoke", string(revokeBody)); code != 200 {
		t.Fatalf("revoke with step-up = %d, want 200", code)
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
	// operator can hit push/channels; owner needed for clients.
	srvOp, op, _, _, _ := adminResourceEnv(t, domain.RoleOperator)
	if c := postCode(t, op, srvOp.URL+"/api/admin/push/jobs", `{"title":"hi","channel_type":"carrier-pigeon"}`); c != http.StatusBadRequest {
		t.Errorf("bad push channel_type = %d, want 400", c)
	}
	if c := postCode(t, op, srvOp.URL+"/api/admin/channels/carrier-pigeon/configure", `{"config":{}}`); c != http.StatusBadRequest {
		t.Errorf("bad channel type = %d, want 400", c)
	}

	srvOwn, own, _, _, _ := adminResourceEnv(t, domain.RoleOwner)
	if c := postCode(t, own, srvOwn.URL+"/api/admin/oauth-clients", `{"name":"x","client_type":"weird"}`); c != http.StatusBadRequest {
		t.Errorf("bad client_type = %d, want 400", c)
	}
	// Missing name → 400 (client_id is system-generated, so name is the only
	// required human field now).
	if c := postCode(t, own, srvOwn.URL+"/api/admin/oauth-clients", `{"client_type":"public"}`); c != http.StatusBadRequest {
		t.Errorf("missing name = %d, want 400", c)
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

func TestAdminOperator_Push(t *testing.T) {
	srv, client, _, _, st := adminResourceEnv(t, domain.RoleOperator)
	// Create a push job → audit recorded.
	if code := postCode(t, client, srv.URL+"/api/admin/push/jobs", `{"title":"hi","content":"x","channel_type":"telegram","target":{"tier":"gold"}}`); code != 200 {
		t.Fatalf("operator POST push = %d", code)
	}
	entries, _ := st.Audit().Recent(context.Background(), 10)
	if len(entries) < 1 {
		t.Fatalf("expected an audit entry for push, got %d", len(entries))
	}
}

func TestAdminOwner_RegisterClientAndRotateKey(t *testing.T) {
	srv, client, priv, vkey, st := adminResourceEnv(t, domain.RoleOwner)

	// Registering a client requires step-up (p12-11): without it → rejected.
	// A supplied "client_id":"c1" must be ignored — the server generates its own.
	const regFields = `"client_id":"c1","name":"App","client_type":"confidential","redirect_uris":["https://app/cb"],"allowed_audiences":["app:ouro"]`
	if code := postCode(t, client, srv.URL+"/api/admin/oauth-clients", `{`+regFields+`}`); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("register without step-up = %d, want 401/403", code)
	}
	// With a fresh step-up signature → confidential client + one-time secret.
	regNonce := stepUpNonce(t, st, vkey)
	regBody := `{` + regFields + `,"cose_key":"` + coseKeyOf(vkey) + `","step_up_nonce":"` + regNonce + `","step_up_signature":"` + signNonce(t, priv, regNonce) + `"}`
	resp := postJSON(t, client, srv.URL+"/api/admin/oauth-clients", regBody)
	if resp["client_secret"] == "" || resp["client_secret"] == nil {
		t.Fatalf("expected one-time client_secret: %v", resp)
	}
	// client_id is system-generated (op-client-… prefix), not the supplied "c1".
	gotID, _ := resp["client_id"].(string)
	if gotID == "c1" || !strings.HasPrefix(gotID, "op-client-") {
		t.Fatalf("client_id = %q, want a generated op-client-… id (supplied id ignored)", gotID)
	}
	origSecret, _ := resp["client_secret"].(string)

	// Regenerate the client secret: without step-up → rejected.
	secretURL := srv.URL + "/api/admin/oauth-clients/" + gotID + "/secret"
	if code := postCode(t, client, secretURL, `{}`); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("regenerate without step-up = %d, want 401/403", code)
	}
	// With step-up → a fresh secret, different from the original.
	rsNonce := stepUpNonce(t, st, vkey)
	rsBody := `{"cose_key":"` + coseKeyOf(vkey) + `","step_up_nonce":"` + rsNonce + `","step_up_signature":"` + signNonce(t, priv, rsNonce) + `"}`
	rs := postJSON(t, client, secretURL, rsBody)
	newSecret, _ := rs["client_secret"].(string)
	if newSecret == "" || newSecret == origSecret {
		t.Fatalf("regenerate: new secret %q must be non-empty and differ from original", newSecret)
	}
	// Regenerating an unknown client → 404.
	rsNonce2 := stepUpNonce(t, st, vkey)
	rsBody2 := `{"cose_key":"` + coseKeyOf(vkey) + `","step_up_nonce":"` + rsNonce2 + `","step_up_signature":"` + signNonce(t, priv, rsNonce2) + `"}`
	if code := postCode(t, client, srv.URL+"/api/admin/oauth-clients/op-client-nope/secret", rsBody2); code != http.StatusNotFound {
		t.Fatalf("regenerate unknown client = %d, want 404", code)
	}

	// Rotate key WITHOUT step-up → unauthorized.
	if code := postCode(t, client, srv.URL+"/api/admin/keys/issuer/rotate", `{}`); code != http.StatusUnauthorized && code != http.StatusForbidden {
		t.Fatalf("rotate without step-up = %d, want 401/403", code)
	}

	// Rotate WITH a valid step-up signature → success.
	suNonce := stepUpNonce(t, st, vkey)
	body, _ := json.Marshal(map[string]string{"cose_key": coseKeyOf(vkey), "step_up_nonce": suNonce, "step_up_signature": signNonce(t, priv, suNonce)})
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
	nonce, _, err := svc.ChallengeStepUp(context.Background(), rewardAddrOf(vkey))
	if err != nil {
		t.Fatal(err)
	}
	return nonce
}

// TestAdminCancelSubscription covers the operator subscription-cancel mutation
// (p14-3): 200 + DB状态 + audit; viewer is forbidden.
func TestAdminCancelSubscription(t *testing.T) {
	srv, client, _, _, st := adminResourceEnv(t, domain.RoleOperator)
	ctx := context.Background()
	if err := st.Subscriptions().Upsert(ctx, domain.SubscriptionSession{
		SessionID: "sess1", PoolID: "pool1", StakeCredentialHash: "h1", ChannelType: "telegram",
		ChannelUserID: "u1", Status: domain.SubActive, Tier: "gold",
		CreatedAt: time.Now(), LastVerifiedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if code := postCode(t, client, srv.URL+"/api/admin/subscriptions/sess1/cancel", ``); code != 200 {
		t.Fatalf("operator cancel = %d, want 200", code)
	}
	sub, _ := st.Subscriptions().GetByChannelUser(ctx, "pool1", "telegram", "u1")
	if sub.Status != domain.SubCancelled {
		t.Fatalf("status = %s, want cancelled", sub.Status)
	}
	entries, _ := st.Audit().Recent(ctx, 10)
	var audited bool
	for _, e := range entries {
		if e.Action == "subscription.cancel" {
			audited = true
		}
	}
	if !audited {
		t.Error("subscription.cancel not written to AuditLog")
	}

	// viewer must not be able to cancel.
	vsrv, vclient, _, _, _ := adminResourceEnv(t, domain.RoleViewer)
	if code := postCode(t, vclient, vsrv.URL+"/api/admin/subscriptions/x/cancel", ``); code != http.StatusForbidden {
		t.Fatalf("viewer cancel = %d, want 403", code)
	}
}

// TestAdminListClients_NoSecretLeak ensures the owner client-list response never
// carries client_secret_hash material (handlers strip it) (p14-3).
func TestAdminListClients_NoSecretLeak(t *testing.T) {
	srv, client, _, _, st := adminResourceEnv(t, domain.RoleOwner)
	secretHash := crypto.HashToken("super-secret")
	if err := st.OAuthClients().Upsert(context.Background(), domain.OAuthClient{
		ClientID: "c1", Name: "App", ClientType: domain.ClientConfidential,
		ClientSecretHash: &secretHash, RedirectURIs: []string{"https://cb"}, Status: "active", CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := client.Get(srv.URL + "/api/admin/oauth-clients")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	raw, _ := json.Marshal(body)
	if strings.Contains(string(raw), secretHash) || strings.Contains(string(raw), "secret_hash") {
		t.Fatalf("client list leaked secret material: %s", raw)
	}
}

// TestAdminListEndpoints_Smoke checks the read endpoints are mounted and respond
// 200 for an owner (covers route wiring + SQL scan paths) (p14-3).
func TestAdminListEndpoints_Smoke(t *testing.T) {
	srv, client, _, _, _ := adminResourceEnv(t, domain.RoleOwner)
	for _, path := range []string{"/api/admin/subscriptions", "/api/admin/push/jobs", "/api/admin/audit"} {
		if code := getCode(t, client, srv.URL+path); code != 200 {
			t.Errorf("GET %s = %d, want 200", path, code)
		}
	}
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

// TestAdminStepUpChallenge_Route covers the S0002 p2-0 enabler: the logged-in
// admin can fetch a step-up nonce over HTTP (previously only via the service).
func TestAdminStepUpChallenge_Route(t *testing.T) {
	srv, client, _, vkey, _ := adminResourceEnv(t, domain.RoleOwner)

	body, _ := json.Marshal(map[string]string{"owner_stake_address": rewardAddrOf(vkey)})
	resp, err := client.Post(srv.URL+"/api/admin/auth/step-up/challenge", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("step-up challenge = %d, want 200", resp.StatusCode)
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	if n, _ := out["nonce"].(string); n == "" {
		t.Fatalf("no nonce in response: %v", out)
	}
	// The route lives in the requireSession group (same gate as /me, covered by
	// TestAdminLogin_CookieFlowAndRBAC), so unauthenticated access is 401.
}
