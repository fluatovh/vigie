package advanced

import (
	"testing"

	"vigie/internal/bench"
)

func TestMatrix(t *testing.T) {
	o := Options{}
	o.Defaults()
	m := o.Matrix()
	if len(m) != 12 {
		t.Fatalf("matrice 4 Kio : %d paliers, attendu 12", len(m))
	}
	if m[0].Kind != bench.RandRead || m[0].QD != 1 || m[0].BlockKiB != 4 || m[0].Warmup != 5 || m[0].Duration != 40 {
		t.Fatalf("premier palier : %+v", m[0])
	}
	if m[8].Kind != bench.RandMix || m[8].ReadPct != 70 || m[11].QD != 32 {
		t.Fatalf("mixte : %+v / %+v", m[8], m[11])
	}
	if o.CeilingMs != 5 {
		t.Fatalf("plafond par défaut (all-flash) : %v", o.CeilingMs)
	}
	o.Full = true
	if len(o.Matrix()) != 36 {
		t.Fatalf("matrice complète : %d", len(o.Matrix()))
	}
	// durée : ~9 min de matrice en 4 Kio (12 × 45 s), ~27 min en complet
	if s := len(o.Matrix()) * (o.WarmupS + o.MeasureS); s != 1620 {
		t.Fatalf("durée complète : %d s", s)
	}
}

func TestFmtInt(t *testing.T) {
	if fmtInt(48745) != "48 745" || fmtInt(999) != "999" || fmtInt(1000000) != "1 000 000" {
		t.Fatal(fmtInt(48745), fmtInt(1000000))
	}
}
