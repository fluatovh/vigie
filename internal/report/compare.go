package report

import (
	"fmt"
	"math"
	"strings"

	"vigie/internal/analysis"
	"vigie/internal/jobs"
)

// StableBand : en deçà de cette variation (en %), un chiffre est considéré stable (bruit de mesure).
const StableBand = 5.0

type delta struct {
	Text, Class string
}

// diff calcule la variation de a vers b ; higherBetter indique le sens favorable.
func diff(a, b float64, higherBetter bool) delta {
	if a == 0 || math.IsNaN(a) || math.IsNaN(b) {
		return delta{"—", "same"}
	}
	pct := (b - a) / math.Abs(a) * 100
	d := delta{Text: Pct(pct)}
	switch {
	case math.Abs(pct) < StableBand:
		d.Class = "same"
	case (pct > 0) == higherBetter:
		d.Class = "better"
	default:
		d.Class = "worse"
	}
	return d
}

func trend(d delta) string {
	switch d.Class {
	case "better":
		return "amélioration"
	case "worse":
		return "dégradation"
	}
	return "stable"
}

type cmpRow struct {
	label        string
	a, b         float64
	fmt          func(float64) string
	higherBetter bool
}

// BuildCompare construit le rapport avant / après à partir de deux jobs (a = avant, b = après).
func BuildCompare(ja, jb *jobs.Job, br Branding, m Meta) (*Page, error) {
	a, err := load(ja)
	if err != nil {
		return nil, fmt.Errorf("run « avant » : %w", err)
	}
	b, err := load(jb)
	if err != nil {
		return nil, fmt.Errorf("run « après » : %w", err)
	}
	if a.main().Started.After(b.main().Started) {
		a, b = b, a // l'avant est toujours le plus ancien
	}
	p := newPage("compare", br, m)
	ma, mb := a.main(), b.main()
	ha, hb := a.head(), b.head()
	p.JobIDs = []string{a.job.ID, b.job.ID}
	p.Title = "Rapport avant / après — datastore " + mb.Datastore.Name
	p.Subject = fmt.Sprintf("Datastore %s, comparaison de deux mesures", mb.Datastore.Name)
	p.RunDate = "avant : " + Date(ma.Started) + " ; après : " + Date(mb.Started)

	rows := []cmpRow{
		{"Débit d'écriture séquentielle", ha.Write, hb.Write, MBps, true},
		{"Débit de lecture séquentielle", ha.Read, hb.Read, MBps, true},
		{"Latence p99 en écriture séquentielle", ha.WriteP99, hb.WriteP99, Ms, false},
	}
	if ha.TxRatio > 0 && hb.TxRatio > 0 {
		rows = append(rows, cmpRow{"Ratio TX/écrit corrigé", ha.TxRatio, hb.TxRatio, Ratio, false})
	}
	if ha.Useful != nil && hb.Useful != nil {
		rows = append(rows, cmpRow{fmt.Sprintf("IOPS utiles (4 Kio 70/30, p99 < %s)", MsCeil(hb.Useful.CeilingMs)), ha.Useful.IOPS, hb.Useful.IOPS, IOPS, true},
			cmpRow{"IOPS maximum brut (4 Kio 70/30)", ha.Useful.RawMax, hb.Useful.RawMax, IOPS, true})
	}
	for _, pat := range []string{"write", "read"} {
		pa, oka := ha.QD1[pat]
		pb, okb := hb.QD1[pat]
		if oka && okb {
			rows = append(rows, cmpRow{"Latence 4 Kio QD1 p50, " + patternFR(pat), pa.P50Ms, pb.P50Ms, Ms, false},
				cmpRow{"Latence 4 Kio QD1 p99, " + patternFR(pat), pa.P99Ms, pb.P99Ms, Ms, false})
		}
	}

	// 1. Synthèse
	s := Section{ID: "synthese", Title: "Synthèse avant / après"}
	s.Verdict = &VerdictBox{Level: string(hb.Level), Class: levelClass(hb.Level),
		Text: fmt.Sprintf("Avant : verdict %s. Après : verdict %s. %s", ha.Level, hb.Level, capitalize(strings.TrimPrefix(sentVerdict(hb.Level, subjectOf(b), hb.Triggered), "Verdict "+string(hb.Level)+" : ")))}
	dw, dr := diff(ha.Write, hb.Write, true), diff(ha.Read, hb.Read, true)
	s.Tiles = append(s.Tiles, Tile{Value: MBps(hb.Write), Label: "Écriture séquentielle", Note: "avant " + MBps(ha.Write), Delta: dw.Text, DeltaClass: dw.Class},
		Tile{Value: MBps(hb.Read), Label: "Lecture séquentielle", Note: "avant " + MBps(ha.Read), Delta: dr.Text, DeltaClass: dr.Class})
	if ha.Useful != nil && hb.Useful != nil {
		d := diff(ha.Useful.IOPS, hb.Useful.IOPS, true)
		s.Tiles = append(s.Tiles, Tile{Value: IOPS(hb.Useful.IOPS), Label: "IOPS utiles", Note: "avant " + IOPS(ha.Useful.IOPS), Delta: d.Text, DeltaClass: d.Class})
	} else {
		d := diff(ha.WriteP99, hb.WriteP99, false)
		s.Tiles = append(s.Tiles, Tile{Value: Ms(hb.WriteP99), Label: "Latence p99 en écriture", Note: "avant " + Ms(ha.WriteP99), Delta: d.Text, DeltaClass: d.Class})
	}
	for _, r := range rows[:2] {
		d := diff(r.a, r.b, r.higherBetter)
		s.Lead = append(s.Lead, fmt.Sprintf("%s : %s avant, %s après (%s, %s).", r.label, r.fmt(r.a), r.fmt(r.b), d.Text, trend(d)))
	}
	if ha.Useful != nil && hb.Useful != nil {
		d := diff(ha.Useful.IOPS, hb.Useful.IOPS, true)
		s.Lead = append(s.Lead, fmt.Sprintf("IOPS utiles en 4 Kio aléatoire 70/30 : %s avant, %s après (%s, %s).", IOPS(ha.Useful.IOPS), IOPS(hb.Useful.IOPS), d.Text, trend(d)))
	}
	t := &Table{Head: []string{"Chiffre", "Avant", "Après", "Variation"}, Num: []bool{false, true, true, true}}
	for _, r := range rows {
		d := diff(r.a, r.b, r.higherBetter)
		t.Rows = append(t.Rows, []Cell{{Text: r.label}, {Text: r.fmt(r.a)}, {Text: r.fmt(r.b)}, {Text: d.Text, Class: "delta " + d.Class}})
	}
	t.Rows = append(t.Rows, []Cell{{Text: "Verdict"}, {Text: string(ha.Level), Class: "lvl " + levelClass(ha.Level)}, {Text: string(hb.Level), Class: "lvl " + levelClass(hb.Level)}, {Text: ""}})
	s.Blocks = append(s.Blocks, Block{Kind: "table", Table: t})
	s.Blocks = append(s.Blocks, Block{Kind: "note", Text: fmt.Sprintf("Variation en vert si le chiffre s'améliore, en rouge s'il se dégrade ; sous ±%s %% elle est considérée comme stable (bruit de mesure).", dec(StableBand, 0))})
	s.Blocks = append(s.Blocks, Block{Kind: "kv", KV: []KV{{"Avant", runLine(a)}, {"Après", runLine(b)}}})
	if w := differences(a, b); len(w) > 0 {
		s.Blocks = append(s.Blocks, Block{Kind: "h3", Text: "Conditions différentes entre les deux mesures"}, Block{Kind: "list", List: w})
	}
	p.Sections = append(p.Sections, s)

	// 2. Débit
	d := Section{ID: "debit", Title: "Débit séquentiel, avant / après"}
	d.Lead = append(d.Lead, "Débit à la seconde ; pointillés : avant, trait plein : après.")
	p.Charts["thr"] = throughputChart([]lineSeries{
		{Label: "écriture avant", Color: color(0), Dash: true, R: ma.Write}, {Label: "écriture après", Color: color(0), R: mb.Write},
		{Label: "lecture avant", Color: color(1), Dash: true, R: ma.Read}, {Label: "lecture après", Color: color(1), R: mb.Read}})
	d.Blocks = append(d.Blocks, Block{Kind: "chart", ChartID: "thr"})
	dt := &Table{Head: []string{"Mesure", "Moyenne avant", "Moyenne après", "Variation", "Minimum avant", "Minimum après"}, Num: []bool{false, true, true, true, true, true}}
	for _, k := range []string{"écriture", "lecture"} {
		ra, rb := ma.Write, mb.Write
		if k == "lecture" {
			ra, rb = ma.Read, mb.Read
		}
		meanA, minA, _ := perSecond(ra)
		meanB, minB, _ := perSecond(rb)
		dd := diff(meanA, meanB, true)
		dt.Rows = append(dt.Rows, []Cell{{Text: k}, {Text: MBps(meanA)}, {Text: MBps(meanB)}, {Text: dd.Text, Class: "delta " + dd.Class}, {Text: MBps(minA)}, {Text: MBps(minB)}})
	}
	d.Blocks = append(d.Blocks, Block{Kind: "table", Table: dt},
		Block{Kind: "note", Text: "Profil d'I/O avant — écriture : " + ma.Write.Profile.Text + "."},
		Block{Kind: "note", Text: "Profil d'I/O après — écriture : " + mb.Write.Profile.Text + "."})
	p.Sections = append(p.Sections, d)

	// 3. IOPS et latence (deux tests avancés)
	if a.adv != nil && b.adv != nil {
		is := Section{ID: "iops", Title: "IOPS et latence, avant / après"}
		var series []xySeries
		for i, pat := range patOrder {
			for _, side := range []struct {
				r    *run
				lbl  string
				dash bool
			}{{a, "avant", true}, {b, "après", false}} {
				xs := xySeries{Label: patternFR(pat) + " " + side.lbl, Color: color(i), Dash: side.dash}
				for _, pt := range side.r.adv.Points {
					if pt.Pattern == pat && pt.BlockKiB == 4 {
						xs.Points = append(xs.Points, xyPoint{X: pt.IOPS, Y: pt.P99Ms, Tip: fmt.Sprintf("QD %d : %s, p99 %s", pt.QD, IOPS(pt.IOPS), Ms(pt.P99Ms))})
					}
				}
				if len(xs.Points) > 0 {
					series = append(series, xs)
				}
			}
		}
		is.Lead = append(is.Lead, "IOPS (abscisse) et latence p99 (ordonnée) par palier de QD ; pointillés : avant, trait plein : après.")
		is.Blocks = append(is.Blocks, Block{Kind: "svg", SVG: svgXY(series, "IOPS", "latence p99 (ms)", hb.Ceiling, "plafond "+MsCeil(hb.Ceiling), 300)})
		it := &Table{Head: []string{"Palier", "IOPS avant", "IOPS après", "Variation", "p99 avant", "p99 après", "Variation"}, Num: []bool{false, true, true, true, true, true, true}}
		idx := map[string]analysis.Point{}
		for _, pt := range a.adv.Points {
			idx[fmt.Sprintf("%s/%d/%d", pt.Pattern, pt.BlockKiB, pt.QD)] = pt
		}
		for _, pt := range b.adv.Points {
			old, ok := idx[fmt.Sprintf("%s/%d/%d", pt.Pattern, pt.BlockKiB, pt.QD)]
			if !ok {
				continue
			}
			di, dl := diff(old.IOPS, pt.IOPS, true), diff(old.P99Ms, pt.P99Ms, false)
			it.Rows = append(it.Rows, []Cell{{Text: fmt.Sprintf("%s %s QD %d", patternFR(pt.Pattern), BlockSize(pt.BlockKiB), pt.QD)}, {Text: IOPS(old.IOPS)}, {Text: IOPS(pt.IOPS)}, {Text: di.Text, Class: "delta " + di.Class},
				{Text: Ms(old.P99Ms)}, {Text: Ms(pt.P99Ms)}, {Text: dl.Text, Class: "delta " + dl.Class}})
		}
		is.Blocks = append(is.Blocks, Block{Kind: "table", Table: it})
		p.Sections = append(p.Sections, is)
	}

	// 4. Distribution
	ds := Section{ID: "distribution", Title: "Distribution des latences, avant / après"}
	var hs []histSeries
	if a.adv != nil && b.adv != nil {
		for _, side := range []struct {
			r    *run
			lbl  string
			dash bool
		}{{a, "avant", true}, {b, "après", false}} {
			for i, pat := range []string{"write", "read"} {
				for k, pt := range side.r.adv.Points {
					if pt.Pattern == pat && pt.QD == 1 && pt.BlockKiB == 4 && k < len(side.r.adv.Steps) {
						hs = append(hs, histSeries{Label: patternFR(pat) + " 4 Kio QD1 " + side.lbl, Color: color(i), Dash: side.dash, R: side.r.adv.Steps[k]})
					}
				}
			}
		}
	} else {
		hs = []histSeries{{Label: "écriture séq. avant", Color: color(0), Dash: true, R: ma.Write}, {Label: "écriture séq. après", Color: color(0), R: mb.Write},
			{Label: "lecture séq. avant", Color: color(1), Dash: true, R: ma.Read}, {Label: "lecture séq. après", Color: color(1), R: mb.Read}}
	}
	ds.Lead = append(ds.Lead, "Part des I/O par classe de latence (échelle logarithmique) ; pointillés : avant, trait plein : après.")
	p.Charts["hist"] = histChart(hs)
	ds.Blocks = append(ds.Blocks, Block{Kind: "chart", ChartID: "hist"})
	ht := &Table{Head: []string{"Série", "p50", "p99", "p99,9", "max"}, Num: []bool{false, true, true, true, true}}
	for _, x := range hs {
		l := x.R.Latency
		ht.Rows = append(ht.Rows, []Cell{{Text: x.Label}, {Text: Ms(l.P50 / 1000)}, {Text: Ms(l.P99 / 1000)}, {Text: Ms(l.P999 / 1000)}, {Text: Ms(l.Max / 1000)}})
	}
	ds.Blocks = append(ds.Blocks, Block{Kind: "table", Table: ht})
	p.Sections = append(p.Sections, ds)

	// 5. Santé du chemin
	hsec := Section{ID: "sante", Title: "Santé du chemin, avant / après"}
	ra, rb := healthRows(a), healthRows(b)
	byLabel := map[string][]Cell{}
	for _, r := range ra {
		byLabel[r[0].Text] = r
	}
	st := &Table{Head: []string{"Indicateur", "Avant", "État avant", "Après", "État après"}}
	for _, r := range rb {
		old := byLabel[r[0].Text]
		if old == nil {
			old = []Cell{{}, {Text: "—"}, {Text: "—"}}
		}
		st.Rows = append(st.Rows, []Cell{r[0], old[1], old[2], r[1], r[2]})
	}
	hsec.Lead = append(hsec.Lead, "Indicateurs de santé du chemin pour chaque mesure, classés OK, à surveiller ou anomalie.")
	hsec.Blocks = append(hsec.Blocks, Block{Kind: "table", Table: st})
	p.Sections = append(p.Sections, hsec)

	// 6. Méthodologie
	ms := Section{ID: "methodologie", Title: "Méthodologie et environnement"}
	ms.Lead = append(ms.Lead, "Les deux mesures sont reprises telles qu'elles ont été enregistrées par l'appliance ; aucune n'est refaite pour ce rapport.")
	ms.Blocks = append(ms.Blocks, Block{Kind: "h3", Text: "Avant — " + Date(ma.Started)}, Block{Kind: "kv", KV: append(envKV(ma), protoKV(a)[4:6]...)},
		Block{Kind: "h3", Text: "Après — " + Date(mb.Started)}, Block{Kind: "kv", KV: append(envKV(mb), protoKV(b)[4:6]...)})
	g := dedupe(append(guards(a), guards(b)...))
	if len(g) == 0 {
		g = []string{"Aucun."}
	}
	ms.Blocks = append(ms.Blocks, Block{Kind: "h3", Text: "Garde-fous déclenchés et avertissements"}, Block{Kind: "list", List: g},
		Block{Kind: "h3", Text: "Unités"}, Block{Kind: "text", Text: unitsText},
		Block{Kind: "h3", Text: "Limites"}, Block{Kind: "list", List: limits})
	p.Sections = append(p.Sections, ms)
	return p, nil
}

