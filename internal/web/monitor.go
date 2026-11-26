package web

// Phase 9 — surveillance continue : historique des mesures (results.jsonl), planificateur de tests,
// seuils modifiables et reconnexion automatique à vCenter (mot de passe mémorisé, chiffré).

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"vigie/internal/analysis"
	"vigie/internal/jobs"
	"vigie/internal/report"
	"vigie/internal/results"
	"vigie/internal/vsphere"
)

// ---------- mot de passe vCenter mémorisé ----------

const secretKeyFile = "secret.key"

func (s *Settings) key() ([]byte, error) {
	p := s.store.Path(secretKeyFile)
	if b, err := os.ReadFile(p); err == nil && len(b) == 32 {
		return b, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	if err := s.store.WriteBytes(secretKeyFile, k, 0o600); err != nil {
		return nil, err
	}
	return k, nil
}

func (s *Settings) seal(plain string) (string, error) {
	k, err := s.key()
	if err != nil {
		return "", err
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(plain), []byte("vigie-vsphere"))), nil
}

func (s *Settings) open(sealed string) (string, error) {
	b, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	k, err := s.key()
	if err != nil {
		return "", err
	}
	blk, _ := aes.NewCipher(k)
	g, _ := cipher.NewGCM(blk)
	if len(b) < g.NonceSize() {
		return "", errors.New("secret tronqué")
	}
	p, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], []byte("vigie-vsphere"))
	return string(p), err
}

// RememberedVSphere renvoie les paramètres de connexion complets si le mot de passe a été mémorisé.
func (s *Settings) RememberedVSphere() (vsphere.ConnectParams, bool) {
	doc := s.VSphere()
	if doc == nil || doc.Secret == "" {
		return vsphere.ConnectParams{}, false
	}
	pw, err := s.open(doc.Secret)
	if err != nil {
		log.Printf("settings: mot de passe vCenter mémorisé illisible : %v", err)
		return vsphere.ConnectParams{}, false
	}
	return vsphere.ConnectParams{URL: doc.URL, Username: doc.Username, Password: pw, Thumbprint: doc.Thumbprint}, true
}

// ForgetSecret oublie le mot de passe mémorisé (déconnexion volontaire) ; URL et compte restent.
func (s *Settings) ForgetSecret() {
	doc := s.VSphere()
	if doc == nil || doc.Secret == "" {
		return
	}
	doc.Secret = ""
	if err := s.store.Save(vsphereFile, doc); err != nil {
		log.Printf("settings: %v", err)
	}
}

// ---------- seuils modifiables ----------

const thresholdsFile = "thresholds.json"

// loadOverrides lit thresholds.json ; une clé absente (seuil apparu après l'enregistrement) garde
// la valeur fournie.
func (s *Settings) loadOverrides() map[string]analysis.Thresholds {
	var raw map[string]json.RawMessage
	out := map[string]analysis.Thresholds{}
	if s == nil || s.store.Load(thresholdsFile, &raw) != nil {
		return out
	}
	builtin := analysis.BuiltinPresets()
	for k, r := range raw {
		t, ok := builtin[k]
		if !ok || json.Unmarshal(r, &t) != nil {
			continue
		}
		out[k] = t
	}
	return out
}

// LoadThresholds applique les seuils modifiés enregistrés (au démarrage).
func (s *Settings) LoadThresholds() { analysis.SetOverrides(s.loadOverrides()) }

func validThresholds(t analysis.Thresholds) error {
	pos := map[string]float64{"tx_ratio_warn": t.TxRatioWarn, "tx_ratio_alert": t.TxRatioAlert, "write_mbps_warn": t.WriteMBpsWarn,
		"write_mbps_alert": t.WriteMBpsAlert, "ds_write_latency_warn_ms": t.DSWriteLatWarnMs, "ds_write_latency_alert_ms": t.DSWriteLatAlertMs,
		"qd1_p99_warn_ms": t.QD1P99WarnMs, "qd1_p99_alert_ms": t.QD1P99AlertMs, "useful_iops_ceiling_ms": t.UsefulIOPSCeilingMs,
		"iops_mismatch_warn_pct": t.IOPSMismatchWarnPct, "worker_cpu_warn_pct": t.WorkerCPUWarnPct,
		"host_spread_warn": t.HostSpreadWarn, "host_spread_alert": t.HostSpreadAlert}
	for k, v := range pos {
		if !(v > 0) || v > 1e6 {
			return fmt.Errorf("%s : valeur strictement positive attendue", k)
		}
	}
	switch {
	case t.TxRatioAlert < t.TxRatioWarn:
		return errors.New("ratio TX/écrit : le seuil ALERTE doit être au moins égal au seuil ATTENTION")
	case t.WriteMBpsAlert > t.WriteMBpsWarn:
		return errors.New("débit d'écriture : le seuil ALERTE doit être au plus égal au seuil ATTENTION")
	case t.DSWriteLatAlertMs < t.DSWriteLatWarnMs, t.QD1P99AlertMs < t.QD1P99WarnMs, t.HostSpreadAlert < t.HostSpreadWarn:
		return errors.New("le seuil ALERTE doit être au moins égal au seuil ATTENTION")
	case t.NFSWarnLinesWarn < 0 || t.NFSWarnLinesAlert < t.NFSWarnLinesWarn:
		return errors.New("avertissements NFS : seuils entiers positifs, ALERTE au moins égal à ATTENTION")
	case t.NICErrorsAlert < 0 || t.LinkFlapsAlert < 0 || t.PhysErrorsAlert < 0 || t.RxMissedWarn < 0:
		return errors.New("erreurs NIC et changements de lien : seuils positifs")
	}
	return nil
}

