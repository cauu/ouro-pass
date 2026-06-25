// Package e2e drives the fully-assembled HTTP router (httpapi.NewRouter) end to
// end over httptest, exactly as main wires it, but with SQLite + a mock chain +
// an in-memory Telegram transport — no external process. These tests exercise
// wiring/route/middleware integration (the class of bug a per-package unit test
// can't see, e.g. a worker never started) on top of the real OAuth/admin logic.
package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fxamacker/cbor/v2"

	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/worker/push"
	"ouro-pass/server/internal/worker/reconciliation"
	"ouro-pass/server/internal/worker/telegram"
)

const testPool = "pool1e2e"

// env is a running issuer: full router over httptest + the live services so
// tests can also reach store/chain/admin directly to assert state or drive the
// non-HTTP subsystems (Telegram processor, push scheduler).
type env struct {
	t      *testing.T
	srv    *httptest.Server
	client *http.Client
	st     *store.Store
	chain  *chain.MockSource
	oauth  *oauth.Server
	admin  *admin.Service
}

func newEnv(t *testing.T) *env {
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

	cipherKey := make([]byte, 32)
	rand.Read(cipherKey)
	cipher, _ := crypto.NewFieldCipher(cipherKey)
	ks := keys.New(st, cipher)
	if _, err := ks.Rotate(ctx); err != nil { // bootstrap an active signing key
		t.Fatal(err)
	}

	wallet := walletauth.New(st, time.Minute)
	mock := chain.NewMockSource(480)
	oas := oauth.New(oauth.Config{
		Store: st, Wallet: wallet, Keys: ks, Chain: mock,
		PoolID: testPool, Issuer: "ouropass:" + testPool, ServerSalt: []byte("e2e-salt"),
		AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	})
	adm := admin.New(admin.Config{Wallet: wallet, Store: st, OwnerKeyHash: nil, PoolID: testPool})

	deps := httpapi.Deps{
		Wallet: wallet, Keys: ks, OAuth: oas, Admin: adm, Store: st,
		PoolID: testPool, TelegramBot: "ouro_e2e_bot", SecureCookies: false,
	}
	srv := httptest.NewServer(httpapi.NewRouter(deps))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	client := srv.Client()
	client.Jar = jar
	return &env{t: t, srv: srv, client: client, st: st, chain: mock, oauth: oas, admin: adm}
}

// --- wallet + helpers ------------------------------------------------------

type wallet struct {
	priv       ed25519.PrivateKey
	vkey       string
	sch        string
	rewardAddr string // CIP-30 reward address (challenge input)
	coseKey    string // CIP-30 signData `key` carrying the vkey (verify input)
}

func newWallet(t *testing.T) wallet {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cred := crypto.Blake2b224(pub)
	ck, _ := cbor.Marshal(map[int]any{1: 1, -1: 6, -2: []byte(pub), 3: -8})
	return wallet{
		priv: priv, vkey: hex.EncodeToString(pub), sch: hex.EncodeToString(cred),
		rewardAddr: hex.EncodeToString(append([]byte{0xe1}, cred...)), coseKey: hex.EncodeToString(ck),
	}
}

