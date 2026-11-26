// Package standard exécute le test standard du §3.4 sur l'hôte courant : ligne de base au repos,
// écriture séquentielle puis lecture séquentielle au temps, collecte PerformanceManager, journaux et
// événements, analyse et verdict. Synchrone en phase 5 ; la phase 6 l'enveloppe dans un job.
package standard

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/vmware/govmomi/vim25/types"

	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/testdisk"
	"vigie/internal/vsphere"
)

// Options paramètrent le test (durées du plan par défaut).
type Options struct {
	BaselineS  int                 `json:"baseline_s"` // 60
	WriteS     int                 `json:"write_s"`    // 80
	ReadS      int                 `json:"read_s"`     // 80
	BlockKiB   int                 `json:"block_kib"`  // 1024
	QD         int                 `json:"qd"`         // 4
	Sync       bool                `json:"sync"`       // O_SYNC (défaut true côté API)
	Prefill    bool                `json:"prefill"`    // préremplir si nécessaire (défaut true côté API)
	Thresholds analysis.Thresholds `json:"thresholds"`
}

// Defaults complète les options.
func (o *Options) Defaults() {
	if o.BaselineS <= 0 {
		o.BaselineS = 60
	}
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
}

// Progress reçoit l'avancement (0-100) et un message.
type Progress func(pct float64, msg string)

// Report est le résultat d'un test standard sur un hôte.
type Report struct {
	Started   time.Time                  `json:"started"`
	Ended     time.Time                  `json:"ended"`
	Options   Options                    `json:"options"`
	Appliance string                     `json:"appliance_version,omitempty"` // version de vigie qui a mesuré
	Server    *vsphere.ServerInfo        `json:"server,omitempty"`            // vCenter (ou ESXi) interrogé
	Host      vsphere.HostInfo           `json:"host"`
	VM        vsphere.ObjectInfo         `json:"vm"`
	Datastore vsphere.DatastoreInfo      `json:"datastore"`
	Path      *vsphere.Path              `json:"path"`
	Clock     vsphere.Clock              `json:"clock"`
	Counters  map[string]string          `json:"counters"`
	Windows   map[string]analysis.Window `json:"windows"` // baseline, write, read (heure hôte)
	Prefill   *bench.Result              `json:"prefill,omitempty"`
	Write     *bench.Result              `json:"write"`
	Read      *bench.Result              `json:"read"`
	TxRatio   *analysis.Ratio            `json:"tx_ratio,omitempty"`
	RxRatio   *analysis.Ratio            `json:"rx_ratio,omitempty"`
	PerVmnic  []VmnicStats               `json:"per_vmnic"`
	DSLatency map[string]float64         `json:"datastore_latency_ms"` // write, read : moyenne vue par l'hôte
	DSIOPS    map[string]float64         `json:"datastore_iops"`       // write, read : vus par l'hôte
	Log       *vsphere.LogReport         `json:"vmkernel_log,omitempty"`
	Events    []vsphere.HostEvent        `json:"events"`
	Verdict   analysis.Verdict           `json:"verdict"`
	Warnings  []string                   `json:"warnings,omitempty"`
}

// VmnicStats détaille un uplink du chemin NFS.
type VmnicStats struct {
	Device      string  `json:"device"`
	Role        string  `json:"role"`
	SpeedMb     int32   `json:"speed_mb"`
	TxWriteMBps float64 `json:"tx_write_mbps"` // pendant l'écriture (moyenne des échantillons complets)
	RxReadMBps  float64 `json:"rx_read_mbps"`  // pendant la lecture
	TxBaseMBps  float64 `json:"tx_baseline_mbps"`
	ErrorsRx    float64 `json:"errors_rx"`
	DroppedRx   float64 `json:"dropped_rx"`
	ErrorsTx    float64 `json:"errors_tx"`
	DroppedTx   float64 `json:"dropped_tx"`
	// Esxcli : Δ des compteurs détaillés pendant le test (esxcli network nic stats get, phase 10) ;
	// absent si esxcli n'est pas autorisé.
	Esxcli vsphere.NICCounters `json:"esxcli,omitempty"`
}

