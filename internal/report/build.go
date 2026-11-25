package report

import (
	"fmt"
	"strings"
	"time"

	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/jobs"
	"vigie/internal/standard"
)

// Meta : informations de génération.
type Meta struct {
	Version string    // version de l'appliance qui génère
	Now     time.Time // date de génération
}

func newPage(kind string, b Branding, m Meta) *Page {
	p := &Page{Kind: kind, Brand: b, Generated: Date(m.Now), Version: m.Version, Charts: map[string]Chart{}}
	if b.Logo != "" {
		p.LogoURL = templateURL(b.Logo)
	}
	for _, para := range strings.Split(strings.ReplaceAll(b.Comment, "\r\n", "\n"), "\n\n") {
		if t := strings.TrimSpace(para); t != "" {
			p.Comment = append(p.Comment, t)
		}
	}
	return p
}

func profileTitle(kind string) string {
	switch kind {
	case "advanced":
		return "test avancé"
	case "tour":
		return "tournée de comparaison entre hôtes"
	}
	return "test standard"
}

// Build construit le rapport d'un job standard, avancé ou d'une tournée.
func Build(j *jobs.Job, b Branding, m Meta) (*Page, error) {
	r, err := load(j)
	if err != nil {
		return nil, err
	}
	p := newPage(r.kind(), b, m)
	mr := r.main()
	h := r.head()
	p.JobIDs = []string{j.ID}
	p.Title = "Rapport de performance — datastore " + mr.Datastore.Name
	p.Subject = fmt.Sprintf("Datastore %s, %s", mr.Datastore.Name, profileTitle(r.kind()))
	if r.tour == nil {
		p.Subject += ", hôte " + mr.Host.Name
	}
	p.RunDate = Date(mr.Started)

	p.Sections = append(p.Sections, synthese(r, h, p))
	p.Sections = append(p.Sections, debit(r, p))
	if r.adv != nil {
		p.Sections = append(p.Sections, iopsSection(r, h))
	}
	p.Sections = append(p.Sections, distribution(r, p))
	if r.tour != nil {
		p.Sections = append(p.Sections, hostsSection(r))
	}
	p.Sections = append(p.Sections, sante(r))
	p.Sections = append(p.Sections, methodo(r, h))
	return p, nil
}

func subjectOf(r *run) string {
	mr := r.main()
	if r.tour != nil {
		return fmt.Sprintf("le chemin entre les hôtes de la tournée et le datastore %s", mr.Datastore.Name)
	}
	return fmt.Sprintf("le chemin entre l'hôte %s et le datastore %s", mr.Host.Name, mr.Datastore.Name)
}

func tiles(r *run, h head) []Tile {
	mr := r.main()
	t := []Tile{{Value: MBps(h.Write) + " / " + MBps(h.Read), Label: "Débit écriture / lecture",
		Note: fmt.Sprintf("séquentiel, bloc %s, QD %d", BlockSize(mr.Write.Profile.BlockKiB), mr.Write.Profile.QD)}}
	switch {
	case h.Useful != nil && h.Useful.IOPS > 0:
		t = append(t, Tile{Value: IOPS(h.Useful.IOPS), Label: "IOPS utiles", Note: fmt.Sprintf("4 Kio aléatoire 70/30, p99 < %s (QD %d)", MsCeil(h.Useful.CeilingMs), h.Useful.QD)})
	case h.Useful != nil:
		t = append(t, Tile{Value: "aucune", Label: "IOPS utiles", Note: fmt.Sprintf("aucun palier sous %s de p99 ; maximum brut %s", MsCeil(h.Useful.CeilingMs), IOPS(h.Useful.RawMax))})
	default:
		t = append(t, Tile{Value: "—", Label: "IOPS utiles", Note: "non mesurées : test avancé requis"})
	}
	if w, ok := h.QD1["write"]; ok {
		note := "4 Kio QD1 en écriture, p50 / p99"
		if r, ok := h.QD1["read"]; ok {
			note += fmt.Sprintf(" ; lecture %s / %s", Ms(r.P50Ms), Ms(r.P99Ms))
		}
		t = append(t, Tile{Value: Ms(w.P50Ms) + " / " + Ms(w.P99Ms), Label: "Latence", Note: note})
	} else {
		t = append(t, Tile{Value: Ms(h.WriteP99), Label: "Latence p99 en écriture", Note: fmt.Sprintf("séquentiel %s QD %d (4 Kio QD1 : test avancé requis)", BlockSize(mr.Write.Profile.BlockKiB), mr.Write.Profile.QD)})
	}
	return t
}

