package web

import (
	"encoding/json"
	"net/http"
)

func (s *Server) tokensList(w http.ResponseWriter, r *http.Request) {
	if s.tokens == nil {
		writeError(w, http.StatusNotImplemented, "jetons indisponibles")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": s.tokens.List()})
}

// tokensCreate : réservé au mot de passe de l'appliance (un jeton ne crée pas de jeton).
func (s *Server) tokensCreate(w http.ResponseWriter, r *http.Request) {
	if s.tokens == nil {
		writeError(w, http.StatusNotImplemented, "jetons indisponibles")
		return
	}
	if principal(r).IsToken {
		writeError(w, http.StatusForbidden, "un jeton d'API ne peut pas créer de jeton")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corps JSON invalide : "+err.Error())
		return
	}
	tok, secret, err := s.tokens.Create(req.Name)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": tok.ID, "name": tok.Name, "created": tok.Created, "token": secret,
		"note": "le secret n'est affiché qu'une fois"})
}

func (s *Server) tokensDelete(w http.ResponseWriter, r *http.Request) {
	if s.tokens == nil {
		writeError(w, http.StatusNotImplemented, "jetons indisponibles")
		return
	}
	if principal(r).IsToken {
		writeError(w, http.StatusForbidden, "un jeton d'API ne peut pas révoquer de jeton")
		return
	}
	if err := s.tokens.Delete(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": s.tokens.List()})
}
