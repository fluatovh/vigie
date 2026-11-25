package bench

import (
	"math/rand"
	"sort"
	"testing"
)

func TestHistPrecision(t *testing.T) {
	var h Hist
	for _, v := range []uint64{1, 500, 1023, 1024, 1500, 4096, 100000, 1 << 20, 3 << 20, 1e9, 2e9} {
		i := h.index(v)
		hi := h.value(i)
		if hi < v {
			t.Fatalf("v=%d index=%d borne haute %d < v", v, i, hi)
		}
		if v >= 1024 && float64(hi-v)/float64(v) > 0.02 {
			t.Fatalf("v=%d borne %d : erreur %.2f%% > 2%%", v, hi, 100*float64(hi-v)/float64(v))
		}
	}
}

func TestHistPercentiles(t *testing.T) {
	var h Hist
	r := rand.New(rand.NewSource(1))
	var vals []uint64
	for i := 0; i < 100000; i++ {
		v := uint64(50000 + r.ExpFloat64()*200000) // latences 50 µs + exponentielle
		vals = append(vals, v)
		h.Add(v)
	}
	sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
	for _, p := range []float64{50, 95, 99, 99.9} {
		exact := vals[int(float64(len(vals))*p/100)-1]
		got := h.Percentile(p)
		if err := float64(got)/float64(exact) - 1; err < -0.0 || err > 0.03 {
			t.Fatalf("p%v : hist %d vs exact %d (%.2f%%)", p, got, exact, err*100)
		}
	}
	if h.Max != vals[len(vals)-1] || h.Min != vals[0] || h.Percentile(100) != h.Max {
		t.Fatal("min/max")
	}
	var m Hist
	m.Merge(&h)
	if m.Count != h.Count || m.Percentile(99) != h.Percentile(99) {
		t.Fatal("merge")
	}
	if s := h.Summary(); s.P99 <= s.P50 || s.Mean <= 0 {
		t.Fatalf("summary : %+v", s)
	}
}

func TestHistEmpty(t *testing.T) {
	var h Hist
	if h.Percentile(99) != 0 || h.Mean() != 0 || len(h.Buckets()) != 0 {
		t.Fatal("vide")
	}
}