func profileLine(label string, res *bench.Result) string {
	return label + " : " + res.Profile.Text
}

func synthese(r *run, h head, p *Page) Section {
	s := Section{ID: "synthese", Title: "Synthèse"}
	s.Verdict = &VerdictBox{Level: string(h.Level), Class: levelClass(h.Level), Text: sentVerdict(h.Level, subjectOf(r), h.Triggered)}
	s.Tiles = tiles(r, h)
	mr := r.main()
	s.Lead = append(s.Lead, sentThroughput(h.Write, h.Read, mr.Write.Profile, h.Reps))
	if h.Useful != nil {
		s.Lead = append(s.Lead, sentUseful(*h.Useful))
	}
	if q := sentQD1(h.QD1); q != "" {
		s.Lead = append(s.Lead, q)
	}
	if r.tour != nil {
		c := r.tour.Comparison
		s.Lead = append(s.Lead, c.Sentence)
		t := &Table{Head: []string{"Hôte", "Passages", "Écriture", "Lecture", "p99 écriture", "vs meilleur", "Verdict"},
			Num: []bool{false, true, true, true, true, true, false}}
		for _, hs := range c.Hosts {
			t.Rows = append(t.Rows, []Cell{{Text: hs.Host}, {Text: fmt.Sprint(hs.Runs)}, {Text: MBps(hs.WriteMBps)}, {Text: MBps(hs.ReadMBps)},
				{Text: Ms(hs.WriteP99)}, {Text: "×" + dec(hs.VsBest, 2)}, {Text: status(hs.Level), Class: "lvl " + levelClass(hs.Level)}})
		}
		s.Blocks = append(s.Blocks, Block{Kind: "table", Table: t})
	}
	kv := []KV{{"Datastore", dsLine(mr)}, {"Hôte", r.hostLabel()}}
	if mr.Host.Cluster != nil {
		kv = append(kv, KV{"Cluster", mr.Host.Cluster.Name})
	}
	kv = append(kv, KV{"Mesure", Date(mr.Started) + " (" + profileTitle(r.kind()) + ")"}, KV{"Seuils", "préréglage « " + mr.Options.Thresholds.Preset + " »"})
	s.Blocks = append(s.Blocks, Block{Kind: "kv", KV: kv})
	s.Blocks = append(s.Blocks, Block{Kind: "note", Text: "Profil d'I/O — " + profileLine("écriture", mr.Write) + " ; " + profileLine("lecture", mr.Read) + "."})
	return s
}

func dsLine(s *standard.Report) string {
	d := s.Datastore
	out := d.Name + " (" + d.Type
	if d.NFSServer != "" {
		out += ", " + d.NFSServer + ":" + d.NFSExport
	}
	return out + ")"
}

