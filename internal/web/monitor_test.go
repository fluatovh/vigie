package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vigie/internal/analysis"
	"vigie/internal/jobs"
	"vigie/internal/results"
	"vigie/internal/store"
	"vigie/internal/vsphere"
)

func monitorServer(t *testing.T) (*store.Store, *jobs.Manager, func(method, path, body string) *httptest.ResponseRecorder) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jm := jobs.NewManager(st)
	h := New(Info{Version: "t", Started: time.Now()}, Deps{Settings: NewSettings(st), Jobs: jm})
	return st, jm, func(method, path, body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
		return rec
	}
}

func TestThresholdsAPI(t *testing.T) {
	defer analysis.SetOverrides(nil)
	st, _, do := monitorServer(t)
	th := analysis.BuiltinPresets()["hybride"]
	th.WriteMBpsWarn, th.WriteMBpsAlert = 120, 40
	b, _ := json.Marshal(map[string]any{"preset": "hybride", "thresholds": th})
	if r := do("PUT", "/api/thresholds", string(b)); r.Code != http.StatusOK {
		t.Fatalf("PUT : %d %s", r.Code, r.Body)
	}
	if got := analysis.Presets()["hybride"]; got.WriteMBpsWarn != 120 || got.Preset != "hybride" {
		t.Errorf("préréglage effectif : %+v", got)
	}
	if analysis.Presets()["all-flash"].WriteMBpsWarn != 50 {
		t.Error("all-flash ne doit pas changer")
	}
	if !st.Exists(thresholdsFile) {
		t.Error("seuils non persistés")
	}
	var p struct {
		Presets map[string]analysis.Thresholds `json:"presets"`
	}
	_ = json.Unmarshal(do("GET", "/api/presets", "").Body.Bytes(), &p)
	if p.Presets["hybride"].WriteMBpsWarn != 120 {
		t.Error("/api/presets ne reflète pas les seuils modifiés")
	}
	bad := th
	bad.WriteMBpsAlert = 200
	b, _ = json.Marshal(map[string]any{"preset": "hybride", "thresholds": bad})
	if r := do("PUT", "/api/thresholds", string(b)); r.Code != http.StatusBadRequest {
		t.Errorf("ALERTE > ATTENTION en débit : %d", r.Code)
	}
	if r := do("PUT", "/api/thresholds", `{"preset":"inconnu","thresholds":null}`); r.Code != http.StatusBadRequest {
		t.Errorf("préréglage inconnu : %d", r.Code)
	}
	analysis.SetOverrides(nil)
	NewSettings(st).LoadThresholds() // redémarrage
	if analysis.Presets()["hybride"].WriteMBpsWarn != 120 {
		t.Error("seuils non rechargés au démarrage")
	}
	if r := do("PUT", "/api/thresholds", `{"preset":"hybride"}`); r.Code != http.StatusOK {
		t.Fatalf("remise à zéro : %d", r.Code)
	}
	if analysis.Presets()["hybride"].WriteMBpsWarn != 50 {
		t.Error("remise aux valeurs fournies")
	}
}

func TestScheduleAPI(t *testing.T) {
	_, _, do := monitorServer(t)
	var st ScheduleState
	_ = json.Unmarshal(do("GET", "/api/schedule", "").Body.Bytes(), &st)
	if st.Enabled || st.RetentionDays != 30 || st.IntervalMin != 60 {
		t.Errorf("défauts : %+v", st)
	}
	if r := do("PUT", "/api/schedule", `{"enabled":true,"interval_min":5,"profile":"standard","retention_days":30}`); r.Code != http.StatusBadRequest {
		t.Errorf("intervalle 5 min : %d", r.Code)
	}
	r := do("PUT", "/api/schedule", `{"enabled":true,"interval_min":15,"profile":"local","retention_days":7}`)
	if r.Code != http.StatusOK {
		t.Fatalf("PUT : %d %s", r.Code, r.Body)
	}
	_ = json.Unmarshal(r.Body.Bytes(), &st)
	if !st.Enabled || st.Next.IsZero() || st.Profile != "local" || st.RetentionDays != 7 {
		t.Errorf("état : %+v", st)
	}
}

func TestScheduleTickSkips(t *testing.T) {
	st, jm, _ := monitorServer(t)
	s := &Server{now: time.Now, settings: NewSettings(st), jobs: jm, vs: vsphere.NewManager()}
	s.schedule.st.Schedule = defaultSchedule()
	s.schedule.st.Enabled = true
	s.schedule.st.Profile = "local"
	s.schedule.st.Next = time.Now().Add(-time.Second)
	s.scheduleTick(context.Background())
	got := s.schedule.get()
	if got.SkipWhy == "" || !strings.Contains(got.SkipWhy, "disque de test") || got.Next.Before(time.Now().Add(59*time.Minute)) {
		t.Errorf("sans disque : %+v", got)
	}
	s.schedule.st.Profile, s.schedule.st.Next = "standard", time.Now().Add(-time.Second)
	s.scheduleTick(context.Background())
	if got = s.schedule.get(); !strings.Contains(got.SkipWhy, "mémorisé") {
		t.Errorf("sans vCenter : %q", got.SkipWhy)
	}
}

