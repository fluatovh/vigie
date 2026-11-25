package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"

	"vigie/internal/netdiag"
)

func networkItems() []Item {
	return []Item{
		{"Ping", newPingView()},
		{"Traceroute", newTraceView()},
		{"Surveillance", &monitorView{}},
		{"Table ARP", &arpView{}},
		{"Débit iperf3", newIperfView()},
		{"Pairs (iperf3)", &peersView{}},
		{"Interfaces", &ifaceView{}},
	}
}

// ---------- traceroute (partagé : vue dédiée et traceroute automatique du ping) ----------

type tracer struct {
	target  string
	ip      string
	hops    []netdiag.Hop
	running bool
	done    bool
	reached bool
	err     string
	cancel  context.CancelFunc
	gen     int
}

func (t *tracer) start(a *App, target string) {
	t.stop()
	g := t.gen + 1
	*t = tracer{target: target, running: true, gen: g}
	ctx, cancel := context.WithCancel(context.Background())
	t.cancel = cancel
	go func() {
		err := a.C.Stream(ctx, "/net/traceroute/stream?target="+url.QueryEscape(target), func(ev string, data []byte) {
			a.Post(func() {
				if t.gen != g {
					return
				}
				switch ev {
				case "start":
					var v struct{ IP string }
					_ = json.Unmarshal(data, &v)
					t.ip = v.IP
				case "hop":
					var h struct {
						TTL     int        `json:"ttl"`
						Addr    string     `json:"addr"`
						Name    string     `json:"name"`
						RTTs    []*float64 `json:"rtts_ms"`
						Reached bool       `json:"reached"`
					}
					if json.Unmarshal(data, &h) == nil {
						hop := netdiag.Hop{TTL: h.TTL, Addr: h.Addr, Name: h.Name, Reached: h.Reached}
						for _, r := range h.RTTs {
							if r == nil || *r < 0 {
								hop.RTTs = append(hop.RTTs, math.NaN())
							} else {
								hop.RTTs = append(hop.RTTs, *r)
							}
						}
						t.hops = append(t.hops, hop)
					}
				case "error":
					var e struct{ Error string }
					_ = json.Unmarshal(data, &e)
					t.err = e.Error
				case "done":
					var d struct{ Reached bool }
					_ = json.Unmarshal(data, &d)
					t.reached, t.done = d.Reached, true
				}
			})
		})
		a.Post(func() {
			if t.gen != g {
				return
			}
			t.running = false
			if err != nil && t.err == "" {
				t.err = err.Error()
			}
		})
	}()
}

func (t *tracer) stop() {
	if t.cancel != nil {
		t.cancel()
		t.cancel = nil
	}
}

func (t *tracer) draw(s *Screen, x, y, w, h int) {
	if h < 2 {
		return
	}
	state := "en cours…"
	if t.done {
		state = "cible atteinte"
		if !t.reached {
			state = "cible non atteinte"
		}
	}
	if t.err != "" {
		state = "erreur : " + t.err
	}
	s.Text(x, y, fmt.Sprintf("Traceroute vers %s (%s) : %s", t.target, t.ip, state), stAccent, w)
	s.Raw(x, y+1, fmt.Sprintf("TTL  %-16s %-21s %s", T("Adresse"), T("RTT (3 sondes)"), T("Nom")), stDim, w)
	start := max(0, len(t.hops)-(h-2))
	for i, hop := range t.hops[start:] {
		var r []string
		for _, v := range hop.RTTs {
			if math.IsNaN(v) {
				r = append(r, "   *   ")
			} else {
				r = append(r, fmt.Sprintf("%7s", strings.TrimSuffix(ms(v), " ms")))
			}
		}
		addr := hop.Addr
		if addr == "" {
			addr = "*"
		}
		st := stNormal
		if hop.Reached {
			st = stOK
		}
		s.Text(x, y+2+i, fmt.Sprintf("%3d  %-15s %s  %s", hop.TTL, addr, strings.Join(r, ""), hop.Name), st, w)
	}
}

// ---------- ping ----------

type pingView struct {
	form    Form
	running bool
	target  string
	ip      string
	onLink  *bool
	rtts    []float64
	stats   *netdiag.Stats
	err     string
	cancel  context.CancelFunc
	trace   tracer
	gen     int
}