func debit(r *run, p *Page) Section {
	s := Section{ID: "debit", Title: "Débit séquentiel"}
	t := &Table{Head: []string{"Mesure", "Moyenne", "Minimum à la seconde", "Maximum à la seconde", "p99", "CPU worker"}, Num: []bool{false, true, true, true, true, true}}
	add := func(label string, res *bench.Result) {
		mean, mn, mx := perSecond(res)
		t.Rows = append(t.Rows, []Cell{{Text: label}, {Text: MBps(mean)}, {Text: MBps(mn)}, {Text: MBps(mx)}, {Text: Ms(res.Latency.P99 / 1000)}, {Text: dec(res.CPUPct, 0) + nbsp + "%"}})
	}
	switch {
	case r.tour != nil:
		var ws, rs []lineSeries
		seen := map[string]bool{}
		for _, l := range r.tour.Legs {
			add(fmt.Sprintf("%s, cycle %d, écriture", l.Host, l.Cycle), l.Report.Write)
			add(fmt.Sprintf("%s, cycle %d, lecture", l.Host, l.Cycle), l.Report.Read)
			if !seen[l.Host] {
				i := len(ws)
				seen[l.Host] = true
				ws = append(ws, lineSeries{Label: l.Host, Color: color(i), R: l.Report.Write})
				rs = append(rs, lineSeries{Label: l.Host, Color: color(i), R: l.Report.Read})
			}
		}
		cw, cr := throughputChart(ws), throughputChart(rs)
		cw.Height, cr.Height = 180, 180 // deux graphs sur la page
		p.Charts["thr-w"], p.Charts["thr-r"] = cw, cr
		s.Lead = append(s.Lead, "Débit à la seconde du premier passage sur chaque hôte, en écriture puis en lecture. Le tableau détaille tous les passages.")
		s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Écriture séquentielle"}, Block{Kind: "chart", ChartID: "thr-w"},
			Block{Kind: "h3", Text: "Lecture séquentielle"}, Block{Kind: "chart", ChartID: "thr-r"})
	default:
		reps := r.reports()
		for i, x := range reps {
			lbl := ""
			if len(reps) > 1 {
				lbl = fmt.Sprintf("répétition %d, ", i+1)
			}
			add(lbl+"écriture", x.Write)
			add(lbl+"lecture", x.Read)
		}
		mr := r.main()
		p.Charts["thr"] = throughputChart([]lineSeries{{Label: "écriture", Color: color(0), R: mr.Write}, {Label: "lecture", Color: color(1), R: mr.Read}})
		_, wmin, _ := perSecond(mr.Write)
		s.Lead = append(s.Lead, fmt.Sprintf("Débit mesuré chaque seconde pendant l'écriture puis la lecture séquentielles. Moyenne en écriture %s, minimum à la seconde %s ; en lecture %s.",
			MBps(mr.Write.MBps), MBps(wmin), MBps(mr.Read.MBps)))
		if len(reps) > 1 {
			s.Lead = append(s.Lead, "Le graph montre la première répétition ; le tableau les détaille toutes.")
		}
		s.Blocks = append(s.Blocks, Block{Kind: "chart", ChartID: "thr"})
	}
	s.Blocks = append(s.Blocks, Block{Kind: "table", Table: t})
	mr := r.main()
	s.Blocks = append(s.Blocks, Block{Kind: "note", Text: "Profil d'I/O — " + profileLine("écriture", mr.Write) + " ; " + profileLine("lecture", mr.Read) + "."})
	return s
}

var patOrder = []string{"read", "write", "mixed"}

func iopsSection(r *run, h head) Section {
	s := Section{ID: "iops", Title: "IOPS et latence"}
	a := r.adv
	var series []xySeries
	for i, pat := range patOrder {
		xs := xySeries{Label: patternFR(pat) + " 4 Kio", Color: color(i)}
		for _, pt := range a.Points {
			if pt.Pattern == pat && pt.BlockKiB == 4 {
				xs.Points = append(xs.Points, xyPoint{X: pt.IOPS, Y: pt.P99Ms, Tip: fmt.Sprintf("QD %d : %s, p99 %s", pt.QD, IOPS(pt.IOPS), Ms(pt.P99Ms))})
			}
		}
		if len(xs.Points) > 0 {
			series = append(series, xs)
		}
	}
	s.Lead = append(s.Lead, "Chaque point est un palier de profondeur de file (QD 1, 4, 16, 32) : IOPS obtenues en abscisse, latence p99 en ordonnée. Là où la courbe se redresse, le stockage sature : les IOPS n'augmentent plus, seule l'attente grandit.")
	for _, u := range a.Useful {
		if u.BlockKiB == 4 {
			s.Lead = append(s.Lead, sentUseful(u))
		}
	}
	s.Blocks = append(s.Blocks, Block{Kind: "svg", SVG: svgXY(series, "IOPS", "latence p99 (ms)", h.Ceiling, "plafond "+MsCeil(h.Ceiling), 260)})
	t := &Table{Head: []string{"Palier", "QD", "IOPS", "Mo/s", "p50", "p99", "p99,9", "vSphere", "Remarque"}, Num: []bool{false, true, true, true, true, true, true, true, false}}
	for i, st := range a.Steps {
		if i >= len(a.Points) {
			break
		}
		pt := a.Points[i]
		chk := "—"
		if pt.MismatchPct != nil {
			chk = Pct(*pt.MismatchPct)
		}
		t.Rows = append(t.Rows, []Cell{{Text: patternFR(pt.Pattern) + " " + BlockSize(pt.BlockKiB)}, {Text: fmt.Sprint(pt.QD)}, {Text: IOPS(st.IOPS)}, {Text: MBps(st.MBps)},
			{Text: Ms(st.Latency.P50 / 1000)}, {Text: Ms(st.Latency.P99 / 1000)}, {Text: Ms(st.Latency.P999 / 1000)}, {Text: chk}, {Text: strings.Join(pt.Flags, ", ")}})
	}
	s.Blocks = append(s.Blocks, Block{Kind: "table", Table: t})
	if len(a.Steps) > 0 {
		st := a.Steps[0]
		s.Blocks = append(s.Blocks, Block{Kind: "note", Text: fmt.Sprintf("Profil d'I/O — accès aléatoires alignés, bloc %s, QD 1 à 32, %d s de chauffe puis %d s de mesure par palier, zone %s, %s, données incompressibles. Colonne vSphere : écart entre les IOPS de l'appliance et celles que vSphere compte sur le disque virtuel.",
			BlockSize(st.Step.BlockKiB), a.Options.WarmupS, a.Options.MeasureS, Size(st.ZoneBytes), flagsText(st.Profile))})
	}
	return s
}