func (s *Server) thresholdsGet(w http.ResponseWriter, r *http.Request) {
	var m map[string]analysis.Thresholds
	if s.settings != nil {
		m = s.settings.loadOverrides()
	}
	mod := []string{}
	for k := range m {
		mod = append(mod, k)
	}
	writeJSON(w, http.StatusOK, map[string]any{"builtin": analysis.BuiltinPresets(), "presets": analysis.Presets(), "modified": mod,
		"default": analysis.DefaultThresholds().Preset})
}

type thresholdsRequest struct {
	Preset     string               `json:"preset"`
	Thresholds *analysis.Thresholds `json:"thresholds"` // nil : retour aux valeurs fournies
}

func (s *Server) thresholdsPut(w http.ResponseWriter, r *http.Request) {
	if s.settings == nil {
		writeError(w, http.StatusNotImplemented, "réglages indisponibles")
		return
	}
	var req thresholdsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("corps JSON invalide : %v", err))
		return
	}
	if _, ok := analysis.BuiltinPresets()[req.Preset]; !ok {
		writeError(w, http.StatusBadRequest, "préréglage inconnu (all-flash, hybride, disques)")
		return
	}
	m := s.settings.loadOverrides()
	if req.Thresholds == nil {
		delete(m, req.Preset)
	} else {
		if err := validThresholds(*req.Thresholds); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		t := *req.Thresholds
		t.Preset = req.Preset
		m[req.Preset] = t
	}
	if err := s.settings.store.Save(thresholdsFile, m); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	analysis.SetOverrides(m)
	log.Printf("seuils : préréglage %s %s par %s", req.Preset, map[bool]string{true: "modifié", false: "remis aux valeurs fournies"}[req.Thresholds != nil], principal(r).Name)
	s.thresholdsGet(w, r)
}

// ---------- historique des mesures ----------

func (s *Server) recordResults(j jobs.Job) {
	if s.results == nil {
		return
	}
	if err := s.results.Append(report.ResultPoints(&j)); err != nil {
		log.Printf("results: %v", err)
	}
}

// backfillResults reconstruit l'historique depuis les jobs conservés (première mise à jour en phase 9).
func (s *Server) backfillResults() {
	if s.results == nil || s.results.Exists() {
		return
	}
	var pts []results.Point
	for _, l := range s.jobs.List("", 0) {
		if l.State != jobs.Succeeded {
			continue
		}
		if j, ok := s.jobs.Get(l.ID); ok {
			pts = append(pts, report.ResultPoints(j)...)
		}
	}
	if err := s.results.Reset(pts); err != nil {
		log.Printf("results: reconstruction : %v", err)
		return
	}
	log.Printf("results: historique reconstruit depuis les jobs : %d mesure(s)", len(pts))
}

// parsePeriod : « 24h », « 7d », « 30d » ou une durée Go.
func parsePeriod(v string) (time.Duration, error) {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		d, err := strconv.Atoi(n)
		if err != nil || d <= 0 || d > 3660 {
			return 0, errors.New("période invalide")
		}
		return time.Duration(d) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, errors.New("période invalide (24h, 7d, 30d…)")
	}
	return d, nil
}

