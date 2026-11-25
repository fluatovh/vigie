package report

import (
	"testing"
	"time"

	"vigie/internal/analysis"
	"vigie/internal/jobs"
)

func TestResultPoints(t *testing.T) {
	at := time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC)
	j := job(t, "s1", "standard", map[string]any{"runs": []any{stdReport("esx1", 450, 1100, at, analysis.OK), stdReport("esx1", 440, 1090, at.Add(10*time.Minute), analysis.Warning)}})
	j.By = "schedule"
	pts := ResultPoints(j)
	if len(pts) != 2 {
		t.Fatalf("%d points", len(pts))
	}
	p := pts[1]
	if p.Host != "esx1" || p.Datastore != "ssd-001557" || p.WriteMBps != 440 || !p.Scheduled || p.Level != "ATTENTION" || p.TxRatio == nil || *p.TxRatio != 1.037 ||
		p.WriteP99Ms != 13.2 || p.IOProfile == "" || p.NICErrors == nil {
		t.Errorf("point standard : %+v", p)
	}
	a := ResultPoints(job(t, "a1", "advanced", advReport(at, 1)))
	if len(a) != 1 || a[0].QD1WriteP99 == nil || a[0].UsefulIOPS == nil || a[0].Profile != "advanced" {
		t.Errorf("point avancé : %+v", a)
	}
	f := job(t, "f1", "standard", map[string]any{})
	f.State = jobs.Failed
	if ResultPoints(f) != nil {
		t.Error("job échoué : aucun point")
	}
}
