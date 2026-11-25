package report

import (
	"encoding/json"

	"vigie/internal/analysis"
	"vigie/internal/jobs"
	"vigie/internal/localtest"
	"vigie/internal/results"
	"vigie/internal/standard"
)

func fptr(v float64) *float64 { return &v }

func stdPoint(j *jobs.Job, s *standard.Report) results.Point {
	p := results.Point{T: s.Started, Job: j.ID, Profile: j.Profile, By: j.By, Scheduled: j.By == "schedule",
		Host: s.Host.Name, Datastore: s.Datastore.Name, WriteMBps: s.Write.MBps, ReadMBps: s.Read.MBps,
		WriteP99Ms: s.Write.Latency.P99 / 1000, ReadP99Ms: s.Read.Latency.P99 / 1000,
		Level: string(s.Verdict.Level), Triggered: s.Verdict.Triggered, IOProfile: s.Write.Profile.Text}
	if s.TxRatio != nil && s.TxRatio.Samples > 0 {
		p.TxRatio = fptr(s.TxRatio.Value)
	}
	if l, ok := s.DSLatency["write"]; ok {
		p.DSWriteLatMs = fptr(l)
	}
	if len(s.PerVmnic) > 0 {
		var n float64
		for _, v := range s.PerVmnic {
			n += v.ErrorsRx + v.DroppedRx
		}
		p.NICErrors = fptr(n)
	}
	return p
}

func qd1(p *results.Point, pts []analysis.Point, useful []analysis.Useful) {
	for _, x := range pts {
		if x.QD == 1 && x.BlockKiB == 4 {
			switch x.Pattern {
			case "write":
				p.QD1WriteP99 = fptr(x.P99Ms)
			case "read":
				p.QD1ReadP99 = fptr(x.P99Ms)
			}
		}
	}
	for _, u := range useful {
		if u.Pattern == "mixed" && u.BlockKiB == 4 {
			p.UsefulIOPS = fptr(u.IOPS)
		}
	}
}

// ResultPoints extrait les mesures d'un job terminé pour l'historique (phase 9) : une par
// répétition d'un test standard, une par passage de tournée, une pour un test avancé ou local.
func ResultPoints(j *jobs.Job) []results.Point {
	if j == nil || j.State != jobs.Succeeded || len(j.Result) == 0 {
		return nil
	}
	if j.Profile == "local" {
		var l localtest.Report
		if json.Unmarshal(j.Result, &l) != nil || l.Write == nil || l.Read == nil {
			return nil
		}
		p := results.Point{T: l.Started, Job: j.ID, Profile: "local", By: j.By, Scheduled: j.By == "schedule",
			Host: "local", Datastore: l.Disk.DatastoreName, WriteMBps: l.Write.MBps, ReadMBps: l.Read.MBps,
			WriteP99Ms: l.Write.Latency.P99 / 1000, ReadP99Ms: l.Read.Latency.P99 / 1000,
			Level: string(l.Verdict.Level), Triggered: l.Verdict.Triggered, IOProfile: l.Write.Profile.Text}
		if p.Datastore == "" {
			p.Datastore = l.Disk.FileName
		}
		qd1(&p, l.Points, l.Useful)
		return []results.Point{p}
	}
	r, err := load(j)
	if err != nil {
		return nil
	}
	var out []results.Point
	switch {
	case r.adv != nil:
		p := stdPoint(j, r.adv.Standard)
		p.Level, p.Triggered = string(r.adv.Verdict.Level), r.adv.Verdict.Triggered
		qd1(&p, r.adv.Points, r.adv.Useful)
		out = append(out, p)
	default:
		for _, s := range r.reports() {
			out = append(out, stdPoint(j, s))
		}
	}
	return out
}
