package web

import "net/http"

// testdiskStatus : état du disque de test. Création et suppression passent par les jobs
// (profils testdisk.create et testdisk.delete), soumis au verrou global.
func (s *Server) testdiskStatus(w http.ResponseWriter, r *http.Request) {
	if s.testdisk == nil {
		writeError(w, http.StatusNotImplemented, "disque de test indisponible")
		return
	}
	ctx, cancel := s.ctx(r)
	defer cancel()
	sess, _ := s.vs.Session() // optionnel : sans session, l'état côté vSphere n'est pas vérifié
	writeJSON(w, http.StatusOK, s.testdisk.Status(ctx, sess))
}
