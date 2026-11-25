package analysis

import (
	"strings"
	"testing"
)

func TestCompareEsx43(t *testing.T) {
	// cas de la méthode manuelle : esx43 ×12 plus lent qu'esx44 en alternance A/B/A/B
	runs := []HostRun{
		{Host: "esx43", Cycle: 1, WriteMBps: 33, TxRatio: 2.9, Level: Alert},
		{Host: "esx44", Cycle: 1, WriteMBps: 410, TxRatio: 1.02, Level: OK},
		{Host: "esx43", Cycle: 2, WriteMBps: 35, TxRatio: 2.8, Level: Alert},
		{Host: "esx44", Cycle: 2, WriteMBps: 398, TxRatio: 1.03, Level: OK},
	}
	c := Compare(runs, DefaultThresholds())
	if c.Level != Alert || len(c.Hosts) != 2 || c.Hosts[0].Runs != 2 {
		t.Fatalf("%+v", c)
	}
	if c.Hosts[0].WriteMBps != 34 || c.Hosts[1].VsBest != 1 || c.Hosts[0].VsBest < 11 || c.Hosts[0].VsBest > 12.5 {
		t.Fatalf("médianes / écarts : %+v", c.Hosts)
	}
	if !strings.Contains(c.Sentence, "esx43") || !strings.Contains(c.Sentence, "esx44") || !strings.Contains(c.Sentence, "ALERTE sur esx43") || !strings.Contains(c.Sentence, "×11,9") {
		t.Fatalf("phrase : %s", c.Sentence)
	}
}

func TestCompareHomogeneous(t *testing.T) {
	c := Compare([]HostRun{{Host: "a", WriteMBps: 460, Level: OK}, {Host: "b", WriteMBps: 480, Level: OK}, {Host: "c", WriteMBps: 300, Level: OK}}, DefaultThresholds())
	if c.Level != OK || !strings.Contains(c.Sentence, "homogènes") || !strings.HasSuffix(c.Sentence, "moins bon.") {
		t.Fatalf("%+v", c)
	}
	c = Compare([]HostRun{{Host: "a", WriteMBps: 460}, {Host: "b", WriteMBps: 200}}, DefaultThresholds())
	if c.Level != Warning || c.Hosts[1].Level != Warning {
		t.Fatalf("×2,3 : %+v", c)
	}
}
