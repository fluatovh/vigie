package web

import (
	"bytes"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	webui "vigie/web"
)

// CSP stricte (§0 et phase 7) : tout vient de l'appliance, aucun script ni style en ligne.
const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; " +
	"font-src 'self'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"

func securityHeaders(h http.Header, tls bool) {
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Opener-Policy", "same-origin")
	if tls {
		h.Set("Strict-Transport-Security", "max-age=31536000")
	}
}

// static sert le front embarqué ; toute route inconnue hors /api renvoie index.html (une seule page).
func (s *Server) static() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "méthode non autorisée", http.StatusMethodNotAllowed)
			return
		}
		securityHeaders(w.Header(), r.TLS != nil)
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p == "" || p == "." {
			p = "index.html"
		}
		if _, err := fs.Stat(webui.FS, p); err != nil {
			p = "index.html"
		}
		if p == "index.html" {
			w.Header().Set("Cache-Control", "no-store")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
		}
		b, err := fs.ReadFile(webui.FS, p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if ct := mime.TypeByExtension(path.Ext(p)); ct != "" {
			w.Header().Set("Content-Type", ct)
		}
		http.ServeContent(w, r, p, time.Time{}, bytes.NewReader(b))
	})
}

// RedirectHandler (port 80) : /api/health reste disponible en HTTP pour les scripts de déploiement,
// tout le reste est redirigé vers HTTPS.
func RedirectHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/health" {
			h.ServeHTTP(w, r)
			return
		}
		host := r.Host
		if i := strings.LastIndex(host, ":"); i >= 0 && !strings.Contains(host[i:], "]") {
			host = host[:i]
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusMovedPermanently)
	})
}
