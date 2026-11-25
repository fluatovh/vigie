// Package web sert l'API HTTP et, à partir de la phase 7, le front embarqué.
package web

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"vigie/internal/analysis"
	"vigie/internal/auth"
	"vigie/internal/jobs"
	"vigie/internal/netdiag"
	"vigie/internal/report"
	"vigie/internal/results"
	"vigie/internal/sysconf"
	"vigie/internal/testdisk"
	"vigie/internal/vsphere"
)

// Info décrit le binaire en cours d'exécution.
type Info struct {
	Version   string
	Commit    string
	BuildDate string
	Started   time.Time
}

// Deps sont les dépendances du serveur ; les champs nil désactivent la fonction correspondante
// (utile dans les tests).
type Deps struct {
	Auth           Verifier
	VSphere        *vsphere.Manager
	Settings       *Settings
	TestDisk       *testdisk.Service
	Jobs           *jobs.Manager
	Tokens         *auth.Tokens
	TLSFingerprint string
	NetMonitor     *netdiag.Monitor
	Peers          *netdiag.Peers
	SysConf        *sysconf.System // configuration de l'appliance depuis la console (phase D)
	// Background : si non nil, lance la surveillance continue (planificateur, rétention,
	// reconnexion vCenter) jusqu'à son annulation. nil dans la plupart des tests.
	Background context.Context
}

// Server regroupe les handlers de l'API.
type Server struct {
	mux      *http.ServeMux
	info     Info
	now      func() time.Time
	auth     Verifier
	vs       *vsphere.Manager
	settings *Settings
	testdisk *testdisk.Service
	jobs     *jobs.Manager
	tokens   *auth.Tokens
	sess     *sessions
	tlsFP    string
	netMon   *netdiag.Monitor
	peers    *netdiag.Peers
	iperf    *netdiag.Server
	sys      *sysconf.System
	results  *results.Store
	schedule scheduler

	selfMu    sync.Mutex
	selfCache *vsphere.Self
	selfSess  *vsphere.Session
	selfAt    time.Time
}

// New construit le handler HTTP racine.
func New(info Info, deps Deps) http.Handler {
	s := &Server{mux: http.NewServeMux(), info: info, now: time.Now, auth: deps.Auth, vs: deps.VSphere, settings: deps.Settings, testdisk: deps.TestDisk, jobs: deps.Jobs, tokens: deps.Tokens, sess: newSessions(), tlsFP: deps.TLSFingerprint,
		netMon: deps.NetMonitor, peers: deps.Peers, iperf: &netdiag.Server{}, sys: deps.SysConf}
	if s.netMon == nil {
		s.netMon = netdiag.NewMonitor(nil)
	}
	if s.peers == nil {
		s.peers = netdiag.NewPeers(nil)
	}
	if s.jobs == nil {
		s.jobs = jobs.NewManager(nil)
	}
	if s.vs == nil {
		s.vs = vsphere.NewManager()
	}
	s.jobs.Summarize = report.Summarize
	if s.settings != nil && s.settings.store != nil {
		s.results = results.Open(s.settings.store.Dir)
		s.settings.LoadThresholds()
	}
	s.jobs.OnDone = s.recordResults
	s.loadSchedule()
	if deps.Background != nil {
		go s.background(deps.Background)
	}
	s.mux.HandleFunc("/api/health", s.health)
	s.mux.HandleFunc("POST /api/login", s.login)
	s.mux.HandleFunc("POST /api/logout", s.logout)
	s.mux.Handle("/", s.static())

	api := http.NewServeMux()
	api.HandleFunc("GET /api/vsphere/thumbprint", s.vsphereThumbprint)
	api.HandleFunc("POST /api/vsphere/connect", s.vsphereConnect)
	api.HandleFunc("GET /api/vsphere/status", s.vsphereStatus)
	api.HandleFunc("POST /api/vsphere/disconnect", s.vsphereDisconnect)
	api.HandleFunc("GET /api/self", s.self)
	api.HandleFunc("GET /api/datastores", s.datastores)
	api.HandleFunc("GET /api/hosts", s.hosts)
	api.HandleFunc("GET /api/path", s.path)
	api.HandleFunc("GET /api/privileges", s.privileges)
	api.HandleFunc("GET /api/migrate/check", s.migrateCheck)
	api.HandleFunc("GET /api/testdisk", s.testdiskStatus)
	api.HandleFunc("GET /api/localdisk", s.localStatus)
	api.HandleFunc("POST /api/localdisk/prepare", s.localPrepare)
	api.HandleFunc("DELETE /api/localdisk/prepare", s.localCancel)
	api.HandleFunc("POST /api/localdisk/scan", s.localScan)
	api.HandleFunc("POST /api/localdisk/adopt", s.localAdopt)
	api.HandleFunc("POST /api/localdisk/release", s.localRelease)
	api.HandleFunc("POST /api/jobs", s.jobsSubmit)
	api.HandleFunc("GET /api/jobs", s.jobsList)
	api.HandleFunc("GET /api/jobs/{id}", s.jobsGet)
	api.HandleFunc("GET /api/jobs/{id}/events", s.jobsEvents)
	api.HandleFunc("POST /api/jobs/{id}/cancel", s.jobsCancel)
	api.HandleFunc("GET /api/net/local", s.netLocal)
	api.HandleFunc("POST /api/net/ping", s.netPing)
	api.HandleFunc("POST /api/net/traceroute", s.netTraceroute)
	api.HandleFunc("GET /api/net/ping/stream", s.netPingStream)
	api.HandleFunc("GET /api/net/traceroute/stream", s.netTracerouteStream)
	api.HandleFunc("GET /api/net/arp", s.netARP)
	api.HandleFunc("DELETE /api/net/arp", s.netARPFlush)
	api.HandleFunc("DELETE /api/net/arp/{ip}", s.netARPDelete)
	api.HandleFunc("GET /api/net/targets", s.netTargets)
	api.HandleFunc("POST /api/net/targets", s.netTargetAdd)
	api.HandleFunc("DELETE /api/net/targets/{id}", s.netTargetDelete)
	api.HandleFunc("GET /api/net/targets/{id}/series", s.netTargetSeries)
	api.HandleFunc("GET /api/net/iperf/server", s.iperfServerStatus)
	api.HandleFunc("POST /api/net/iperf/server", s.iperfServerStart)
	api.HandleFunc("DELETE /api/net/iperf/server", s.iperfServerStop)
	api.HandleFunc("GET /api/net/peers", s.peersList)
	api.HandleFunc("GET /api/net/peers/fingerprint", s.peerFingerprint)
	api.HandleFunc("POST /api/net/peers", s.peerAdd)
	api.HandleFunc("DELETE /api/net/peers/{id}", s.peerDelete)
	api.HandleFunc("GET /api/reports/compare", s.reportCompare)
	api.HandleFunc("GET /api/reports/{id}", s.reportGet)
	api.HandleFunc("GET /api/branding", s.brandingGet)
	api.HandleFunc("PUT /api/branding", s.brandingPut)
	api.HandleFunc("GET /api/about", s.about)
	api.HandleFunc("GET /api/console", s.consoleInfo)
	api.HandleFunc("GET /api/system/network", s.systemNetwork)
	api.HandleFunc("PUT /api/system/network", s.systemNetworkPut)
	api.HandleFunc("PUT /api/system/password", s.systemPassword)
	api.HandleFunc("POST /api/system/power", s.systemPower)
	api.HandleFunc("PUT /api/system/language", s.systemLanguage)
	api.HandleFunc("GET /api/presets", s.presets)
	api.HandleFunc("GET /api/thresholds", s.thresholdsGet)
	api.HandleFunc("PUT /api/thresholds", s.thresholdsPut)
	api.HandleFunc("GET /api/schedule", s.scheduleGet)
	api.HandleFunc("PUT /api/schedule", s.schedulePut)
	api.HandleFunc("GET /api/results", s.resultsQuery)
	api.HandleFunc("GET /api/tokens", s.tokensList)
	api.HandleFunc("POST /api/tokens", s.tokensCreate)
	api.HandleFunc("DELETE /api/tokens/{id}", s.tokensDelete)
	api.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeError(w, http.StatusNotFound, "route inconnue") })
	s.mux.Handle("/api/", s.requireAuth(api))
	return s.mux
}

