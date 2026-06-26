package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/membership"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi/respond"
	"ouro-pass/server/internal/utils/chain"
	"ouro-pass/server/internal/utils/crypto"
	"ouro-pass/server/internal/worker/telegram"
)

// mountAdminResources wires the admin resource endpoints with the §9.8 role
// matrix. All routes are already behind requireSession.
func (h *apiHandlers) mountAdminResources(r chi.Router) {
	viewer := func(fn http.HandlerFunc) http.Handler { return h.requireRole(domain.RoleViewer)(fn) }
	operator := func(fn http.HandlerFunc) http.Handler { return h.requireRole(domain.RoleOperator)(fn) }
	owner := func(fn http.HandlerFunc) http.Handler { return h.requireRole(domain.RoleOwner)(fn) }

	r.Method(http.MethodGet, "/members", viewer(h.adminMembers))
	r.Method(http.MethodGet, "/delegators", viewer(h.adminDelegators))
	r.Method(http.MethodGet, "/pool", viewer(h.adminGetPool))
	r.Method(http.MethodPost, "/pool/tier-rules", operator(h.adminSetTierRules))
	r.Method(http.MethodPost, "/members/{sch}/revoke", operator(h.adminRevokeMember))
	r.Method(http.MethodGet, "/subscriptions", viewer(h.adminSubscriptions))
	r.Method(http.MethodPost, "/subscriptions/{id}/cancel", operator(h.adminCancelSub))
	r.Method(http.MethodGet, "/channels", viewer(h.adminListChannels))
	r.Method(http.MethodPost, "/channels/{type}/configure", operator(h.adminConfigureChannel))
	r.Method(http.MethodGet, "/push/jobs", operator(h.adminListPushJobs))
	r.Method(http.MethodPost, "/push/jobs", operator(h.adminCreatePushJob))
	r.Method(http.MethodGet, "/oauth-clients", owner(h.adminListClients))
	r.Method(http.MethodPost, "/oauth-clients", owner(h.adminRegisterClient))
	r.Method(http.MethodPost, "/oauth-clients/{client_id}/secret", owner(h.adminRegenerateClientSecret))
	r.Method(http.MethodPost, "/keys/issuer/generate", owner(h.adminRotateKey))
	r.Method(http.MethodPost, "/keys/issuer/rotate", owner(h.adminRotateKey))
	r.Method(http.MethodPost, "/keys/issuer/{kid}/retire", owner(h.adminRetireKey))
	r.Method(http.MethodGet, "/audit", owner(h.adminAudit))
}

// requireStepUp re-verifies a fresh owner signature for sensitive ops (§9.8).
// The body must carry {cose_key, step_up_nonce, step_up_signature}.
func (h *apiHandlers) requireStepUp(r *http.Request, stepUp struct{ CoseKey, Nonce, Signature string }) error {
	u := adminFromCtx(r.Context())
	if u == nil { // defensive: requireSession should always populate this
		return admin.ErrUnauthorized
	}
	return h.d.Admin.VerifyStepUp(r.Context(), stepUp.CoseKey, stepUp.Nonce, stepUp.Signature, u.OwnerKeyHash)
}

func (h *apiHandlers) audit(r *http.Request, action, target string) {
	u := adminFromCtx(r.Context())
	_ = h.d.Store.Audit().Append(r.Context(), domain.AuditLog{
		AuditID: crypto.RandomID(), Actor: u.AdminID, Action: action, Target: target,
		IP: ptrIfSet(h.clientIP(r)), CreatedAt: time.Now(),
	})
}

// ---- members & subscriptions ----

