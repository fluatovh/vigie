package bench

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func tempTarget(t *testing.T, size int64) *Target {
	t.Helper()
	p := filepath.Join(t.TempDir(), "disk.img")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(size)
	f.Close()
	tgt, err := OpenTarget(p, size, false, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tgt.File.Close() })
	return tgt
}

func TestPrefillAndSteps(t *testing.T) {
	const size = 8 << 20 // zone de 7 Mio
	tgt := tempTarget(t, size)
	pool := NewPool(7, 1<<20)
	ctx := context.Background()

	var secs int
	res, err := Run(ctx, tgt, Step{Kind: Prefill, BlockKiB: 1024, QD: 2}, pool, func(Second) { secs++ })
	if err != nil {
		t.Fatal(err)
	}
	if res.Bytes != 7<<20 || res.Ops != 7 || res.Errors != 0 {
		t.Fatalf("prefill : %+v", res)
	}
	// le bloc 0 n'a pas été touché
	f, _ := os.Open(tgt.File.Name())
	head := make([]byte, 4096)
	f.ReadAt(head, 0)
	f.Close()
	for _, b := range head {
		if b != 0 {
			t.Fatal("le bloc 0 a été écrit")
		}
	}
	// la zone est bien remplie de données non nulles
	f, _ = os.Open(tgt.File.Name())
	zone := make([]byte, 4096)
	f.ReadAt(zone, ZoneStart+3<<20)
	f.Close()
	nz := 0
	for _, b := range zone {
		if b != 0 {
			nz++
		}
	}
	if nz < 4000 {
		t.Fatal("zone non remplie")
	}

	for _, st := range []Step{
		{Kind: SeqWrite, Duration: 1, BlockKiB: 256, QD: 2},
		{Kind: SeqRead, Duration: 1, BlockKiB: 1024, QD: 1},
		{Kind: RandRead, Duration: 1, Warmup: 1, BlockKiB: 4, QD: 4, Seed: 99},
		{Kind: RandMix, Duration: 1, BlockKiB: 4, QD: 2, ReadPct: 70},
	} {
		res, err := Run(ctx, tgt, st, pool, nil)
		if err != nil {
			t.Fatalf("%s : %v", st.Kind, err)
		}
		if res.Ops == 0 || res.Errors != 0 || res.IOPS <= 0 || res.MBps <= 0 || res.Latency.P99 <= 0 {
			t.Fatalf("%s : %+v", st.Kind, res)
		}
		if len(res.Seconds) < 1 || len(res.Seconds) > st.Duration+1 {
			t.Fatalf("%s : %d secondes mesurées", st.Kind, len(res.Seconds))
		}
		if res.Profile.Text == "" || res.Profile.BlockKiB != st.BlockKiB || res.Profile.QD != st.QD {
			t.Fatalf("%s : profil %+v", st.Kind, res.Profile)
		}
		if st.Seed != 0 && res.Seed != st.Seed {
			t.Fatal("graine non conservée")
		}
		if res.Measured < 0.9 {
			t.Fatalf("%s : durée mesurée %.2f s", st.Kind, res.Measured)
		}
	}
}

func TestRunAbort(t *testing.T) {
	tgt := tempTarget(t, 8<<20)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	res, err := Run(ctx, tgt, Step{Kind: SeqRead, Duration: 30, BlockKiB: 64, QD: 1}, NewPool(1, 1<<20), nil)
	if err != nil || !res.Aborted {
		t.Fatalf("annulation : %v %+v", err, res)
	}
}

func TestZoneTooSmall(t *testing.T) {
	tgt := tempTarget(t, 1<<20)
	if _, err := Run(context.Background(), tgt, Step{Kind: SeqRead, Duration: 1, BlockKiB: 1024, QD: 1}, NewPool(1, 4096), nil); err == nil {
		t.Fatal("zone vide acceptée")
	}
}
