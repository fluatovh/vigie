package web

import (
	"net/http"
	"os"
	"strings"

	"vigie/internal/netdiag"
	"vigie/internal/sysconf"
)

// Configuration de l'appliance depuis la console (phase D) : réseau, mot de passe, alimentation.
// Réservé au socket de la console (root) : jamais par HTTP(S), même avec un jeton.

type passwordSetter interface{ Set(string) error }

func (s *Server) consoleOnly(w http.ResponseWriter, r *http.Request) bool {
	if !isConsole(r) {
		writeError(w, http.StatusForbidden, "réservé à la console de l'appliance")
		return false
	}
	if s.sys == nil {
		writeError(w, http.StatusNotImplemented, "configuration système indisponible")
		return false
	}
	return true
}

func (s *Server) systemNetwork(w http.ResponseWriter, r *http.Request) {
	if !s.consoleOnly(w, r) {
		return
	}
	host, _ := os.Hostname()
	source := "dhcp"
	if s.sys.Saved() != nil {
		source = "console"
	} else if b, err := os.ReadFile("/etc/network/interfaces"); err == nil && !containsDHCP(b) {
		source = "ovf"
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": s.sys.Saved(), "source": source, "hostname": host, "current": netdiag.LocalInfo()})
}

func containsDHCP(b []byte) bool { return strings.Contains(string(b), "inet dhcp") }

func (s *Server) systemNetworkPut(w http.ResponseWriter, r *http.Request) {
	if !s.consoleOnly(w, r) {
		return
	}
	var n sysconf.Network
	if !decode(w, r, &n) {
		return
	}
	if err := s.sys.ApplyNetwork(n); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"saved": s.sys.Saved()})
}

func (s *Server) systemPassword(w http.ResponseWriter, r *http.Request) {
	if !s.consoleOnly(w, r) {
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	ps, ok := s.auth.(passwordSetter)
	if !ok {
		writeError(w, http.StatusNotImplemented, "mot de passe non modifiable")
		return
	}
	if err := s.sys.SetPassword(req.Password, ps.Set); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.sess.reset() // les sessions web ouvertes avec l'ancien mot de passe sont fermées
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) systemPower(w http.ResponseWriter, r *http.Request) {
	if !s.consoleOnly(w, r) {
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if !decode(w, r, &req) {
		return
	}
	if j := s.jobs.Current(); j != nil && r.URL.Query().Get("force") != "1" {
		writeError(w, http.StatusConflict, "un job est en cours ("+j.Profile+") : l'annuler d'abord")
		return
	}
	if err := s.sys.Power(req.Action); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

// Langues de la console et de l'interface web (l'anglais par défaut).
var languages = map[string]bool{"en": true, "fr": true, "es": true, "it": true, "pl": true, "de": true}

const languageFile = "language.json"

// Language renvoie la langue enregistrée de l'appliance (« en » par défaut).
func (st *Settings) Language() string {
	var v struct {
		Language string `json:"language"`
	}
	if st != nil && st.store != nil && st.store.Load(languageFile, &v) == nil && languages[v.Language] {
		return v.Language
	}
	return "en"
}

// systemLanguage : langue de la console (socket console uniquement).
func (s *Server) systemLanguage(w http.ResponseWriter, r *http.Request) {
	if !isConsole(r) {
		writeError(w, http.StatusForbidden, "réservé à la console de l'appliance")
		return
	}
	var req struct {
		Language string `json:"language"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !languages[req.Language] {
		writeError(w, http.StatusBadRequest, "langue : en, fr, es, it, pl ou de")
		return
	}
	if s.settings == nil || s.settings.store.Save(languageFile, req) != nil {
		writeError(w, http.StatusInternalServerError, "langue non enregistrée")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