func newPingView() *pingView {
	return &pingView{form: Form{Button: "lancer le ping", Fields: []*Field{
		{Label: "Cible", Max: 60, Accept: hostRunes, Hint: "IP ou nom"},
		{Label: "Nombre", Choices: []string{"10", "20", "50", "100", "300", "1000", "continu"}, Idx: 0},
		{Label: "Intervalle", Choices: []string{"1 s", "0,5 s", "0,2 s"}},
	}}}
}

func (v *pingView) Help() string {
	if v.running {
		return "s arrêter"
	}
	return "Tab champ  flèches choix  Entrée lancer"
}

func (v *pingView) Leave(a *App) { v.stop() }

func (v *pingView) stop() {
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.trace.stop()
}

func (v *pingView) Key(a *App, k Key) bool {
	if v.running && (k.R == 's' || k.R == 'S' || k.Name == "Ctrl-C") {
		v.stop()
		return true
	}
	if v.running && k.Name == "Esc" {
		v.stop()
		return false
	}
	sub, used := v.form.Key(k)
	if sub {
		v.start(a)
	}
	return used
}

func (v *pingView) start(a *App) {
	target := v.form.Fields[0].Get()
	if target == "" {
		a.Status("Saisir une cible (adresse IP ou nom).", stWarn)
		return
	}
	v.stop()
	count := v.form.Fields[1].Get()
	if count == "continu" {
		count = "0"
	}
	interval := map[string]string{"1 s": "1000", "0,5 s": "500", "0,2 s": "200"}[v.form.Fields[2].Get()]
	v.running, v.target, v.ip, v.onLink, v.rtts, v.stats, v.err = true, target, "", nil, nil, nil, ""
	v.trace = tracer{gen: v.trace.gen}
	v.gen++
	g := v.gen
	ctx, cancel := context.WithCancel(context.Background())
	v.cancel = cancel
	path := "/net/ping/stream?target=" + url.QueryEscape(target) + "&count=" + count + "&interval_ms=" + interval
	go func() {
		err := a.C.Stream(ctx, path, func(ev string, data []byte) {
			a.Post(func() {
				if v.gen != g {
					return
				}
				switch ev {
				case "start":
					var s struct {
						IP     string `json:"ip"`
						OnLink bool   `json:"on_link"`
					}
					_ = json.Unmarshal(data, &s)
					v.ip, v.onLink = s.IP, &s.OnLink
					if !s.OnLink {
						v.trace.start(a, target) // hors du sous-réseau : traceroute automatique
					}
				case "reply":
					var r struct {
						RTT float64 `json:"rtt_ms"`
					}
					_ = json.Unmarshal(data, &r)
					if r.RTT < 0 {
						r.RTT = math.NaN()
					}
					v.rtts = append(v.rtts, r.RTT)
					if len(v.rtts) > 3600 {
						v.rtts = v.rtts[len(v.rtts)-3600:]
					}
				case "error":
					var e struct{ Error string }
					_ = json.Unmarshal(data, &e)
					v.err = e.Error
				case "done":
					var d struct{ Stats netdiag.Stats }
					_ = json.Unmarshal(data, &d)
					v.stats = &d.Stats
				}
			})
		})
		a.Post(func() {
			if v.gen != g {
				return
			}
			v.running = false
			if err != nil && v.err == "" {
				v.err = err.Error()
			}
		})
	}()
}

func (v *pingView) Draw(a *App, s *Screen, x, y, w, h int) {
	n := v.form.Draw(s, x, y, w, a.Focus)
	row := y + n + 1
	if v.target == "" {
		Paragraph(s, x, row, w, "Ping ICMP en direct : RTT de chaque réponse, perte, gigue. Hors du sous-réseau de l'appliance, un traceroute est lancé automatiquement.", stDim)
		return
	}
	st := netdiag.Compute(v.rtts)
	if v.stats != nil {
		st = *v.stats
	}
	head := fmt.Sprintf("%s (%s)", v.target, v.ip)
	if v.onLink != nil {
		if *v.onLink {
			head += " — même sous-réseau"
		} else {
			head += " — routé"
		}
	}
	if v.running {
		head += " — en cours"
	}
	s.Text(x, row, head, stAccent, w)
	lossSt := stOK
	if st.LossPct > 0 {
		lossSt = stWarn
	}
	if st.LossPct >= 5 {
		lossSt = stAlert
	}
	c := s.Text(x, row+1, fmt.Sprintf("%d/%d reçus  ", st.Recv, st.Sent), stNormal, w)
	c += s.Text(x+c, row+1, "perte "+pct(st.LossPct), lossSt, w-c)
	s.Text(x+c, row+1, "  gigue "+ms(st.Jitter), stNormal, w-c)
	if v.err != "" {
		s.Text(x, row+2, "Erreur : "+v.err, stAlert, w)
	} else if st.Recv > 0 {
		s.Text(x, row+2, fmt.Sprintf("RTT min %s  médiane %s  max %s", ms(st.Min), ms(st.Median), ms(st.Max)), stNormal, w)
	}
	chartH := 4 // routé : place pour le traceroute
	if v.onLink == nil || *v.onLink {
		chartH = h - (row - y) - 5
	}
	BarChart(s, x, row+3, w, chartH, v.rtts, "ms", stChart)
	if v.onLink != nil && !*v.onLink {
		ty := row + 3 + chartH + 1
		v.trace.draw(s, x, ty, w, y+h-ty)
	}
}