func TestRememberVSphere(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s := NewSettings(st)
	p := vsphere.ConnectParams{URL: "https://vc", Username: "u", Password: "pa$$wörd", Thumbprint: "AA"}
	s.SaveVSphere(p, false)
	if _, ok := s.RememberedVSphere(); ok {
		t.Fatal("mot de passe mémorisé sans le demander")
	}
	s.SaveVSphere(p, true)
	raw, _ := os.ReadFile(st.Path(vsphereFile))
	if strings.Contains(string(raw), "pa$$") {
		t.Fatal("mot de passe en clair sur disque")
	}
	got, ok := NewSettings(st).RememberedVSphere()
	if !ok || got != p {
		t.Fatalf("relu : %+v %v", got, ok)
	}
	if fi, _ := os.Stat(st.Path(secretKeyFile)); fi == nil || fi.Mode().Perm() != 0o600 {
		t.Error("clé absente ou mal protégée")
	}
	s.ForgetSecret()
	if _, ok := s.RememberedVSphere(); ok {
		t.Error("secret non oublié")
	}
	if d := s.VSphere(); d == nil || d.URL != "https://vc" {
		t.Error("URL perdue en oubliant le secret")
	}
}

func TestResultsAPIAndBackfill(t *testing.T) {
	st, jm, do := monitorServer(t)
	r := do("GET", "/api/results?period=7d", "")
	if r.Code != http.StatusOK || !strings.Contains(r.Body.String(), `"points": []`) {
		t.Fatalf("vide : %d %s", r.Code, r.Body)
	}
	rs := results.Open(st.Dir)
	_ = rs.Append([]results.Point{{T: time.Now().Add(-time.Hour), Job: "x", Profile: "standard", Host: "esx1", Datastore: "ds", WriteMBps: 450, IOProfile: "p"},
		{T: time.Now().Add(-10 * 24 * time.Hour), Job: "y", Profile: "standard", Host: "esx2", Datastore: "ds", WriteMBps: 300}})
	var out struct {
		Points []results.Point `json:"points"`
		Series [][2]string     `json:"series"`
	}
	_ = json.Unmarshal(do("GET", "/api/results?period=7d", "").Body.Bytes(), &out)
	if len(out.Points) != 1 || len(out.Series) != 2 {
		t.Errorf("filtre période : %+v", out)
	}
	c := do("GET", "/api/results?format=csv&host=esx2", "")
	if !strings.HasPrefix(c.Header().Get("Content-Type"), "text/csv") || strings.Count(c.Body.String(), "\n") != 2 {
		t.Errorf("CSV : %s", c.Body)
	}
	if r := do("GET", "/api/results?period=abc", ""); r.Code != http.StatusBadRequest {
		t.Errorf("période invalide : %d", r.Code)
	}
	// un job terminé alimente l'historique (OnDone)
	j, _ := jm.Submit("net.iperf", nil, "ui", func(ctx context.Context, r *jobs.Reporter) (any, error) { return map[string]int{"x": 1}, nil })
	jm.Wait(j.ID)
	time.Sleep(50 * time.Millisecond)
	if all, _ := rs.Query(results.Filter{}); len(all) != 2 {
		t.Errorf("un job iperf ne doit pas ajouter de mesure : %d", len(all))
	}
}

func TestJobsScheduledRetention(t *testing.T) {
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	jm := jobs.NewManager(st)
	j, _ := jm.Submit("local", nil, "schedule", func(ctx context.Context, r *jobs.Reporter) (any, error) { return nil, nil })
	jm.Wait(j.ID)
	if !strings.HasSuffix(j.ID, "s") {
		t.Errorf("ID de job planifié : %s", j.ID)
	}
	if _, ok := jm.Get(j.ID); !ok {
		t.Error("job planifié introuvable")
	}
}

func TestThresholdsNewKeysKeepDefaults(t *testing.T) {
	defer analysis.SetOverrides(nil)
	st, err := store.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// seuils enregistrés avant la phase 10 : sans rx_missed_warn
	_ = st.WriteBytes(thresholdsFile, []byte(`{"hybride":{"preset":"hybride","tx_ratio_warn":1.2,"tx_ratio_alert":1.3}}`), 0o600)
	NewSettings(st).LoadThresholds()
	h := analysis.Presets()["hybride"]
	if h.TxRatioWarn != 1.2 || h.RxMissedWarn != 1000 || h.WriteMBpsWarn != 50 {
		t.Errorf("seuils fusionnés : %+v", h)
	}
}
