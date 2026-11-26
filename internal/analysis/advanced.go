package analysis

import (
	"fmt"
	"sort"
	"strings"
)

// Point est un palier du test avancé, vu pour le calcul des IOPS utiles et des verdicts.
type Point struct {
	Pattern  string  `json:"pattern"` // read, write, mixed
	BlockKiB int     `json:"block_kib"`
	QD       int     `json:"qd"`
	IOPS     float64 `json:"iops"`
	P50Ms    float64 `json:"p50_ms"`
	P99Ms    float64 `json:"p99_ms"`
	CPUPct   float64 `json:"cpu_pct"` // usage CPU du worker rapporté aux CPU dont il dispose (100 = saturé)
	// Contrôle croisé (nil : non mesurable)
	HostIOPS    *float64 `json:"host_iops,omitempty"`
	MismatchPct *float64 `json:"mismatch_pct,omitempty"`
	Flags       []string `json:"flags,omitempty"` // « mesure à vérifier », « limité par l'appliance »
}

// Useful décrit les IOPS utiles d'un motif : IOPS du palier le plus élevé dont la p99 reste sous le
// plafond (§3.4), et le maximum brut pour le détail.
type Useful struct {
	Pattern   string  `json:"pattern"`
	BlockKiB  int     `json:"block_kib"`
	CeilingMs float64 `json:"ceiling_ms"`
	IOPS      float64 `json:"iops"` // 0 : aucun palier sous le plafond
	QD        int     `json:"qd"`
	P99Ms     float64 `json:"p99_ms"`
	RawMax    float64 `json:"raw_max_iops"`
	RawMaxQD  int     `json:"raw_max_qd"`
	RawMaxP99 float64 `json:"raw_max_p99_ms"`
}

// UsefulIOPS calcule les IOPS utiles pour chaque motif et taille de bloc.
func UsefulIOPS(points []Point, ceilingMs float64) []Useful {
	type key struct {
		p string
		b int
	}
	groups := map[key][]Point{}
	var order []key
	for _, p := range points {
		k := key{p.Pattern, p.BlockKiB}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], p)
	}
	var out []Useful
	for _, k := range order {
		u := Useful{Pattern: k.p, BlockKiB: k.b, CeilingMs: ceilingMs}
		for _, p := range groups[k] {
			if p.IOPS > u.RawMax {
				u.RawMax, u.RawMaxQD, u.RawMaxP99 = p.IOPS, p.QD, p.P99Ms
			}
			if p.P99Ms <= ceilingMs && p.IOPS > u.IOPS {
				u.IOPS, u.QD, u.P99Ms = p.IOPS, p.QD, p.P99Ms
			}
		}
		out = append(out, u)
	}
	return out
}

// Mismatch renvoie l'écart relatif (en %) entre les IOPS de l'appliance et celles vues par vSphere
// sur les mêmes intervalles.
func Mismatch(appliance, host float64) float64 {
	if appliance <= 0 {
		return 0
	}
	d := (host - appliance) / appliance * 100
	if d < 0 {
		return -d
	}
	return d
}

// EvaluateAdvanced ajoute au verdict les indicateurs du test avancé : latence 4 Kio QD1 p99 en
// lecture et en écriture, écart IOPS appliance / hôte, CPU du worker (§7). Les paliers sont annotés.
func EvaluateAdvanced(v *Verdict, points []Point, th Thresholds) {
	add := func(ind Indicator) {
		v.Indicators = append(v.Indicators, ind)
		if ind.Level.rank() > v.Level.rank() {
			v.Level = ind.Level
		}
		if ind.Level == Warning || ind.Level == Alert {
			v.Triggered = append(v.Triggered, fmt.Sprintf("%s : %s", ind.Label, ind.Why))
		}
	}
	for _, pat := range []struct{ p, label string }{{"read", "lecture"}, {"write", "écriture"}} {
		var qd1 *Point
		for i := range points {
			if points[i].Pattern == pat.p && points[i].QD == 1 && points[i].BlockKiB == 4 {
				qd1 = &points[i]
			}
		}
		label := "Latence 4 Kio QD1 p99, " + pat.label
		if qd1 == nil {
			add(Indicator{Key: "qd1_p99_" + pat.p, Label: label, Level: Unknown, Why: "palier absent"})
			continue
		}
		ind := Indicator{Key: "qd1_p99_" + pat.p, Label: label, Value: qd1.P99Ms, Unit: "ms", Reliable: true, Level: OK,
			Why: fr("%.2f ms (seuils %.0f / %.0f)", qd1.P99Ms, th.QD1P99WarnMs, th.QD1P99AlertMs)}
		switch {
		case qd1.P99Ms > th.QD1P99AlertMs:
			ind.Level = Alert
		case qd1.P99Ms > th.QD1P99WarnMs:
			ind.Level = Warning
		}
		add(ind)
	}

	var bad, measured, cpuBad int
	worst := 0.0
	var badSteps, cpuSteps []string
	for i := range points {
		p := &points[i]
		name := fmt.Sprintf("%s %d Kio QD%d", map[string]string{"read": "lecture", "write": "écriture", "mixed": "mixte"}[p.Pattern], p.BlockKiB, p.QD)
		if p.MismatchPct != nil {
			measured++
			if *p.MismatchPct > worst {
				worst = *p.MismatchPct
			}
			if *p.MismatchPct > th.IOPSMismatchWarnPct {
				bad++
				badSteps = append(badSteps, name)
				p.Flags = append(p.Flags, "mesure à vérifier")
			}
		}
		if p.CPUPct > th.WorkerCPUWarnPct {
			cpuBad++
			cpuSteps = append(cpuSteps, name)
			p.Flags = append(p.Flags, "limité par l'appliance, pas par le stockage")
		}
	}
	if measured == 0 {
		add(Indicator{Key: "iops_mismatch", Label: "Écart IOPS appliance / vSphere", Level: Unknown, Why: "compteurs vSphere indisponibles"})
	} else {
		ind := Indicator{Key: "iops_mismatch", Label: "Écart IOPS appliance / vSphere", Value: worst, Unit: "%", Reliable: true, Level: OK,
			Why: fr("au plus %.1f %% sur %d palier(s) (seuil %.0f %%)", worst, measured, th.IOPSMismatchWarnPct)}
		if bad > 0 {
			ind.Level = Warning
			ind.Why = fmt.Sprintf("%s — mesure à vérifier : %s", ind.Why, strings.Join(badSteps, ", "))
		}
		add(ind)
	}
	ind := Indicator{Key: "worker_cpu", Label: "CPU du worker", Reliable: true, Level: OK, Why: fr("sous %.0f %% sur tous les paliers", th.WorkerCPUWarnPct)}
	if cpuBad > 0 {
		ind.Level = Warning
		ind.Why = fr("au-delà de %.0f %% sur %s : résultat limité par l'appliance, passer la VM à 4 vCPU", th.WorkerCPUWarnPct, strings.Join(cpuSteps, ", "))
	}
	add(ind)
}

// SortPoints trie par motif, taille de bloc puis profondeur de file.
func SortPoints(p []Point) {
	order := map[string]int{"read": 0, "write": 1, "mixed": 2}
	sort.SliceStable(p, func(i, j int) bool {
		if p[i].BlockKiB != p[j].BlockKiB {
			return p[i].BlockKiB < p[j].BlockKiB
		}
		if p[i].Pattern != p[j].Pattern {
			return order[p[i].Pattern] < order[p[j].Pattern]
		}
		return p[i].QD < p[j].QD
	})
}