// ---------- traceroute ----------

type traceView struct {
	form  Form
	trace tracer
}

func newTraceView() *traceView {
	return &traceView{form: Form{Button: "lancer le traceroute", Fields: []*Field{{Label: "Cible", Max: 60, Accept: hostRunes, Hint: "IP ou nom"}}}}
}

func (v *traceView) Help() string { return "Entrée lancer  s arrêter" }
func (v *traceView) Leave(a *App) { v.trace.stop() }

func (v *traceView) Key(a *App, k Key) bool {
	if k.R == 's' && v.trace.running {
		v.trace.stop()
		return true
	}
	sub, used := v.form.Key(k)
	if sub {
		if t := v.form.Fields[0].Get(); t != "" {
			v.trace.start(a, t)
		}
	}
	return used
}

func (v *traceView) Draw(a *App, s *Screen, x, y, w, h int) {
	n := v.form.Draw(s, x, y, w, a.Focus)
	if v.trace.target == "" {
		Paragraph(s, x, y+n+1, w, "Échos ICMP à TTL croissant, 3 sondes par saut, 30 sauts au maximum, avec résolution inverse des noms.", stDim)
		return
	}
	v.trace.draw(s, x, y+n+1, w, h-n-1)
}

// ---------- surveillance (smokeping) ----------

var monRanges = []string{"3h", "12h", "48h", "7d", "14d", "1y"}

type monitorView struct {
	targets []netdiag.Target
	list    List
	graph   bool
	rng     int
	points  []netdiag.Point
	stepS   int64
	adding  *Form
	loaded  bool
	tick    int
}

func (v *monitorView) Help() string {
	switch {
	case v.adding != nil:
		return "Entrée ajouter  Échap annuler"
	case v.graph:
		return "gauche/droite période  Échap liste"
	}
	return "a ajouter  Entrée graph  Suppr retirer"
}

func (v *monitorView) Enter(a *App) { v.load(a) }

func (v *monitorView) Tick(a *App) {
	v.tick++
	if v.tick%15 == 0 {
		v.load(a)
	}
}

func (v *monitorView) load(a *App) {
	a.Go(func() (any, error) {
		var r struct{ Targets []netdiag.Target }
		err := a.C.Get("/net/targets", &r)
		return r.Targets, err
	}, func(x any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		v.targets, v.loaded = x.([]netdiag.Target), true
		v.list.Clamp(len(v.targets))
		if v.graph {
			v.loadSeries(a)
		}
	})
}

func (v *monitorView) loadSeries(a *App) {
	if len(v.targets) == 0 {
		return
	}
	id := v.targets[v.list.Sel].ID
	a.Go(func() (any, error) {
		var r struct {
			StepS  int64           `json:"step_s"`
			Points []netdiag.Point `json:"points"`
		}
		err := a.C.Get("/net/targets/"+url.PathEscape(id)+"/series?range="+monRanges[v.rng], &r)
		return r, err
	}, func(x any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		r := x.(struct {
			StepS  int64           `json:"step_s"`
			Points []netdiag.Point `json:"points"`
		})
		v.points, v.stepS = r.Points, r.StepS
	})
}