func (s *Server) resultsQuery(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := results.Filter{Host: q.Get("host"), Datastore: q.Get("datastore")}
	var err error
	for k, dst := range map[string]*time.Time{"from": &f.From, "to": &f.To} {
		if v := q.Get(k); v != "" {
			if *dst, err = time.Parse(time.RFC3339, v); err != nil {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("%s : date RFC 3339 attendue", k))
				return
			}
		}
	}
	if v := q.Get("period"); v != "" {
		d, err := parsePeriod(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		f.From = s.now().Add(-d)
	}
	if s.results == nil {
		writeJSON(w, http.StatusOK, map[string]any{"points": []results.Point{}, "series": [][2]string{}})
		return
	}
	pts, err := s.results.Query(f)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stamp := s.now().UTC().Format("20060102-1504")
	switch q.Get("format") {
	case "csv":
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="vigie-results-`+stamp+`.csv"`)
		_ = results.WriteCSV(w, pts)
		return
	case "json":
		w.Header().Set("Content-Disposition", `attachment; filename="vigie-results-`+stamp+`.json"`)
		writeJSON(w, http.StatusOK, pts)
		return
	}
	series, _ := s.results.Series()
	writeJSON(w, http.StatusOK, map[string]any{"points": pts, "series": series, "retention_days": s.schedule.get().RetentionDays})
}

// ---------- planificateur ----------

const scheduleFile = "schedule.json"

// Schedule est la configuration de la surveillance continue.
type Schedule struct {
	Enabled       bool   `json:"enabled"`
	IntervalMin   int    `json:"interval_min"` // 15 à 1440
	Profile       string `json:"profile"`      // standard (vCenter) ou local
	WriteS        int    `json:"write_s"`
	ReadS         int    `json:"read_s"`
	BaselineS     int    `json:"baseline_s"`
	Prefill       bool   `json:"prefill"`
	Preset        string `json:"preset"`
	RetentionDays int    `json:"retention_days"` // historique des mesures (30 par défaut)
}

// ScheduleState est l'état courant du planificateur.
type ScheduleState struct {
	Schedule
	Next      time.Time `json:"next,omitzero"`
	LastRun   time.Time `json:"last_run,omitzero"`
	LastJob   string    `json:"last_job,omitempty"`
	LastSkip  time.Time `json:"last_skip,omitzero"`
	SkipWhy   string    `json:"skip_reason,omitempty"`
	Remember  bool      `json:"vsphere_remembered"`
	Reconnect string    `json:"vsphere_reconnect,omitempty"` // dernière erreur de reconnexion
}

func defaultSchedule() Schedule {
	return Schedule{IntervalMin: 60, Profile: "standard", WriteS: 30, ReadS: 30, BaselineS: 30, RetentionDays: 30}
}

func (c *Schedule) validate() error {
	if c.IntervalMin < 15 || c.IntervalMin > 1440 {
		return errors.New("intervalle de 15 à 1440 minutes")
	}
	if c.Profile != "standard" && c.Profile != "local" {
		return errors.New("profil : standard ou local")
	}
	for _, d := range []int{c.WriteS, c.ReadS, c.BaselineS} {
		if d < 0 || d > 600 {
			return errors.New("durées de 0 à 600 s")
		}
	}
	if c.Preset != "" {
		if _, ok := analysis.BuiltinPresets()[c.Preset]; !ok {
			return errors.New("préréglage inconnu (all-flash, hybride, disques)")
		}
	}
	if c.RetentionDays < 1 || c.RetentionDays > 3650 {
		return errors.New("rétention de 1 à 3650 jours")
	}
	return nil
}

type scheduler struct {
	mu sync.Mutex
	st ScheduleState
}

func (sc *scheduler) get() ScheduleState {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.st
}

func (s *Server) loadSchedule() {
	c := defaultSchedule()
	if s.settings != nil {
		_ = s.settings.store.Load(scheduleFile, &c)
	}
	if c.validate() != nil {
		en := c.Enabled
		c = defaultSchedule()
		c.Enabled = en
	}
	s.schedule.mu.Lock()
	s.schedule.st.Schedule = c
	if c.Enabled {
		s.schedule.st.Next = s.now().Add(2 * time.Minute) // laisser le temps à la reconnexion vCenter
	}
	s.schedule.mu.Unlock()
}

func (s *Server) scheduleGet(w http.ResponseWriter, r *http.Request) {
	st := s.schedule.get()
	if s.settings != nil {
		_, st.Remember = s.settings.RememberedVSphere()
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) schedulePut(w http.ResponseWriter, r *http.Request) {
	c := defaultSchedule()
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&c); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("corps JSON invalide : %v", err))
		return
	}
	if err := c.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.settings != nil {
		if err := s.settings.store.Save(scheduleFile, c); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	s.schedule.mu.Lock()
	s.schedule.st.Schedule = c
	s.schedule.st.Next = time.Time{}
	if c.Enabled {
		s.schedule.st.Next = s.now().Add(time.Minute)
	}
	s.schedule.mu.Unlock()
	log.Printf("planificateur : %s toutes les %d min (%s), par %s", map[bool]string{true: "actif", false: "arrêté"}[c.Enabled], c.IntervalMin, c.Profile, principal(r).Name)
	s.scheduleGet(w, r)
}

// reconnect rétablit la session vCenter si le mot de passe est mémorisé.
func (s *Server) reconnect(ctx context.Context) error {
	if s.settings == nil || (s.vs.Status().Connected && s.vs.Alive(ctx)) {
		return nil
	}
	p, ok := s.settings.RememberedVSphere()
	if !ok {
		return errors.New("vCenter non connecté (mot de passe non mémorisé)")
	}
	ctx, cancel := context.WithTimeout(ctx, apiTimeout)
	defer cancel()
	if _, err := s.vs.Connect(ctx, p); err != nil {
		return fmt.Errorf("reconnexion à vCenter : %w", err)
	}
	log.Printf("vsphere : reconnecté à %s (mot de passe mémorisé)", p.URL)
	return nil
}

// scheduleTick lance le test planifié s'il est dû ; sinon note pourquoi il est sauté.
func (s *Server) scheduleTick(ctx context.Context) {
	st := s.schedule.get()
	now := s.now()
	if !st.Enabled || st.Next.IsZero() || now.Before(st.Next) {
		return
	}
	next := now.Add(time.Duration(st.IntervalMin) * time.Minute)
	skip := func(why string) {
		s.schedule.mu.Lock()
		s.schedule.st.LastSkip, s.schedule.st.SkipWhy, s.schedule.st.Next = now, why, next
		s.schedule.mu.Unlock()
		log.Printf("planificateur : test sauté : %s", why)
	}
	if cur := s.jobs.Current(); cur != nil {
		skip(fmt.Sprintf("un job est en cours (%s)", cur.Profile))
		return
	}
	if st.Profile == "standard" {
		if err := s.reconnect(ctx); err != nil {
			skip(err.Error())
			return
		}
	}
	pre := st.Prefill
	req := jobRequest{Profile: st.Profile, Repetitions: 1, BaselineS: st.BaselineS, WriteS: st.WriteS, ReadS: st.ReadS, Prefill: &pre, Preset: st.Preset}
	fn, err := s.jobFunc(&req)
	if err != nil {
		skip(err.Error())
		return
	}
	j, err := s.jobs.Submit(req.Profile, req, "schedule", fn)
	if err != nil {
		skip(err.Error())
		return
	}
	s.schedule.mu.Lock()
	s.schedule.st.LastRun, s.schedule.st.LastJob, s.schedule.st.Next, s.schedule.st.SkipWhy = now, j.ID, next, ""
	s.schedule.mu.Unlock()
	log.Printf("planificateur : job %s lancé (%s)", j.ID, st.Profile)
}

// background : reconstruction de l'historique, rétention, reconnexion vCenter et planificateur.
func (s *Server) background(ctx context.Context) {
	s.backfillResults()
	var lastPrune, lastReconnect, lastAlive time.Time
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		now := s.now()
		if now.Sub(lastPrune) > 6*time.Hour && s.results != nil {
			lastPrune = now
			if n, err := s.results.Prune(s.schedule.get().RetentionDays, now); err != nil {
				log.Printf("results: rétention : %v", err)
			} else if n > 0 {
				log.Printf("results: %d mesure(s) de plus de %d jours retirée(s)", n, s.schedule.get().RetentionDays)
			}
		}
		// session expirée côté serveur (vCenter redémarré…) : vérifiée toutes les 5 minutes, hors job
		if s.vs.Status().Connected && s.jobs.Current() == nil && now.Sub(lastAlive) > 5*time.Minute {
			lastAlive = now
			s.vs.Alive(ctx)
		}
		// reconnexion automatique (démarrage, session perdue) : au plus toutes les 5 minutes
		if s.settings != nil && !s.vs.Status().Connected && now.Sub(lastReconnect) > 5*time.Minute {
			if _, ok := s.settings.RememberedVSphere(); ok {
				lastReconnect = now
				err := s.reconnect(ctx)
				s.schedule.mu.Lock()
				s.schedule.st.Reconnect = ""
				if err != nil {
					s.schedule.st.Reconnect = err.Error()
					log.Print(err)
				}
				s.schedule.mu.Unlock()
			}
		}
		s.scheduleTick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
