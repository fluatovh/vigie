package vsphere

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/vmware/govmomi/find"
)

// Sonde du lab (ignorée sans VIGIE_LAB_URL) : voisins CDP/LLDP et compteurs esxcli du premier hôte.
func TestLabNIC(t *testing.T) {
	u := os.Getenv("VIGIE_LAB_URL")
	if u == "" {
		t.Skip("VIGIE_LAB_URL absent")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tp, err := Thumbprint(ctx, u)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewManager().Connect(ctx, ConnectParams{URL: u, Username: os.Getenv("VIGIE_LAB_USER"), Password: os.Getenv("VIGIE_LAB_PASS"), Thumbprint: tp.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	f := find.NewFinder(s.Client)
	dc, err := f.DefaultDatacenter(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.SetDatacenter(dc)
	hosts, err := f.HostSystemList(ctx, "*")
	if err != nil {
		t.Fatal(err)
	}
	h := hosts[0].Reference()
	ns, _ := hosts[0].ConfigManager().NetworkSystem(ctx)
	raw, err := ns.QueryNetworkHint(ctx, nil)
	rb, _ := json.MarshalIndent(raw, "", " ")
	t.Logf("brut %v :\n%s", err, rb)
	p, err := Peers(ctx, s, h, []string{"vmnic0", "vmnic1"})
	b, _ := json.MarshalIndent(p, "", "  ")
	t.Logf("peers %v :\n%s", err, b)
	st, err := NICStats(ctx, s, h, []string{"vmnic0"})
	t.Logf("stats %v : %v", err, st)
}
