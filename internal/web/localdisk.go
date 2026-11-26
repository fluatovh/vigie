package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"vigie/internal/testdisk"
)

// Disque de test en mode local (phase T) : l'utilisateur ajoute le disque dans vSphere, l'appliance
// le détecte et l'adopte après contrôles et confirmation. Refusé pendant un job (verrou global).

// LocalGuide décrit les paramètres à saisir dans le client vSphere (affichés par la console).
type LocalGuide struct {
	AdviseGiB  int64  `json:"advise_gib"`
	MinGiB     int64  `json:"min_gib"`
	Controller string `json:"controller"`
}

type localState struct {
	*testdisk.State
	Expect *testdisk.Expect `json:"expect,omitempty"`
	Guide  LocalGuide       `json:"guide"`
}

func (s *Server) localGuide() LocalGuide {
	ctrl := "contrôleur SCSI 0 existant (VMware Paravirtual)"
	return LocalGuide{AdviseGiB: testdisk.LocalAdviseGiB, MinGiB: testdisk.LocalMinBytes >> 30, Controller: ctrl}
}

func (s *Server) localStatus(w http.ResponseWriter, r *http.Request) {
	if s.testdisk == nil {
		writeError(w, http.StatusNotImplemented, "disque de test indisponible")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	writeJSON(w, http.StatusOK, localState{State: s.testdisk.Status(ctx, nil), Expect: s.testdisk.LocalExpect(), Guide: s.localGuide()})
}

func (s *Server) localBusy(w http.ResponseWriter) bool {
	if s.testdisk == nil {
		writeError(w, http.StatusNotImplemented, "disque de test indisponible")
		return true
	}
	if j := s.jobs.Current(); j != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("un job est en cours (%s)", j.Profile))
		return true
	}
	return false
}

func (s *Server) localPrepare(w http.ResponseWriter, r *http.Request) {
	if s.localBusy(w) {
		return
	}
	e, err := s.testdisk.LocalPrepare()
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"expect": e, "guide": s.localGuide()})
}

func (s *Server) localCancel(w http.ResponseWriter, r *http.Request) {
	if s.testdisk == nil {
		writeError(w, http.StatusNotImplemented, "disque de test indisponible")
		return
	}
	_ = s.testdisk.LocalCancel()
	w.WriteHeader(http.StatusNoContent)
}

// localScan : rescan et contrôles des disques apparus (lecture seule). ?wait=10 attend jusqu'à 10 s.
func (s *Server) localScan(w http.ResponseWriter, r *http.Request) {
	if s.localBusy(w) {
		return
	}
	wait := 5 * time.Second
	if v, err := time.ParseDuration(r.URL.Query().Get("wait") + "s"); err == nil && v >= 0 && v <= 30*time.Second {
		wait = v
	}
	c, err := s.testdisk.LocalScan(wait)
	if err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if c == nil {
		c = []testdisk.Candidate{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"candidates": c})
}

// localAdopt {name, confirm, label} : seule opération du mode local qui écrit (la signature).
func (s *Server) localAdopt(w http.ResponseWriter, r *http.Request) {
	if s.localBusy(w) {
		return
	}
	var req struct {
		Name    string `json:"name"`
		Confirm string `json:"confirm"`
		Label   string `json:"label"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corps JSON invalide")
		return
	}
	st, err := s.testdisk.LocalAdopt(req.Name, req.Confirm, req.Label)
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "state": st})
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) localRelease(w http.ResponseWriter, r *http.Request) {
	if s.localBusy(w) {
		return
	}
	if err := s.testdisk.LocalRelease(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