func (v *monitorView) Key(a *App, k Key) bool {
	if v.adding != nil {
		if k.Name == "Esc" {
			v.adding = nil
			return true
		}
		sub, used := v.adding.Key(k)
		if sub {
			host, label := v.adding.Fields[0].Get(), v.adding.Fields[1].Get()
			v.adding = nil
			a.Go(func() (any, error) {
				return nil, a.C.Post("/net/targets", map[string]string{"host": host, "label": label}, nil)
			},
				func(_ any, err error) {
					if err != nil {
						a.Error(err)
						return
					}
					a.Status("Cible "+host+" ajoutée : 20 pings par minute.", stOK)
					v.load(a)
				})
		}
		return used
	}
	if v.graph {
		switch k.Name {
		case "Esc":
			v.graph = false
			return true
		case "Left":
			v.rng = (v.rng + len(monRanges) - 1) % len(monRanges)
			v.loadSeries(a)
			return true
		case "Right":
			v.rng = (v.rng + 1) % len(monRanges)
			v.loadSeries(a)
			return true
		}
		return false
	}
	if v.list.Key(k, len(v.targets), 10) {
		return true
	}
	switch {
	case k.R == 'a' || k.R == 'A' || k.Name == "Insert":
		v.adding = &Form{Button: "ajouter à la surveillance", Fields: []*Field{{Label: "Cible", Max: 60, Accept: hostRunes}, {Label: "Libellé", Max: 30}}}
		return true
	case k.Name == "Enter" && len(v.targets) > 0:
		v.graph, v.points = true, nil
		v.loadSeries(a)
		return true
	case (k.Name == "Delete" || k.R == 'd') && len(v.targets) > 0:
		t := v.targets[v.list.Sel]
		a.Confirm("Retirer la cible", []string{"Arrêter la surveillance de " + t.Host + " ?", "Son historique sera effacé."}, func() {
			a.Go(func() (any, error) { return nil, a.C.Delete("/net/targets/"+url.PathEscape(t.ID), nil) }, func(_ any, err error) {
				a.Error(err)
				v.load(a)
			})
		})
		return true
	case k.R == 'r':
		v.load(a)
		return true
	}
	return false
}

func (v *monitorView) Draw(a *App, s *Screen, x, y, w, h int) {
	if v.adding != nil {
		s.Text(x, y, "Nouvelle cible de surveillance (20 pings par minute, historique d'un an)", stAccent, w)
		v.adding.Draw(s, x, y+2, w, true)
		return
	}
	if v.graph && len(v.targets) > 0 {
		v.drawGraph(s, x, y, w, h)
		return
	}
	s.Raw(x, y, fmt.Sprintf("%-17s %-15s %7s %8s %8s", T("Cible"), "IP", T("Perte"), T("Médiane"), "p90"), stDim, w)
	if v.loaded && len(v.targets) == 0 {
		Paragraph(s, x, y+2, w, "Aucune cible. a : ajouter une adresse à surveiller (20 pings par minute, consolidation 1 min / 5 min / 1 h sur un an).", stDim)
		return
	}
	start, end := v.list.Window(h-2, len(v.targets))
	for i := start; i < end; i++ {
		t := v.targets[i]
		name := t.Host
		if t.Label != "" {
			name = t.Label
		}
		loss, med, p90 := "—", "—", "—"
		st := stNormal
		if p := t.Last; p != nil && p.Sent > 0 {
			l := 100 * float64(p.Sent-p.Recv) / float64(p.Sent)
			loss = pct(l)
			if p.Recv > 0 {
				med, p90 = ms(p.Med), ms(p.P90)
			}
			switch {
			case l >= 5:
				st = stAlert
			case l > 0:
				st = stWarn
			default:
				st = stOK
			}
		}
		if t.LastErr != "" {
			loss, st = T("erreur"), stAlert
		}
		row := y + 1 + i - start
		ip := t.LastIP
		if ip == "" {
			ip = t.Host
		}
		txt := fmt.Sprintf("%-17.17s %-15.15s %7s %8s %8s", name, ip, loss, strings.TrimSuffix(med, " ms"), strings.TrimSuffix(p90, " ms"))
		if i == v.list.Sel && a.Focus {
			s.Fill(x, row, w, 1, ' ', stSel)
			s.Text(x, row, txt, stSel, w)
		} else {
			s.Text(x, row, txt, st, w)
		}
	}
}