func (h *apiHandlers) adminMembers(w http.ResponseWriter, r *http.Request) {
	sessions, err := h.d.Store.Subscriptions().ListActive(r.Context(), h.d.PoolID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	// The admin roster addresses members by their on-chain stake credential hash
	// (a public derived value the SPO can already see on-chain). The pseudonymous
	// token `sub` is only for external token consumers (decision D10).
	type member struct {
		StakeCredentialHash string `json:"stake_credential_hash"`
		Tier                string `json:"tier"`
		Channel             string `json:"channel_type"`
	}
	seen := map[string]bool{}
	out := []member{}
	for _, s := range sessions {
		if seen[s.StakeCredentialHash] {
			continue
		}
		seen[s.StakeCredentialHash] = true
		out = append(out, member{StakeCredentialHash: s.StakeCredentialHash, Tier: s.Tier, Channel: s.ChannelType})
	}
	respond.JSON(w, http.StatusOK, map[string]any{"members": out})
}

// adminGetPool returns the served pool's config, including the first-party tier
// mapping (S0004 §2.6). Defaults to an empty tier_rules array when no PoolConfig
// row exists yet.
func (h *apiHandlers) adminGetPool(w http.ResponseWriter, r *http.Request) {
	pc, err := h.d.Store.PoolConfig().Get(r.Context(), h.d.PoolID)
	if errors.Is(err, domain.ErrNotFound) {
		respond.JSON(w, http.StatusOK, map[string]any{
			"pool_id": h.d.PoolID, "network": h.d.Network, "tier_rules": json.RawMessage("[]"),
		})
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	tr := pc.TierRules
	if len(tr) == 0 {
		tr = json.RawMessage("[]")
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"pool_id": pc.PoolID, "network": pc.Network, "ticker": pc.Ticker, "tier_rules": tr,
	})
}

// adminSetTierRules replaces the pool's first-party tier mapping (operator). The
// rules are validated (shape, states, integer thresholds) before persisting; the
// PoolConfig row is created on first set. Editing tier_rules is the only way to
// configure first-party tiers — external RPs use the raw token facts (S0004 §2.6).
func (h *apiHandlers) adminSetTierRules(w http.ResponseWriter, r *http.Request) {
	var body struct {
		TierRules json.RawMessage `json:"tier_rules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.TierRules) == 0 {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "body must be {\"tier_rules\": [...]}")
		return
	}
	if err := membership.ValidateTierRules(body.TierRules); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	now := time.Now()
	pc, err := h.d.Store.PoolConfig().Get(r.Context(), h.d.PoolID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		pc = &domain.PoolConfig{PoolID: h.d.PoolID, Network: h.d.Network, CreatedAt: now}
	case err != nil:
		serverError(w, r, err)
		return
	}
	pc.TierRules = body.TierRules
	pc.UpdatedAt = now
	if err := h.d.Store.PoolConfig().Upsert(r.Context(), *pc); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "pool.tier_rules_set", h.d.PoolID)
	respond.JSON(w, http.StatusOK, map[string]any{"pool_id": h.d.PoolID, "tier_rules": pc.TierRules})
}

// adminDelegators enumerates the pool's full on-chain delegator set, one page at
// a time (S0004 §2.7, track C): `GET /api/admin/delegators?page=N`. This is the
// full delegating set (a superset of `members`, who are active subscribers). It
// is a cold, read-only roster query served directly from the chain source (no
// cache), and degrades to 501 when the configured source cannot enumerate.
func (h *apiHandlers) adminDelegators(w http.ResponseWriter, r *http.Request) {
	lister, ok := h.d.Chain.(chain.DelegatorLister)
	if h.d.Chain == nil || !ok {
		notImplemented(w, r)
		return
	}
	page := 0
	if p := r.URL.Query().Get("page"); p != "" {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			page = n
		}
	}
	hashes, err := lister.Delegators(r.Context(), h.d.PoolID, page)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if hashes == nil {
		hashes = []string{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"delegators": hashes, "page": page})
}

// adminRevokeMember blacklists a member by stake credential hash and immediately
// revokes their active tokens, refresh grants, and subscriptions (§9.8, D10).
func (h *apiHandlers) adminRevokeMember(w http.ResponseWriter, r *http.Request) {
	sch := chi.URLParam(r, "sch")
	// Member revoke cascades token/grant/session revocation — a high blast-radius
	// op, so it requires a fresh step-up signature like key rotation (p12-11/D19).
	var su struct {
		CoseKey         string `json:"cose_key"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	_ = json.NewDecoder(r.Body).Decode(&su)
	if err := h.requireStepUp(r, struct{ CoseKey, Nonce, Signature string }{su.CoseKey, su.StepUpNonce, su.StepUpSignature}); err != nil {
		writeAdminErr(w, err)
		return
	}
	now := time.Now()
	// Blacklist gates future authorize/refresh/activation (evaluate() keys on sch).
	if err := h.d.Store.Blacklist().Add(r.Context(), domain.Blacklist{
		StakeCredentialHash: sch, Reason: ptrStr("admin revoke"), CreatedAt: now,
	}); err != nil {
		serverError(w, r, err)
		return
	}
	// Cascade-revoke so existing credentials stop working immediately.
	tokens, err := h.d.Store.IssuedTokens().RevokeByStakeCredential(r.Context(), sch, now)
	if err != nil {
		serverError(w, r, err)
		return
	}
	grants, err := h.d.Store.RefreshGrants().RevokeByStakeCredential(r.Context(), sch)
	if err != nil {
		serverError(w, r, err)
		return
	}
	subs, err := h.d.Store.Subscriptions().CancelByStakeCredential(r.Context(), sch)
	if err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "member.revoke", sch)
	respond.JSON(w, http.StatusOK, map[string]any{
		"revoked": true, "tokens_revoked": tokens, "grants_revoked": grants, "sessions_cancelled": subs,
	})
}

func (h *apiHandlers) adminSubscriptions(w http.ResponseWriter, r *http.Request) {
	subs, err := h.d.Store.Subscriptions().ListActive(r.Context(), h.d.PoolID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"subscriptions": subs})
}