func flagsText(p bench.Profile) string {
	f := []string{}
	if p.Direct {
		f = append(f, "O_DIRECT")
	}
	if p.Sync {
		f = append(f, "O_SYNC")
	}
	if len(f) == 0 {
		return "E/S tamponnées"
	}
	return strings.Join(f, "+")
}

func distribution(r *run, p *Page) Section {
	s := Section{ID: "distribution", Title: "Distribution des latences"}
	var hs []histSeries
	var title string
	if r.adv != nil {
		for i, pat := range []string{"write", "read"} {
			for k, pt := range r.adv.Points {
				if pt.Pattern == pat && pt.QD == 1 && pt.BlockKiB == 4 && k < len(r.adv.Steps) {
					hs = append(hs, histSeries{Label: patternFR(pat) + " 4 Kio QD1", Color: color(i), R: r.adv.Steps[k]})
				}
			}
		}
		title = "4 Kio aléatoire, QD1"
	} else {
		mr := r.main()
		hs = []histSeries{{Label: "écriture séquentielle", Color: color(0), R: mr.Write}, {Label: "lecture séquentielle", Color: color(1), R: mr.Read}}
		title = fmt.Sprintf("séquentiel %s QD %d", BlockSize(mr.Write.Profile.BlockKiB), mr.Write.Profile.QD)
	}
	s.Lead = append(s.Lead, "Part des I/O par classe de latence ("+title+"), en échelle logarithmique. Une queue longue à droite se voit ici, même quand la moyenne paraît correcte.")
	p.Charts["hist"] = histChart(hs)
	s.Blocks = append(s.Blocks, Block{Kind: "chart", ChartID: "hist"})
	t := &Table{Head: []string{"Série", "p50", "p95", "p99", "p99,9", "max", "moyenne", "I/O"}, Num: []bool{false, true, true, true, true, true, true, true}}
	for _, x := range hs {
		l := x.R.Latency
		t.Rows = append(t.Rows, []Cell{{Text: x.Label}, {Text: Ms(l.P50 / 1000)}, {Text: Ms(l.P95 / 1000)}, {Text: Ms(l.P99 / 1000)}, {Text: Ms(l.P999 / 1000)},
			{Text: Ms(l.Max / 1000)}, {Text: Ms(l.Mean / 1000)}, {Text: group(x.R.Ops)}})
		s.Lead = append(s.Lead, sentDistribution(capitalize(x.Label), l))
	}
	s.Blocks = append(s.Blocks, Block{Kind: "table", Table: t})
	for _, x := range hs {
		s.Blocks = append(s.Blocks, Block{Kind: "note", Text: "Profil d'I/O — " + x.Label + " : " + x.R.Profile.Text + "."})
	}
	return s
}