func (v *monitorView) drawGraph(s *Screen, x, y, w, h int) {
	t := v.targets[v.list.Sel]
	title := t.Host
	if t.Label != "" {
		title = t.Label + " (" + t.Host + ")"
	}
	rx := x + w - 6*len(monRanges)
	s.Text(x, y, title, stAccent, rx-x-1)
	for i, r := range monRanges {
		st := stNormal
		if i == v.rng {
			st = stTabOn
		}
		s.Text(rx+6*i, y, " "+r+" ", st, 0)
	}
	pts := make([]SmokePoint, len(v.points))
	var sent, recv int
	var meds []float64
	for i, p := range v.points {
		pts[i] = SmokePoint{Sent: p.Sent, Recv: p.Recv, P10: p.P10, Med: p.Med, P90: p.P90, Max: p.Max}
		sent += p.Sent
		recv += p.Recv
		if p.Recv > 0 {
			meds = append(meds, p.Med)
		}
	}
	SmokeChart(s, x, y+2, w, h-6, pts)
	loss := 0.0
	if sent > 0 {
		loss = 100 * float64(sent-recv) / float64(sent)
	}
	step := "—"
	if v.stepS > 0 {
		step = strconv.FormatInt(v.stepS/60, 10) + " min"
		if v.stepS < 60 {
			step = strconv.FormatInt(v.stepS, 10) + " s"
		}
	}
	s.Text(x, y+h-3, fmt.Sprintf("1 point = %s ; %d pings, perte %s, médiane %s", step, sent, pct(loss), ms(median(meds))), stNormal, w)
	c := s.Text(x, y+h-2, "■", stOK, 1)
	c += s.Text(x+c, y+h-2, " sans perte ", stDim, 0)
	c += s.Text(x+c, y+h-2, "■", stWarn, 1)
	c += s.Text(x+c, y+h-2, " perte < 5 % ", stDim, 0)
	c += s.Text(x+c, y+h-2, "■", stAlert, 1)
	c += s.Text(x+c, y+h-2, " ≥ 5 % ", stDim, 0)
	s.Text(x+c, y+h-2, "░ p10-p90  x perdu", stDim, w-c)
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return math.NaN()
	}
	c := append([]float64(nil), v...)
	for i := 1; i < len(c); i++ {
		for j := i; j > 0 && c[j] < c[j-1]; j-- {
			c[j], c[j-1] = c[j-1], c[j]
		}
	}
	return c[len(c)/2]
}

// ---------- ARP ----------

type arpView struct {
	entries []netdiag.ARPEntry
	list    List
	tick    int
}

func (v *arpView) Help() string { return "Suppr supprimer  v vider  r rafraîchir" }
func (v *arpView) Enter(a *App) { v.load(a) }
func (v *arpView) Tick(a *App) {
	v.tick++
	if v.tick%5 == 0 {
		v.load(a)
	}
}

func (v *arpView) load(a *App) {
	a.Go(func() (any, error) {
		var r struct{ Entries []netdiag.ARPEntry }
		err := a.C.Get("/net/arp", &r)
		return r.Entries, err
	}, func(x any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		v.entries = x.([]netdiag.ARPEntry)
		v.list.Clamp(len(v.entries))
	})
}

func (v *arpView) Key(a *App, k Key) bool {
	if v.list.Key(k, len(v.entries), 10) {
		return true
	}
	switch {
	case (k.Name == "Delete" || k.R == 'd') && len(v.entries) > 0:
		e := v.entries[v.list.Sel]
		a.Confirm("Supprimer une entrée ARP", []string{"Supprimer " + e.IP + " (" + e.MAC + ") ?", "Elle sera réapprise au prochain échange."}, func() {
			a.Go(func() (any, error) { return nil, a.C.Delete("/net/arp/"+url.PathEscape(e.IP), nil) }, func(_ any, err error) {
				if err == nil {
					a.Status("Entrée "+e.IP+" supprimée.", stOK)
				}
				a.Error(err)
				v.load(a)
			})
		})
		return true
	case k.R == 'v' || k.R == 'V':
		a.Confirm("Vider la table ARP", []string{"Supprimer toutes les entrées dynamiques ?"}, func() {
			a.Go(func() (any, error) { return nil, a.C.Delete("/net/arp", nil) }, func(_ any, err error) {
				a.Error(err)
				v.load(a)
			})
		})
		return true
	case k.R == 'r':
		v.load(a)
		return true
	}
	return false
}

