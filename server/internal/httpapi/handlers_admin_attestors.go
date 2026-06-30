package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"ouro-pass/server/internal/core/attestor"
	"ouro-pass/server/internal/domain"
	"ouro-pass/server/internal/httpapi/respond"
	"ouro-pass/server/internal/utils/crypto"
)

// attestorIDParam reads the {id} path param, percent-decoding it. chi routes on the
// raw (encoded) path, so a client that percent-encodes the id (e.g. a legacy ':'
// id sent as %3A) would otherwise arrive un-decoded and never match. New ids are
// URL-safe, making this a no-op for them.
func attestorIDParam(r *http.Request) string {
	id := chi.URLParam(r, "id")
	if dec, err := url.PathUnescape(id); err == nil {
		return dec
	}
	return id
}

// adminListAttestors returns the configured on-chain credential sources (S0006):
// the generalization of "the served pool". Params carry no secrets (pool_stake =
// pool_id/network/ticker/name), so they are returned verbatim.
func (h *apiHandlers) adminListAttestors(w http.ResponseWriter, r *http.Request) {
	cfgs, err := h.d.Store.Attestors().List(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(cfgs))
	for _, c := range cfgs {
		params := c.Params
		if len(params) == 0 {
			params = json.RawMessage("{}")
		}
		out = append(out, map[string]any{
			"attestor_id": c.AttestorID, "kind": c.Kind, "label": c.Label,
			"params": params, "status": c.Status,
		})
	}
	respond.JSON(w, http.StatusOK, map[string]any{"attestors": out})
}

// adminCreateAttestor registers a new credential source (operator). Kind + params
// are validated; a duplicate (kind, label) is a 409.
func (h *apiHandlers) adminCreateAttestor(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind   string          `json:"kind"`
		Label  string          `json:"label"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "invalid body")
		return
	}
	if body.Label == "" {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "label is required")
		return
	}
	if err := validateAttestorInput(body.Kind, body.Params); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if dup, err := h.attestorLabelExists(r, body.Kind, body.Label, ""); err != nil {
		serverError(w, r, err)
		return
	} else if dup {
		respond.Error(w, http.StatusConflict, "conflict", "an attestor of this kind with that label already exists")
		return
	}
	now := time.Now()
	// URL-safe id (hex): it travels in the path of update/delete, which the client
	// percent-encodes — a ':' here would survive as %3A and never match the route.
	id := crypto.RandomID()
	if err := h.d.Store.Attestors().Create(r.Context(), domain.AttestorConfig{
		AttestorID: id, Kind: body.Kind, Label: body.Label, Params: body.Params,
		Status: domain.AttestorActive, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "attestor.create", id)
	respond.JSON(w, http.StatusOK, map[string]any{"attestor_id": id})
}

// adminUpdateAttestor edits an attestor's label/params/status by id (operator).
// Kind is immutable.
func (h *apiHandlers) adminUpdateAttestor(w http.ResponseWriter, r *http.Request) {
	id := attestorIDParam(r)
	var body struct {
		Label  *string         `json:"label"`
		Params json.RawMessage `json:"params"`
		Status *string         `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", "invalid body")
		return
	}
	cur, err := h.d.Store.Attestors().Get(r.Context(), id)
	if errors.Is(err, domain.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not_found", "attestor not found")
		return
	}
	if err != nil {
		serverError(w, r, err)
		return
	}
	if body.Label != nil {
		cur.Label = *body.Label
	}
	if len(body.Params) > 0 {
		cur.Params = body.Params
	}
	if body.Status != nil {
		switch *body.Status {
		case domain.AttestorActive, domain.AttestorDisabled:
			cur.Status = *body.Status
		default:
			respond.Error(w, http.StatusBadRequest, "invalid_request", "status must be active or disabled")
			return
		}
	}
	if err := validateAttestorInput(cur.Kind, cur.Params); err != nil {
		respond.Error(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if dup, err := h.attestorLabelExists(r, cur.Kind, cur.Label, id); err != nil {
		serverError(w, r, err)
		return
	} else if dup {
		respond.Error(w, http.StatusConflict, "conflict", "an attestor of this kind with that label already exists")
		return
	}
	cur.UpdatedAt = time.Now()
	if err := h.d.Store.Attestors().Update(r.Context(), *cur); err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "attestor.update", id)
	respond.JSON(w, http.StatusOK, map[string]any{"attestor_id": id})
}

// adminDeleteAttestor removes an attestor by id (operator). Subjects holding only
// this credential will no longer pass the thin gate on next issuance.
func (h *apiHandlers) adminDeleteAttestor(w http.ResponseWriter, r *http.Request) {
	id := attestorIDParam(r)
	if err := h.d.Store.Attestors().Delete(r.Context(), id); errors.Is(err, domain.ErrNotFound) {
		respond.Error(w, http.StatusNotFound, "not_found", "attestor not found")
		return
	} else if err != nil {
		serverError(w, r, err)
		return
	}
	h.audit(r, "attestor.delete", id)
	respond.JSON(w, http.StatusOK, map[string]any{"deleted": id})
}

// attestorLabelExists reports whether another attestor of the same kind already
// uses the label (excluding excludeID), so create/update can return a clean 409
// instead of a raw unique-constraint 500.
func (h *apiHandlers) attestorLabelExists(r *http.Request, kind, label, excludeID string) (bool, error) {
	cfgs, err := h.d.Store.Attestors().List(r.Context())
	if err != nil {
		return false, err
	}
	for _, c := range cfgs {
		if c.Kind == kind && c.Label == label && c.AttestorID != excludeID {
			return true, nil
		}
	}
	return false, nil
}

// validateAttestorInput checks a kind + its params. Only pool_stake is implemented
// this cycle (S0006 C5): NFT and other kinds are rejected until their evaluators land.
func validateAttestorInput(kind string, params json.RawMessage) error {
	switch kind {
	case attestor.KindPoolStake:
		var p attestor.PoolStakeParams
		if err := json.Unmarshal(params, &p); err != nil {
			return fmt.Errorf("invalid params: %w", err)
		}
		if strings.TrimSpace(p.PoolID) == "" {
			return errors.New("pool_id is required")
		}
		switch p.Network {
		case "", "mainnet", "preprod", "preview":
		default:
			return fmt.Errorf("invalid network %q (want mainnet|preprod|preview)", p.Network)
		}
		return nil
	default:
		return fmt.Errorf("unsupported attestor kind %q (only pool_stake)", kind)
	}
}
