// Command devflow is a narrated, end-to-end driver for the auth (OAuth) and
// subscription (activation → Telegram bot → subscription → push) flows. It runs
// the REAL issuer stack (httpapi.NewRouter + the live services) over an in-process
// HTTP server, with a synthetic CIP-30 wallet and a mock chain, so the whole
// happy path can be exercised and watched without a browser, a real Cardano
// wallet, a real Telegram bot, or a real chain.
//
// Why in-process and not against `make dev`'s :8080: the activation step needs a
// Telegram `/start` (no real bot here) and SQLite is single-writer, so a second
// process can't cleanly share `make dev`'s DB. This harness hosts the same router
// `make dev` serves, so it exercises identical code. Point --db at the dev DB
// (with `make dev` stopped) to make the seeded instance + subscription visible in
// `make dev` afterwards.
//
// Usage:
//
//	go run ./cmd/devflow                       # ephemeral temp DB
//	go run ./cmd/devflow --db file:.dev/ouro.db  # seed the dev DB (stop `make dev` first)
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fxamacker/cbor/v2"

	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/oauth"
	"ouro-pass/server/internal/core/walletauth"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi"
	"ouro-pass/server/internal/store"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/worker/push"
	"ouro-pass/server/internal/worker/telegram"
)

const (
	pool      = "pool1devflowxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"
	channelID = "ch-devflow-members"
	clientID  = "spa-devflow"
)

// scope is the first-party namespace (PoolID) every seeded row is tagged with. It
// MUST equal the issuer scope of whatever server will read the data back. `make
// dev` derives scope from DEV_ISSUER, so the default matches it — otherwise the
// admin pages (which filter by PoolID) would never show the seeded rows.
var scope = "http://localhost:8080"

func main() {
	dbFlag := flag.String("db", "", "SQLite DSN (default: an ephemeral temp file)")
	network := flag.String("network", "preview", "network label for the attestor")
	scopeFlag := flag.String("scope", scope, "first-party scope/PoolID; must match the reading server's OUROPASS_ISSUER")
	flag.Parse()
	scope = *scopeFlag

	if err := run(*dbFlag, *network); err != nil {
		fmt.Fprintln(os.Stderr, "devflow error:", err)
		os.Exit(1)
	}
}