func (v *arpView) Draw(a *App, s *Screen, x, y, w, h int) {
	s.Raw(x, y, fmt.Sprintf("%-16s %-18s %-7s %s", "IP", "MAC", T("Interf."), T("État")), stDim, w)
	start, end := v.list.Window(h-1, len(v.entries))
	for i := start; i < end; i++ {
		e := v.entries[i]
		txt := fmt.Sprintf("%-16s %-18s %-7s %s", e.IP, e.MAC, e.Device, T(e.State))
		row := y + 1 + i - start
		if i == v.list.Sel && a.Focus {
			s.Fill(x, row, w, 1, ' ', stSel)
			s.Text(x, row, txt, stSel, w)
		} else {
			s.Text(x, row, txt, stNormal, w)
		}
	}
	if len(v.entries) == 0 {
		s.Text(x, y+2, "Table ARP vide.", stDim, w)
	}
}

// ---------- iperf3 ----------

type iperfView struct {
	form   Form
	peers  []netdiag.Peer
	job    follower
	ivals  []float64
	result *netdiag.IperfResult
}

func newIperfView() *iperfView {
	return &iperfView{form: Form{Button: "lancer le test de débit", LabelW: 12, Fields: []*Field{
		{Label: "Vers", Choices: []string{"(aucun pair)"}},
		{Label: "Hôte", Max: 60, Accept: hostRunes, Hint: "si « autre serveur »"},
		{Label: "Protocole", Choices: []string{"tcp", "udp"}},
		{Label: "Durée", Choices: []string{"10 s", "30 s", "60 s", "5 s"}},
		{Label: "Flux", Choices: []string{"1", "2", "4", "8"}},
		{Label: "Sens", Choices: []string{"l'appliance émet", "le pair émet (-R)"}},
		{Label: "Débit UDP", Choices: []string{"1G", "500M", "100M", "5G", "10G"}},
	}}}
}

func (v *iperfView) Help() string {
	if v.job.Running() {
		return "x annuler"
	}
	return "Tab champ  flèches choix  Entrée lancer"
}

func (v *iperfView) Enter(a *App) {
	a.Go(func() (any, error) {
		var r struct{ Peers []netdiag.Peer }
		err := a.C.Get("/net/peers", &r)
		return r.Peers, err
	}, func(x any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		v.peers = x.([]netdiag.Peer)
		ch := []string{}
		for _, p := range v.peers {
			ch = append(ch, p.Name)
		}
		ch = append(ch, "autre serveur iperf3")
		f := v.form.Fields[0]
		if f.Idx >= len(ch) {
			f.Idx = 0
		}
		f.Choices = ch
	})
}

func (v *iperfView) Key(a *App, k Key) bool {
	if v.job.Running() {
		if k.R == 'x' {
			a.cancelJob(&v.job)
			return true
		}
		return false
	}
	sub, used := v.form.Key(k)
	if sub {
		f := v.form.Fields
		req := map[string]any{"profile": "net.iperf", "protocol": f[2].Get(), "duration_s": atoi(strings.TrimSuffix(f[3].Get(), " s")),
			"streams": atoi(f[4].Get()), "reverse": f[5].Idx == 1}
		if f[2].Get() == "udp" {
			req["bitrate"] = f[6].Get()
		}
		if f[0].Idx < len(v.peers) {
			req["peer"] = v.peers[f[0].Idx].ID
		} else if h := f[1].Get(); h != "" {
			req["host"] = h
		} else {
			a.Status("Choisir un pair (Pairs) ou saisir l'hôte d'un serveur iperf3.", stWarn)
			return true
		}
		v.ivals, v.result = nil, nil
		a.submitJob(req, &v.job, nil, func(typ string, data json.RawMessage) {
			if typ == "iperf" {
				var iv netdiag.Interval
				if json.Unmarshal(data, &iv) == nil {
					v.ivals = append(v.ivals, iv.Mbps)
				}
			}
		})
	}
	return used
}

func (v *iperfView) Leave(a *App) { v.job.stop() }

