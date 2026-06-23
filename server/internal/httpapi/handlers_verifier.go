package httpapi

import (
	"net/http"

	"github.com/poolops/issuer/internal/utils/jose"
)

// jwks publishes the issuer's signing public keys as a JWKS — public keys and
// per-key status only, no certificate chain (detailed §9.6, C9).
//
//	GET /.well-known/poolops/jwks.json
func (h *apiHandlers) jwks(w http.ResponseWriter, r *http.Request) {
	if h.d.Keys == nil {
		notImplemented(w, r)
		return
	}
	pub, err := h.d.Keys.PublicJWKSKeys(r.Context())
	if err != nil {
		serverError(w, r, err)
		return
	}
	doc, err := jose.BuildJWKS(pub)
	if err != nil {
		serverError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=60")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(doc)
}
