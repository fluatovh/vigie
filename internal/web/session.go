package web

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Sessions de l'interface web : cookie HttpOnly/Secure/SameSite=Strict, état en mémoire (une
// session ne survit pas à un redémarrage, c'est voulu), expiration après 12 h d'inactivité.
const (
	sessionCookie = "vigie_session"
	sessionIdle   = 12 * time.Hour
	// csrfHeader doit accompagner toute requête non-GET authentifiée par cookie : un en-tête
	// personnalisé force un pré-vol CORS, qu'un site tiers ne peut pas passer.
	csrfHeader = "X-Vigie"
)

type sessions struct {
	mu   sync.Mutex
	m    map[string]time.Time // id -> dernière activité
	fail map[string][]time.Time
}

func newSessions() *sessions {
	return &sessions{m: map[string]time.Time{}, fail: map[string][]time.Time{}}
}

func (ss *sessions) create() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	ss.mu.Lock()
	ss.m[id] = time.Now()
	ss.mu.Unlock()
	return id
}

func (ss *sessions) valid(id string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	t, ok := ss.m[id]
	if !ok {
		return false
	}
	if time.Since(t) > sessionIdle {
		delete(ss.m, id)
		return false
	}
	ss.m[id] = time.Now()
	return true
}

func (ss *sessions) drop(id string) {
	ss.mu.Lock()
	delete(ss.m, id)
	ss.mu.Unlock()
}

// throttled : plus de 5 échecs de connexion en 5 minutes depuis la même adresse.
func (ss *sessions) throttled(addr string) bool {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var keep []time.Time
	for _, t := range ss.fail[addr] {
		if time.Since(t) < 5*time.Minute {
			keep = append(keep, t)
		}
	}
	ss.fail[addr] = keep
	return len(keep) >= 5
}

func (ss *sessions) failed(addr string) {
	ss.mu.Lock()
	ss.fail[addr] = append(ss.fail[addr], time.Now())
	ss.mu.Unlock()
}

func clientAddr(r *http.Request) string {
	h := r.RemoteAddr
	for i := len(h) - 1; i >= 0; i-- {
		if h[i] == ':' {
			return h[:i]
		}
	}
	return h
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	addr := clientAddr(r)
	if s.sess.throttled(addr) {
		writeError(w, http.StatusTooManyRequests, "trop de tentatives, réessayer dans quelques minutes")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corps JSON invalide")
		return
	}
	if s.auth == nil || !s.auth.Verify(req.Password) {
		s.sess.failed(addr)
		time.Sleep(time.Second)
		writeError(w, http.StatusUnauthorized, "mot de passe incorrect")
		return
	}
	id := s.sess.create()
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: id, Path: "/", HttpOnly: true, Secure: r.TLS != nil,
		SameSite: http.SameSiteStrictMode, MaxAge: int(sessionIdle.Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.sess.drop(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// reset ferme toutes les sessions (changement de mot de passe).
func (s *sessions) reset() {
	s.mu.Lock()
	s.m = map[string]time.Time{}
	s.mu.Unlock()
}
