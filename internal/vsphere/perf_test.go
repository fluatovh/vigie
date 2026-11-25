package vsphere

import (
	"context"
	"testing"
	"time"

	"vigie/internal/analysis"
)

func TestClassifyEvents(t *testing.T) {
	cases := map[string][2]bool{
		"esx.problem.net.vmnic.linkstate.down":   {true, true},
		"esx.clear.net.vmnic.linkstate.up":       {true, false},
		"esx.problem.net.redundancy.lost":        {true, true},
		"esx.problem.vmfs.nfs.server.disconnect": {true, false},
		"esx.problem.storage.apd.start":          {true, false},
		"com.vmware.vc.HA.VmRestartedByHAEvent":  {false, false},
	}
	for id, want := range cases {
		rel, flap := classify(id)
		if rel != want[0] || flap != want[1] {
			t.Errorf("%s : rel=%v flap=%v, attendu %v", id, rel, flap, want)
		}
	}
}

func TestNFSWarningRegex(t *testing.T) {
	yes := []string{
		"2026-11-17T10:00:00Z cpu3:2097)WARNING: NFS: 2311: Lost connection to the server 172.17.136.11 mount point /ssd/main",
		"NFS: 346: Connection to server 172.17.136.11 restored",
		"NFS41: NFS41ProcessSessionUp:2097: Session to server timed out",
	}
	no := []string{"ScsiDeviceIO: 4325: Cmd(0x45d8) 0x28", "vmxnet3: link up"}
	for _, l := range yes {
		if !nfsWarning.MatchString(l) {
			t.Errorf("non reconnu : %s", l)
		}
	}
	for _, l := range no {
		if nfsWarning.MatchString(l) {
			t.Errorf("faux positif : %s", l)
		}
	}
}

func TestSumInstances(t *testing.T) {
	t0 := time.Unix(0, 0)
	mk := func(v ...float64) []analysis.Sample {
		var out []analysis.Sample
		for i, x := range v {
			s := t0.Add(time.Duration(i) * 20 * time.Second)
			out = append(out, analysis.Sample{Start: s, End: s.Add(20 * time.Second), Value: x})
		}
		return out
	}
	sum := SumInstances(map[string][]analysis.Sample{"vmnic0": mk(1, 2, 3), "vmnic1": mk(10, 20, 30), "vmnic9": mk(100)}, []string{"vmnic0", "vmnic1"})
	if len(sum) != 3 || sum[0].Value != 11 || sum[2].Value != 33 {
		t.Fatalf("%+v", sum)
	}
}

func TestVCSimPerfAndClock(t *testing.T) {
	_, s, cleanup := startVCSim(t)
	defer cleanup()
	ctx := context.Background()
	c, err := ResolveCounters(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("compteurs résolus : %v", c.Names)
	vm := anyVM(t, s)
	clk, err := MeasureClock(ctx, s, *vm.Runtime.Host)
	if err != nil {
		t.Fatal(err)
	}
	if clk.RTT <= 0 || clk.ServerOffset > time.Minute || clk.ServerOffset < -time.Minute {
		t.Fatalf("horloge : %+v", clk)
	}
	end := time.Now()
	ser, err := QueryRealtime(ctx, s, c, *vm.Runtime.Host, []string{"tx", "rx", "dsWriteLat"}, end.Add(-2*time.Minute), end)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("séries : %d clés", len(ser))
}
