package web

import (
	"context"
	"net"
	"net/http"
	"strings"

	"vigie/internal/auth"
)

// Verifier vérifie le mot de passe de l'appliance.
type Verifier interface {
	Verify(secret string) bool
	IsSet() bool
}

type ctxKey int

const principalKey ctxKey = 1

// consoleKey marque les connexions reçues sur le socket Unix de la console (root uniquement).
const consoleKey ctxKey = 2

// ConsoleConn est le ConnContext du serveur du socket console : les requêtes qui en viennent sont
// authentifiées comme « console » (la TUI de tty1 tourne en root, le socket est en 0600).
func ConsoleConn(ctx context.Context, _ net.Conn) context.Context {
	return context.WithValue(ctx, consoleKey, true)
}

func isConsole(r *http.Request) bool {
	v, _ := r.Context().Value(consoleKey).(bool)
	return v
}

// Principal identifie l'appelant : « ui » (mot de passe) ou « token:<nom> » (jeton d'API).
type Principal struct {
	Name    string
	IsToken bool
}

func principal(r *http.Request) Principal {
	p, _ := r.Context().Value(principalKey).(Principal)
	return p
}

// requireAuth protège l'API : cookie de session (interface web, avec l'en-tête X-Vigie sur les
// requêtes qui modifient), « Authorization: Bearer <mot de passe> » ou « Bearer vg_… » (jeton d'API).
// /api/health, /api/login et le front restent publics.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isConsole(r) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, Principal{Name: "console"})))
			return
		}
		if s.auth == nil {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, Principal{Name: "ui"})))
			return
		}
		if c, err := r.Cookie(sessionCookie); err == nil && s.sess.valid(c.Value) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) != "1" {
				writeError(w, http.StatusForbidden, "en-tête "+csrfHeader+" requis")
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, Principal{Name: "ui"})))
			return
		}
		h := r.Header.Get("Authorization")
		if strings.HasPrefix(h, "Bearer ") {
			secret := strings.TrimSpace(h[7:])
			if strings.HasPrefix(secret, auth.TokenPrefix) && s.tokens != nil {
				if name, ok := s.tokens.Check(secret); ok {
					next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, Principal{Name: "token:" + name, IsToken: true})))
					return
				}
			} else if s.auth.Verify(secret) {
				next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, Principal{Name: "ui"})))
				return
			}
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="vigie"`)
		if !s.auth.IsSet() {
			writeError(w, http.StatusUnauthorized, "aucun mot de passe défini sur l'appliance")
			return
		}
		writeError(w, http.StatusUnauthorized, "authentification requise")
	})
}

type errorResponse struct {
	Error string `json:"error"`
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}
