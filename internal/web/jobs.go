package web

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"github.com/vmware/govmomi/vim25/types"
	"net/http"
	"sort"
	"strconv"
	"time"

	"vigie/internal/advanced"
	"vigie/internal/analysis"
	"vigie/internal/bench"
	"vigie/internal/jobs"
	"vigie/internal/localtest"
	"vigie/internal/standard"
	"vigie/internal/tour"
	"vigie/internal/vsphere"
)

// jobRequest : profil et paramètres. Profils : standard, bench, testdisk.create, testdisk.delete.
type jobRequest struct {
	Profile string `json:"profile"`
	// standard
	Repetitions int    `json:"repetitions"` // 1 ou 3
	BaselineS   int    `json:"baseline_s"`
	WriteS      int    `json:"write_s"`
	ReadS       int    `json:"read_s"`
	BlockKiB    int    `json:"block_kib"`
	QD          int    `json:"qd"`
	Sync        *bool  `json:"sync"`
	Prefill     *bool  `json:"prefill"`
	Preset      string `json:"preset"`
	// bench
	Seed  uint64       `json:"seed"`
	Steps []bench.Step `json:"steps"`
	// advanced, local
	Advanced  bool    `json:"advanced"`
	Full      bool    `json:"full"`
	CeilingMs float64 `json:"ceiling_ms"`
	// net.iperf
	Peer          string `json:"peer"`
	Host          string `json:"host"`
	IperfProtocol string `json:"protocol"`
	IperfDuration int    `json:"duration_s"`
	IperfStreams  int    `json:"streams"`
	IperfReverse  bool   `json:"reverse"`
	IperfBitrate  string `json:"bitrate"`
	// tour / move
	Hosts       []string `json:"hosts"`
	Cycles      int      `json:"cycles"`
	DRSOverride *bool    `json:"drs_override"`
	ReturnHome  *bool    `json:"return_home"`
	TargetHost  string   `json:"target_host"`
	// testdisk.create
	Datastore string `json:"datastore"`
	SizeGB    int    `json:"size_gb"`
}

// StandardResult regroupe les répétitions d'un test standard.
type StandardResult struct {
	Runs    []*standard.Report `json:"runs"`
	Summary StandardSummary    `json:"summary"`
}

// StandardSummary : médianes et pire verdict sur les répétitions.
type StandardSummary struct {
	Host         string         `json:"host"`
	Datastore    string         `json:"datastore"`
	Repetitions  int            `json:"repetitions"`
	WriteMBps    float64        `json:"write_mbps_median"`
	ReadMBps     float64        `json:"read_mbps_median"`
	TxRatio      float64        `json:"tx_ratio_median"`
	WriteP99Ms   float64        `json:"write_p99_ms_median"`
	Level        analysis.Level `json:"level"`
	Triggered    []string       `json:"triggered"`
	WriteProfile string         `json:"write_profile"`
	ReadProfile  string         `json:"read_profile"`
}

func (s *Server) jobsSubmit(w http.ResponseWriter, r *http.Request) {
	if s.jobs == nil {
		writeError(w, http.StatusNotImplemented, "jobs indisponibles")
		return
	}
	var req jobRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "corps JSON invalide : "+err.Error())
		return
	}
	fn, err := s.jobFunc(&req)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	job, err := s.jobs.Submit(req.Profile, req, principal(r).Name, fn)
	if errors.Is(err, jobs.ErrBusy) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Location", "/api/jobs/"+job.ID)
	writeJSON(w, http.StatusAccepted, job)
}