func hostsSection(r *run) Section {
	s := Section{ID: "hotes", Title: "Comparatif entre hôtes"}
	c := r.tour.Comparison
	s.Lead = append(s.Lead, c.Sentence, fmt.Sprintf("Médianes sur %d cycle(s) en alternance (A, B, A, B…) : une variation de charge du serveur touche ainsi tous les hôtes à parts égales. Seuils d'écart : ATTENTION au-delà de ×%s, ALERTE au-delà de ×%s.",
		r.tour.Options.Cycles, dec(r.main().Options.Thresholds.HostSpreadWarn, 0), dec(r.main().Options.Thresholds.HostSpreadAlert, 0)))
	var w, rd, p []bar
	var wv, rv, pv []float64
	for _, hs := range c.Hosts {
		wv, rv, pv = append(wv, hs.WriteMBps), append(rv, hs.ReadMBps), append(pv, hs.WriteP99)
	}
	mw, mrd, mp := median(wv), median(rv), median(pv)
	vsMed := func(v, m float64) string {
		if m == 0 {
			return ""
		}
		return Pct((v - m) / m * 100)
	}
	for i, hs := range c.Hosts {
		col := color(i)
		w = append(w, bar{Label: hs.Host, Value: hs.WriteMBps, Text: MBps(hs.WriteMBps) + "  (" + vsMed(hs.WriteMBps, mw) + ")", Color: col, Tip: hs.Host + " : écriture " + MBps(hs.WriteMBps)})
		rd = append(rd, bar{Label: hs.Host, Value: hs.ReadMBps, Text: MBps(hs.ReadMBps) + "  (" + vsMed(hs.ReadMBps, mrd) + ")", Color: col, Tip: hs.Host + " : lecture " + MBps(hs.ReadMBps)})
		p = append(p, bar{Label: hs.Host, Value: hs.WriteP99, Text: Ms(hs.WriteP99) + "  (" + vsMed(hs.WriteP99, mp) + ")", Color: col, Tip: hs.Host + " : p99 écriture " + Ms(hs.WriteP99)})
	}
	s.Blocks = append(s.Blocks,
		Block{Kind: "svg", SVG: svgBars("Débit d'écriture (médiane, écart à la médiane des hôtes)", w)},
		Block{Kind: "svg", SVG: svgBars("Débit de lecture", rd)},
		Block{Kind: "svg", SVG: svgBars("Latence p99 en écriture séquentielle", p)})
	t := &Table{Head: []string{"Cycle", "Hôte", "vMotion", "Écriture", "Lecture", "Ratio TX/écrit", "Verdict"}, Num: []bool{true, false, true, true, true, true, false}}
	for _, l := range r.tour.Legs {
		mv := "—"
		if l.Moved {
			mv = dec(l.MoveS, 0) + nbsp + "s"
		}
		tx := "n/d"
		if l.Report.TxRatio != nil && l.Report.TxRatio.Samples > 0 {
			tx = Ratio(l.Report.TxRatio.Value)
		}
		t.Rows = append(t.Rows, []Cell{{Text: fmt.Sprint(l.Cycle)}, {Text: l.Host}, {Text: mv}, {Text: MBps(l.Report.Write.MBps)}, {Text: MBps(l.Report.Read.MBps)}, {Text: tx},
			{Text: status(l.Report.Verdict.Level), Class: "lvl " + levelClass(l.Report.Verdict.Level)}})
	}
	s.Blocks = append(s.Blocks, Block{Kind: "table", Table: t})
	return s
}

// healthRows agrège les indicateurs (pire niveau par indicateur, sur toutes les mesures).
func healthRows(r *run) [][]Cell {
	type agg struct {
		ind  analysis.Indicator
		host string
	}
	var order []string
	best := map[string]agg{}
	var inds [][2]any
	switch {
	case r.adv != nil:
		for _, i := range r.adv.Verdict.Indicators {
			inds = append(inds, [2]any{i, ""})
		}
	case r.tour != nil:
		for _, l := range r.tour.Legs {
			for _, i := range l.Report.Verdict.Indicators {
				inds = append(inds, [2]any{i, l.Host})
			}
		}
	default:
		for k, s := range r.std {
			for _, i := range s.Verdict.Indicators {
				lbl := ""
				if len(r.std) > 1 {
					lbl = fmt.Sprintf("répétition %d", k+1)
				}
				inds = append(inds, [2]any{i, lbl})
			}
		}
	}
	for _, x := range inds {
		i, host := x[0].(analysis.Indicator), x[1].(string)
		cur, ok := best[i.Key]
		if !ok {
			order = append(order, i.Key)
		}
		if !ok || rank[i.Level] > rank[cur.ind.Level] {
			best[i.Key] = agg{i, host}
		}
	}
	var rows [][]Cell
	for _, k := range order {
		a := best[k]
		v := indicatorValue(a.ind)
		if a.host != "" && len(inds) > len(order) && a.ind.Level != analysis.OK {
			v += " — " + a.host
		}
		rows = append(rows, []Cell{{Text: a.ind.Label}, {Text: v}, {Text: status(a.ind.Level), Class: "lvl " + levelClass(a.ind.Level)}, {Text: explain[k], Class: "expl"}})
	}
	return rows
}