func run(dsn, network string) error {
	ctx := context.Background()
	if dsn == "" {
		dsn = "file:" + filepath.Join(os.TempDir(), fmt.Sprintf("devflow-%d.db", time.Now().UnixNano()))
	}
	fmt.Printf("DB: %s\n", dsn)
	fmt.Printf("scope (PoolID): %s  (must match the reading server's OUROPASS_ISSUER)\n", scope)

	st, err := store.Open(ctx, store.SQLite, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return err
	}

	// Field cipher: the fixed dev key, so a telegram instance seeded here decrypts
	// under `make dev` too (DEV_FIELD_KEY in server/Makefile).
	cipher, err := crypto.NewFieldCipherHex("0000000000000000000000000000000000000000000000000000000000000001")
	if err != nil {
		return err
	}
	ks := keys.New(st, cipher)
	if _, err := ks.Rotate(ctx); err != nil { // bootstrap a signing key
		return err
	}
	wallet := walletauth.New(st, time.Minute)

	// Mock chain (no real network). The attestor set is DB-driven, exactly like main.
	mock := chain.NewMockSource(480)
	srcFor := func(string) (chain.Source, error) { return mock, nil }
	reg := attestor.DefaultRegistry()
	attestorsFor := func(ctx context.Context) (*attestor.Set, error) {
		cfgs, err := st.Attestors().ListActive(ctx)
		if err != nil {
			return nil, err
		}
		return attestor.BuildSet(cfgs, reg, srcFor)
	}

	oas := oauth.New(oauth.Config{
		Store: st, Wallet: wallet, Keys: ks, Attestors: attestorsFor,
		Issuer: scope, ServerSalt: []byte("devflow-salt"), AccessTTL: time.Hour, RefreshTTL: 24 * time.Hour,
	})
	adm := admin.New(admin.Config{Wallet: wallet, Store: st, PoolID: scope})
	deps := httpapi.Deps{
		Wallet: wallet, Keys: ks, OAuth: oas, Admin: adm, Store: st, Chain: mock, Cipher: cipher,
		PoolID: scope, TelegramBot: "DevFlowBot", SecureCookies: false,
	}
	srv := httptest.NewServer(httpapi.NewRouter(deps))
	defer srv.Close()
	cl := srv.Client()
	cl.Jar, _ = cookiejar.New(nil)
	h := &harness{srv: srv, cl: cl}

	// ---- seed prerequisites (attestor, tier rules, telegram instance, client) ----
	section("0. Seed prerequisites")
	params, _ := json.Marshal(attestor.PoolStakeParams{PoolID: pool, Network: network})
	if err := st.Attestors().Create(ctx, domain.AttestorConfig{
		AttestorID: "att-devflow", Kind: attestor.KindPoolStake, Label: "devflow", Params: params,
		Status: domain.AttestorActive, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		return err
	}
	fmt.Printf("  • attestor pool_stake → pool=%s network=%s\n", pool, network)
	_ = st.Issuer().SetTierRules(ctx, json.RawMessage(
		`[{"tier":"gold","when":{"fact":"total_active_stake","op":">=","value":"1000000"}}]`), time.Now())
	fmt.Println("  • tier_rules → gold when total_active_stake >= 1000000 (1 ADA)")

	tgCfg, _ := telegram.EncodeConfig(cipher, "123456:DEVFLOW-FAKE-TOKEN", "DevFlowBot")
	if err := st.Channels().Create(ctx, domain.ChannelConfig{
		ChannelID: channelID, PoolID: scope, ChannelType: "telegram", Name: "members",
		Config: tgCfg, Status: "active", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		return err
	}
	fmt.Printf("  • telegram instance %q (name=members, bot=@DevFlowBot)\n", channelID)
	if err := st.OAuthClients().Upsert(ctx, domain.OAuthClient{
		ClientID: clientID, Name: "Devflow SPA", ClientType: domain.ClientPublic,
		RedirectURIs: []string{"https://devflow/cb"}, AllowedAudiences: []string{"app:devflow"},
		Status: "active", CreatedAt: time.Now(),
	}); err != nil {
		return err
	}
	fmt.Printf("  • oauth client %q (public, PKCE)\n", clientID)

	// Synthetic wallet, made eligible (active member in the attestor's pool).
	w := newWallet()
	mock.Put(&chain.Snapshot{
		StakeCredentialHash: w.sch, Epoch: 480, DelegatedPoolID: pool, ActiveStakePoolID: pool,
		ActiveStakeLovelace: "5000000", EpochsDelegated: 5, AccountStatus: "registered",
	})
	fmt.Printf("  • wallet sch=%s… made ACTIVE (5 ADA active stake in pool)\n", w.sch[:16])

	// ---- AUTH FLOW ----
	if err := h.authFlow(w); err != nil {
		return err
	}
	// ---- SUBSCRIPTION FLOW ----
	if err := h.subscriptionFlow(ctx, st, oas, w); err != nil {
		return err
	}

	section("Done")
	fmt.Println("Both flows completed.")
	fmt.Println("To view in `make dev`: re-run with `--db file:.dev/ouro.db` (stop `make dev` first),")
	fmt.Println("then start `make dev` and open /admin → Channels / Subscriptions / Members.")
	fmt.Println("The seeded rows use scope =", scope, "— it must equal make dev's OUROPASS_ISSUER.")
	return nil
}

func (h *harness) authFlow(w wallet) error {
	section("1. Auth / OAuth flow (challenge → sign → authorize → token → introspect → refresh)")
	verifier := "devflow-pkce-code-verifier-0123456789abcdef"
	challenge := pkceS256(verifier)
	device := hex.EncodeToString([]byte("devflow-device-public-key-32byte"))

	// 1a. challenge (purpose=issue)
	_, body := h.post("/api/auth/challenge", `{"purpose":"issue","stake_address":"`+w.rewardAddr+`"}`)
	nonce, _ := body["nonce"].(string)
	fmt.Printf("  → POST /api/auth/challenge       → nonce=%s…\n", trunc(nonce, 12))

	// 1b. authorize → 302 ?code=
	status, loc := h.authorize(w, challenge, device)
	code := codeFrom(loc)
	fmt.Printf("  → POST /api/connect/authorize    → %d Location code=%s…\n", status, trunc(code, 12))
	if code == "" {
		return fmt.Errorf("authorize returned no code: %s", loc)
	}

	// 1c. token (authorization_code + PKCE + device)
	_, tok := h.post("/api/oauth/token",
		`{"grant_type":"authorization_code","code":"`+code+`","client_id":"`+clientID+
			`","code_verifier":"`+verifier+`","redirect_uri":"https://devflow/cb","device_pubkey":"`+device+`"}`)
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	if access == "" || refresh == "" {
		return fmt.Errorf("token response missing tokens: %v", tok)
	}
	fmt.Printf("  → POST /api/oauth/token          → access=%s… refresh=%s…\n", trunc(access, 16), trunc(refresh, 10))

	// 1d. introspect
	_, intro := h.post("/api/oauth/introspect", `{"token":"`+access+`"}`)
	fmt.Printf("  → POST /api/oauth/introspect     → active=%v tier=%v\n", intro["active"], intro["tier"])

	// 1e. refresh (rotates; public client requires device PoP)
	_, ref := h.post("/api/oauth/token",
		`{"grant_type":"refresh_token","refresh_token":"`+refresh+`","client_id":"`+clientID+`","device_pubkey":"`+device+`"}`)
	newRefresh, _ := ref["refresh_token"].(string)
	fmt.Printf("  → POST /api/oauth/token (refresh)→ rotated refresh=%s… (old now revoked-on-replay)\n", trunc(newRefresh, 10))
	if newRefresh == "" || newRefresh == refresh {
		return fmt.Errorf("refresh did not rotate")
	}
	return nil
}

func (h *harness) subscriptionFlow(ctx context.Context, st *store.Store, oas *oauth.Server, w wallet) error {
	section("2. Subscription flow (challenge → activation → bot /start → subscription → push)")

	// 2a. activation challenge + create (bound to the telegram instance)
	_, body := h.post("/api/auth/challenge", `{"purpose":"activation","stake_address":"`+w.rewardAddr+`"}`)
	nonce, _ := body["nonce"].(string)
	_, act := h.post("/api/activation/create",
		`{"channel_type":"telegram","channel_id":"`+channelID+`","nonce":"`+nonce+
			`","cose_key":"`+w.coseKey+`","signature":"`+cose(w.priv, nonce)+`"}`)
	code, _ := act["activation_code"].(string)
	deep, _ := act["deep_link"].(string)
	if code == "" {
		return fmt.Errorf("activation/create failed: %v", act)
	}
	fmt.Printf("  → POST /api/activation/create    → code=%s… deep_link=%s\n", trunc(code, 10), deep)

	// 2b. drive the REAL Telegram processor for this instance (the bot worker's unit)
	proc := telegram.NewInstanceProcessor(st, oas, scope, channelID)
	reply := proc.Handle(ctx, telegram.Update{UserID: "tg-1001", ChatID: "tg-1001", Text: "/start " + code})
	fmt.Printf("  → bot /start <code> (instance)   → %q\n", reply)

	sess, err := st.Subscriptions().GetByInstanceUser(ctx, channelID, "tg-1001")
	if err != nil {
		return fmt.Errorf("subscription not created: %w", err)
	}
	fmt.Printf("  ✓ subscription: channel_id=%s user=%s tier=%s status=%s\n",
		sess.ChannelID, sess.ChannelUserID, sess.Tier, sess.Status)

	// 2c. push a gold message to THIS instance → only its subscriber receives it
	rec := &capturing{}
	sched := push.NewScheduler(st, rec, push.Options{Route: func(domain.PushJob) (push.Sender, error) { return rec, nil }})
	cid := channelID
	job := domain.PushJob{
		JobID: crypto.RandomID(), PoolID: scope, Title: "Hello", Content: "members only",
		ChannelID: &cid, ChannelType: "telegram", TargetTier: strptr("gold"),
		Status: domain.PushScheduled, CreatedBy: "devflow", CreatedAt: time.Now(),
	}
	_ = st.PushJobs().Create(ctx, job)
	res, err := sched.Run(ctx, job)
	if err != nil {
		return err
	}
	fmt.Printf("  → push job (channel_id=%s, tier=gold) → sent=%d recipients=%v\n", channelID, res.Sent, rec.chats)
	return nil
}

// ---- HTTP harness ---------------------------------------------------------

type harness struct {
	srv *httptest.Server
	cl  *http.Client
}

func (h *harness) post(path, body string) (int, map[string]any) {
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.cl.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	raw, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(raw, &m)
	return resp.StatusCode, m
}

func (h *harness) authorize(w wallet, challenge, device string) (int, string) {
	_, body := h.post("/api/auth/challenge", `{"purpose":"issue","stake_address":"`+w.rewardAddr+`"}`)
	nonce, _ := body["nonce"].(string)
	req, _ := http.NewRequest(http.MethodPost, h.srv.URL+"/api/connect/authorize", strings.NewReader(
		`{"client_id":"`+clientID+`","redirect_uri":"https://devflow/cb","aud":"app:devflow","nonce":"`+nonce+
			`","cose_key":"`+w.coseKey+`","signature":"`+cose(w.priv, nonce)+`","code_challenge":"`+challenge+
			`","device_pubkey":"`+device+`"}`))
	req.Header.Set("Content-Type", "application/json")
	prev := h.cl.CheckRedirect
	h.cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer func() { h.cl.CheckRedirect = prev }()
	resp, err := h.cl.Do(req)
	if err != nil {
		panic(err)
	}
	resp.Body.Close()
	return resp.StatusCode, resp.Header.Get("Location")
}

// ---- synthetic CIP-30 wallet ----------------------------------------------

type wallet struct {
	priv       ed25519.PrivateKey
	vkey       string
	sch        string
	rewardAddr string
	coseKey    string
}

func newWallet() wallet {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	cred := crypto.Blake2b224(pub)
	ck, _ := cbor.Marshal(map[int]any{1: 1, -1: 6, -2: []byte(pub), 3: -8})
	return wallet{
		priv: priv, vkey: hex.EncodeToString(pub), sch: hex.EncodeToString(cred),
		rewardAddr: hex.EncodeToString(append([]byte{0xe1}, cred...)), coseKey: hex.EncodeToString(ck),
	}
}

func cose(priv ed25519.PrivateKey, nonce string) string {
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

func pkceS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// ---- misc helpers ---------------------------------------------------------

type capturing struct{ chats []string }

func (c *capturing) SendMessage(_ context.Context, chatID, _ string) error {
	c.chats = append(c.chats, chatID)
	return nil
}

func codeFrom(loc string) string {
	_, rest, ok := strings.Cut(loc, "code=")
	if !ok {
		return ""
	}
	if amp := strings.IndexByte(rest, '&'); amp >= 0 {
		rest = rest[:amp]
	}
	return rest
}

func strptr(s string) *string { return &s }

func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func section(title string) { fmt.Printf("\n=== %s ===\n", title) }