// Deps sont les services nécessaires.
type Deps struct {
	Session  *vsphere.Session
	Self     *vsphere.Self
	Record   *testdisk.Record
	Runner   *bench.Runner
	Progress Progress
	OnSecond func(kind bench.Kind, s bench.Second) // série à la seconde (mini-graph en direct)
	Version  string                                // version de l'appliance, recopiée dans le rapport
	// Sleep et Now sont remplaçables dans les tests.
	Sleep func(ctx context.Context, d time.Duration) error
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Run exécute le test standard.
func Run(ctx context.Context, d Deps, opt Options) (*Report, error) {
	opt.Defaults()
	if d.Sleep == nil {
		d.Sleep = sleepCtx
	}
	prog := d.Progress
	if prog == nil {
		prog = func(float64, string) {}
	}
	if d.Record == nil {
		return nil, errors.New("aucun disque de test")
	}
	s := d.Session
	rep := &Report{Started: time.Now(), Options: opt, Host: d.Self.Host, VM: d.Self.VM, Windows: map[string]analysis.Window{},
		DSLatency: map[string]float64{}, DSIOPS: map[string]float64{}, Appliance: d.Version}
	if s != nil {
		srv := s.Server
		rep.Server = &srv
	}
	hostRef, _ := vsphere.Ref(d.Self.Host.Ref)
	dsRef, ok := vsphere.Ref(d.Record.DatastoreRef)
	if !ok {
		return nil, errors.New("datastore du disque de test inconnu")
	}
	warn := func(format string, a ...any) {
		msg := fmt.Sprintf(format, a...)
		rep.Warnings = append(rep.Warnings, msg)
		log.Printf("standard: %s", msg)
	}

	// 1. Contexte : datastore, chemin réseau, compteurs, horloges, curseur de journal.
	prog(1, "lecture du datastore et du chemin réseau")
	ds, err := vsphere.Datastore(ctx, s, dsRef, &hostRef)
	if err != nil {
		return nil, err
	}
	rep.Datastore = *ds
	if ds.IsNFS() {
		if p, err := vsphere.HostPath(ctx, s, hostRef, dsRef); err != nil {
			warn("chemin NFS non déterminé : %v", err)
		} else {
			rep.Path = p
		}
	} else {
		warn("datastore %s de type %s : le ratio TX/écrit ne s'applique qu'à NFS", ds.Name, ds.Type)
	}
	counters, err := vsphere.ResolveCounters(ctx, s)
	if err != nil {
		return nil, err
	}
	rep.Counters = counters.Names
	clk, err := vsphere.MeasureClock(ctx, s, hostRef)
	if err != nil {
		return nil, err
	}
	rep.Clock = clk
	cursor, err := vsphere.OpenLog(ctx, s, hostRef, "vmkernel")
	if err != nil {
		warn("vmkernel.log illisible : %v", err)
	}

	// 2. Préremplissage éventuel (avant la ligne de base pour ne pas la polluer).
	if opt.Prefill {
		prog(3, "préremplissage du disque de test si nécessaire")
		r, err := d.Runner.Run(ctx, bench.Request{Record: *d.Record, Prefill: true, Sync: opt.Sync}, func(e bench.Event) {
			if e.Type == "prefill" && e.Result == nil {
				prog(3+e.Percent*0.17, fmt.Sprintf("préremplissage %.0f %%", e.Percent))
			}
		})
		if err != nil {
			return nil, fmt.Errorf("préremplissage : %w", err)
		}
		rep.Prefill = r.Prefill
	}

	// Compteurs détaillés des uplinks (esxcli via l'API), relevés avant et après la mesure.
	var nicDevs []string
	if rep.Path != nil {
		for _, u := range rep.Path.Uplinks {
			nicDevs = append(nicDevs, u.Device)
		}
	}
	var nicBefore, nicDelta map[string]vsphere.NICCounters
	if len(nicDevs) > 0 {
		if nicBefore, err = vsphere.NICStats(ctx, s, hostRef, nicDevs); err != nil {
			warn("compteurs détaillés des vmnic (esxcli) : %v", err)
			nicBefore = nil
		}
	}

	// 3. Ligne de base au repos.
	prog(20, fmt.Sprintf("ligne de base au repos : %d s", opt.BaselineS))
	bStart := clk.ToHost(time.Now())
	if err := d.Sleep(ctx, time.Duration(opt.BaselineS)*time.Second); err != nil {
		return nil, err
	}
	bEnd := clk.ToHost(time.Now())
	rep.Windows["baseline"] = analysis.Window{Start: bStart, End: bEnd}

	// 4. Écriture puis lecture séquentielles au temps.
	total := float64(opt.WriteS + opt.ReadS)
	elapsed := 0.0
	steps := []bench.Step{
		{Kind: bench.SeqWrite, Duration: opt.WriteS, BlockKiB: opt.BlockKiB, QD: opt.QD, Name: "écriture séquentielle"},
		{Kind: bench.SeqRead, Duration: opt.ReadS, BlockKiB: opt.BlockKiB, QD: opt.QD, Name: "lecture séquentielle"},
	}
	prog(40, "écriture continue")
	br, err := d.Runner.Run(ctx, bench.Request{Record: *d.Record, Sync: opt.Sync, Steps: steps}, func(e bench.Event) {
		if e.Type == "second" && e.Second != nil && d.OnSecond != nil {
			d.OnSecond(e.Kind, *e.Second)
		}
		if e.Type == "second" && e.Second != nil && e.Second.T > 0 {
			elapsed++
			what := "écriture"
			if e.Kind == bench.SeqRead {
				what = "lecture"
			}
			prog(40+40*elapsed/total, fmt.Sprintf("%s continue : %d s — %.0f Mo/s — p99 %.1f ms", what, e.Second.T, float64(e.Second.Bytes)/1e6, e.Second.P99US/1000))
		}
	})
	if err != nil {
		return nil, fmt.Errorf("mesure : %w", err)
	}
	if len(br.Results) != 2 {
		return nil, fmt.Errorf("mesure incomplète (%d palier(s))", len(br.Results))
	}
	rep.Write, rep.Read = br.Results[0], br.Results[1]
	if nicBefore != nil {
		if after, err := vsphere.NICStats(ctx, s, hostRef, nicDevs); err != nil {
			warn("compteurs détaillés des vmnic (esxcli) après la mesure : %v", err)
		} else {
			nicDelta = vsphere.Delta(nicBefore, after)
		}
	}
	wTicks, wWin := ticks(rep.Write, clk)
	rTicks, rWin := ticks(rep.Read, clk)
	rep.Windows["write"], rep.Windows["read"] = wWin, rWin

	// 5. Attente des échantillons (disponibles 20 à 40 s après leur fin).
	need := rWin.End
	var ser vsphere.Series
	keys := []string{"tx", "rx", "errorsRx", "droppedRx", "errorsTx", "droppedTx", "dsWriteLat", "dsReadLat", "dsWriteOps", "dsReadOps"}
	for attempt := 0; ; attempt++ {
		prog(82, "attente des échantillons PerformanceManager")
		ser, err = vsphere.QueryRealtime(ctx, s, counters, hostRef, keys, bStart.Add(-40*time.Second), need.Add(40*time.Second))
		if err != nil {
			return nil, err
		}
		if lastEnd(ser).After(need.Add(-time.Duration(vsphere.RealtimeInterval)*time.Second)) || attempt >= 6 {
			if attempt >= 6 {
				warn("échantillons PerformanceManager incomplets après 90 s d'attente")
			}
			break
		}
		if err := d.Sleep(ctx, 15*time.Second); err != nil {
			return nil, err
		}
	}

	// 6. Analyse.
	prog(92, "analyse")
	if rep.Path != nil {
		var uplinks []string
		for _, u := range rep.Path.Uplinks {
			if u.Role == "active" {
				uplinks = append(uplinks, u.Device)
			}
		}
		if len(uplinks) == 0 {
			warn("aucun uplink actif sur le chemin NFS")
		}
		tx := vsphere.SumInstances(ser["tx"], uplinks)
		rx := vsphere.SumInstances(ser["rx"], uplinks)
		tr := analysis.ComputeRatio(analysis.RatioInput{Baseline: analysis.Inside(tx, rep.Windows["baseline"]), Bench: tx, Window: wWin, Ticks: wTicks})
		rr := analysis.ComputeRatio(analysis.RatioInput{Baseline: analysis.Inside(rx, rep.Windows["baseline"]), Bench: rx, Window: rWin, Ticks: rTicks})
		rep.TxRatio, rep.RxRatio = &tr, &rr
		bench := analysis.Window{Start: wWin.Start, End: rWin.End}
		for _, u := range rep.Path.Uplinks {
			vs := VmnicStats{Device: u.Device, Role: u.Role, SpeedMb: u.SpeedMb}
			vs.TxWriteMBps = meanMBps(ser["tx"][u.Device], wWin)
			vs.RxReadMBps = meanMBps(ser["rx"][u.Device], rWin)
			vs.TxBaseMBps = meanMBps(ser["tx"][u.Device], rep.Windows["baseline"])
			vs.ErrorsRx = sumIn(ser["errorsRx"][u.Device], bench)
			vs.DroppedRx = sumIn(ser["droppedRx"][u.Device], bench)
			vs.ErrorsTx = sumIn(ser["errorsTx"][u.Device], bench)
			vs.DroppedTx = sumIn(ser["droppedTx"][u.Device], bench)
			vs.Esxcli = nicDelta[u.Device]
			rep.PerVmnic = append(rep.PerVmnic, vs)
		}
	}
	inst := vsphere.DatastoreInstance(ds)
	if m, _ := analysis.Stats(analysis.Inside(ser["dsWriteLat"][inst], wWin)); len(analysis.Inside(ser["dsWriteLat"][inst], wWin)) > 0 {
		rep.DSLatency["write"] = m
	}
	if m, _ := analysis.Stats(analysis.Inside(ser["dsReadLat"][inst], rWin)); len(analysis.Inside(ser["dsReadLat"][inst], rWin)) > 0 {
		rep.DSLatency["read"] = m
	}
	if m, _ := analysis.Stats(analysis.Inside(ser["dsWriteOps"][inst], wWin)); m > 0 {
		rep.DSIOPS["write"] = m
	}
	if m, _ := analysis.Stats(analysis.Inside(ser["dsReadOps"][inst], rWin)); m > 0 {
		rep.DSIOPS["read"] = m
	}
	if len(ser["dsWriteLat"]) > 0 && len(ser["dsWriteLat"][inst]) == 0 {
		warn("latence datastore : instance %s absente des compteurs de l'hôte", inst)
	}

	if cursor != nil {
		if lr, err := vsphere.ReadNewLines(ctx, s, hostRef, cursor, 20000); err != nil {
			warn("lecture de vmkernel.log : %v", err)
		} else {
			rep.Log = lr
		}
	}
	eventsOK := false
	if evs, err := vsphere.HostEvents(ctx, s, hostRef, bStart.Add(-time.Minute), clk.ToHost(time.Now())); err != nil {
		warn("événements : %v", err)
	} else {
		rep.Events, eventsOK = evs, true
		if rep.Events == nil {
			rep.Events = []vsphere.HostEvent{}
		}
	}

	facts := analysis.Facts{TxRatio: rep.TxRatio, RxRatio: rep.RxRatio, WriteMBps: rep.Write.MBps}
	if rep.Path != nil && len(ser["errorsRx"]) > 0 {
		var n float64
		for _, v := range rep.PerVmnic {
			n += v.ErrorsRx + v.DroppedRx
		}
		facts.NICErrors = &n
	}
	if rep.Log != nil {
		n := rep.Log.NFSWarning
		facts.NFSWarnLines = &n
	}
	if eventsOK {
		n := 0
		for _, e := range rep.Events {
			if e.Flap {
				n++
			}
		}
		facts.LinkFlaps = &n
	}
	if l, ok := rep.DSLatency["write"]; ok {
		facts.DSWriteLatMs = &l
	}
	if nicDelta != nil {
		var phys, missed float64
		for _, c := range nicDelta {
			phys += float64(c["rx_crc"] + c["rx_frame"] + c["rx_length"])
			missed += float64(c["rx_missed"] + c["rx_fifo"] + c["rx_over"])
		}
		facts.PhysErrors, facts.RxMissed = &phys, &missed
	}
	rep.Verdict = analysis.Evaluate(facts, opt.Thresholds)
	rep.Ended = time.Now()
	prog(100, fmt.Sprintf("terminé — verdict : %s", rep.Verdict.Level))
	return rep, nil
}

// ticks convertit les secondes mesurées d'un palier en journal horodaté à l'heure de l'hôte, et
// renvoie la fenêtre mesurée correspondante.
func ticks(r *bench.Result, clk vsphere.Clock) ([]analysis.Tick, analysis.Window) {
	var out []analysis.Tick
	for _, s := range r.Seconds {
		if s.T <= 0 || s.At.IsZero() {
			continue
		}
		out = append(out, analysis.Tick{At: clk.ToHost(s.At), Bytes: s.Bytes})
	}
	if len(out) == 0 {
		return nil, analysis.Window{}
	}
	return out, analysis.Window{Start: out[0].At.Add(-time.Second), End: out[len(out)-1].At}
}

func lastEnd(ser vsphere.Series) time.Time {
	var t time.Time
	for _, inst := range ser["tx"] {
		for _, s := range inst {
			if s.End.After(t) {
				t = s.End
			}
		}
	}
	return t
}

func meanMBps(samples []analysis.Sample, w analysis.Window) float64 {
	m, _ := analysis.Stats(analysis.Inside(samples, w))
	return m * analysis.KBps / 1e6
}

func sumIn(samples []analysis.Sample, w analysis.Window) float64 {
	var n float64
	for _, s := range analysis.Inside(samples, w) {
		n += s.Value
	}
	return n
}

var _ = types.ManagedObjectReference{}
