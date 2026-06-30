// Package authpage renders the issuer-served Authorization Page and the channel
// binding page (S0003 p2): full HTML returned by the issuer at /connect and
// /bind, driven by a single same-origin vanilla-JS asset that runs the CIP-30
// flow (discover wallet → enable → getRewardAddresses → signData) and forwards
// the COSE_Key + signature to the backend. The browser never parses CBOR; all
// COSE/address parsing stays server-side (S0003 C4/C5). OAuth parameters reach
// the page as data-* attributes (HTML-attribute-escaped by html/template), not
// inline script, so the page needs no 'unsafe-inline' for scripts.
package authpage

import (
	"embed"
	"html/template"
	"net/http"
)

//go:embed assets/ouropass-auth.js templates/connect.html templates/bind.html
var files embed.FS

var tmpl = template.Must(template.ParseFS(files, "templates/*.html"))

// csp locks the page down: only the same-origin script runs (no inline script),
// requests stay same-origin, wallet icons may be data:/https:. form-action is
// intentionally unset so the authorize form can POST and follow the issuer's
// 302 to the client redirect_uri (form-action does not inherit from default-src).
const csp = "default-src 'none'; script-src 'self'; style-src 'unsafe-inline'; " +
	"img-src 'self' data: https:; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'"

// ConnectData parameterizes the Authorization Page.
type ConnectData struct {
	ClientID, RedirectURI, State, Aud, Scope, CodeChallenge, DevicePubkey string
}

// BindData parameterizes the channel binding page.
type BindData struct {
	ChannelType string
}

func writeHeader(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
}

// RenderConnect writes the Authorization Page for the given OAuth context.
func RenderConnect(w http.ResponseWriter, d ConnectData) error {
	writeHeader(w)
	return tmpl.ExecuteTemplate(w, "connect.html", d)
}

// RenderBind writes the channel binding page.
func RenderBind(w http.ResponseWriter, d BindData) error {
	writeHeader(w)
	return tmpl.ExecuteTemplate(w, "bind.html", d)
}

// Asset serves the embedded wallet JS (same-origin, content-addressable cache).
func Asset() http.Handler {
	body, err := files.ReadFile("assets/ouropass-auth.js")
	if err != nil {
		panic("authpage: missing embedded asset: " + err.Error())
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		_, _ = w.Write(body)
	})
}