// jobFunc valide la requête et construit le corps du job. Les vérifications qui peuvent échouer
// immédiatement (session, disque) sont faites ici pour répondre 400/409 sans créer de job.
func (s *Server) jobFunc(req *jobRequest) (jobs.Func, error) {
	switch req.Profile {
	case "standard":
		if s.testdisk == nil || s.testdisk.Record() == nil {
			return nil, errors.New("aucun disque de test : lancer d'abord un job testdisk.create")
		}
		if _, err := s.vs.Session(); err != nil {
			return nil, err
		}
		if req.Repetitions == 0 {
			req.Repetitions = 1
		}
		if req.Repetitions != 1 && req.Repetitions != 3 {
			return nil, errors.New("repetitions : 1 ou 3")
		}
		opt := standard.Options{BaselineS: req.BaselineS, WriteS: req.WriteS, ReadS: req.ReadS, BlockKiB: req.BlockKiB, QD: req.QD,
			Sync: req.Sync == nil || *req.Sync, Prefill: req.Prefill == nil || *req.Prefill}
		for _, d := range []int{opt.BaselineS, opt.WriteS, opt.ReadS} {
			if d < 0 || d > 3600 {
				return nil, errors.New("durées de 0 à 3600 s")
			}
		}
		if req.Preset != "" {
			th, ok := analysis.Presets()[req.Preset]
			if !ok {
				return nil, errors.New("préréglage inconnu (all-flash, hybride, disques)")
			}
			opt.Thresholds = th
		}
		return s.standardJob(opt, req.Repetitions), nil
	case "bench":
		if s.testdisk == nil || s.testdisk.Record() == nil {
			return nil, errors.New("aucun disque de test : lancer d'abord un job testdisk.create")
		}
		pre := req.Prefill != nil && *req.Prefill
		if len(req.Steps) == 0 && !pre {
			return nil, errors.New("aucun palier et pas de préremplissage")
		}
		for _, st := range req.Steps {
			if st.Kind == bench.Prefill || st.Duration <= 0 || st.Duration > 3600 {
				return nil, errors.New("palier invalide (kind, duration_s 1..3600)")
			}
		}
		breq := bench.Request{Record: *s.testdisk.Record(), Prefill: pre, Sync: req.Sync == nil || *req.Sync, Seed: req.Seed, Steps: req.Steps}
		return s.benchJob(breq), nil
	case "testdisk.create":
		if s.testdisk == nil {
			return nil, errors.New("disque de test indisponible")
		}
		if req.Datastore == "" {
			return nil, errors.New("datastore requis (Type:valeur)")
		}
		if req.SizeGB == 0 {
			req.SizeGB = 4
		}
		if _, err := s.vs.Session(); err != nil {
			return nil, err
		}
		if rec := s.testdisk.Record(); rec != nil {
			return nil, fmt.Errorf("un disque de test existe déjà (%s)", rec.FileName)
		}
		ds, size := req.Datastore, req.SizeGB
		return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
			sess, err := s.vs.Session()
			if err != nil {
				return nil, err
			}
			self, err := s.freshIdentify(ctx, sess)
			if err != nil {
				return nil, err
			}
			rep.Progress(1, fmt.Sprintf("création du disque de test %d Go", size))
			st, err := s.testdisk.Create(context.WithoutCancel(ctx), sess, self, ds, size, func(p int, msg string) {
				if msg == "" {
					msg = "tâche vCenter en cours"
				}
				rep.Progress(float64(p), msg)
			})
			return st, err
		}, nil
	case "testdisk.delete":
		if s.testdisk == nil {
			return nil, errors.New("disque de test indisponible")
		}
		if _, err := s.vs.Session(); err != nil {
			return nil, err
		}
		return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
			sess, err := s.vs.Session()
			if err != nil {
				return nil, err
			}
			rep.Progress(1, "suppression du disque de test")
			// une tâche vCenter commencée est menée à son terme, même en cas d'annulation (§5.3)
			if err := s.testdisk.Delete(context.WithoutCancel(ctx), sess, func(p int, msg string) { rep.Progress(float64(p), msg) }); err != nil {
				return nil, err
			}
			return s.testdisk.Status(ctx, sess), nil
		}, nil
	case "net.iperf":
		return s.iperfJob(req)
	case "tour":
		if s.testdisk == nil || s.testdisk.Record() == nil {
			return nil, errors.New("aucun disque de test : lancer d'abord un job testdisk.create")
		}
		if _, err := s.vs.Session(); err != nil {
			return nil, err
		}
		if len(req.Hosts) < 2 || len(req.Hosts) > 8 {
			return nil, errors.New("tournée : 2 à 8 hôtes")
		}
		if req.Cycles == 0 {
			req.Cycles = 2
		}
		if req.Cycles < 1 || req.Cycles > 5 {
			return nil, errors.New("cycles : 1 à 5")
		}
		opt := tour.Options{Hosts: req.Hosts, Cycles: req.Cycles, DRSOverride: req.DRSOverride == nil || *req.DRSOverride, ReturnHome: req.ReturnHome == nil || *req.ReturnHome,
			Standard: standard.Options{BaselineS: req.BaselineS, WriteS: req.WriteS, ReadS: req.ReadS, Sync: req.Sync == nil || *req.Sync, Prefill: req.Prefill == nil || *req.Prefill}}
		if req.Preset != "" {
			th, ok := analysis.Presets()[req.Preset]
			if !ok {
				return nil, errors.New("préréglage inconnu (all-flash, hybride, disques)")
			}
			opt.Standard.Thresholds = th
		}
		return s.tourJob(opt), nil
	case "move":
		if _, err := s.vs.Session(); err != nil {
			return nil, err
		}
		ref, ok := vsphere.Ref(req.TargetHost)
		if !ok {
			return nil, errors.New("target_host requis (HostSystem:host-…)")
		}
		return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
			sess, err := s.vs.Session()
			if err != nil {
				return nil, err
			}
			self, err := s.freshIdentify(ctx, sess)
			if err != nil {
				return nil, err
			}
			var dsRef *types.ManagedObjectReference
			if rec := s.testdisk.Record(); rec != nil {
				if r, ok := vsphere.Ref(rec.DatastoreRef); ok {
					dsRef = &r
				}
			}
			vmRef, _ := vsphere.Ref(self.VM.Ref)
			c := vsphere.CheckMigration(ctx, sess, vmRef, ref, dsRef)
			if c.Current {
				return c, nil
			}
			if !c.OK {
				return c, fmt.Errorf("vMotion vers %s impossible : %v", c.Host, c.Errors)
			}
			rep.Progress(2, "vMotion de l'appliance vers "+c.Host)
			if err := vsphere.MigrateSelf(ctx, sess, self, ref, func(p int, _ string) { rep.Progress(float64(p), fmt.Sprintf("vMotion vers %s : %d %%", c.Host, p)) }); err != nil {
				return c, err
			}
			after, _ := s.freshIdentify(context.WithoutCancel(ctx), sess)
			return after, nil
		}, nil
	case "local":
		if s.testdisk == nil || s.testdisk.Record() == nil {
			return nil, errors.New("aucun disque de test : en ajouter un (console, onglet Storage) ou le créer via vCenter")
		}
		opt := localtest.Options{WriteS: req.WriteS, ReadS: req.ReadS, Advanced: req.Full || req.Advanced, CeilingMs: req.CeilingMs,
			Sync: req.Sync == nil || *req.Sync, Prefill: req.Prefill == nil || *req.Prefill}
		for _, d := range []int{opt.WriteS, opt.ReadS} {
			if d < 0 || d > 3600 {
				return nil, errors.New("durées de 0 à 3600 s")
			}
		}
		if req.Preset != "" {
			th, ok := analysis.Presets()[req.Preset]
			if !ok {
				return nil, errors.New("préréglage inconnu (all-flash, hybride, disques)")
			}
			opt.Thresholds = th
		}
		return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
			r, err := localtest.Run(ctx, localtest.Deps{Record: s.testdisk.Record(), Runner: &bench.Runner{}, Version: s.info.Version,
				Progress: func(p float64, m string) { rep.Progress(p, m) },
				OnSecond: func(step int, kind bench.Kind, sec bench.Second) {
					rep.Data("second", map[string]any{"step": step, "kind": kind, "t": sec.T, "mbps": float64(sec.Bytes) / 1e6, "iops": sec.Ops, "p99_ms": sec.P99US / 1000})
				}}, opt)
			if err == nil {
				rep.Logf("verdict %s", r.Summary())
			}
			return r, err
		}, nil
	case "advanced":
		if s.testdisk == nil || s.testdisk.Record() == nil {
			return nil, errors.New("aucun disque de test : lancer d'abord un job testdisk.create")
		}
		if _, err := s.vs.Session(); err != nil {
			return nil, err
		}
		opt := advanced.Options{Full: req.Full, CeilingMs: req.CeilingMs, Standard: standard.Options{BaselineS: req.BaselineS, WriteS: req.WriteS, ReadS: req.ReadS,
			Sync: req.Sync == nil || *req.Sync, Prefill: req.Prefill == nil || *req.Prefill}}
		if req.Preset != "" {
			th, ok := analysis.Presets()[req.Preset]
			if !ok {
				return nil, errors.New("préréglage inconnu (all-flash, hybride, disques)")
			}
			opt.Standard.Thresholds = th
		}
		if req.CeilingMs < 0 || req.CeilingMs > 1000 {
			return nil, errors.New("ceiling_ms de 0 à 1000")
		}
		return s.advancedJob(opt), nil
	}
	return nil, fmt.Errorf("profil inconnu %q (standard, advanced, local, tour, move, bench, net.iperf, testdisk.create, testdisk.delete)", req.Profile)
}