func (h *apiHandlers) adminCancelSub(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := h.d.Store.Subscriptions().SetStatus(r.Context(), id, domain.SubCancelled); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "subscription.cancel", id)
	respond.JSON(w, http.StatusOK, map[string]bool{"cancelled": true})
}

// ---- rules ----

// ---- channels ----

// adminListChannels reports which delivery channels are configured (no secrets):
// the Channels/Setup UI uses it to show "configured" state.
func (h *apiHandlers) adminListChannels(w http.ResponseWriter, r *http.Request) {
	_, err := h.d.Store.Channels().GetByType(r.Context(), "telegram")
	telegramConfigured := err == nil
	if err != nil && !errors.Is(err, domain.ErrNotFound) {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"channels": []map[string]any{
			{"channel_type": "telegram", "configured": telegramConfigured},
		},
	})
}

func (h *apiHandlers) adminConfigureChannel(w http.ResponseWriter, r *http.Request) {
	channelType := chi.URLParam(r, "type")
	if !domain.ValidChannelType(channelType) {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "unsupported channel_type")
		return
	}
	var body struct {
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "config required")
		return
	}
	// Telegram: the bot token is a secret — encrypt it at rest (field cipher) and
	// store only the ciphertext, never plaintext (S0004 p8-1).
	if channelType == "telegram" {
		if h.d.Cipher == nil {
			notImplemented(w, r)
			return
		}
		var in struct {
			BotToken string `json:"bot_token"`
		}
		if err := json.Unmarshal(body.Config, &in); err != nil || in.BotToken == "" {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "bot_token required")
			return
		}
		enc, err := telegram.EncodeToken(h.d.Cipher, in.BotToken)
		if err != nil {
			serverError(w, r, err)
			return
		}
		body.Config = enc
	}
	now := time.Now()
	id := crypto.RandomID()
	if err := h.d.Store.Channels().Upsert(r.Context(), domain.ChannelConfig{
		ChannelID: id, PoolID: h.d.PoolID, ChannelType: channelType, Config: body.Config,
		Status: "active", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "channel.configure", channelType)
	respond.JSON(w, http.StatusOK, map[string]string{"channel_id": id})
}