func (v *iperfView) Draw(a *App, s *Screen, x, y, w, h int) {
	n := v.form.Draw(s, x, y, w, a.Focus && !v.job.Running())
	row := y + n + 1
	if v.job.ID == "" {
		if len(v.peers) == 0 {
			Paragraph(s, x, row, w, "Aucun pair : en ajouter un dans « Pairs » (une autre appliance vigie, qui ouvre son serveur iperf3 pour le test), ou viser un serveur iperf3 quelconque.", stDim)
		}
		return
	}
	if v.job.Result != nil && v.result == nil {
		var r netdiag.IperfResult
		if json.Unmarshal(v.job.Result, &r) == nil {
			v.result = &r
		}
	}
	s.Text(x, row, fmt.Sprintf("iperf3 : %s — %s", stateFR(v.job.State), v.job.Step), stateStyle(v.job.State), w)
	if v.job.Err != "" {
		s.Text(x, row+1, v.job.Err, stAlert, w)
	}
	chartH := max(3, y+h-row-9)
	BarChart(s, x, row+2, w, chartH, v.ivals, "Mbit/s", stChart)
	if r := v.result; r != nil && r.Error == "" {
		ry := row + 4 + chartH
		s.Text(x, ry, fmt.Sprintf("Émis %s Mbit/s, reçu %s Mbit/s, retransmissions %d", num(r.SentMbps), num(r.RecvMbps), r.Retransmits), stOK, w)
		s.Text(x, ry+1, fmt.Sprintf("Gigue %s, perte %s, CPU émetteur %s, récepteur %s", ms(r.JitterMs), pct(r.LostPct), pct(r.SenderCPU), pct(r.ReceiverCPU)), stNormal, w)
		sens := "l'appliance émet"
		if r.Options.Reverse {
			sens = "le pair émet (-R)"
		}
		prof := fmt.Sprintf("Profil : %s, %s, %s, %d flux, %d s", r.Version, r.Options.Protocol, sens, r.Options.Streams, r.Options.Duration)
		if r.Options.Protocol == "udp" {
			prof += ", cible " + r.Options.Bitrate
		}
		Paragraph(s, x, ry+2, w, prof, stDim)
	}
}

func atoi(s string) int { n, _ := strconv.Atoi(strings.TrimSpace(s)); return n }

// ---------- pairs ----------

type peersView struct {
	peers  []netdiag.Peer
	server netdiag.ServerStatus
	list   List
	adding *Form
}

func (v *peersView) Help() string {
	if v.adding != nil {
		return "Entrée vérifier l'empreinte  Échap annuler"
	}
	return "a ajouter  Suppr retirer  s serveur iperf3"
}

func (v *peersView) Enter(a *App) { v.load(a) }

func (v *peersView) load(a *App) {
	a.Go(func() (any, error) {
		var r struct{ Peers []netdiag.Peer }
		var st netdiag.ServerStatus
		err := a.C.Get("/net/peers", &r)
		if err == nil {
			err = a.C.Get("/net/iperf/server", &st)
		}
		return []any{r.Peers, st}, err
	}, func(x any, err error) {
		if err != nil {
			a.Error(err)
			return
		}
		r := x.([]any)
		v.peers, v.server = r[0].([]netdiag.Peer), r[1].(netdiag.ServerStatus)
		v.list.Clamp(len(v.peers))
	})
}

func (v *peersView) Key(a *App, k Key) bool {
	if v.adding != nil {
		if k.Name == "Esc" {
			v.adding = nil
			return true
		}
		sub, used := v.adding.Key(k)
		if sub {
			f := v.adding.Fields
			name, u, tok := f[0].Get(), f[1].Get(), f[2].Get()
			if !strings.Contains(u, "://") {
				u = "https://" + u
			}
			a.Go(func() (any, error) {
				var r struct{ SHA256 string }
				err := a.C.Get("/net/peers/fingerprint?url="+url.QueryEscape(u), &r)
				return r.SHA256, err
			}, func(x any, err error) {
				if err != nil {
					a.Error(err)
					return
				}
				fp := x.(string)
				a.Confirm("Empreinte du pair", []string{"Certificat présenté par " + u + " :", fp[:min(len(fp), 47)], fp[min(len(fp), 48):], "Est-ce bien celle affichée sur la console du pair ?"}, func() {
					a.Go(func() (any, error) {
						return nil, a.C.Post("/net/peers", map[string]string{"name": name, "url": u, "token": tok, "fingerprint": fp}, nil)
					}, func(_ any, err error) {
						if err != nil {
							a.Error(err)
							return
						}
						v.adding = nil
						a.Status("Pair "+name+" ajouté.", stOK)
						v.load(a)
					})
				})
			})
		}
		return used
	}
	if v.list.Key(k, len(v.peers), 10) {
		return true
	}
	switch {
	case k.R == 'a' || k.Name == "Insert":
		v.adding = &Form{Button: "vérifier l'empreinte puis ajouter", LabelW: 10, Fields: []*Field{
			{Label: "Nom", Max: 30}, {Label: "Adresse", Max: 60, Hint: "https://IP du pair"}, {Label: "Jeton", Max: 80, Hint: "vg_… créé sur le pair"}}}
		return true
	case (k.Name == "Delete" || k.R == 'd') && len(v.peers) > 0:
		p := v.peers[v.list.Sel]
		a.Confirm("Retirer le pair", []string{"Oublier le pair " + p.Name + " (" + p.URL + ") ?"}, func() {
			a.Go(func() (any, error) { return nil, a.C.Delete("/net/peers/"+url.PathEscape(p.ID), nil) }, func(_ any, err error) {
				a.Error(err)
				v.load(a)
			})
		})
		return true
	case k.R == 's':
		if v.server.Running {
			a.Go(func() (any, error) { return nil, a.C.Delete("/net/iperf/server", nil) }, func(_ any, err error) { a.Error(err); v.load(a) })
		} else {
			a.Go(func() (any, error) { return nil, a.C.Post("/net/iperf/server", map[string]int{"duration_s": 600}, nil) }, func(_ any, err error) {
				if err == nil {
					a.Status("Serveur iperf3 ouvert pour 10 minutes (port 5201).", stOK)
				}
				a.Error(err)
				v.load(a)
			})
		}
		return true
	}
	return false
}