func (s *Server) freshIdentify(ctx context.Context, sess *vsphere.Session) (*vsphere.Self, error) {
	s.selfMu.Lock()
	s.selfCache = nil // un vMotion a pu changer l'hôte
	s.selfMu.Unlock()
	return s.identify(ctx, sess)
}

func (s *Server) standardJob(opt standard.Options, reps int) jobs.Func {
	return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
		sess, err := s.vs.Session()
		if err != nil {
			return nil, err
		}
		res := &StandardResult{}
		for i := 0; i < reps; i++ {
			base, span := float64(i)*100/float64(reps), 100/float64(reps)
			prefix := ""
			if reps > 1 {
				prefix = fmt.Sprintf("répétition %d/%d : ", i+1, reps)
			}
			self, err := s.freshIdentify(ctx, sess)
			if err != nil {
				return res, err
			}
			rep.Logf("%sVM %s sur l'hôte %s", prefix, self.VM.Name, self.Host.Name)
			o := opt
			if i > 0 {
				o.Prefill = false // déjà fait à la première répétition
			}
			r, err := standard.Run(ctx, standard.Deps{Session: sess, Self: self, Record: s.testdisk.Record(), Runner: &bench.Runner{}, Version: s.info.Version,
				Progress: func(pct float64, msg string) { rep.Progress(base+pct*span/100, prefix+msg) },
				OnSecond: func(kind bench.Kind, sec bench.Second) {
					rep.Data("second", map[string]any{"kind": kind, "t": sec.T, "mbps": float64(sec.Bytes) / 1e6, "p99_ms": sec.P99US / 1000, "repetition": i + 1})
				}}, o)
			if err != nil {
				return res, err
			}
			res.Runs = append(res.Runs, r)
			rep.Logf("%sverdict %s — écriture %.0f Mo/s, lecture %.0f Mo/s, ratio TX/écrit %s", prefix, r.Verdict.Level, r.Write.MBps, r.Read.MBps, ratioText(r.TxRatio))
		}
		res.Summary = summarize(res.Runs)
		if s.settings != nil {
			_ = s.settings.store.Save(standardLastFile, res)
		}
		return res, nil
	}
}