// ---- push jobs ----

func (h *apiHandlers) adminListPushJobs(w http.ResponseWriter, r *http.Request) {
	jobs, err := h.d.Store.PushJobs().ListByPool(r.Context(), h.d.PoolID, 100)
	if err != nil {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"jobs": jobs})
}

func (h *apiHandlers) adminCreatePushJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Title       string `json:"title"`
		Content     string `json:"content"`
		ChannelType string `json:"channel_type"`
		Target      struct {
			Tier        string `json:"tier"`
			Topic       string `json:"topic"`
			Entitlement string `json:"entitlement"`
		} `json:"target"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Title == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "title and channel_type required")
		return
	}
	if !domain.ValidChannelType(body.ChannelType) {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "unsupported channel_type")
		return
	}
	u := adminFromCtx(r.Context())
	id := crypto.RandomID()
	if err := h.d.Store.PushJobs().Create(r.Context(), domain.PushJob{
		JobID: id, PoolID: h.d.PoolID, Title: body.Title, Content: body.Content, ChannelType: body.ChannelType,
		TargetTier: ptrIfSet(body.Target.Tier), TargetTopic: ptrIfSet(body.Target.Topic),
		RequiredEntitlement: ptrIfSet(body.Target.Entitlement), Status: domain.PushScheduled,
		CreatedBy: u.AdminID, CreatedAt: time.Now(),
	}); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "push.create", id)
	respond.JSON(w, http.StatusOK, map[string]string{"job_id": id, "status": string(domain.PushScheduled)})
}

// ---- oauth clients ----

func (h *apiHandlers) adminListClients(w http.ResponseWriter, r *http.Request) {
	clients, err := h.d.Store.OAuthClients().List(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	for i := range clients {
		clients[i].ClientSecretHash = nil // never expose secret hashes
	}
	respond.JSON(w, http.StatusOK, map[string]any{"clients": clients})
}

func (h *apiHandlers) adminRegisterClient(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name         string   `json:"name"`
		ClientType   string   `json:"client_type"`
		RedirectURIs []string `json:"redirect_uris"`
		Audiences    []string `json:"allowed_audiences"`
		// Registering a client issues credentials → require a fresh step-up
		// signature like key rotation (p12-11/D19).
		CoseKey         string `json:"cose_key"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	// The client_id is system-generated (like the secret) — opaque and unique, so
	// callers can't pick a guessable or colliding id; `name` is the human label.
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "name required")
		return
	}
	if ct := domain.ClientType(body.ClientType); !ct.Valid() {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "client_type must be public or confidential")
		return
	}
	// Step-up gates the actual registration (after request-shape validation).
	if err := h.requireStepUp(r, struct{ CoseKey, Nonce, Signature string }{body.CoseKey, body.StepUpNonce, body.StepUpSignature}); err != nil {
		writeAdminErr(w, err)
		return
	}
	clientID := "op-client-" + crypto.RandomToken(9)
	c := domain.OAuthClient{
		ClientID: clientID, Name: body.Name, ClientType: domain.ClientType(body.ClientType),
		RedirectURIs:     body.RedirectURIs,
		AllowedAudiences: body.Audiences,
		Status:           "active", CreatedAt: time.Now(),
	}
	// Confidential clients get a one-time secret (returned once, stored hashed).
	var plainSecret string
	if c.ClientType == domain.ClientConfidential {
		plainSecret = crypto.RandomToken(32)
		hash := crypto.HashToken(plainSecret)
		c.ClientSecretHash = &hash
	}
	if err := h.d.Store.OAuthClients().Upsert(r.Context(), c); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "oauth_client.register", clientID)
	resp := map[string]any{"client_id": clientID}
	if plainSecret != "" {
		resp["client_secret"] = plainSecret // shown once
	}
	respond.JSON(w, http.StatusOK, resp)
}

