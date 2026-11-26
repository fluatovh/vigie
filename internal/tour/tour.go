// Package tour exécute la tournée de comparaison du §3.3 : l'appliance passe d'un hôte à l'autre par
// vMotion en alternance (A, B, A, B…), un test standard par passage, puis un verdict comparatif sur
// les médianes par hôte. Un override DRS manuel peut être posé sur l'appliance seule pendant la
// tournée ; l'état DRS d'origine est toujours restauré, de même que l'hôte de départ si demandé.
package tour

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/standard"
	"vigie/internal/testdisk"
	"vigie/internal/vsphere"
)

// Options de la tournée.
type Options struct {
	Hosts       []string         `json:"hosts"`  // références HostSystem:host-…
	Cycles      int              `json:"cycles"` // 2 par défaut
	DRSOverride bool             `json:"drs_override"`
	ReturnHome  bool             `json:"return_home"`
	Standard    standard.Options `json:"standard"`
}

// Leg est un passage sur un hôte.
type Leg struct {
	Cycle   int              `json:"cycle"`
	Host    string           `json:"host"`
	HostRef string           `json:"host_ref"`
	Moved   bool             `json:"moved"`
	MoveS   float64          `json:"vmotion_s,omitempty"`
	Report  *standard.Report `json:"report"`
}

// Report est le résultat d'une tournée.
type Report struct {
	Started    time.Time           `json:"started"`
	Ended      time.Time           `json:"ended"`
	Options    Options             `json:"options"`
	HomeHost   string              `json:"home_host"`
	DRS        *vsphere.DRSState   `json:"drs,omitempty"`
	Legs       []Leg               `json:"legs"`
	Comparison analysis.Comparison `json:"comparison"`
	Warnings   []string            `json:"warnings,omitempty"`
}

// Deps : services nécessaires.
type Deps struct {
	Session  *vsphere.Session
	Identify func(ctx context.Context) (*vsphere.Self, error)
	Record   func() *testdisk.Record
	Runner   *bench.Runner
	Progress func(pct float64, msg string)
	OnSecond func(kind bench.Kind, s bench.Second)
	Version  string
}

