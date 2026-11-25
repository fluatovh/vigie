// Package localtest exécute les tests de stockage sans vCenter (phase T, onglet Storage de la
// console) : débit séquentiel et, en option, la matrice 4 Kio du test avancé, sur le disque de test
// (adopté en mode local ou créé via vCenter). Seuls les indicateurs mesurés par l'appliance
// s'appliquent : pas de ratio TX/écrit, d'erreurs NIC, de journaux ni de contrôle croisé vSphere.
package localtest

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"vigie/internal/advanced"
	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/testdisk"
)

// Options : durées et contenu du test local.
type Options struct {
	WriteS     int                 `json:"write_s"`   // 80
	ReadS      int                 `json:"read_s"`    // 80
	BlockKiB   int                 `json:"block_kib"` // 1024
	QD         int                 `json:"qd"`        // 4
	Advanced   bool                `json:"advanced"`  // ajoute la matrice 4 Kio aléatoire
	WarmupS    int                 `json:"warmup_s"`  // 5
	MeasureS   int                 `json:"measure_s"` // 40
	QDs        []int               `json:"qds"`       // 1, 4, 16, 32
	CeilingMs  float64             `json:"ceiling_ms"`
	Sync       bool                `json:"sync"`
	Prefill    bool                `json:"prefill"`
	Thresholds analysis.Thresholds `json:"thresholds"`
}

// Defaults complète les options.
func (o *Options) Defaults() {
	if o.WriteS <= 0 {
		o.WriteS = 80
	}
	if o.ReadS <= 0 {
		o.ReadS = 80
	}
	if o.BlockKiB <= 0 {
		o.BlockKiB = 1024
	}
	if o.QD <= 0 {
		o.QD = 4
	}
	if o.Thresholds.Preset == "" {
		o.Thresholds = analysis.DefaultThresholds()
	}
	if o.CeilingMs <= 0 {
		o.CeilingMs = o.Thresholds.UsefulIOPSCeilingMs
	}
	if o.WarmupS <= 0 {
		o.WarmupS = 5
	}
	if o.MeasureS <= 0 {
		o.MeasureS = 40
	}
	if len(o.QDs) == 0 {
		o.QDs = []int{1, 4, 16, 32}
	}
}

// Steps renvoie les paliers du test.
func (o Options) Steps() []bench.Step {
	steps := []bench.Step{
		{Kind: bench.SeqWrite, Duration: o.WriteS, BlockKiB: o.BlockKiB, QD: o.QD, Name: "écriture séquentielle"},
		{Kind: bench.SeqRead, Duration: o.ReadS, BlockKiB: o.BlockKiB, QD: o.QD, Name: "lecture séquentielle"},
	}
	if o.Advanced {
		steps = append(steps, advanced.Options{WarmupS: o.WarmupS, MeasureS: o.MeasureS, QDs: o.QDs}.Matrix()...)
	}
	return steps
}

// Report est le résultat d'un test local.
type Report struct {
	Started   time.Time         `json:"started"`
	Ended     time.Time         `json:"ended"`
	Options   Options           `json:"options"`
	Appliance string            `json:"appliance_version,omitempty"`
	Disk      testdisk.Record   `json:"disk"`
	Prefill   *bench.Result     `json:"prefill,omitempty"`
	Write     *bench.Result     `json:"write"`
	Read      *bench.Result     `json:"read"`
	Steps     []*bench.Result   `json:"steps,omitempty"`
	Points    []analysis.Point  `json:"points,omitempty"`
	Useful    []analysis.Useful `json:"useful,omitempty"`
	WorkerCPU int               `json:"worker_cpus"`
	Verdict   analysis.Verdict  `json:"verdict"`
	Warnings  []string          `json:"warnings,omitempty"`
}

// Deps : services nécessaires.
type Deps struct {
	Record   *testdisk.Record
	Runner   *bench.Runner
	Progress func(pct float64, msg string)
	OnSecond func(step int, kind bench.Kind, s bench.Second)
	Version  string
}