// adminRegenerateClientSecret issues a fresh client_secret for a confidential
// client (owner + step-up), invalidating the previous one. Secrets are stored
// hashed and never recoverable, so the new plaintext is returned exactly once.
func (h *apiHandlers) adminRegenerateClientSecret(w http.ResponseWriter, r *http.Request) {
	clientID := chi.URLParam(r, "client_id")
	var body struct {
		CoseKey         string `json:"cose_key"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.requireStepUp(r, struct{ CoseKey, Nonce, Signature string }{body.CoseKey, body.StepUpNonce, body.StepUpSignature}); err != nil {
		writeAdminErr(w, err)
		return
	}
	c, err := h.d.Store.OAuthClients().Get(r.Context(), clientID)
	switch {
	case errors.Is(err, domain.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "not_found", "no such client")
		return
	case err != nil:
		serverError(w, r, err)
		return
	case c.ClientType != domain.ClientConfidential:
		respond.Error(w, http.StatusConflict, "invalid_state", "public clients have no secret")
		return
	}
	plainSecret := crypto.RandomToken(32)
	hash := crypto.HashToken(plainSecret)
	c.ClientSecretHash = &hash
	if err := h.d.Store.OAuthClients().Upsert(r.Context(), *c); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "oauth_client.secret_regenerate", clientID)
	respond.JSON(w, http.StatusOK, map[string]any{"client_id": clientID, "client_secret": plainSecret})
}

// ---- signing keys (owner + step-up) ----

func (h *apiHandlers) adminRotateKey(w http.ResponseWriter, r *http.Request) {
	if h.d.Keys == nil {
		notImplemented(w, r)
		return
	}
	var body struct {
		CoseKey         string `json:"cose_key"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.requireStepUp(r, struct{ CoseKey, Nonce, Signature string }{body.CoseKey, body.StepUpNonce, body.StepUpSignature}); err != nil {
		writeAdminErr(w, err)
		return
	}
	kid, err := h.d.Keys.Rotate(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "issuer_key.rotate", kid)
	respond.JSON(w, http.StatusOK, map[string]any{"new_kid": kid, "status": "active", "jwks_updated": true})
}

// adminRetireKey removes a single rotating key from the JWKS overlap set once
// the owner judges its short-lived tokens have all expired (manual cleanup;
// owner + step-up). Only rotating keys are eligible — the active signing key
// cannot be retired.
func (h *apiHandlers) adminRetireKey(w http.ResponseWriter, r *http.Request) {
	if h.d.Keys == nil {
		notImplemented(w, r)
		return
	}
	kid := chi.URLParam(r, "kid")
	var body struct {
		CoseKey         string `json:"cose_key"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.requireStepUp(r, struct{ CoseKey, Nonce, Signature string }{body.CoseKey, body.StepUpNonce, body.StepUpSignature}); err != nil {
		writeAdminErr(w, err)
		return
	}
	switch err := h.d.Keys.Retire(r.Context(), kid); {
	case errors.Is(err, domain.ErrNotFound):
		respond.Error(w, http.StatusNotFound, "not_found", "no such signing key")
		return
	case errors.Is(err, keys.ErrNotRotating):
		respond.Error(w, http.StatusConflict, "invalid_state", "only rotating keys can be retired")
		return
	case err != nil:
		serverError(w, r, err)
		return
	}
	h.audit(r, "issuer_key.retire", kid)
	respond.JSON(w, http.StatusOK, map[string]any{"kid": kid, "status": "retired"})
}

// ---- audit ----

func (h *apiHandlers) adminAudit(w http.ResponseWriter, r *http.Request) {
	entries, err := h.d.Store.Audit().Recent(r.Context(), 200)
	if err != nil {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"audit": entries})
}

func ptrIfSet(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func ptrStr(s string) *string { return &s }
