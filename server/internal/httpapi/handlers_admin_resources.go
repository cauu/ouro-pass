package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"ouro-pass/server/internal/core/admin"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi/respond"
	"ouro-pass/server/internal/utils/crypto"
)

// mountAdminResources wires the admin resource endpoints with the §9.8 role
// matrix. All routes are already behind requireSession.
func (h *apiHandlers) mountAdminResources(r chi.Router) {
	viewer := func(fn http.HandlerFunc) http.Handler { return h.requireRole(domain.RoleViewer)(fn) }
	operator := func(fn http.HandlerFunc) http.Handler { return h.requireRole(domain.RoleOperator)(fn) }
	owner := func(fn http.HandlerFunc) http.Handler { return h.requireRole(domain.RoleOwner)(fn) }

	r.Method(http.MethodGet, "/members", viewer(h.adminMembers))
	r.Method(http.MethodPost, "/members/{sch}/revoke", operator(h.adminRevokeMember))
	r.Method(http.MethodGet, "/subscriptions", viewer(h.adminSubscriptions))
	r.Method(http.MethodPost, "/subscriptions/{id}/cancel", operator(h.adminCancelSub))
	r.Method(http.MethodGet, "/rules", operator(h.adminListRules))
	r.Method(http.MethodPost, "/rules", operator(h.adminUpsertRule))
	r.Method(http.MethodPost, "/channels/{type}/configure", operator(h.adminConfigureChannel))
	r.Method(http.MethodGet, "/push/jobs", operator(h.adminListPushJobs))
	r.Method(http.MethodPost, "/push/jobs", operator(h.adminCreatePushJob))
	r.Method(http.MethodGet, "/oauth-clients", owner(h.adminListClients))
	r.Method(http.MethodPost, "/oauth-clients", owner(h.adminRegisterClient))
	r.Method(http.MethodPost, "/keys/issuer/generate", owner(h.adminRotateKey))
	r.Method(http.MethodPost, "/keys/issuer/rotate", owner(h.adminRotateKey))
	r.Method(http.MethodGet, "/audit", owner(h.adminAudit))
}

// requireStepUp re-verifies a fresh owner signature for sensitive ops (§9.8).
// The body must carry {owner_vkey, step_up_nonce, step_up_signature}.
func (h *apiHandlers) requireStepUp(r *http.Request, stepUp struct{ OwnerVkey, Nonce, Signature string }) error {
	u := adminFromCtx(r.Context())
	if u == nil { // defensive: requireSession should always populate this
		return admin.ErrUnauthorized
	}
	return h.d.Admin.VerifyStepUp(r.Context(), stepUp.OwnerVkey, stepUp.Nonce, stepUp.Signature, u.OwnerKeyHash)
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

// adminRevokeMember blacklists a member by stake credential hash and immediately
// revokes their active tokens, refresh grants, and subscriptions (§9.8, D10).
func (h *apiHandlers) adminRevokeMember(w http.ResponseWriter, r *http.Request) {
	sch := chi.URLParam(r, "sch")
	// Member revoke cascades token/grant/session revocation — a high blast-radius
	// op, so it requires a fresh step-up signature like key rotation (p12-11/D19).
	var su struct {
		OwnerVkey       string `json:"owner_vkey"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	_ = json.NewDecoder(r.Body).Decode(&su)
	if err := h.requireStepUp(r, struct{ OwnerVkey, Nonce, Signature string }{su.OwnerVkey, su.StepUpNonce, su.StepUpSignature}); err != nil {
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

func (h *apiHandlers) adminListRules(w http.ResponseWriter, r *http.Request) {
	rules, err := h.d.Store.Rules().ListActive(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	respond.JSON(w, http.StatusOK, map[string]any{"rules": rules})
}

func (h *apiHandlers) adminUpsertRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RuleID       string          `json:"rule_id"`
		Name         string          `json:"name"`
		RuleConfig   json.RawMessage `json:"rule_config"`
		Tier         string          `json:"tier"`
		Entitlements []string        `json:"entitlements"`
		Priority     int             `json:"priority"`
		Status       string          `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RuleID == "" || body.Tier == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "rule_id and tier required")
		return
	}
	status := domain.RuleStatus(body.Status)
	if status == "" {
		status = domain.RuleActive
	} else if !status.Valid() {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "status must be active or disabled")
		return
	}
	now := time.Now()
	if err := h.d.Store.Rules().Upsert(r.Context(), domain.MembershipRule{
		RuleID: body.RuleID, Name: body.Name, RuleConfig: body.RuleConfig, Tier: body.Tier,
		Entitlements: body.Entitlements, Priority: body.Priority, Status: status, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "rule.upsert", body.RuleID)
	respond.JSON(w, http.StatusOK, map[string]string{"rule_id": body.RuleID})
}

// ---- channels ----

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
		ClientID     string   `json:"client_id"`
		Name         string   `json:"name"`
		ClientType   string   `json:"client_type"`
		Party        string   `json:"party"`
		RedirectURIs []string `json:"redirect_uris"`
		Audiences    []string `json:"allowed_audiences"`
		Scopes       []string `json:"allowed_scopes"`
		PKCERequired bool     `json:"pkce_required"`
		// Registering a client issues credentials → require a fresh step-up
		// signature like key rotation (p12-11/D19).
		OwnerVkey       string `json:"owner_vkey"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ClientID == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "client_id required")
		return
	}
	if ct := domain.ClientType(body.ClientType); !ct.Valid() {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "client_type must be public or confidential")
		return
	}
	if p := domain.ClientParty(body.Party); !p.Valid() {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "party must be first_party or third_party")
		return
	}
	// Step-up gates the actual registration (after request-shape validation).
	if err := h.requireStepUp(r, struct{ OwnerVkey, Nonce, Signature string }{body.OwnerVkey, body.StepUpNonce, body.StepUpSignature}); err != nil {
		writeAdminErr(w, err)
		return
	}
	c := domain.OAuthClient{
		ClientID: body.ClientID, Name: body.Name, ClientType: domain.ClientType(body.ClientType),
		Party: domain.ClientParty(body.Party), RedirectURIs: body.RedirectURIs,
		AllowedAudiences: body.Audiences, AllowedScopes: body.Scopes, PKCERequired: body.PKCERequired,
		Status: "active", CreatedAt: time.Now(),
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
	h.audit(r, "oauth_client.register", body.ClientID)
	resp := map[string]any{"client_id": body.ClientID}
	if plainSecret != "" {
		resp["client_secret"] = plainSecret // shown once
	}
	respond.JSON(w, http.StatusOK, resp)
}

// ---- signing keys (owner + step-up) ----

func (h *apiHandlers) adminRotateKey(w http.ResponseWriter, r *http.Request) {
	if h.d.Keys == nil {
		notImplemented(w, r)
		return
	}
	var body struct {
		OwnerVkey       string `json:"owner_vkey"`
		StepUpNonce     string `json:"step_up_nonce"`
		StepUpSignature string `json:"step_up_signature"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if err := h.requireStepUp(r, struct{ OwnerVkey, Nonce, Signature string }{body.OwnerVkey, body.StepUpNonce, body.StepUpSignature}); err != nil {
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