func (v *peersView) Draw(a *App, s *Screen, x, y, w, h int) {
	if v.adding != nil {
		s.Text(x, y, "Nouveau pair : une autre appliance vigie", stAccent, w)
		v.adding.Draw(s, x, y+2, w, true)
		Paragraph(s, x, y+8, w, "Sur le pair : Réglages > Jetons d'API, créer un jeton. L'empreinte du certificat du pair est affichée sur sa console ; elle est vérifiée avant l'ajout puis épinglée.", stDim)
		return
	}
	srv := "serveur iperf3 local : fermé (s : l'ouvrir 10 min)"
	st := stNormal
	if v.server.Running {
		srv, st = fmt.Sprintf("serveur iperf3 local : ouvert sur le port %d jusqu'à %s UTC (s : fermer)", v.server.Port, v.server.Until.UTC().Format("15:04")), stOK
	}
	s.Text(x, y, srv, st, w)
	s.Raw(x, y+2, fmt.Sprintf("%-14s %-24s %s", T("Nom"), T("Adresse"), T("Empreinte")), stDim, w)
	for i, p := range v.peers {
		if i >= h-4 {
			break
		}
		fp := p.Fingerprint
		if len(fp) > 17 {
			fp = fp[:17] + "…"
		}
		txt := fmt.Sprintf("%-14.14s %-24.24s %s", p.Name, p.URL, fp)
		if i == v.list.Sel && a.Focus {
			s.Fill(x, y+3+i, w, 1, ' ', stSel)
			s.Text(x, y+3+i, txt, stSel, w)
		} else {
			s.Text(x, y+3+i, txt, stNormal, w)
		}
	}
	if len(v.peers) == 0 {
		s.Text(x, y+4, "Aucun pair. a : ajouter une autre appliance.", stDim, w)
	}
}

// ---------- interfaces ----------

type ifaceView struct{}

func (v *ifaceView) Help() string           { return "" }
func (v *ifaceView) Key(a *App, k Key) bool { return false }
func (v *ifaceView) Enter(a *App)           { a.refreshInfo() }

func (v *ifaceView) Draw(a *App, s *Screen, x, y, w, h int) {
	s.Raw(x, y, fmt.Sprintf("%-7s %-19s %-18s %5s %s", T("Interf."), T("Adresse"), "MAC", "MTU", T("État")), stDim, w)
	row := y + 1
	for _, i := range a.Local.Ifaces {
		st, up := stNormal, T("bas")
		if i.Up {
			up = T("actif")
		}
		s.Text(x, row, fmt.Sprintf("%-7s %-19s %-18s %5d %s", i.Name, i.CIDR, i.MAC, i.MTU, up), st, w)
		row++
	}
	row++
	gw := a.Local.Gateway
	if gw == "" {
		gw = "aucune"
	}
	s.Text(x, row, "Passerelle : "+gw, stNormal, w)
	s.Text(x, row+1, "DNS        : "+strings.Join(a.Local.DNS, ", "), stNormal, w)
	Paragraph(s, x, row+3, w, "Adressage par DHCP ou fixe : propriétés OVF du déploiement, ou onglet Système > Réseau (IP).", stDim)
}
