package analysis

import (
	"math"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 17, 10, 0, 0, 0, time.UTC)

// samples20 génère des échantillons de 20 s alignés sur t0+offset, à débit constant (KBps).
func samples20(from time.Time, n int, kbps func(i int) float64) []Sample {
	var out []Sample
	for i := 0; i < n; i++ {
		s := from.Add(time.Duration(i) * 20 * time.Second)
		out = append(out, Sample{Start: s, End: s.Add(20 * time.Second), Value: kbps(i)})
	}
	return out
}

// ticks génère un journal à la seconde, octets constants, sur [from, from+n s].
func ticks(from time.Time, n int, bytes int64) []Tick {
	var out []Tick
	for i := 1; i <= n; i++ {
		out = append(out, Tick{At: from.Add(time.Duration(i) * time.Second), Bytes: bytes})
	}
	return out
}

func TestInsideDropsEdges(t *testing.T) {
	s := samples20(t0, 6, func(int) float64 { return 1 }) // 10:00:00 .. 10:02:00
	w := Window{Start: t0.Add(5 * time.Second), End: t0.Add(85 * time.Second)}
	got := Inside(s, w)
	if len(got) != 3 || !got[0].Start.Equal(t0.Add(20*time.Second)) || !got[2].End.Equal(t0.Add(80*time.Second)) {
		t.Fatalf("Inside : %+v", got)
	}
}

func TestBytesInProrata(t *testing.T) {
	tk := ticks(t0, 10, 1000)
	if b := BytesIn(tk, t0.Add(2500*time.Millisecond), t0.Add(5*time.Second)); math.Abs(b-2500) > 1e-6 {
		t.Fatalf("BytesIn = %v, attendu 2500", b)
	}
}

// Cas sain : écriture à 100 Mo/s, le réseau émet exactement les octets écrits + 2 % d'en-têtes,
// sur un fond constant de 5 Mo/s.
func TestRatioHealthy(t *testing.T) {
	const writeBps = 100e6
	bg := 5e6 / KBps
	base := samples20(t0, 3, func(int) float64 { return bg })
	start := t0.Add(60 * time.Second)
	w := Window{Start: start.Add(3 * time.Second), End: start.Add(83 * time.Second)}
	bench := samples20(start, 5, func(int) float64 { return bg + writeBps*1.02/KBps })
	r := ComputeRatio(RatioInput{Baseline: base, Bench: bench, Window: w, Ticks: ticks(start, 100, writeBps)})
	if r.Samples != 3 || !r.Reliable || math.Abs(r.Value-1.02) > 0.001 {
		t.Fatalf("ratio : %+v", r)
	}
	v := Evaluate(Facts{TxRatio: &r, WriteMBps: 100}, DefaultThresholds())
	if v.Level != OK && v.Level != Unknown {
		// les indicateurs non fournis sont INCONNU mais ne dégradent pas le niveau
	}
	for _, ind := range v.Indicators {
		if ind.Key == "tx_ratio" && ind.Level != OK {
			t.Fatalf("tx_ratio : %+v", ind)
		}
	}
}

// Cas esx43 de la méthode manuelle : 188 Mo émis pour 64 Mo écrits, soit un ratio d'environ 2,9.
func TestRatioEsx43(t *testing.T) {
	// écriture lente : 64 Mo sur 40 s (1,6 Mo/s), le réseau émet 188 Mo sur la même période
	start := t0.Add(60 * time.Second)
	w := Window{Start: start, End: start.Add(40 * time.Second)}
	usefulPerSec := int64(64e6 / 40)
	netKBps := 188e6 / 40 / KBps
	bench := samples20(start, 2, func(int) float64 { return netKBps })
	base := samples20(t0, 3, func(int) float64 { return 0 })
	r := ComputeRatio(RatioInput{Baseline: base, Bench: bench, Window: w, Ticks: ticks(start, 40, usefulPerSec)})
	if math.Abs(r.Value-188.0/64) > 0.01 {
		t.Fatalf("ratio esx43 = %.3f, attendu %.3f", r.Value, 188.0/64)
	}
	v := Evaluate(Facts{TxRatio: &r, WriteMBps: 1.6}, DefaultThresholds())
	if v.Level != Alert || len(v.Triggered) < 2 {
		t.Fatalf("verdict esx43 : %+v", v)
	}
}

func TestRatioBaselineNoise(t *testing.T) {
	base := samples20(t0, 3, func(i int) float64 { return []float64{1000, 5000, 9000}[i] })
	start := t0.Add(60 * time.Second)
	bench := samples20(start, 4, func(int) float64 { return 100000 })
	r := ComputeRatio(RatioInput{Baseline: base, Bench: bench, Window: Window{start, start.Add(80 * time.Second)}, Ticks: ticks(start, 80, 100e6)})
	if r.Reliable {
		t.Fatalf("ligne de base bruitée : doit être peu fiable %+v", r)
	}
	// un ratio peu fiable ne déclenche pas d'ALERTE à lui seul
	r.Value = 3
	v := Evaluate(Facts{TxRatio: &r, WriteMBps: 500}, DefaultThresholds())
	for _, ind := range v.Indicators {
		if ind.Key == "tx_ratio" && ind.Level != Warning {
			t.Fatalf("ratio peu fiable : %+v", ind)
		}
	}
}

