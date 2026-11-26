package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"vigie/internal/jobs"
	"vigie/internal/report"
)

const brandingFile = "branding.json"

// Branding renvoie la personnalisation du rapport (vide si absente).
func (st *Settings) Branding() report.Branding {
	var b report.Branding
	if st == nil || st.store == nil {
		return b
	}
	_ = st.store.Load(brandingFile, &b)
	return b
}

func (s *Server) brandingGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.settings.Branding())
}

// brandingPut remplace la personnalisation (logo en data URI ; "" pour le retirer).
func (s *Server) brandingPut(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusNotImplemented, "réglages indisponibles")
		return
	}
	var b report.Branding
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 400<<10)).Decode(&b); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("corps JSON invalide (logo de 200 Kio au maximum) : %v", err))
		return
	}
	if err := b.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.settings.store.Save(brandingFile, b); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, b)
}

func (s *Server) reportMeta() report.Meta {
	return report.Meta{Version: s.info.Version, Now: s.now()}
}

func (s *Server) reportGet(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job inconnu")
		return
	}
	p, err := report.Build(j, s.settings.Branding(), s.reportMeta())
	s.serveReport(w, r, p, err)
}

func (s *Server) reportCompare(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("a") == "" || q.Get("b") == "" || q.Get("a") == q.Get("b") {
		writeError(w, http.StatusBadRequest, "deux jobs distincts requis : ?a=<id>&b=<id>")
		return
	}
	var js [2]*jobs.Job
	for i, k := range []string{"a", "b"} {
		j, ok := s.jobs.Get(q.Get(k))
		if !ok {
			writeError(w, http.StatusNotFound, fmt.Sprintf("job inconnu : %s", q.Get(k)))
			return
		}
		js[i] = j
	}
	p, err := report.BuildCompare(js[0], js[1], s.settings.Branding(), s.reportMeta())
	s.serveReport(w, r, p, err)
}

func (s *Server) serveReport(w http.ResponseWriter, r *http.Request, p *report.Page, err error) {
	if errors.Is(err, report.ErrNoReport) {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var buf bytes.Buffer
	nonce, err := report.Render(&buf, p)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("rendu du rapport : %v", err))
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", report.CSP(nonce)+"; frame-ancestors 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cache-Control", "no-store")
	if r.URL.Query().Get("download") == "1" {
		h.Set("Content-Disposition", `attachment; filename="`+report.FileName(p, s.now())+`"`)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(buf.Bytes())
}
