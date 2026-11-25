package netdiag

import (
	"context"
	"math"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCompute(t *testing.T) {
	s := Compute([]float64{1, 2, math.NaN(), 3, 2})
	if s.Sent != 5 || s.Recv != 4 || s.LossPct != 20 || s.Min != 1 || s.Max != 3 || s.Median != 2 || s.Avg != 2 {
		t.Fatalf("%+v", s)
	}
	// gigue : |2-1| + |3-2| + |2-3| = 3 sur 3 écarts
	if s.Jitter != 1 {
		t.Fatalf("gigue %v", s.Jitter)
	}
	if e := Compute([]float64{math.NaN(), math.NaN()}); e.LossPct != 100 || e.Recv != 0 {
		t.Fatalf("%+v", e)
	}
}

func TestResolveRejectsInjection(t *testing.T) {
	for _, bad := range []string{"", "1.2.3.4; rm -rf /", "$(id)", "a b", "::1", strings.Repeat("a", 300)} {
		if _, err := Resolve(context.Background(), bad); err == nil {
			t.Errorf("%q accepté", bad)
		}
	}
	if ip, err := Resolve(context.Background(), "192.168.0.1"); err != nil || ip.String() != "192.168.0.1" {
		t.Fatal(err)
	}
}

func TestRRDConsolidation(t *testing.T) {
	r := NewRRD()
	t0 := time.Date(2026, 11, 18, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 12; i++ { // 12 minutes
		rtts := make([]float64, 20)
		for k := range rtts {
			rtts[k] = float64(10 + i)
		}
		if i == 3 {
			rtts[0] = math.NaN()
		}
		r.Add(FromRTTs(t0.Add(time.Duration(i)*time.Minute), rtts))
	}
	if len(r.Tiers[0].Points) != 12 {
		t.Fatalf("1 min : %d points", len(r.Tiers[0].Points))
	}
	// deux intervalles de 5 min complets (10:00 et 10:05), le troisième est en attente
	if len(r.Tiers[1].Points) != 2 {
		t.Fatalf("5 min : %d points", len(r.Tiers[1].Points))
	}
	p := r.Tiers[1].Points[0]
	if p.Sent != 100 || p.Recv != 99 || p.Min != 10 || p.Max != 14 || math.Abs(p.Med-12) > 0.05 {
		t.Fatalf("consolidation : %+v", p)
	}
	step, pts := r.Range(t0.Add(2*time.Minute), t0.Add(5*time.Minute))
	if step != 60 || len(pts) != 4 {
		t.Fatalf("plage : step %d, %d points", step, len(pts))
	}
	// capacité bornée
	for i := 0; i < 3000; i++ {
		r.Add(FromRTTs(t0.Add(time.Duration(20+i)*time.Minute), []float64{1}))
	}
	if len(r.Tiers[0].Points) != 2880 {
		t.Fatalf("capacité 1 min : %d", len(r.Tiers[0].Points))
	}
}

func TestARPParsing(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "net"), 0o755)
	os.WriteFile(filepath.Join(dir, "net/arp"), []byte(`IP address       HW type     Flags       HW address            Mask     Device
192.168.0.1      0x1         0x2         aa:bb:cc:dd:ee:01     *        eth0
192.168.0.50     0x1         0x0         00:00:00:00:00:00     *        eth0
192.168.0.9      0x1         0x6         aa:bb:cc:dd:ee:09     *        eth0
`), 0o644)
	os.WriteFile(filepath.Join(dir, "net/route"), []byte("Iface\tDestination\tGateway \tFlags\nnet0\t00000000\t0100A8C0\t0003\n"), 0o644)
	old := ProcRoot
	ProcRoot = dir
	defer func() { ProcRoot = old }()
	e, err := ARP()
	if err != nil || len(e) != 3 || !strings.HasPrefix(e[0].State, "complète") || !strings.HasPrefix(e[1].State, "incomplète") || !strings.HasPrefix(e[2].State, "permanente") {
		t.Fatalf("%v %+v", err, e)
	}
	if gw := defaultGateway(); gw != "192.168.0.1" {
		t.Fatalf("passerelle %s", gw)
	}
	if err := DeleteARP("1.2.3.4; reboot"); err == nil {
		t.Fatal("entrée invalide acceptée")
	}
	if err := DeleteARP("10.9.9.9"); err == nil {
		t.Fatal("entrée absente acceptée")
	}
}

func TestOnLink(t *testing.T) {
	l := Local{Ifaces: []Iface{{Name: "eth0", CIDR: "192.168.0.119/24"}}}
	if !OnLink(net.ParseIP("192.168.0.1"), l) || OnLink(net.ParseIP("172.17.136.11"), l) {
		t.Fatal("OnLink")
	}
}

func TestIperfOptions(t *testing.T) {
	o := IperfOptions{Host: "192.168.0.120"}
	if err := o.Validate(); err != nil || o.Port != 5201 || o.Duration != 10 || o.Streams != 1 || o.Protocol != "tcp" {
		t.Fatalf("%v %+v", err, o)
	}
	a := strings.Join(o.Args(), " ")
	if a != "-c 192.168.0.120 -p 5201 -t 10 -P 1 --json-stream -f m" {
		t.Fatal(a)
	}
	u := IperfOptions{Host: "192.168.0.120", Protocol: "udp", Reverse: true, Streams: 4, Duration: 30}
	if err := u.Validate(); err != nil || !strings.Contains(strings.Join(u.Args(), " "), "-R -u -b 1G") {
		t.Fatalf("%v %v", err, u.Args())
	}
	for _, bad := range []IperfOptions{{Host: "a;b"}, {Host: "1.2.3.4", Protocol: "sctp"}, {Host: "1.2.3.4", Duration: 9999}, {Host: "1.2.3.4", Streams: 99}, {Host: "1.2.3.4", Bitrate: "1G;id"}} {
		if err := bad.Validate(); err == nil {
			t.Errorf("accepté : %+v", bad)
		}
	}
}

// Ping réel sur 127.0.0.1 : uniquement si le socket ICMP brut est disponible (root).
func TestPingLoopback(t *testing.T) {
	if _, err := getConn(); err != nil {
		t.Skip(err)
	}
	rtts, err := Ping(context.Background(), net.ParseIP("127.0.0.1"), PingOptions{Count: 3, Interval: 100 * time.Millisecond})
	if err != nil || Compute(rtts).Recv != 3 {
		t.Fatalf("%v %v", err, rtts)
	}
	hops, err := Traceroute(context.Background(), net.ParseIP("127.0.0.1"), 5, 1, time.Second)
	if err != nil || len(hops) != 1 || !hops[0].Reached {
		t.Fatalf("%v %+v", err, hops)
	}
}
