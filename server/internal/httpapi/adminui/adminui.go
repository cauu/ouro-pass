package adminui

import (
	"embed"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed all:dist
var distFS embed.FS

const placeholder = `<!doctype html><meta charset="utf-8"><title>Ouro Pass Admin</title>
<body style="font-family:system-ui;max-width:34rem;margin:4rem auto;padding:0 1rem;line-height:1.5">
<h1>Ouro Pass Admin</h1>
<p>The admin UI is not built into this binary. From <code>server/</code> run
<code>make web</code> (builds <code>../web</code> and stages it), then rebuild the issuer.</p>
</body>`

// Handler serves the embedded Admin SPA: hashed assets get an immutable long
// cache; every other path falls back to index.html so client-side routes resolve.
// Before the SPA is built in (only dist/.gitkeep present) it serves a placeholder.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("adminui: " + err.Error())
	}
	index, readErr := fs.ReadFile(sub, "index.html")
	built := readErr == nil
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !built {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(placeholder))
			return
		}
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" && p != "index.html" {
			if f, openErr := sub.Open(p); openErr == nil {
				_ = f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(index)
	})
}