// Run exécute le test local.
func Run(ctx context.Context, d Deps, opt Options) (*Report, error) {
	opt.Defaults()
	if d.Record == nil {
		return nil, errors.New("aucun disque de test : en ajouter un (onglet Storage de la console)")
	}
	prog := d.Progress
	if prog == nil {
		prog = func(float64, string) {}
	}
	steps := opt.Steps()
	total := 0
	for _, s := range steps {
		total += s.Duration + s.Warmup
	}
	rep := &Report{Started: time.Now(), Options: opt, Appliance: d.Version, Disk: *d.Record}
	done := 0
	prog(1, "démarrage du worker")
	r, err := d.Runner.Run(ctx, bench.Request{Record: *d.Record, Prefill: opt.Prefill, Sync: opt.Sync, Steps: steps}, func(e bench.Event) {
		switch e.Type {
		case "start":
			rep.WorkerCPU = len(e.CPUs)
		case "prefill":
			if e.Result == nil {
				prog(1+e.Percent*0.09, fmt.Sprintf("préremplissage %.0f %%", e.Percent))
			}
		case "second":
			done++
			if e.Second != nil && d.OnSecond != nil {
				d.OnSecond(e.Step, e.Kind, *e.Second)
			}
			name := ""
			if e.Step >= 1 && e.Step <= len(steps) {
				name = steps[e.Step-1].Name
			}
			msg := fmt.Sprintf("palier %d/%d %s : chauffe", e.Step, len(steps), name)
			if e.Second != nil && e.Second.T > 0 {
				msg = fmt.Sprintf("palier %d/%d %s : %d s — %.0f Mo/s — %d IO/s — p99 %.2f ms", e.Step, len(steps), name, e.Second.T, float64(e.Second.Bytes)/1e6, e.Second.Ops, e.Second.P99US/1000)
			}
			prog(10+88*float64(done)/float64(total), msg)
		}
	})
	if err != nil {
		return nil, fmt.Errorf("mesure : %w", err)
	}
	if len(r.Results) != len(steps) {
		return nil, fmt.Errorf("mesure incomplète (%d/%d paliers)", len(r.Results), len(steps))
	}
	if rep.WorkerCPU == 0 {
		rep.WorkerCPU = max(1, len(r.CPUs))
	}
	rep.Prefill = r.Prefill
	rep.Write, rep.Read = r.Results[0], r.Results[1]
	rep.Steps = r.Results[2:]
	for _, res := range rep.Steps {
		rep.Points = append(rep.Points, analysis.Point{Pattern: pattern(res.Step.Kind), BlockKiB: res.Step.BlockKiB, QD: res.Step.QD, IOPS: res.IOPS,
			P50Ms: res.Latency.P50 / 1000, P99Ms: res.Latency.P99 / 1000, CPUPct: res.CPUPct / float64(rep.WorkerCPU)})
	}
	if len(rep.Points) > 0 {
		rep.Useful = analysis.UsefulIOPS(rep.Points, opt.CeilingMs)
	}
	rep.Verdict = verdict(rep, opt)
	rep.Warnings = append(rep.Warnings, "Mode local : sans vCenter, ni le ratio TX/écrit, ni les erreurs NIC, ni les journaux de l'hôte ne sont mesurés ; seul ce que voit la VM est évalué.")
	rep.Ended = time.Now()
	prog(100, "terminé — verdict : "+string(rep.Verdict.Level))
	return rep, nil
}

func pattern(k bench.Kind) string {
	switch k {
	case bench.RandRead:
		return "read"
	case bench.RandWrite:
		return "write"
	}
	return "mixed"
}

// verdict : uniquement les indicateurs mesurés par l'appliance.
func verdict(rep *Report, opt Options) analysis.Verdict {
	v := analysis.Evaluate(analysis.Facts{WriteMBps: rep.Write.MBps}, opt.Thresholds)
	if len(rep.Points) > 0 {
		analysis.EvaluateAdvanced(&v, rep.Points, opt.Thresholds)
	}
	keep := map[string]bool{"write_mbps": true, "qd1_p99_read": true, "qd1_p99_write": true, "worker_cpu": true}
	out := analysis.Verdict{Level: analysis.OK, Preset: v.Preset, Triggered: []string{}}
	rank := map[analysis.Level]int{analysis.Unknown: 0, analysis.OK: 1, analysis.Warning: 2, analysis.Alert: 3}
	for _, ind := range v.Indicators {
		if !keep[ind.Key] {
			continue
		}
		out.Indicators = append(out.Indicators, ind)
		if rank[ind.Level] > rank[out.Level] {
			out.Level = ind.Level
		}
		if ind.Level == analysis.Warning || ind.Level == analysis.Alert {
			out.Triggered = append(out.Triggered, ind.Label+" : "+ind.Why)
		}
	}
	return out
}

// Summary : une ligne lisible (journal, historique, console).
func (r *Report) Summary() string {
	s := fmt.Sprintf("%s — écriture %.0f Mo/s, lecture %.0f Mo/s", r.Verdict.Level, r.Write.MBps, r.Read.MBps)
	for _, u := range r.Useful {
		if u.Pattern == "mixed" && u.BlockKiB == 4 {
			s += fmt.Sprintf(", %.0f IO/s utiles (4 Kio 70/30, p99 < %.0f ms)", u.IOPS, u.CeilingMs)
		}
	}
	if len(r.Verdict.Triggered) > 0 {
		s += " ; " + strings.Join(r.Verdict.Triggered, " ; ")
	}
	return s
}