type healthResponse struct {
	Status    string  `json:"status"`
	Version   string  `json:"version"`
	Commit    string  `json:"commit"`
	BuildDate string  `json:"build_date"`
	Hostname  string  `json:"hostname"`
	UptimeS   float64 `json:"uptime_s"`
	VSphere   bool    `json:"vsphere_connected"`
	Job       string  `json:"job_running,omitempty"`
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "méthode non autorisée")
		return
	}
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, healthResponse{
		Status:    "ok",
		Version:   s.info.Version,
		Commit:    s.info.Commit,
		BuildDate: s.info.BuildDate,
		Hostname:  host,
		UptimeS:   s.now().Sub(s.info.Started).Seconds(),
		VSphere:   s.vs.Status().Connected,
		Job:       currentJobID(s),
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func currentJobID(s *Server) string {
	if s.jobs == nil {
		return ""
	}
	if j := s.jobs.Current(); j != nil {
		return j.ID
	}
	return ""
}

func (s *Server) about(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.info.Version, "commit": s.info.Commit, "build_date": s.info.BuildDate, "hostname": host,
		"tls_fingerprint": s.tlsFP, "user": principal(r).Name,
	})
}

func (s *Server) presets(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"default": analysis.DefaultThresholds().Preset, "presets": analysis.Presets()})
}

// consoleInfo : ce que la console affiche en permanence (mot de passe compris). Réservé au socket
// console : par HTTP(S), 403.
func (s *Server) consoleInfo(w http.ResponseWriter, r *http.Request) {
	if !isConsole(r) {
		writeError(w, http.StatusForbidden, "réservé à la console de l'appliance")
		return
	}
	host, _ := os.Hostname()
	out := map[string]any{"version": s.info.Version, "hostname": host, "tls_fingerprint": s.tlsFP, "vsphere": s.vs.Status(), "language": s.settings.Language(),
		"live": isLive(), "platform": platform()}
	if s.settings != nil && s.settings.store != nil {
		if b, err := os.ReadFile(s.settings.store.Path("first-access-token")); err == nil {
			out["password"] = strings.TrimSpace(string(b))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// isLive : démarrage depuis l'ISO (vigie.live) : rien n'est conservé.
func isLive() bool {
	if _, err := os.Stat("/run/vigie/live"); err == nil {
		return true
	}
	b, _ := os.ReadFile("/proc/cmdline")
	for _, f := range strings.Fields(string(b)) {
		if f == "vigie.live" {
			return true
		}
	}
	return false
}

// platform : vmware, kvm ou other (détectée au démarrage par init.sh).
func platform() string {
	b, err := os.ReadFile("/run/vigie/platform")
	if err != nil {
		return "vmware"
	}
	return strings.TrimSpace(string(b))
}