func ratioText(r *analysis.Ratio) string {
	if r == nil || r.Samples == 0 {
		return "n/d"
	}
	return strconv.FormatFloat(r.Value, 'f', 3, 64)
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	return c[len(c)/2]
}

func summarize(runs []*standard.Report) StandardSummary {
	sum := StandardSummary{Repetitions: len(runs), Level: analysis.OK}
	var w, rd, tx, p99 []float64
	rank := map[analysis.Level]int{analysis.Unknown: 0, analysis.OK: 1, analysis.Warning: 2, analysis.Alert: 3}
	for _, r := range runs {
		sum.Host, sum.Datastore = r.Host.Name, r.Datastore.Name
		w = append(w, r.Write.MBps)
		rd = append(rd, r.Read.MBps)
		p99 = append(p99, r.Write.Latency.P99/1000)
		if r.TxRatio != nil && r.TxRatio.Samples > 0 {
			tx = append(tx, r.TxRatio.Value)
		}
		if rank[r.Verdict.Level] > rank[sum.Level] {
			sum.Level = r.Verdict.Level
		}
		sum.Triggered = append(sum.Triggered, r.Verdict.Triggered...)
		sum.WriteProfile, sum.ReadProfile = r.Write.Profile.Text, r.Read.Profile.Text
	}
	sum.WriteMBps, sum.ReadMBps, sum.TxRatio, sum.WriteP99Ms = median(w), median(rd), median(tx), median(p99)
	return sum
}