// cose builds a CIP-8 COSE_Sign1 over the nonce, as a CIP-30 wallet's signData.
func cose(priv ed25519.PrivateKey, nonce string) string {
	protected, _ := cbor.Marshal(map[int]int{1: -8}) // alg EdDSA
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

// post sends a JSON body and returns status + decoded map (no redirect follow).
func (e *env) post(path, body string) (int, map[string]any) {
	e.t.Helper()
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	var m map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

// noRedirectClient is a clone that captures 3xx instead of following.
func (e *env) authorize(w wallet, clientID, redirect, aud, codeChallenge, devicePubkey string) (int, string) {
	e.t.Helper()
	// 1. challenge
	st, body := e.post("/api/auth/challenge", `{"purpose":"issue","stake_address":"`+w.rewardAddr+`"}`)
	if st != 200 {
		e.t.Fatalf("challenge = %d", st)
	}
	nonce, _ := body["nonce"].(string)
	// 2. authorize (returns 302 with ?code= or ?error=)
	req, _ := http.NewRequest(http.MethodPost, e.srv.URL+"/api/connect/authorize", strings.NewReader(
		`{"client_id":"`+clientID+`","redirect_uri":"`+redirect+`","aud":"`+aud+`","nonce":"`+nonce+
			`","cose_key":"`+w.coseKey+`","signature":"`+cose(w.priv, nonce)+`","code_challenge":"`+codeChallenge+
			`","device_pubkey":"`+devicePubkey+`"}`))
	req.Header.Set("Content-Type", "application/json")
	// Capture the redirect rather than following it.
	prev := e.client.CheckRedirect
	e.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { e.client.CheckRedirect = prev }()
	resp, err := e.client.Do(req)
	if err != nil {
		e.t.Fatalf("authorize: %v", err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	return resp.StatusCode, loc
}

func codeFromLocation(t *testing.T, loc string) string {
	t.Helper()
	i := strings.Index(loc, "code=")
	if i < 0 {
		return ""
	}
	rest := loc[i+5:]
	if amp := strings.IndexByte(rest, '&'); amp >= 0 {
		rest = rest[:amp]
	}
	return rest
}

// eligible registers a gold rule and marks the wallet a qualifying delegator.
func (e *env) eligible(w wallet) {
	e.t.Helper()
	ctx := context.Background()
	_ = e.st.Rules().Upsert(ctx, domain.MembershipRule{
		RuleID: "gold", Name: "gold", Tier: "gold", Priority: 10, Status: domain.RuleActive,
		RuleConfig: json.RawMessage(`{"min_active_stake_lovelace":"1000000"}`), Entitlements: []string{"read"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	e.chain.Put(&chain.Snapshot{
		StakeCredentialHash: w.sch, Epoch: 480, DelegatedPoolID: testPool, ActiveStakePoolID: testPool,
		ActiveStakeLovelace: "5000000", EpochsDelegated: 5, AccountStatus: "registered",
	})
}

func (e *env) seedClient(c domain.OAuthClient) {
	c.Status, c.CreatedAt = "active", time.Now()
	if err := e.st.OAuthClients().Upsert(context.Background(), c); err != nil {
		e.t.Fatal(err)
	}
}

// --- Flow A: confidential authorization-code lifecycle ---------------------

func TestE2E_ConfidentialAuthCodeLifecycle(t *testing.T) {
	e := newEnv(t)
	w := newWallet(t)
	e.eligible(w)
	secret := "top-secret"
	e.seedClient(domain.OAuthClient{
		ClientID: "web", Name: "Web", ClientType: domain.ClientConfidential,
		ClientSecretHash: ptr(crypto.HashToken(secret)), RedirectURIs: []string{"https://web/cb"},
		AllowedAudiences: []string{"app:ouro"},
	})

	// authorize → code (PKCE is mandatory for all clients, confidential included)
	verifier := "the-pkce-code-verifier-string-e2e-web"
	challenge := pkceS256(verifier)
	st, loc := e.authorize(w, "web", "https://web/cb", "app:ouro", challenge, "")
	if st != http.StatusFound {
		t.Fatalf("authorize status = %d, want 302 (loc=%s)", st, loc)
	}
	code := codeFromLocation(t, loc)
	if code == "" {
		t.Fatalf("no code in %s", loc)
	}

	// token (authorization_code) — confidential client presents secret AND verifier
	st, tok := e.post("/api/oauth/token",
		`{"grant_type":"authorization_code","code":"`+code+`","client_id":"web","client_secret":"`+secret+`","code_verifier":"`+verifier+`","redirect_uri":"https://web/cb"}`)
	if st != 200 {
		t.Fatalf("token = %d (%v)", st, tok)
	}
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("missing tokens: %v", tok)
	}

	// introspect → active
	st, intro := e.post("/api/oauth/introspect", `{"token":"`+access+`"}`)
	if st != 200 || intro["active"] != true || intro["tier"] != "gold" {
		t.Fatalf("introspect = %d %v", st, intro)
	}

	// refresh → new pair, old refresh now revoked-on-replay
	st, refreshed := e.post("/api/oauth/token",
		`{"grant_type":"refresh_token","refresh_token":"`+refresh+`","client_id":"web","client_secret":"`+secret+`"}`)
	if st != 200 {
		t.Fatalf("refresh = %d (%v)", st, refreshed)
	}
	newRefresh, _ := refreshed["refresh_token"].(string)
	if newRefresh == "" || newRefresh == refresh {
		t.Fatalf("refresh did not rotate: %v", refreshed)
	}
	// Replaying the old refresh → invalid_grant (theft chain revoke).
	if st, _ := e.post("/api/oauth/token",
		`{"grant_type":"refresh_token","refresh_token":"`+refresh+`","client_id":"web","client_secret":"`+secret+`"}`); st != http.StatusBadRequest {
		t.Fatalf("replayed old refresh = %d, want 400 invalid_grant", st)
	}

	// revoke the access token → introspect inactive
	if st, _ := e.post("/api/oauth/revoke", `{"token":"`+access+`"}`); st != 200 {
		t.Fatalf("revoke = %d", st)
	}
	if _, intro := e.post("/api/oauth/introspect", `{"token":"`+access+`"}`); intro["active"] != false {
		t.Fatalf("revoked token still active: %v", intro)
	}
}

// --- Flow B: public PKCE + device PoP --------------------------------------

func TestE2E_PublicPKCEDevicePoP(t *testing.T) {
	e := newEnv(t)
	w := newWallet(t)
	e.eligible(w)
	e.seedClient(domain.OAuthClient{
		ClientID: "spa", Name: "SPA", ClientType: domain.ClientPublic,
		RedirectURIs: []string{"https://spa/cb"}, AllowedAudiences: []string{"app:ouro"},
	})
	verifier := "the-pkce-code-verifier-string-e2e"
	challenge := pkceS256(verifier)
	device := hex.EncodeToString([]byte("device-public-key-bytes-32-xxxxx")) // 32 bytes

	st, loc := e.authorize(w, "spa", "https://spa/cb", "app:ouro", challenge, "")
	if st != http.StatusFound {
		t.Fatalf("authorize = %d", st)
	}
	code := codeFromLocation(t, loc)

	st, tok := e.post("/api/oauth/token",
		`{"grant_type":"authorization_code","code":"`+code+`","client_id":"spa","code_verifier":"`+verifier+`","redirect_uri":"https://spa/cb","device_pubkey":"`+device+`"}`)
	if st != 200 {
		t.Fatalf("public token = %d (%v)", st, tok)
	}
	refresh, _ := tok["refresh_token"].(string)

	// Refresh WITHOUT device → invalid_grant; WITH device → ok.
	if st, _ := e.post("/api/oauth/token", `{"grant_type":"refresh_token","refresh_token":"`+refresh+`","client_id":"spa"}`); st != http.StatusBadRequest {
		t.Fatalf("refresh without device = %d, want 400", st)
	}
	if st, _ := e.post("/api/oauth/token", `{"grant_type":"refresh_token","refresh_token":"`+refresh+`","client_id":"spa","device_pubkey":"`+device+`"}`); st != 200 {
		t.Fatalf("refresh with device = %d, want 200", st)
	}
}

// --- Flow C: ineligible + admin revoke cascade -----------------------------

func TestE2E_IneligibleAndAdminRevoke(t *testing.T) {
	e := newEnv(t)
	owner := newWallet(t)
	// Owner allowlist: rebuild env admin with this owner key.
	e.seedOwner(owner)

	member := newWallet(t)
	e.eligible(member)
	e.seedClient(domain.OAuthClient{
		ClientID: "web", Name: "Web", ClientType: domain.ClientConfidential,
		ClientSecretHash: ptr(crypto.HashToken("s")), RedirectURIs: []string{"https://web/cb"},
		AllowedAudiences: []string{"app:ouro"},
	})

	verifier := "the-pkce-code-verifier-string-e2e-revoke"
	challenge := pkceS256(verifier)

	// Member is eligible → gets a token.
	_, loc := e.authorize(member, "web", "https://web/cb", "app:ouro", challenge, "")
	code := codeFromLocation(t, loc)
	if st, _ := e.post("/api/oauth/token", `{"grant_type":"authorization_code","code":"`+code+`","client_id":"web","client_secret":"s","code_verifier":"`+verifier+`","redirect_uri":"https://web/cb"}`); st != 200 {
		t.Fatalf("eligible token = %d", st)
	}

	// Admin logs in (owner) and revokes the member with step-up.
	e.adminLogin(owner)
	suNonce, _, _ := e.admin.ChallengeStepUp(context.Background(), owner.rewardAddr)
	revokeBody := `{"cose_key":"` + owner.coseKey + `","step_up_nonce":"` + suNonce + `","step_up_signature":"` + cose(owner.priv, suNonce) + `"}`
	if st, _ := e.post("/api/admin/members/"+member.sch+"/revoke", revokeBody); st != 200 {
		t.Fatalf("admin revoke = %d", st)
	}

	// Now blacklisted → a fresh authorize is not_eligible.
	_, loc2 := e.authorize(member, "web", "https://web/cb", "app:ouro", challenge, "")
	if !strings.Contains(loc2, "error=not_eligible") {
		t.Fatalf("revoked member authorize = %s, want error=not_eligible", loc2)
	}

	// An ineligible (non-delegating) wallet is rejected too.
	other := newWallet(t)
	e.chain.Put(&chain.Snapshot{StakeCredentialHash: other.sch, Epoch: 480, DelegatedPoolID: "pool1other", ActiveStakeLovelace: "5000000"})
	_, loc3 := e.authorize(other, "web", "https://web/cb", "app:ouro", challenge, "")
	if !strings.Contains(loc3, "error=not_eligible") {
		t.Fatalf("non-delegator authorize = %s, want error=not_eligible", loc3)
	}
}

// --- Flow D: activation -> Telegram bot -> subscription --------------------

func TestE2E_ActivationToTelegramSubscription(t *testing.T) {
	e := newEnv(t)
	w := newWallet(t)
	e.eligible(w)

	// Get an activation nonce + sign it, then create the activation code (HTTP).
	st, body := e.post("/api/auth/challenge", `{"purpose":"activation","stake_address":"`+w.rewardAddr+`"}`)
	if st != 200 {
		t.Fatalf("activation challenge = %d", st)
	}
	nonce, _ := body["nonce"].(string)
	st, act := e.post("/api/activation/create",
		`{"channel_type":"telegram","nonce":"`+nonce+`","cose_key":"`+w.coseKey+`","signature":"`+cose(w.priv, nonce)+`"}`)
	if st != 200 {
		t.Fatalf("activation create = %d (%v)", st, act)
	}
	code, _ := act["activation_code"].(string)
	if code == "" || !strings.Contains(act["deep_link"].(string), code) {
		t.Fatalf("activation: %v", act)
	}

	// Drive the real Telegram processor (the bot worker's unit) with /start <code>.
	proc := telegram.NewProcessor(e.st, e.oauth, testPool)
	reply := proc.Handle(context.Background(), telegram.Update{UserID: "tg-42", ChatID: "tg-42", Text: "/start " + code})
	if reply == "" {
		t.Fatal("empty bot reply")
	}
	sess, err := e.st.Subscriptions().GetByChannelUser(context.Background(), testPool, "telegram", "tg-42")
	if err != nil || sess.Status != domain.SubActive || sess.Tier != "gold" {
		t.Fatalf("subscription not created: %v %+v", err, sess)
	}
	// Re-using the consumed code must not create a second session.
	_ = proc.Handle(context.Background(), telegram.Update{UserID: "tg-99", ChatID: "tg-99", Text: "/start " + code})
	if _, err := e.st.Subscriptions().GetByChannelUser(context.Background(), testPool, "telegram", "tg-99"); err == nil {
		t.Fatal("consumed activation code created a second subscription")
	}

	// Push: a job to gold members is delivered to the session via the scheduler.
	rec := &capturingSender{}
	sched := push.NewScheduler(e.st, rec, push.Options{})
	job := domain.PushJob{
		JobID: crypto.RandomID(), PoolID: testPool, Title: "Hi", Content: "members only",
		ChannelType: "telegram", TargetTier: ptr("gold"), Status: domain.PushScheduled, CreatedAt: time.Now(),
	}
	_ = e.st.PushJobs().Create(context.Background(), job)
	res, err := sched.Run(context.Background(), job)
	if err != nil || res.Sent != 1 {
		t.Fatalf("push run: sent=%d err=%v", res.Sent, err)
	}
	if len(rec.chats) != 1 || rec.chats[0] != "tg-42" {
		t.Fatalf("push recipients = %v, want [tg-42]", rec.chats)
	}
}

type capturingSender struct{ chats []string }

func (c *capturingSender) SendMessage(_ context.Context, chatID, _ string) error {
	c.chats = append(c.chats, chatID)
	return nil
}

// --- Flow F: key rotation + JWKS overlap -----------------------------------

func TestE2E_KeyRotationJWKSOverlap(t *testing.T) {
	e := newEnv(t)
	owner := newWallet(t)
	e.seedOwner(owner)
	e.adminLogin(owner)

	// JWKS starts with one active key.
	before := e.jwks(t)
	if len(before) != 1 {
		t.Fatalf("initial jwks keys = %d, want 1", len(before))
	}

	suNonce, _, _ := e.admin.ChallengeStepUp(context.Background(), owner.rewardAddr)
	body := `{"cose_key":"` + owner.coseKey + `","step_up_nonce":"` + suNonce + `","step_up_signature":"` + cose(owner.priv, suNonce) + `"}`
	if st, r := e.post("/api/admin/keys/issuer/rotate", body); st != 200 || r["new_kid"] == nil {
		t.Fatalf("rotate = %d (%v)", st, r)
	}
	// After rotation JWKS publishes both (overlap: new active + prior rotating).
	after := e.jwks(t)
	if len(after) != 2 {
		t.Fatalf("post-rotate jwks keys = %d, want 2 (overlap)", len(after))
	}
}

// TestE2E_IntrospectIgnoresBareJTI proves the jti-oracle defense at the ROUTE
// layer (p14-4/TC-21): a client-supplied bare jti is ignored by the handler even
// when that jti is an active ledger token, so the endpoint can't be a
// token-status oracle by jti enumeration.
func TestE2E_IntrospectIgnoresBareJTI(t *testing.T) {
	e := newEnv(t)
	jti := "active-jti-" + hex.EncodeToString([]byte("xyz"))
	if err := e.st.IssuedTokens().Create(context.Background(), nil, domain.IssuedToken{
		JTI: jti, StakeCredentialHash: "h1", Kind: domain.TokenAccess, Audience: "app",
		KID: "k1", Status: domain.TokenActive, IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	st, res := e.post("/api/oauth/introspect", `{"jti":"`+jti+`"}`)
	if st != 200 || res["active"] != false {
		t.Fatalf("introspect by bare jti = %d %v, want active:false (jti must be ignored)", st, res)
	}
}

// TestE2E_IssuancePlaneRateLimited proves publicLimit is actually mounted on the
// issuance plane (p14-4/TC-21): rapid same-IP token requests eventually 429.
func TestE2E_IssuancePlaneRateLimited(t *testing.T) {
	e := newEnv(t)
	var got429 bool
	for i := 0; i < 80 && !got429; i++ {
		st, _ := e.post("/api/oauth/token", `{"grant_type":"refresh_token","refresh_token":"nope","client_id":"c"}`)
		if st == http.StatusTooManyRequests {
			got429 = true
		}
	}
	if !got429 {
		t.Fatal("expected 429 after exceeding the burst on /api/oauth/token (publicLimit not mounted?)")
	}
}

// TestE2E_ReconciliationExpiresIneligible is the reconciliation flow (p14-6,
// closes TC-24's 6th flow): an active member who loses eligibility is expired by
// the reconciler running against the same store the router uses.
func TestE2E_ReconciliationExpiresIneligible(t *testing.T) {
	e := newEnv(t)
	w := newWallet(t)
	e.eligible(w)
	ctx := context.Background()
	if err := e.st.Subscriptions().Upsert(ctx, domain.SubscriptionSession{
		SessionID: "s-recon", PoolID: testPool, StakeCredentialHash: w.sch, ChannelType: "telegram",
		ChannelUserID: "tg-recon", Status: domain.SubActive, Tier: "gold",
		CreatedAt: time.Now(), LastVerifiedAt: time.Now(), ExpiresAt: time.Now().Add(48 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	// Member moves delegation away → no longer eligible.
	e.chain.Put(&chain.Snapshot{StakeCredentialHash: w.sch, Epoch: 481, DelegatedPoolID: "pool1other", ActiveStakeLovelace: "5000000"})

	rec := reconciliation.New(e.st, e.oauth, e.chain, testPool)
	res, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if res.Expired != 1 {
		t.Fatalf("reconcile result = %+v, want Expired=1", res)
	}
	if sess, _ := e.st.Subscriptions().GetByChannelUser(ctx, testPool, "telegram", "tg-recon"); sess.Status != domain.SubExpired {
		t.Fatalf("session status = %s, want expired", sess.Status)
	}
}

func (e *env) jwks(t *testing.T) []any {
	t.Helper()
	resp, err := e.client.Get(e.srv.URL + "/.well-known/ouropass/jwks.json")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var doc struct {
		Keys []any `json:"keys"`
	}
	json.NewDecoder(resp.Body).Decode(&doc)
	return doc.Keys
}

// seedOwner inserts an owner AdminUser whose key-hash is the owner wallet's
// stake-credential hash (admin owner keys are blake2b224(vkey), same derivation
// as sch). Login then resolves to this owner without touching the signing keys.
func (e *env) seedOwner(owner wallet) {
	e.t.Helper()
	if err := e.st.AdminUsers().Upsert(context.Background(), domain.AdminUser{
		AdminID: crypto.RandomID(), PoolID: testPool, OwnerKeyHash: owner.sch,
		Role: domain.RoleOwner, CreatedAt: time.Now(),
	}); err != nil {
		e.t.Fatal(err)
	}
}

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (e *env) adminLogin(owner wallet) {
	e.t.Helper()
	st, body := e.post("/api/admin/auth/challenge", `{"owner_stake_address":"`+owner.rewardAddr+`"}`)
	if st != 200 {
		e.t.Fatalf("admin challenge = %d", st)
	}
	nonce, _ := body["nonce"].(string)
	if st, _ := e.post("/api/admin/auth/verify",
		`{"cose_key":"`+owner.coseKey+`","nonce":"`+nonce+`","signature":"`+cose(owner.priv, nonce)+`"}`); st != 200 {
		e.t.Fatalf("admin verify = %d", st)
	}
}

func ptr[T any](v T) *T { return &v }