func sante(r *run) Section {
	s := Section{ID: "sante", Title: "Santé du chemin"}
	s.Lead = append(s.Lead, "Chaque indicateur est classé OK, à surveiller ou anomalie selon les seuils du préréglage « "+r.main().Options.Thresholds.Preset+" ». Rappel : les erreurs physiques se comptent sur le récepteur ; une vmnic qui émet mal n'apparaît que par le ratio TX/écrit.")
	if r.tour != nil {
		s.Lead = append(s.Lead, "Pour une tournée, chaque ligne donne le pire résultat observé sur l'ensemble des passages, avec l'hôte concerné.")
	}
	s.Blocks = append(s.Blocks, Block{Kind: "table", Table: &Table{Head: []string{"Indicateur", "Valeur", "État", "Explication"}, Rows: healthRows(r)}})
	mr := r.main()
	if len(mr.PerVmnic) > 0 {
		t := &Table{Head: []string{"Uplink", "Rôle", "Vitesse", "Pilote", "TX écriture", "RX lecture", "TX au repos", "Err./pertes RX", "Err./pertes TX"}, Num: []bool{false, false, true, false, true, true, true, true, true}}
		esx := false
		for _, v := range mr.PerVmnic {
			esx = esx || v.Esxcli != nil
		}
		if esx {
			t.Head = append(t.Head, "CRC/trame/longueur", "Manquées RX")
			t.Num = append(t.Num, true, true)
		}
		drv := map[string]string{}
		if mr.Path != nil {
			for _, u := range mr.Path.Uplinks {
				drv[u.Device] = u.Driver
			}
		}
		for _, v := range mr.PerVmnic {
			t.Rows = append(t.Rows, []Cell{{Text: v.Device}, {Text: v.Role}, {Text: speed(v.SpeedMb)}, {Text: drv[v.Device]}, {Text: MBps(v.TxWriteMBps)}, {Text: MBps(v.RxReadMBps)},
				{Text: dec(v.TxBaseMBps, 1) + nbsp + "Mo/s"}, {Text: dec(v.ErrorsRx+v.DroppedRx, 0)}, {Text: dec(v.ErrorsTx+v.DroppedTx, 0)}})
			if esx {
				row := &t.Rows[len(t.Rows)-1]
				if c := v.Esxcli; c != nil {
					*row = append(*row, Cell{Text: dec(float64(c["rx_crc"]+c["rx_frame"]+c["rx_length"]), 0)}, Cell{Text: dec(float64(c["rx_missed"]+c["rx_fifo"]+c["rx_over"]), 0)})
				} else {
					*row = append(*row, Cell{Text: "—"}, Cell{Text: "—"})
				}
			}
		}
		lbl := "Uplinks du chemin NFS"
		if r.tour != nil {
			lbl += " (hôte " + mr.Host.Name + ", premier passage)"
		}
		s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: lbl}, Block{Kind: "table", Table: t})
	}
	var ratio []string
	for _, x := range r.reports() {
		if x.TxRatio != nil {
			for _, n := range x.TxRatio.Notes {
				ratio = append(ratio, "Ratio TX/écrit : "+n)
			}
		}
		if x.Log != nil && len(x.Log.Samples) > 0 {
			for _, l := range x.Log.Samples {
				ratio = append(ratio, "vmkernel.log : "+l)
			}
		}
		for _, e := range x.Events {
			ratio = append(ratio, "Événement "+DateShort(e.Time)+" : "+e.Message)
		}
	}
	if len(ratio) > 12 {
		ratio = append(ratio[:12], fmt.Sprintf("… et %d autre(s) ligne(s)", len(ratio)-12))
	}
	if len(ratio) > 0 {
		s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Observations"}, Block{Kind: "list", List: dedupe(ratio)})
	}
	return s
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func speed(mb int32) string {
	switch {
	case mb <= 0:
		return "lien coupé ?"
	case mb%1000 == 0:
		return fmt.Sprintf("%d%sGb/s", mb/1000, nbsp)
	}
	return fmt.Sprintf("%d%sMb/s", mb, nbsp)
}