func (s *Server) tourJob(opt tour.Options) jobs.Func {
	return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
		sess, err := s.vs.Session()
		if err != nil {
			return nil, err
		}
		r, err := tour.Run(ctx, tour.Deps{Session: sess, Identify: func(c context.Context) (*vsphere.Self, error) { return s.freshIdentify(c, sess) },
			Record: s.testdisk.Record, Runner: &bench.Runner{}, Version: s.info.Version,
			Progress: func(p float64, m string) { rep.Progress(p, m) },
			OnSecond: func(kind bench.Kind, sec bench.Second) {
				rep.Data("second", map[string]any{"kind": kind, "t": sec.T, "mbps": float64(sec.Bytes) / 1e6})
			}}, opt)
		_, _ = s.freshIdentify(context.WithoutCancel(ctx), sess) // l'appliance a bougé : identité à relire
		if r != nil {
			for _, h := range r.Comparison.Hosts {
				rep.Logf("%s : %s — écriture %.0f Mo/s (médiane de %d), ×%.2f du meilleur", h.Host, h.Level, h.WriteMBps, h.Runs, h.VsBest)
			}
		}
		return r, err
	}
}

func (s *Server) advancedJob(opt advanced.Options) jobs.Func {
	return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
		sess, err := s.vs.Session()
		if err != nil {
			return nil, err
		}
		self, err := s.freshIdentify(ctx, sess)
		if err != nil {
			return nil, err
		}
		rep.Logf("VM %s sur l'hôte %s", self.VM.Name, self.Host.Name)
		r, err := advanced.Run(ctx, standard.Deps{Session: sess, Self: self, Record: s.testdisk.Record(), Runner: &bench.Runner{}, Version: s.info.Version,
			Progress: func(pct float64, msg string) { rep.Progress(pct, msg) },
			OnSecond: func(kind bench.Kind, sec bench.Second) {
				rep.Data("second", map[string]any{"kind": kind, "t": sec.T, "mbps": float64(sec.Bytes) / 1e6, "iops": sec.Ops, "p99_ms": sec.P99US / 1000})
			}}, opt)
		if err != nil {
			return r, err
		}
		h := r.Headline
		rep.Logf("verdict %s — %.0f Mo/s en écriture, %.0f Mo/s en lecture, %.0f IO/s utiles (4 Kio mixte, p99 < %.0f ms), QD1 p99 lecture %.2f ms / écriture %.2f ms",
			r.Verdict.Level, h.WriteMBps, h.ReadMBps, h.UsefulIOPS, h.CeilingMs, h.ReadQD1P99, h.WriteQD1P99)
		return r, nil
	}
}