func runLine(r *run) string {
	m := r.main()
	return fmt.Sprintf("%s — %s, hôte %s, datastore %s (job %s)", Date(m.Started), profileTitle(r.kind()), r.hostLabel(), m.Datastore.Name, r.job.ID)
}

// differences liste ce qui rend la comparaison moins directe (dit, jamais bloqué).
func differences(a, b *run) []string {
	ma, mb := a.main(), b.main()
	var out []string
	if ma.Datastore.Name != mb.Datastore.Name {
		out = append(out, fmt.Sprintf("Datastores différents : %s puis %s.", ma.Datastore.Name, mb.Datastore.Name))
	}
	if a.hostLabel() != b.hostLabel() {
		out = append(out, fmt.Sprintf("Hôtes différents : %s puis %s.", a.hostLabel(), b.hostLabel()))
	}
	if ma.Write.Profile.Text != mb.Write.Profile.Text {
		out = append(out, "Profils d'écriture différents : « "+ma.Write.Profile.Text+" » puis « "+mb.Write.Profile.Text+" ».")
	}
	if ma.Options.Thresholds.Preset != mb.Options.Thresholds.Preset {
		out = append(out, fmt.Sprintf("Préréglages de seuils différents : %s puis %s.", ma.Options.Thresholds.Preset, mb.Options.Thresholds.Preset))
	}
	if (a.adv == nil) != (b.adv == nil) {
		out = append(out, "Un seul des deux runs est un test avancé : IOPS et latences 4 Kio ne sont pas comparées.")
	}
	return out
}