// envKV : environnement mesuré.
func envKV(s *standard.Report) []KV {
	kv := []KV{}
	if s.Server != nil {
		v := s.Server.FullName
		if !strings.Contains(v, s.Server.Build) {
			v += " (build " + s.Server.Build + ")"
		}
		kv = append(kv, KV{"vSphere", v})
	} else {
		kv = append(kv, KV{"vSphere", "non enregistré dans ce résultat (mesure antérieure à la phase 8c)"})
	}
	host := fmt.Sprintf("%s, ESXi %s (build %s)", s.Host.Name, s.Host.Version, s.Host.Build)
	if s.Host.Cluster != nil {
		host += ", cluster " + s.Host.Cluster.Name
	}
	kv = append(kv, KV{"Hôte", host}, KV{"Appliance", s.VM.Name + vers(s.Appliance)})
	d := s.Datastore
	ds := fmt.Sprintf("%s, %s", d.Name, nfsVersion(d.Type))
	if d.NFSServer != "" {
		ds += fmt.Sprintf(", serveur %s, export %s", d.NFSServer, d.NFSExport)
	}
	if d.Capacity > 0 {
		ds += fmt.Sprintf(", %s libres sur %s", Size(d.Free), Size(d.Capacity))
	}
	kv = append(kv, KV{"Datastore", ds})
	if p := s.Path; p != nil {
		var ups []string
		for _, u := range p.Uplinks {
			x := u.Device + " " + u.Role + " " + speed(u.SpeedMb)
			if u.Driver != "" {
				x += " (" + u.Driver + ")"
			}
			if u.Peer != nil {
				x += " ↔ " + strings.TrimSpace(u.Peer.Switch+" "+u.Peer.Port)
			}
			ups = append(ups, x)
		}
		kv = append(kv, KV{"Chemin NFS", fmt.Sprintf("%s via %s %s (MTU %d) → %s %s → %s", p.NFSIP, p.VMK.Device, p.VMK.IP, p.VMK.MTU, switchType(p.SwitchType), p.Switch, strings.Join(ups, ", "))})
	}
	return kv
}

func vers(v string) string {
	if v == "" {
		return ""
	}
	return ", vigie " + v
}

func switchType(t string) string {
	if t == "distributed" {
		return "vDS"
	}
	return "vSwitch"
}

func nfsVersion(t string) string {
	switch t {
	case "NFS":
		return "NFS v3"
	case "NFS41":
		return "NFS v4.1"
	}
	return t
}

