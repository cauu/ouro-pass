package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/core/keys"
	"ouro-pass/server/internal/core/tier"
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
	r.Method(http.MethodGet, "/attestors", viewer(h.adminListAttestors))
	r.Method(http.MethodPost, "/attestors", operator(h.adminCreateAttestor))
	r.Method(http.MethodPost, "/attestors/{id}", operator(h.adminUpdateAttestor))
	r.Method(http.MethodDelete, "/attestors/{id}", operator(h.adminDeleteAttestor))
	r.Method(http.MethodPost, "/members/{sch}/revoke", operator(h.adminRevokeMember))
	r.Method(http.MethodGet, "/subscriptions", viewer(h.adminSubscriptions))
	r.Method(http.MethodPost, "/subscriptions/{id}/cancel", operator(h.adminCancelSub))
	r.Method(http.MethodGet, "/channels", viewer(h.adminListChannels))
	r.Method(http.MethodPost, "/channels", operator(h.adminCreateChannel))
	r.Method(http.MethodPost, "/channels/{id}", operator(h.adminUpdateChannel))
	r.Method(http.MethodPost, "/channels/{id}/enable", operator(h.adminEnableChannel))
	r.Method(http.MethodPost, "/channels/{id}/disable", operator(h.adminDisableChannel))
	r.Method(http.MethodDelete, "/channels/{id}", operator(h.adminDeleteChannel))
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
	// S0006: tier_rules are issuer-global (the boolean DSL over aggregate facts),
	// no longer per-pool. GetTierRules returns "[]" when unset.
	tr, err := h.d.Store.Issuer().GetTierRules(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{
		"pool_id": h.d.PoolID, "network": h.d.Network, "tier_rules": tr,
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
	if err := tier.Validate(body.TierRules); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	// S0006: persist to the issuer-global singleton (was PoolConfig.tier_rules).
	if err := h.d.Store.Issuer().SetTierRules(r.Context(), body.TierRules, time.Now()); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "pool.tier_rules_set", h.d.PoolID)
	respond.JSON(w, http.StatusOK, map[string]any{"pool_id": h.d.PoolID, "tier_rules": body.TierRules})
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
	// S0006: the served pool is configured as a pool_stake attestor, not an env var.
	// Resolve the deployment's primary pool for the roster (multi-pool selection by
	// query param is future admin work).
	poolID := h.primaryPoolID(r.Context())
	if poolID == "" {
		respond.Error(w, http.StatusNotFound, "no_pool", "no active pool_stake attestor configured")
		return
	}
	hashes, err := lister.Delegators(r.Context(), poolID, page)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if hashes == nil {
		hashes = []string{}
	}
	respond.JSON(w, http.StatusOK, map[string]any{"delegators": hashes, "page": page})
}

// primaryPoolID resolves the deployment's primary stake pool — the pool_id of the
// first active pool_stake attestor — for cold roster queries (delegators). Returns
// "" when no pool_stake attestor is configured.
func (h *apiHandlers) primaryPoolID(ctx context.Context) string {
	cfgs, err := h.d.Store.Attestors().ListActive(ctx)
	if err != nil {
		return ""
	}
	for _, c := range cfgs {
		if c.Kind != attestor.KindPoolStake {
			continue
		}
		var p attestor.PoolStakeParams
		if json.Unmarshal(c.Params, &p) == nil && p.PoolID != "" {
			return p.PoolID
		}
	}
	return ""
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

// ---- channels (S0005 p4-1: per-instance CRUD) ----

// channelView is the non-secret instance projection the Channels/Setup UI reads.
func channelView(c domain.ChannelConfig) map[string]any {
	v := map[string]any{
		"channel_id": c.ChannelID, "channel_type": c.ChannelType, "name": c.Name,
		"status": c.Status, "configured": c.Status == "active",
	}
	if c.ChannelType == "telegram" {
		v["bot_username"] = telegram.DecodeUsername(c.Config) // public, never the token
	}
	return v
}

// adminListChannels lists all of the pool's channel instances (no secrets).
func (h *apiHandlers) adminListChannels(w http.ResponseWriter, r *http.Request) {
	insts, err := h.d.Store.Channels().List(r.Context(), h.d.PoolID)
	if err != nil {
		serverError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(insts))
	for _, c := range insts {
		out = append(out, channelView(c))
	}
	respond.JSON(w, http.StatusOK, map[string]any{"channels": out})
}

// adminCreateChannel creates a new channel instance. Telegram bot tokens are
// encrypted at rest (field cipher); the public bot username is stored in clear
// for deep links. A duplicate name within (pool, type) returns 409.
func (h *apiHandlers) adminCreateChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ChannelType string          `json:"channel_type"`
		Name        string          `json:"name"`
		Config      json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Name == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "channel_type and name required")
		return
	}
	if !domain.ValidChannelType(body.ChannelType) {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "unsupported channel_type")
		return
	}
	config, ok := h.encodeChannelConfig(w, r, body.ChannelType, body.Config, nil)
	if !ok {
		return
	}
	now := time.Now()
	id := crypto.RandomID()
	err := h.d.Store.Channels().Create(r.Context(), domain.ChannelConfig{
		ChannelID: id, PoolID: h.d.PoolID, ChannelType: body.ChannelType, Name: body.Name,
		Config: config, Status: "active", CreatedAt: now, UpdatedAt: now,
	})
	if errors.Is(err, domain.ErrConflict) {
		respond.Error(w, http.StatusConflict, "conflict", "an instance with that name already exists for this channel type")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "channel.create", id)
	respond.JSON(w, http.StatusOK, map[string]string{"channel_id": id})
}