func TestRatioNoCompleteSample(t *testing.T) {
	start := t0.Add(5 * time.Second)
	r := ComputeRatio(RatioInput{Bench: samples20(t0, 2, func(int) float64 { return 1 }), Window: Window{start, start.Add(30 * time.Second)}})
	if r.Samples != 0 || r.Reliable {
		t.Fatalf("%+v", r)
	}
	v := Evaluate(Facts{TxRatio: &r, WriteMBps: 500}, DefaultThresholds())
	if v.Indicators[0].Level != Unknown {
		t.Fatal("ratio sans échantillon : INCONNU attendu")
	}
}

func TestEvaluateThresholds(t *testing.T) {
	th := DefaultThresholds()
	zero, five, six := 0.0, 5, 6
	flaps := 1
	lat := 12.0
	nic := 3.0
	v := Evaluate(Facts{WriteMBps: 40, NICErrors: &zero, NFSWarnLines: &five, LinkFlaps: new(int), DSWriteLatMs: &lat}, th)
	want := map[string]Level{"write_mbps": Warning, "nic_errors": OK, "nfs_warnings": Warning, "link_flaps": OK, "ds_write_latency": Warning}
	for _, ind := range v.Indicators {
		if l, ok := want[ind.Key]; ok && ind.Level != l {
			t.Fatalf("%s : %s, attendu %s (%s)", ind.Key, ind.Level, l, ind.Why)
		}
	}
	if v.Level != Warning {
		t.Fatalf("niveau global %s", v.Level)
	}
	v = Evaluate(Facts{WriteMBps: 10, NICErrors: &nic, NFSWarnLines: &six, LinkFlaps: &flaps}, th)
	if v.Level != Alert || len(v.Triggered) != 4 {
		t.Fatalf("alertes : %+v", v.Triggered)
	}
	if p := Presets(); p["disques"].QD1P99AlertMs <= p["all-flash"].QD1P99AlertMs {
		t.Fatal("préréglages")
	}
}

func TestRatioSingleSampleInconclusive(t *testing.T) {
	start := t0.Add(60 * time.Second)
	w := Window{Start: start.Add(5 * time.Second), End: start.Add(45 * time.Second)} // 40 s : un seul échantillon complet
	base := samples20(t0, 3, func(int) float64 { return 1000 })
	bench := samples20(start, 3, func(int) float64 { return 1000 + 108e6/KBps })
	r := ComputeRatio(RatioInput{Baseline: base, Bench: bench, Window: w, Ticks: ticks(start, 50, 100e6)})
	if r.Samples != 1 || r.Reliable {
		t.Fatalf("%+v", r)
	}
	v := Evaluate(Facts{TxRatio: &r, WriteMBps: 100}, DefaultThresholds())
	if v.Indicators[0].Level != Unknown || v.Level != OK {
		t.Fatalf("un ratio sur un seul échantillon ne doit pas déclencher : %+v", v)
	}
}

func TestRatioIncoherent(t *testing.T) {
	start := t0.Add(60 * time.Second)
	base := samples20(t0, 3, func(int) float64 { return 20e6 / KBps })
	// fond pendant la mesure plus faible que pendant la ligne de base : ratio < 1
	bench := samples20(start, 4, func(int) float64 { return (100e6 + 2e6) / KBps })
	r := ComputeRatio(RatioInput{Baseline: base, Bench: bench, Window: Window{start, start.Add(80 * time.Second)}, Ticks: ticks(start, 80, 100e6)})
	if r.Reliable || r.Value >= MinCoherentRatio {
		t.Fatalf("%+v", r)
	}
}

func TestEsxcliIndicators(t *testing.T) {
	th := DefaultThresholds()
	f := Facts{WriteMBps: 400}
	v := Evaluate(f, th)
	if !hasIndicator(v, "nic_phys_errors", Unknown) || hasIndicator(v, "rx_missed", Unknown) {
		t.Errorf("sans esxcli : %+v", v.Indicators)
	}
	phys, missed := 2.0, 5000.0
	f.PhysErrors, f.RxMissed = &phys, &missed
	v = Evaluate(f, th)
	if !hasIndicator(v, "nic_phys_errors", Alert) || !hasIndicator(v, "rx_missed", Warning) || v.Level != Alert {
		t.Errorf("avec erreurs : %+v", v.Indicators)
	}
	phys, missed = 0, 10
	v = Evaluate(f, th)
	if !hasIndicator(v, "nic_phys_errors", OK) || !hasIndicator(v, "rx_missed", OK) {
		t.Errorf("sans erreur : %+v", v.Indicators)
	}
}

func hasIndicator(v Verdict, key string, l Level) bool {
	for _, i := range v.Indicators {
		if i.Key == key {
			return i.Level == l
		}
	}
	return false
}