// protoKV : protocole de mesure.
func protoKV(r *run) []KV {
	mr := r.main()
	o := mr.Options
	w := mr.Write
	kv := []KV{
		{"Zone de test", fmt.Sprintf("%s sur le disque de test dédié de l'appliance (VMDK sur le datastore mesuré, signature vérifiée avant toute écriture)", Size(w.ZoneBytes))},
		{"Données", w.Profile.Data},
		{"Accès", flagsText(w.Profile) + " : ni le cache de la VM ni celui de Linux n'interviennent"},
		{"Ligne de base", fmt.Sprintf("%d s au repos avant la mesure (bruit de fond retiré du ratio TX/écrit)", o.BaselineS)},
		{"Écriture séquentielle", w.Profile.Text},
		{"Lecture séquentielle", mr.Read.Profile.Text},
	}
	if mr.Prefill != nil {
		kv = append(kv, KV{"Préremplissage", fmt.Sprintf("zone entièrement écrite avant la mesure (%s)", MBps(mr.Prefill.MBps))})
	}
	switch {
	case r.adv != nil:
		a := r.adv
		blocks := "4 Kio"
		if a.Options.Full {
			blocks = "4, 8 et 64 Kio"
		}
		qds := []string{}
		for _, q := range a.Options.QDs {
			qds = append(qds, fmt.Sprint(q))
		}
		kv = append(kv, KV{"Paliers aléatoires", fmt.Sprintf("%s ; lecture 100 %%, écriture 100 %%, mixte 70/30 ; QD %s ; %d s de chauffe non comptées puis %d s de mesure ; %d palier(s) ; worker sur %d CPU",
			blocks, strings.Join(qds, ", "), a.Options.WarmupS, a.Options.MeasureS, len(a.Steps), a.WorkerCPU)},
			KV{"IOPS utiles", fmt.Sprintf("IOPS du palier le plus élevé dont la latence p99 reste sous %s", MsCeil(a.Options.CeilingMs))})
	case r.tour != nil:
		t := r.tour
		kv = append(kv, KV{"Tournée", fmt.Sprintf("%d hôtes, %d cycle(s) en alternance, vMotion de l'appliance seule entre les passages ; override DRS : %s ; retour à l'hôte de départ (%s) : %s",
			len(t.Options.Hosts), t.Options.Cycles, ouiNon(t.Options.DRSOverride), t.HomeHost, ouiNon(t.Options.ReturnHome))})
	default:
		if len(r.std) > 1 {
			kv = append(kv, KV{"Répétitions", fmt.Sprintf("%d mesures enchaînées ; les chiffres titres sont les médianes", len(r.std))})
		}
	}
	kv = append(kv, KV{"Compteurs vSphere", "PerformanceManager, intervalle de 20 s ; seuls les échantillons entièrement couverts par la mesure sont retenus ; horloges appliance / hôte recalées"},
		KV{"Seuils", "préréglage « " + o.Thresholds.Preset + " »"}, KV{"Horodatage", "UTC"})
	return kv
}

func ouiNon(b bool) string {
	if b {
		return "oui"
	}
	return "non"
}

// guards : garde-fous déclenchés et avertissements de mesure.
func guards(r *run) []string {
	var g []string
	if r.adv != nil {
		for _, pt := range r.adv.Points {
			for _, f := range pt.Flags {
				g = append(g, fmt.Sprintf("Palier %s %s QD %d : %s.", patternFR(pt.Pattern), BlockSize(pt.BlockKiB), pt.QD, f))
			}
		}
		if len(r.adv.Steps) > 0 && r.adv.Steps[0].ZoneBytes < 64<<30 {
			g = append(g, fmt.Sprintf("Zone de test de %s : les IOPS mesurées incluent l'effet du cache du serveur NFS. Une zone plus grande (jusqu'à 64 Gio) sollicite davantage les disques.", Size(r.adv.Steps[0].ZoneBytes)))
		}
		g = append(g, r.adv.Warnings...)
	}
	if r.tour != nil {
		g = append(g, r.tour.Warnings...)
	}
	for _, x := range r.reports() {
		if x.TxRatio != nil && x.TxRatio.Samples > 0 && !x.TxRatio.Reliable {
			g = append(g, fmt.Sprintf("Ratio TX/écrit de %s marqué peu fiable (%s).", x.Host.Name, strings.Join(x.TxRatio.Notes, " ; ")))
		}
		g = append(g, x.Warnings...)
	}
	return dedupe(g)
}

func methodo(r *run, h head) Section {
	s := Section{ID: "methodologie", Title: "Méthodologie et environnement"}
	s.Lead = append(s.Lead, "Mesure faite par l'appliance vigie depuis une VM de l'hôte, sur un disque de test dédié posé sur le datastore. Toutes les informations d'infrastructure viennent de l'API vSphere, en lecture seule ; aucune connexion SSH aux hôtes.")
	s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Environnement"}, Block{Kind: "kv", KV: envKV(r.main())})
	s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Protocole"}, Block{Kind: "kv", KV: protoKV(r)})
	g := guards(r)
	if len(g) == 0 {
		g = []string{"Aucun."}
	}
	s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Garde-fous déclenchés et avertissements"}, Block{Kind: "list", List: g})
	s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Unités"}, Block{Kind: "text", Text: unitsText})
	s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Limites"}, Block{Kind: "list", List: limits})
	return s
}

// capitalize met la première lettre en majuscule (UTF-8 : « écriture » → « Écriture »).
func capitalize(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	return strings.ToUpper(string(r[0])) + string(r[1:])
}