func (s *Server) benchJob(req bench.Request) jobs.Func {
	return func(ctx context.Context, rep *jobs.Reporter) (any, error) {
		runner := &bench.Runner{}
		total := 0
		for _, st := range req.Steps {
			total += st.Duration + st.Warmup
		}
		done := 0
		rep.Progress(0, "démarrage du worker")
		res, err := runner.Run(ctx, req, func(e bench.Event) {
			switch e.Type {
			case "prefill":
				if e.Result == nil {
					rep.Progress(e.Percent*0.3, fmt.Sprintf("préremplissage %.0f %%", e.Percent))
				}
			case "step":
				if e.Result == nil {
					rep.Logf("palier %d (%s) : début", e.Step, e.Kind)
				} else {
					rep.Logf("palier %d : %.0f Mo/s, %.0f IOPS, p99 %.2f ms — %s", e.Step, e.Result.MBps, e.Result.IOPS, e.Result.Latency.P99/1000, e.Result.Profile.Text)
				}
			case "second":
				done++
				if total > 0 {
					rep.Progress(30+70*float64(done)/float64(total), fmt.Sprintf("palier %d (%s)", e.Step, e.Kind))
				}
				if e.Second != nil {
					rep.Data("second", map[string]any{"step": e.Step, "kind": e.Kind, "t": e.Second.T, "mbps": float64(e.Second.Bytes) / 1e6, "iops": e.Second.Ops, "p99_ms": e.Second.P99US / 1000})
				}
			}
		})
		if res != nil && s.settings != nil {
			_ = s.settings.store.Save(benchLastFile, res)
		}
		return res, err
	}
}

func (s *Server) jobsList(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 {
		limit = 50
	}
	writeJSON(w, http.StatusOK, map[string]any{"current": s.jobs.Current(), "jobs": s.jobs.List(r.URL.Query().Get("profile"), limit)})
}

func (s *Server) jobsGet(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "job inconnu")
		return
	}
	writeJSON(w, http.StatusOK, j)
}

func (s *Server) jobsCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.jobs.Cancel(r.PathValue("id")); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	j, _ := s.jobs.Get(r.PathValue("id"))
	writeJSON(w, http.StatusAccepted, j)
}

// jobsEvents diffuse les événements d'un job en Server-Sent Events. L'historique est rejoué
// (à partir de Last-Event-ID ou ?after=), puis le flux suit jusqu'à la fin du job.
func (s *Server) jobsEvents(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	after, _ := strconv.Atoi(r.Header.Get("Last-Event-ID"))
	if a, err := strconv.Atoi(r.URL.Query().Get("after")); err == nil {
		after = a
	}
	hist, ch, unsub, ok := s.jobs.Subscribe(id, after)
	if !ok {
		// job d'une session précédente : on renvoie son état final en un seul événement
		j, found := s.jobs.Get(id)
		if !found {
			writeError(w, http.StatusNotFound, "job inconnu")
			return
		}
		b, _ := json.Marshal(jobs.Event{Seq: 1, Type: "state", State: j.State, Msg: j.Error, Time: j.Ended})
		hist, ch, unsub = []jobs.Event{{Seq: 1, Type: "state", State: j.State, Msg: j.Error, Time: j.Ended, Data: b}}, closedChan(), func() {}
	}
	defer unsub()
	fl, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "flux non supporté")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	send := func(e jobs.Event) {
		b, _ := json.Marshal(e)
		fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", e.Seq, e.Type, b)
	}
	for _, e := range hist {
		send(e)
	}
	fl.Flush()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e, open := <-ch:
			if !open {
				return
			}
			send(e)
			fl.Flush()
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

func closedChan() <-chan jobs.Event {
	c := make(chan jobs.Event)
	close(c)
	return c
}
