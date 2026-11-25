package analysis

import "testing"

func f(v float64) *float64 { return &v }

func TestUsefulIOPS(t *testing.T) {
	pts := []Point{
		{Pattern: "read", BlockKiB: 4, QD: 1, IOPS: 3000, P99Ms: 0.6},
		{Pattern: "read", BlockKiB: 4, QD: 4, IOPS: 11000, P99Ms: 1.2},
		{Pattern: "read", BlockKiB: 4, QD: 16, IOPS: 40000, P99Ms: 4.8},
		{Pattern: "read", BlockKiB: 4, QD: 32, IOPS: 48000, P99Ms: 9.5},
		{Pattern: "write", BlockKiB: 4, QD: 1, IOPS: 1300, P99Ms: 6},
		{Pattern: "write", BlockKiB: 4, QD: 4, IOPS: 4000, P99Ms: 8},
	}
	u := UsefulIOPS(pts, 5)
	if len(u) != 2 {
		t.Fatalf("%+v", u)
	}
	if u[0].IOPS != 40000 || u[0].QD != 16 || u[0].RawMax != 48000 || u[0].RawMaxQD != 32 {
		t.Fatalf("lecture : %+v", u[0])
	}
	if u[1].IOPS != 0 || u[1].RawMax != 4000 {
		t.Fatalf("écriture (aucun palier sous 5 ms) : %+v", u[1])
	}
}

func TestEvaluateAdvanced(t *testing.T) {
	th := DefaultThresholds()
	pts := []Point{
		{Pattern: "read", BlockKiB: 4, QD: 1, IOPS: 3000, P99Ms: 0.8, CPUPct: 10, HostIOPS: f(3050), MismatchPct: f(1.7)},
		{Pattern: "write", BlockKiB: 4, QD: 1, IOPS: 1300, P99Ms: 3.2, CPUPct: 5, HostIOPS: f(1310), MismatchPct: f(0.8)},
		{Pattern: "read", BlockKiB: 4, QD: 32, IOPS: 60000, P99Ms: 6, CPUPct: 92, HostIOPS: f(70000), MismatchPct: f(16.7)},
	}
	v := Verdict{Level: OK}
	EvaluateAdvanced(&v, pts, th)
	want := map[string]Level{"qd1_p99_read": OK, "qd1_p99_write": Warning, "iops_mismatch": Warning, "worker_cpu": Warning}
	for _, ind := range v.Indicators {
		if ind.Level != want[ind.Key] {
			t.Fatalf("%s : %s (%s)", ind.Key, ind.Level, ind.Why)
		}
	}
	if v.Level != Warning || len(pts[2].Flags) != 2 || len(pts[0].Flags) != 0 {
		t.Fatalf("verdict %s, drapeaux %v / %v", v.Level, pts[2].Flags, pts[0].Flags)
	}
	// préréglage « disques » : 3,2 ms en QD1 n'est plus une ATTENTION
	v = Verdict{Level: OK}
	EvaluateAdvanced(&v, pts[:2], Presets()["disques"])
	for _, ind := range v.Indicators {
		if ind.Key == "qd1_p99_write" && ind.Level != OK {
			t.Fatalf("préréglage disques : %+v", ind)
		}
	}
	if Mismatch(1000, 1100) != 10 || Mismatch(1000, 900) != 10 || Mismatch(0, 5) != 0 {
		t.Fatal("Mismatch")
	}
}