// Run exécute la tournée.
func Run(ctx context.Context, d Deps, opt Options) (rep *Report, err error) {
	if len(opt.Hosts) < 2 {
		return nil, errors.New("au moins deux hôtes pour une tournée")
	}
	if opt.Cycles <= 0 {
		opt.Cycles = 2
	}
	opt.Standard.Defaults()
	prog := d.Progress
	if prog == nil {
		prog = func(float64, string) {}
	}
	s := d.Session
	self, err := d.Identify(ctx)
	if err != nil {
		return nil, err
	}
	rep = &Report{Started: time.Now(), Options: opt, HomeHost: self.Host.Name}
	home, _ := vsphere.Ref(self.Host.Ref)
	warn := func(f string, a ...any) {
		m := fmt.Sprintf(f, a...)
		rep.Warnings = append(rep.Warnings, m)
		log.Printf("tour: %s", m)
	}

	// Prévalidation de tous les hôtes avant de bouger.
	rec := d.Record()
	if rec == nil {
		return nil, errors.New("aucun disque de test")
	}
	dsRef, _ := vsphere.Ref(rec.DatastoreRef)
	vmRef, _ := vsphere.Ref(self.VM.Ref)
	names := map[string]string{}
	for i, h := range opt.Hosts {
		ref, ok := vsphere.Ref(h)
		if !ok {
			return nil, fmt.Errorf("hôte %q invalide", h)
		}
		prog(float64(i)/float64(len(opt.Hosts)), fmt.Sprintf("prévalidation du vMotion vers %s", h))
		c := vsphere.CheckMigration(ctx, s, vmRef, ref, &dsRef)
		if !c.OK {
			return nil, fmt.Errorf("vMotion vers %s impossible : %v", c.Host, c.Errors)
		}
		names[h] = c.Host
	}

	// Override DRS (appliance seule), toujours restauré.
	if opt.DRSOverride {
		st, err := vsphere.DRSOf(ctx, s, self)
		if err != nil {
			warn("lecture du DRS : %v", err)
		} else if st.Enabled {
			rep.DRS = st
			prog(2, "override DRS « manuel » sur l'appliance")
			if err := vsphere.SetDRSOverride(ctx, s, self, st, true); err != nil {
				return nil, fmt.Errorf("override DRS : %w", err)
			}
			defer func() {
				c, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				if e := vsphere.SetDRSOverride(c, s, self, st, false); e != nil {
					warn("restauration du DRS : %v", e)
				} else {
					log.Printf("tour: DRS restauré")
				}
			}()
		}
	}

	total := opt.Cycles * len(opt.Hosts)
	share := 95.0 / float64(total)
	n := 0
	for cyc := 1; cyc <= opt.Cycles; cyc++ {
		for _, h := range opt.Hosts {
			if ctx.Err() != nil {
				return rep, ctx.Err()
			}
			base := 3 + float64(n)*share
			label := fmt.Sprintf("cycle %d/%d, %s", cyc, opt.Cycles, names[h])
			leg := Leg{Cycle: cyc, Host: names[h], HostRef: h}
			cur, err := d.Identify(ctx)
			if err != nil {
				return rep, err
			}
			if cur.Host.Ref != h {
				t0 := time.Now()
				ref, _ := vsphere.Ref(h)
				if err := vsphere.MigrateSelf(ctx, s, cur, ref, func(p int, _ string) {
					prog(base+share*0.08*float64(p)/100, fmt.Sprintf("%s : vMotion %d %%", label, p))
				}); err != nil {
					return rep, err
				}
				leg.Moved, leg.MoveS = true, time.Since(t0).Seconds()
				time.Sleep(5 * time.Second) // stabilisation réseau après vMotion
				if cur, err = d.Identify(ctx); err != nil {
					return rep, err
				}
				if cur.Host.Ref != h {
					return rep, fmt.Errorf("après vMotion, l'appliance est sur %s et non %s (DRS ?)", cur.Host.Name, names[h])
				}
			}
			o := opt.Standard
			o.Prefill = n == 0 && o.Prefill
			r, err := standard.Run(ctx, standard.Deps{Session: s, Self: cur, Record: d.Record(), Runner: d.Runner, OnSecond: d.OnSecond, Version: d.Version,
				Progress: func(p float64, m string) { prog(base+share*0.08+share*0.92*p/100, label+" : "+m) }}, o)
			if err != nil {
				return rep, fmt.Errorf("%s : %w", label, err)
			}
			leg.Report = r
			rep.Legs = append(rep.Legs, leg)
			n++
		}
	}

	var runs []analysis.HostRun
	for _, l := range rep.Legs {
		hr := analysis.HostRun{Host: l.Host, Cycle: l.Cycle, WriteMBps: l.Report.Write.MBps, ReadMBps: l.Report.Read.MBps,
			WriteP99: l.Report.Write.Latency.P99 / 1000, Level: l.Report.Verdict.Level}
		if l.Report.TxRatio != nil && l.Report.TxRatio.Samples >= analysis.MinSamples {
			hr.TxRatio = l.Report.TxRatio.Value
		}
		runs = append(runs, hr)
	}
	rep.Comparison = analysis.Compare(runs, opt.Standard.Thresholds)

	if opt.ReturnHome {
		cur, err := d.Identify(ctx)
		if err == nil && cur.Host.Ref != home.String() {
			prog(98, fmt.Sprintf("retour sur l'hôte de départ %s", rep.HomeHost))
			if err := vsphere.MigrateSelf(ctx, s, cur, home, nil); err != nil {
				warn("retour sur l'hôte de départ : %v", err)
			}
		}
	}
	rep.Ended = time.Now()
	prog(100, fmt.Sprintf("terminé — %s", rep.Comparison.Sentence))
	return rep, nil
}