// adminUpdateChannel updates an instance's name, status, and/or secret config by
// id. A telegram config with only a bot_username preserves the existing token.
func (h *apiHandlers) adminUpdateChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	inst, err := h.d.Store.Channels().Get(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not_found", "no such channel instance")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	var body struct {
		Name   *string         `json:"name"`
		Status *string         `json:"status"`
		Config json.RawMessage `json:"config"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed JSON body")
		return
	}
	if body.Name != nil && *body.Name != "" {
		inst.Name = *body.Name
	}
	if body.Status != nil {
		if *body.Status != "active" && *body.Status != "disabled" {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "status must be active or disabled")
			return
		}
		inst.Status = *body.Status
	}
	if len(body.Config) > 0 {
		config, ok := h.encodeChannelConfig(w, r, inst.ChannelType, body.Config, inst.Config)
		if !ok {
			return
		}
		inst.Config = config
	}
	inst.UpdatedAt = time.Now()
	if err := h.d.Store.Channels().Upsert(r.Context(), *inst); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "channel.update", id)
	respond.JSON(w, http.StatusOK, channelView(*inst))
}

func (h *apiHandlers) adminEnableChannel(w http.ResponseWriter, r *http.Request)  { h.setChannelStatus(w, r, "active") }
func (h *apiHandlers) adminDisableChannel(w http.ResponseWriter, r *http.Request) { h.setChannelStatus(w, r, "disabled") }

func (h *apiHandlers) setChannelStatus(w http.ResponseWriter, r *http.Request, status string) {
	id := chi.URLParam(r, "id")
	if _, err := h.d.Store.Channels().Get(r.Context(), id); errors.Is(err, domain.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not_found", "no such channel instance")
		return
	} else if err != nil {
		serverError(w, r, err)
		return
	}
	if err := h.d.Store.Channels().SetStatus(r.Context(), id, status, time.Now()); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "channel."+map[string]string{"active": "enable", "disabled": "disable"}[status], id)
	respond.JSON(w, http.StatusOK, map[string]string{"channel_id": id, "status": status})
}

// adminDeleteChannel removes an instance and cascades by cancelling its active
// subscriptions (D7), then audits the action with the affected count.
func (h *apiHandlers) adminDeleteChannel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if _, err := h.d.Store.Channels().Get(r.Context(), id); errors.Is(err, domain.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not_found", "no such channel instance")
		return
	} else if err != nil {
		serverError(w, r, err)
		return
	}
	cancelled, err := h.d.Store.Subscriptions().CancelByChannelID(r.Context(), id)
	if err != nil {
		serverError(w, r, err)
		return
	}
	if err := h.d.Store.Channels().Delete(r.Context(), id); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "channel.delete", id)
	respond.JSON(w, http.StatusOK, map[string]any{"deleted": true, "sessions_cancelled": cancelled})
}

// encodeChannelConfig validates and encrypts an inbound channel config. For
// telegram it encrypts the bot token (required on create; on update a token-less
// body keeps the prior token) and stores the public bot_username in clear. For
// other types the raw config passes through. prior is the existing stored config
// on update (nil on create). Returns (config, ok); on failure it has written the
// error response and ok is false.
func (h *apiHandlers) encodeChannelConfig(w http.ResponseWriter, r *http.Request, channelType string, in, prior json.RawMessage) (json.RawMessage, bool) {
	if channelType != "telegram" {
		return in, true
	}
	if h.d.Cipher == nil {
		notImplemented(w, r)
		return nil, false
	}
	var cfg struct {
		BotToken    string `json:"bot_token"`
		BotUsername string `json:"bot_username"`
	}
	if len(in) > 0 {
		if err := json.Unmarshal(in, &cfg); err != nil {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "malformed config")
			return nil, false
		}
	}
	// Username defaults to the prior stored value when not supplied.
	username := cfg.BotUsername
	if username == "" && prior != nil {
		username = telegram.DecodeUsername(prior)
	}
	token := cfg.BotToken
	if token == "" {
		// No new token: on update, preserve the existing one (e.g. username-only
		// edit); on create, the token is required.
		if prior == nil {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "bot_token required")
			return nil, false
		}
		existing, err := telegram.DecodeToken(h.d.Cipher, prior)
		if err != nil || existing == "" {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "bot_token required")
			return nil, false
		}
		token = existing
	}
	enc, err := telegram.EncodeConfig(h.d.Cipher, token, username)
	if err != nil {
		serverError(w, r, err)
		return nil, false
	}
	return enc, true
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
		ChannelID   string `json:"channel_id"`
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
	// A channel-scoped job (S0005 p3-1) must target an existing active instance of
	// the declared type, so delivery routes to the right bot.
	if body.ChannelID != "" {
		inst, err := h.d.Store.Channels().Get(r.Context(), body.ChannelID)
		if err != nil || inst.Status != "active" || inst.ChannelType != body.ChannelType {
			respond.Error(w, http.StatusBadRequest, "invalid_request", "unknown or inactive channel instance for channel_type")
			return
		}
	}
	u := adminFromCtx(r.Context())
	id := crypto.RandomID()
	if err := h.d.Store.PushJobs().Create(r.Context(), domain.PushJob{
		JobID: id, PoolID: h.d.PoolID, Title: body.Title, Content: body.Content,
		ChannelID: ptrIfSet(body.ChannelID), ChannelType: body.ChannelType,
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
