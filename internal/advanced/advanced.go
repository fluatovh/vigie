// Package advanced exécute le test avancé du §3.4 : le test standard, puis la matrice de paliers
// aléatoires (4 Kio lecture / écriture / mixte 70-30 × QD 1, 4, 16, 32 ; option « complet » : 8 et
// 64 Kio), avec contrôle croisé des IOPS par les compteurs vSphere, IOPS utiles sous plafond de
// latence et garde-fous (CPU du worker, écart appliance / hôte).
package advanced

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/standard"
	"vigie/internal/vsphere"
)

// Options du test avancé.
type Options struct {
	Standard  standard.Options `json:"standard"`
	Full      bool             `json:"full"`       // 8 et 64 Kio en plus de 4 Kio
	CeilingMs float64          `json:"ceiling_ms"` // plafond des IOPS utiles (défaut : préréglage)
	WarmupS   int              `json:"warmup_s"`   // 5
	MeasureS  int              `json:"measure_s"`  // 40
	QDs       []int            `json:"qds"`        // 1, 4, 16, 32
}

// Defaults complète les options.
func (o *Options) Defaults() {
	o.Standard.Defaults()
	if o.CeilingMs <= 0 {
		o.CeilingMs = o.Standard.Thresholds.UsefulIOPSCeilingMs
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

// Matrix renvoie les paliers du test.
func (o Options) Matrix() []bench.Step {
	blocks := []int{4}
	if o.Full {
		blocks = append(blocks, 8, 64)
	}
	var steps []bench.Step
	for _, b := range blocks {
		for _, pat := range []struct {
			kind bench.Kind
			pct  int
			name string
		}{{bench.RandRead, 100, "lecture"}, {bench.RandWrite, 0, "écriture"}, {bench.RandMix, 70, "mixte 70/30"}} {
			for _, qd := range o.QDs {
				steps = append(steps, bench.Step{Kind: pat.kind, ReadPct: pat.pct, BlockKiB: b, QD: qd, Warmup: o.WarmupS, Duration: o.MeasureS,
					Name: fmt.Sprintf("%s %d Kio QD%d", pat.name, b, qd)})
			}
		}
	}
	return steps
}

// Report est le résultat du test avancé.
type Report struct {
	Started   time.Time         `json:"started"`
	Ended     time.Time         `json:"ended"`
	Options   Options           `json:"options"`
	Standard  *standard.Report  `json:"standard"`
	Steps     []*bench.Result   `json:"steps"`
	Points    []analysis.Point  `json:"points"`
	Useful    []analysis.Useful `json:"useful"`
	Headline  Headline          `json:"headline"`
	Counters  map[string]string `json:"counters"`
	Instance  string            `json:"vdisk_instance"`
	WorkerCPU int               `json:"worker_cpus"`
	Verdict   analysis.Verdict  `json:"verdict"`
	Warnings  []string          `json:"warnings,omitempty"`
}

// Headline : les trois chiffres titres du §3.4.
type Headline struct {
	WriteMBps   float64 `json:"write_mbps"`
	ReadMBps    float64 `json:"read_mbps"`
	UsefulIOPS  float64 `json:"useful_iops"` // 4 Kio aléatoire mixte 70/30 sous plafond
	UsefulQD    int     `json:"useful_qd"`
	CeilingMs   float64 `json:"ceiling_ms"`
	ReadQD1P50  float64 `json:"read_qd1_p50_ms"`
	ReadQD1P99  float64 `json:"read_qd1_p99_ms"`
	WriteQD1P50 float64 `json:"write_qd1_p50_ms"`
	WriteQD1P99 float64 `json:"write_qd1_p99_ms"`
}

// Progress : avancement (0-100) et message.
type Progress func(pct float64, msg string)

// Run exécute le test avancé.
func Run(ctx context.Context, d standard.Deps, opt Options) (*Report, error) {
	opt.Defaults()
	prog := d.Progress
	if prog == nil {
		prog = func(float64, string) {}
	}
	if d.Sleep == nil {
		d.Sleep = func(ctx context.Context, dur time.Duration) error {
			t := time.NewTimer(dur)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	steps := opt.Matrix()
	stdS := float64(opt.Standard.BaselineS+opt.Standard.WriteS+opt.Standard.ReadS) + 45
	matS := float64(len(steps) * (opt.WarmupS + opt.MeasureS))
	stdShare := 100 * stdS / (stdS + matS + 60)
	rep := &Report{Started: time.Now(), Options: opt}
	warn := func(format string, a ...any) {
		m := fmt.Sprintf(format, a...)
		rep.Warnings = append(rep.Warnings, m)
		log.Printf("advanced: %s", m)
	}

	// 1. Test standard (débit séquentiel, ratio TX/écrit, santé du chemin).
	sd := d
	sd.Progress = func(p float64, m string) { prog(p*stdShare/100, fmt.Sprintf("standard : %s", m)) }
	std, err := standard.Run(ctx, sd, opt.Standard)
	if err != nil {
		return nil, fmt.Errorf("test standard : %w", err)
	}
	rep.Standard = std

	// 2. Matrice aléatoire (un seul worker pour tous les paliers).
	s := d.Session
	hostRef, _ := vsphere.Ref(d.Self.Host.Ref)
	vmRef, _ := vsphere.Ref(d.Self.VM.Ref)
	clk, err := vsphere.MeasureClock(ctx, s, hostRef)
	if err != nil {
		return nil, err
	}
	done, total := 0.0, matS
	r, err := d.Runner.Run(ctx, bench.Request{Record: *d.Record, Sync: opt.Standard.Sync, Steps: steps}, func(e bench.Event) {
		switch e.Type {
		case "start":
			rep.WorkerCPU = len(e.CPUs)
		case "second":
			done++
			if e.Second != nil && d.OnSecond != nil {
				d.OnSecond(e.Kind, *e.Second)
			}
			if e.Second != nil && e.Second.T > 0 {
				prog(stdShare+(100-stdShare-8)*done/total, fmt.Sprintf("palier %d/%d %s : %d s — %s IO/s — p99 %.2f ms", e.Step, len(steps), steps[e.Step-1].Name, e.Second.T, fmtInt(e.Second.Ops), e.Second.P99US/1000))
			} else {
				prog(stdShare+(100-stdShare-8)*done/total, fmt.Sprintf("palier %d/%d %s : chauffe", e.Step, len(steps), steps[e.Step-1].Name))
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("matrice : %w", err)
	}
	if len(r.Results) != len(steps) {
		return nil, fmt.Errorf("matrice incomplète (%d/%d paliers)", len(r.Results), len(steps))
	}
	rep.Steps = r.Results
	if rep.WorkerCPU == 0 {
		rep.WorkerCPU = len(r.CPUs)
	}
	if rep.WorkerCPU == 0 {
		rep.WorkerCPU = 1
	}

	// 3. Compteurs vSphere du disque virtuel (et du datastore côté hôte) sur toute la matrice.
	counters, err := vsphere.ResolveCounters(ctx, s)
	if err != nil {
		return nil, err
	}
	rep.Counters = counters.Names
	inst, err := vsphere.DiskInstance(ctx, s, vmRef, d.Record.ControllerKey, d.Record.UnitNumber)
	if err != nil {
		warn("instance du disque virtuel : %v", err)
	}
	rep.Instance = inst
	first, last := windowOf(r.Results[0], clk), windowOf(r.Results[len(r.Results)-1], clk)
	var ser vsphere.Series
	for attempt := 0; ; attempt++ {
		prog(93, "attente des échantillons PerformanceManager")
		ser, err = vsphere.QueryRealtime(ctx, s, counters, vmRef, []string{"vdReadOps", "vdWriteOps", "vdReadLat", "vdWriteLat"}, first.Start.Add(-40*time.Second), last.End.Add(40*time.Second))
		if err != nil {
			warn("compteurs du disque virtuel : %v", err)
			break
		}
		if latest(ser, inst).After(last.End.Add(-20*time.Second)) || attempt >= 6 {
			break
		}
		if err := d.Sleep(ctx, 15*time.Second); err != nil {
			return nil, err
		}
	}

	// 4. Points, contrôle croisé, IOPS utiles, verdict.
	prog(98, "analyse")
	for _, res := range r.Results {
		p := analysis.Point{Pattern: patternOf(res.Step.Kind), BlockKiB: res.Step.BlockKiB, QD: res.Step.QD, IOPS: res.IOPS,
			P50Ms: res.Latency.P50 / 1000, P99Ms: res.Latency.P99 / 1000, CPUPct: res.CPUPct / float64(rep.WorkerCPU)}
		if inst != "" && ser != nil {
			w := windowOf(res, clk)
			var opsTicks []analysis.Tick
			for _, sec := range res.Seconds {
				if sec.T > 0 {
					opsTicks = append(opsTicks, analysis.Tick{At: clk.ToHost(sec.At), Bytes: sec.Ops})
				}
			}
			// Marge de 3 s : un échantillon de 20 s de l'hôte peut déborder de quelques secondes sur la
			// chauffe du palier suivant (biais positif observé sur le lab avant les sauts d'IOPS).
			wm := analysis.Window{Start: w.Start.Add(-3 * time.Second), End: w.End.Add(-3 * time.Second)}
			wm.Start = w.Start
			reads := analysis.Inside(ser["vdReadOps"][inst], wm)
			writes := analysis.Inside(ser["vdWriteOps"][inst], wm)
			if len(reads) > 0 && len(reads) == len(writes) {
				var hostOps, appOps, dur float64
				for i := range reads {
					sec := reads[i].End.Sub(reads[i].Start).Seconds()
					hostOps += (reads[i].Value + writes[i].Value) * sec
					appOps += analysis.BytesIn(opsTicks, reads[i].Start, reads[i].End)
					dur += sec
				}
				h := hostOps / dur
				mm := analysis.Mismatch(appOps/dur, h)
				p.HostIOPS, p.MismatchPct = &h, &mm
			}
		}
		rep.Points = append(rep.Points, p)
	}
	if inst != "" && ser != nil && rep.Points[0].HostIOPS == nil {
		warn("aucun échantillon de 20 s complet dans les paliers : contrôle croisé impossible (mesure trop courte ?)")
	}
	rep.Useful = analysis.UsefulIOPS(rep.Points, opt.CeilingMs)
	rep.Verdict = std.Verdict
	rep.Verdict.Triggered = append([]string(nil), std.Verdict.Triggered...)
	rep.Verdict.Indicators = append([]analysis.Indicator(nil), std.Verdict.Indicators...)
	analysis.EvaluateAdvanced(&rep.Verdict, rep.Points, opt.Standard.Thresholds)
	rep.Headline = headline(rep, opt.CeilingMs)
	rep.Ended = time.Now()
	prog(100, fmt.Sprintf("terminé — verdict : %s", rep.Verdict.Level))
	return rep, nil
}

func headline(rep *Report, ceiling float64) Headline {
	h := Headline{WriteMBps: rep.Standard.Write.MBps, ReadMBps: rep.Standard.Read.MBps, CeilingMs: ceiling}
	for _, u := range rep.Useful {
		if u.Pattern == "mixed" && u.BlockKiB == 4 {
			h.UsefulIOPS, h.UsefulQD = u.IOPS, u.QD
		}
	}
	for _, p := range rep.Points {
		if p.QD == 1 && p.BlockKiB == 4 {
			switch p.Pattern {
			case "read":
				h.ReadQD1P50, h.ReadQD1P99 = p.P50Ms, p.P99Ms
			case "write":
				h.WriteQD1P50, h.WriteQD1P99 = p.P50Ms, p.P99Ms
			}
		}
	}
	return h
}

func patternOf(k bench.Kind) string {
	switch k {
	case bench.RandRead:
		return "read"
	case bench.RandWrite:
		return "write"
	}
	return "mixed"
}

// windowOf renvoie la fenêtre mesurée d'un palier (hors chauffe), en heure de l'hôte.
func windowOf(r *bench.Result, clk vsphere.Clock) analysis.Window {
	var w analysis.Window
	for _, s := range r.Seconds {
		if s.T <= 0 || s.At.IsZero() {
			continue
		}
		at := clk.ToHost(s.At)
		if w.Start.IsZero() {
			w.Start = at.Add(-time.Second)
		}
		w.End = at
	}
	return w
}

func latest(ser vsphere.Series, inst string) time.Time {
	var t time.Time
	for _, s := range ser["vdReadOps"][inst] {
		if s.End.After(t) {
			t = s.End
		}
	}
	return t
}

func fmtInt(n int64) string {
	s := fmt.Sprintf("%d", n)
	out := ""
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out += " "
		}
		out += string(c)
	}
	return out
}

var _ = errors.New
